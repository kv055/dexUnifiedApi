package main

import (
	"commons/apierror"
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
		apiFail(w, http.StatusBadRequest, "invalid_request", "The quote request couldn't be read. Reload the page and try again.")
		return
	}
	unit := strings.ToLower(strings.TrimSpace(req.Unit))
	if unit != "btc" && unit != "fiat" {
		apiFail(w, http.StatusBadRequest, "invalid_amount", "Choose BTC or fiat as the amount unit.")
		return
	}
	amount, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(req.Amount), ",", "."), 64)
	if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		apiFail(w, http.StatusBadRequest, "invalid_amount", "Enter an amount greater than zero, for example 0.005 or 150.")
		return
	}
	offer := hub.offerByIdentity(req.OfferDBID, req.Exchange, req.OfferID, req.CoordinatorID)
	if offer == nil {
		apiFail(w, http.StatusNotFound, "offer_not_live", "This offer is no longer in the live order book (it was taken, cancelled or expired). Pick another offer.")
		return
	}
	if strings.EqualFold(offer.Exchange, "robosats") && strings.TrimSpace(offer.VenueCoordinatorID) == "" {
		apiFail(w, http.StatusConflict, "offer_incomplete", "This RoboSats offer doesn't have its coordinator yet, so it can't be priced. Wait for the next order-book refresh (about a minute) and try again.")
		return
	}
	paymentMethod := strings.TrimSpace(req.PaymentMethod)
	if paymentMethod == "" && len(offer.PaymentMethods) == 1 {
		paymentMethod = offer.PaymentMethods[0]
	}
	if paymentMethod == "" {
		apiFail(w, http.StatusBadRequest, "payment_method_required", "This offer accepts several payment methods. Choose one before requesting a quote.")
		return
	}
	nativeID := ""
	if offer.OfferID != nil {
		nativeID = *offer.OfferID
	}
	if nativeID == "" {
		apiFail(w, http.StatusConflict, "offer_incomplete", "This offer doesn't have its venue order ID yet, so it can't be priced. Wait for the next order-book refresh and try again.")
		return
	}
	if req.OfferID != "" && req.OfferID != nativeID || req.Exchange != "" && !strings.EqualFold(req.Exchange, offer.Exchange) || req.CoordinatorID != "" && req.CoordinatorID != offer.VenueCoordinatorID {
		apiFail(w, http.StatusConflict, "offer_changed", "This offer changed in the order book since you opened it. Close it and open it again from the list.")
		return
	}
	var native *roboNativeTerms
	if strings.EqualFold(offer.Exchange, "robosats") {
		if !strings.EqualFold(offer.Direction, "bid") {
			apiFail(w, http.StatusConflict, "unsupported_route", "Only RoboSats offers where the maker buys bitcoin can be taken; this one sells bitcoin.")
			return
		}
		req.Amount, req.Unit = strings.ReplaceAll(strings.TrimSpace(req.Amount), ",", "."), unit
		native, err = loadRoboNativeTerms(r.Context(), req, offer, paymentMethod)
		if err != nil {
			writeAPIError(w, http.StatusConflict, err)
			return
		}
	}

	offerPrice := offer.Price
	if native != nil {
		offerPrice, err = strconv.ParseFloat(native.Price, 64)
	}
	if err != nil || offerPrice <= 0 || math.IsNaN(offerPrice) || math.IsInf(offerPrice, 0) {
		apiFail(w, http.StatusConflict, "offer_price_unavailable", "This offer has no usable price right now. Wait for the next order-book refresh and try again.")
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
			apiFail(w, http.StatusConflict, "robosats_terms_changed", "RoboSats returned an unusable fiat amount for this offer. Try again; if it keeps happening, pick another offer.")
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
		apiFail(w, http.StatusBadRequest, "amount_out_of_range", fmt.Sprintf("%s BTC is below this offer's minimum of %s BTC. Enter a larger amount.", formatBTCAmount(btcAmount), formatBTCAmount(*offer.BTCMin)))
		return
	}
	if offer.BTCMax != nil && btcAmount-1e-12 > *offer.BTCMax {
		apiFail(w, http.StatusBadRequest, "amount_out_of_range", fmt.Sprintf("%s BTC is above this offer's maximum of %s BTC. Enter a smaller amount.", formatBTCAmount(btcAmount), formatBTCAmount(*offer.BTCMax)))
		return
	}
	if offer.FiatMin != nil && fiatAmount+0.005 < float64(*offer.FiatMin) {
		apiFail(w, http.StatusBadRequest, "amount_out_of_range", fmt.Sprintf("%.2f %s is below this offer's minimum of %v %s. Enter a larger amount.", fiatAmount, offer.FiatCurrency, *offer.FiatMin, offer.FiatCurrency))
		return
	}
	if offer.FiatMax != nil && fiatAmount-0.005 > float64(*offer.FiatMax) {
		apiFail(w, http.StatusBadRequest, "amount_out_of_range", fmt.Sprintf("%.2f %s is above this offer's maximum of %v %s. Enter a smaller amount.", fiatAmount, offer.FiatCurrency, *offer.FiatMax, offer.FiatCurrency))
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
		apiFail(w, http.StatusBadRequest, "invalid_amount", "That amount is larger than all bitcoin in existence. Check the amount and its unit (BTC or fiat).")
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
		apiFail(w, http.StatusBadRequest, "invalid_amount", "This amount is too small to put anything in escrow. Enter a larger amount.")
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
		apiFail(w, http.StatusServiceUnavailable, "quotes_unavailable", "Quotes can't be saved because the Unified API has no database connection. Check the Postgres container and the Unified API log.")
		return
	}
	err = db.QueryRowContext(r.Context(), `INSERT INTO execution_quotes
		(id, offer_db_id, exchange, coordinator_id, offer_id, payment_method, input_unit, input_amount, fiat_amount, btc_amount, max_bond_sat, max_escrow_sat, bond_fee_sat, escrow_fee_sat, route, expires_at, native_terms)
		VALUES (gen_random_uuid(), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?::jsonb)
		RETURNING id::text`,
		offer.ID, strings.ToLower(offer.Exchange), nullIfBlank(offer.VenueCoordinatorID), nativeID, paymentMethod, unit, amount, fiatAmount, btcAmount, maxBond, maxEscrow, bondFee, escrowFee, string(routeJSON), expires, nativeJSON,
	).Scan(&quoteID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apierror.Wrap(fmt.Errorf("persist execution quote: %w", err), apierror.CodeDatabase,
			"The quote couldn't be saved to the database, so it can't be confirmed. Try again; if it keeps failing, check the Postgres container."))
		return
	}
	resp := executionQuoteResponse{
		QuoteID: quoteID, ExpiresAt: expires.Format(time.RFC3339), CoordinatorID: offer.VenueCoordinatorID,
		OfferID: nativeID, PaymentMethod: paymentMethod, FiatAmount: fiatAmount, BTCAmount: btcAmount,
		MaxBondSat: maxBond, MaxEscrowSat: maxEscrow, BondFeeSat: bondFee, EscrowFeeSat: escrowFee, ResolvedRoute: route,
		Executable: route != nil,
	}
	if route == nil {
		resp.BlockedReason = noRouteReason(spot, hedge, settings.TradeMode)
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

// noRouteReason explains why no exchange can carry a trade of this size.
func noRouteReason(spotExcluded, hedgeExcluded []string, tradeMode string) string {
	msg := "No exchange can carry this trade right now."
	if len(spotExcluded) > 0 {
		msg += " Buying is disabled on " + strings.Join(spotExcluded, ", ") + "."
	}
	if tradeMode != tradecost.TradeModeHedgelessDoubleCollateral && len(hedgeExcluded) > 0 {
		msg += " Hedging is disabled on " + strings.Join(hedgeExcluded, ", ") + "."
	}
	msg += " Each trade needs an exchange with spot trading"
	if tradeMode != tradecost.TradeModeHedgelessDoubleCollateral {
		msg += " and hedging"
	}
	return msg + " enabled and its fees loaded. Check the exchange permissions under Trading Accounts, then reopen the offer."
}

// formatBTCAmount prints a BTC amount without trailing zeros.
func formatBTCAmount(v float64) string {
	return strconv.FormatFloat(tradecost.Round8(v), 'f', -1, 64)
}

// apiFail answers with an operator-facing error.
func apiFail(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(w, status, apierror.New(code, message))
}
