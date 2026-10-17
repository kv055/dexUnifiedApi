package main

import (
	"commons/tradecost"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type executionQuoteRequest struct {
	OfferDBID     int64  `json:"offer_db_id"`
	OfferID       string `json:"offer_id"`
	Exchange      string `json:"exchange"`
	CoordinatorID string `json:"venue_coordinator_id"`
	Amount        string `json:"amount"`
	Unit          string `json:"unit"`
	PaymentMethod string `json:"payment_method"`
}

type executionQuoteResponse struct {
	QuoteID       string         `json:"quote_id"`
	ExpiresAt     string         `json:"expires_at"`
	CoordinatorID string         `json:"venue_coordinator_id,omitempty"`
	OfferID       string         `json:"offer_id"`
	PaymentMethod string         `json:"payment_method"`
	FiatAmount    float64        `json:"fiat_amount"`
	BTCAmount     float64        `json:"btc_amount"`
	MaxBondSat    int64          `json:"max_bond_sat"`
	MaxEscrowSat  int64          `json:"max_escrow_sat"`
	BondFeeSat    int64          `json:"bond_fee_sat"`
	EscrowFeeSat  int64          `json:"escrow_fee_sat"`
	ResolvedRoute *resolvedRoute `json:"resolved_route"`
	Executable    bool           `json:"executable"`
	BlockedReason string         `json:"blocked_reason,omitempty"`
}

func handleExecutionQuotes(w http.ResponseWriter, r *http.Request) {
	var req executionQuoteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	unit := strings.ToLower(strings.TrimSpace(req.Unit))
	if unit != "btc" && unit != "fiat" {
		http.Error(w, "unit must be btc or fiat", http.StatusBadRequest)
		return
	}
	amount, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(req.Amount), ",", "."), 64)
	if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		http.Error(w, "amount must be a positive decimal", http.StatusBadRequest)
		return
	}
	offer := hub.offerByIdentity(req.OfferDBID, req.Exchange, req.OfferID, req.CoordinatorID)
	if offer == nil {
		http.Error(w, "offer is not currently live", http.StatusNotFound)
		return
	}
	if strings.EqualFold(offer.Exchange, "robosats") && strings.TrimSpace(offer.VenueCoordinatorID) == "" {
		http.Error(w, "offer is missing coordinator identity; refresh it from source before trading", http.StatusConflict)
		return
	}
	paymentMethod := strings.TrimSpace(req.PaymentMethod)
	if paymentMethod == "" && len(offer.PaymentMethods) == 1 {
		paymentMethod = offer.PaymentMethods[0]
	}
	if paymentMethod == "" {
		http.Error(w, "payment_method is required", http.StatusBadRequest)
		return
	}
	nativeID := ""
	if offer.OfferID != nil {
		nativeID = *offer.OfferID
	}
	if nativeID == "" {
		http.Error(w, "native offer identity is missing", http.StatusConflict)
		return
	}
	if req.OfferID != "" && req.OfferID != nativeID || req.Exchange != "" && !strings.EqualFold(req.Exchange, offer.Exchange) || req.CoordinatorID != "" && req.CoordinatorID != offer.VenueCoordinatorID {
		http.Error(w, "offer identity does not match", http.StatusConflict)
		return
	}
	var native *roboNativeTerms
	if strings.EqualFold(offer.Exchange, "robosats") {
		if !strings.EqualFold(offer.Direction, "bid") {
			http.Error(w, "only RoboSats bids are executable", http.StatusConflict)
			return
		}
		req.Amount, req.Unit = strings.ReplaceAll(strings.TrimSpace(req.Amount), ",", "."), unit
		native, err = loadRoboNativeTerms(r.Context(), req, offer, paymentMethod)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}

	offerPrice := offer.Price
	if native != nil {
		offerPrice, err = strconv.ParseFloat(native.Price, 64)
	}
	if err != nil || offerPrice <= 0 || math.IsNaN(offerPrice) || math.IsInf(offerPrice, 0) {
		http.Error(w, "offer price is unavailable", http.StatusConflict)
		return
	}
	var btcAmount, fiatAmount float64
	if unit == "fiat" {
		fiatAmount = tradecost.Round2(amount)
		btcAmount = tradecost.Round8(fiatAmount / offerPrice)
	} else {
		btcAmount = tradecost.Round8(amount)
		fiatAmount = tradecost.Round2(btcAmount * offerPrice)
	}
	if native != nil {
		fiatAmount, err = strconv.ParseFloat(native.FiatAmount, 64)
		if err != nil || fiatAmount <= 0 || math.IsInf(fiatAmount, 0) || math.IsNaN(fiatAmount) {
			http.Error(w, "invalid native fiat amount", http.StatusConflict)
			return
		}
		btcAmount = float64(native.PrincipalSat) / 1e8
		// Native validation has already checked exact fiat limits; snapshot BTC
		// limits can be stale when the coordinator's floating price changes.
		offer.BTCMin, offer.BTCMax, offer.FiatMin, offer.FiatMax = nil, nil, nil, nil
		pct, _ := strconv.ParseFloat(native.BondPercent, 64)
		offer.SecurityDepositPct = &pct
	}
	if offer.BTCMin != nil && btcAmount+1e-12 < *offer.BTCMin {
		http.Error(w, "amount is below the offer minimum", http.StatusBadRequest)
		return
	}
	if offer.BTCMax != nil && btcAmount-1e-12 > *offer.BTCMax {
		http.Error(w, "amount is above the offer maximum", http.StatusBadRequest)
		return
	}
	if offer.FiatMin != nil && fiatAmount+0.005 < float64(*offer.FiatMin) {
		http.Error(w, "amount is below the offer minimum", http.StatusBadRequest)
		return
	}
	if offer.FiatMax != nil && fiatAmount-0.005 > float64(*offer.FiatMax) {
		http.Error(w, "amount is above the offer maximum", http.StatusBadRequest)
		return
	}

	settings := hub.snapshotRoutingSettings()
	pct := computeDirectionalPremiumPct(offer.Direction, offerPrice, offer.ReferencePrice)
	spot, hedge := hub.capabilityExclusions()
	_, _, bd := computeNetPremiumFieldsFull(pct, btcAmount, offer.ReferencePrice, offer.Exchange, offer.Direction, 0, spot, hedge, settings.FeeTier, settings.CoupledHedge, offer.SecurityDepositPct)
	if settings.TradeMode == tradecost.TradeModeHedgelessDoubleCollateral {
		if hedgeless := computeHedgelessOnlyBreakdown(pct, btcAmount, offer.ReferencePrice, offer.Exchange, offer.Direction, 0, spot, hedge, settings.FeeTier, settings.CoupledHedge, offer.SecurityDepositPct); hedgeless != nil {
			bd = *hedgeless
		}
	}
	route := resolvedRouteFromBreakdown(bd, offer.FiatCurrency, settings)
	if route != nil {
		route.FiatAmount = fiatAmount
		route.BTCAmount = btcAmount
	}
	// Guard the float->satoshi conversions: an out-of-range float would
	// otherwise wrap int64 and persist a corrupt bond/escrow amount.
	bondBTC := bd.Capital.SecurityDepositBTC
	if math.IsNaN(bondBTC) || math.IsInf(bondBTC, 0) || bondBTC < 0 || bondBTC > 21_000_000 || btcAmount > 21_000_000 {
		http.Error(w, "amount is outside the possible bitcoin supply", http.StatusBadRequest)
		return
	}
	maxBond := int64(math.Ceil(math.Max(0, bondBTC) * 1e8))
	maxEscrow := int64(math.Ceil(btcAmount * 1e8))
	if native != nil {
		maxBond, maxEscrow = native.MaxBondSat, native.MaxEscrowSat
		if route != nil {
			route.SecurityDepositBTC = float64(maxBond) / 1e8
			route.RequiredBTCLiquidity = float64(maxBond+maxEscrow) / 1e8
			// Preserve the route's CEX/hedge capital and reserve the native seller
			// fee difference in addition to the principal used for route selection.
			extra := math.Max(0, float64(maxEscrow-native.PrincipalSat)/1e8-bd.DexFeeBTC)
			route.RequiredCapital += extra * offer.ReferencePrice
		}
	}
	if maxEscrow <= 0 {
		http.Error(w, "escrow amount is invalid", http.StatusBadRequest)
		return
	}
	bondFee, escrowFee := int64(0), int64(0)
	if route != nil && route.RoutingFeeAllowanceSat > 0 {
		bondFee = route.RoutingFeeAllowanceSat / 2
		escrowFee = route.RoutingFeeAllowanceSat - bondFee
	}
	expires := time.Now().UTC().Add(60 * time.Second)
	var nativeJSON any
	if native != nil {
		if native.ExpiresAt.Add(-30 * time.Second).Before(expires) {
			expires = native.ExpiresAt.Add(-30 * time.Second)
		}
		raw, _ := json.Marshal(native)
		nativeJSON = string(raw)
	}
	routeJSON, _ := json.Marshal(route)
	var quoteID string
	if db == nil {
		http.Error(w, "quotes are unavailable", http.StatusServiceUnavailable)
		return
	}
	err = db.QueryRowContext(r.Context(), `INSERT INTO execution_quotes
		(id, offer_db_id, exchange, coordinator_id, offer_id, payment_method, input_unit, input_amount, fiat_amount, btc_amount, max_bond_sat, max_escrow_sat, bond_fee_sat, escrow_fee_sat, route, expires_at, native_terms)
		VALUES (gen_random_uuid(), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?::jsonb)
		RETURNING id::text`,
		offer.ID, strings.ToLower(offer.Exchange), nullIfBlank(offer.VenueCoordinatorID), nativeID, paymentMethod, unit, amount, fiatAmount, btcAmount, maxBond, maxEscrow, bondFee, escrowFee, string(routeJSON), expires, nativeJSON,
	).Scan(&quoteID)
	if err != nil {
		writeError(w, fmt.Errorf("persist execution quote: %w", err))
		return
	}
	resp := executionQuoteResponse{
		QuoteID: quoteID, ExpiresAt: expires.Format(time.RFC3339), CoordinatorID: offer.VenueCoordinatorID,
		OfferID: nativeID, PaymentMethod: paymentMethod, FiatAmount: fiatAmount, BTCAmount: btcAmount,
		MaxBondSat: maxBond, MaxEscrowSat: maxEscrow, BondFeeSat: bondFee, EscrowFeeSat: escrowFee, ResolvedRoute: route,
		Executable: route != nil,
	}
	if route == nil {
		resp.BlockedReason = "No executable route for this size"
	}
	writeJSON(w, resp)
}

func nullIfBlank(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func (h *LiveHub) offerByIdentity(dbID int64, exchange, offerID, coordinatorID string) *LiveOffer {
	for _, o := range h.getOffers() {
		if dbID > 0 {
			if o.ID == dbID {
				cp := o
				return &cp
			}
			continue
		}
		if strings.EqualFold(exchange, "robosats") && coordinatorID == "" {
			continue
		}
		if strings.EqualFold(o.Exchange, exchange) && o.OfferID != nil && *o.OfferID == offerID {
			if coordinatorID == "" || o.VenueCoordinatorID == coordinatorID {
				cp := o
				return &cp
			}
		}
	}
	return nil
}

func (h *LiveHub) snapshotRoutingSettings() transactionalRoutingSettings {
	h.routingSettingsMu.RLock()
	defer h.routingSettingsMu.RUnlock()
	return h.routingSettings
}
