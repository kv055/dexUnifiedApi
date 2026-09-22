package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"commons/bisqroute"
	"commons/tradecost"
)

// Only MM may call this endpoint. No browser-provided cost, leverage, hedge
// venue or capital estimate is used. Exact daemon amounts arrive over the
// authenticated service boundary after MM has checked the real Bisq quote.
func handleBisqRequote(w http.ResponseWriter, r *http.Request) {
	var req bisqroute.Request
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		http.Error(w, "invalid Bisq route quote", http.StatusBadRequest)
		return
	}
	route, err := hub.requoteBisq(req, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(route)
}

func (h *LiveHub) requoteBisq(req bisqroute.Request, now time.Time) (*resolvedRoute, error) {
	bad := errors.New("Bisq route cannot be requoted from current server policy and offer")
	const maxSats = 21_000_000 * 100_000_000
	if req.TradeAmountSats == 0 || req.TradeAmountSats > maxSats || req.TotalToPaySats > maxSats || req.SecurityDepositSats > maxSats || req.TakerFeeSats > maxSats || req.MiningFeeSats > maxSats || req.FundingFeeCapSats > maxSats || req.VolumeAsLong <= 0 ||
		req.TotalToPaySats != req.TradeAmountSats+req.SecurityDepositSats+req.TakerFeeSats+req.MiningFeeSats {
		return nil, bad
	}
	settings := h.transactionalRoutingSettings()
	if settings.ConfigVersion <= 0 || settings.TargetDirection != "bid" || settings.TradeMode != req.TradeMode || !strings.EqualFold(settings.FiatCurrency, req.FiatCurrency) {
		return nil, bad
	}
	h.bookMu.RLock()
	settings.SnapshotRevision = h.revision
	h.bookMu.RUnlock()
	if settings.SnapshotRevision <= 0 {
		return nil, bad
	}
	settings.ResolvedAt = now.Format(time.RFC3339Nano)
	var offer *LiveOffer
	for _, o := range h.getOffers() {
		if strings.EqualFold(o.Exchange, "bisq") && o.OfferID != nil && *o.OfferID == req.OfferID {
			cp := o
			offer = &cp
			break
		}
	}
	if offer == nil || marketSideForOffer(offer.Direction) != "bid" || !strings.EqualFold(offer.FiatCurrency, req.FiatCurrency) || offer.ReferencePrice <= 0 || math.IsNaN(offer.ReferencePrice) || math.IsInf(offer.ReferencePrice, 0) {
		return nil, bad
	}
	seen, err := time.Parse(time.RFC3339Nano, offer.LastSeen)
	if err != nil || now.Sub(seen) > 5*time.Minute || seen.After(now.Add(time.Second*10)) {
		return nil, errors.New("Bisq offer snapshot is stale")
	}
	btc := float64(req.TradeAmountSats) / 1e8
	maxSize, ok := routeSizeForSettings(*offer, offer.ReferencePrice, settings)
	if !ok || btc > maxSize+1e-12 || (offer.BTCMin != nil && btc < *offer.BTCMin-1e-12) {
		return nil, errors.New("Bisq amount is outside offer or configured limits")
	}
	spotExcluded, hedgeExcluded := h.capabilityExclusions()
	premium := computeDirectionalPremiumPct(offer.Direction, offer.Price, offer.ReferencePrice)
	depositPct := float64(req.SecurityDepositSats) / float64(req.TradeAmountSats)
	var bd tradecost.CostBreakdown
	switch req.TradeMode {
	case tradecost.TradeModeHedgefullSingleCollateral:
		_, _, bd = computeNetPremiumFieldsFull(premium, btc, offer.ReferencePrice, "bisq", offer.Direction, 0, spotExcluded, hedgeExcluded, settings.FeeTier, settings.CoupledHedge, &depositPct)
	case tradecost.TradeModeHedgelessDoubleCollateral:
		candidate := computeHedgelessOnlyBreakdown(premium, btc, offer.ReferencePrice, "bisq", offer.Direction, 0, spotExcluded, hedgeExcluded, settings.FeeTier, settings.CoupledHedge, &depositPct)
		if candidate == nil {
			return nil, bad
		}
		bd = *candidate
	default:
		return nil, bad
	}
	route := resolvedRouteFromBreakdown(bd, req.FiatCurrency, settings)
	if route == nil || route.Settlement != "onchain_direct" || !strings.EqualFold(route.Provider, req.Provider) {
		return nil, errors.New("selected provider is no longer the authoritative direct route")
	}
	// Rebuild capital from the exact funding requirement. A pool's fiat
	// valuation is not an amount the CEX is authorized to spend.
	fundingBTC := float64(req.TotalToPaySats) / 1e8
	spotBTC := fundingBTC
	if req.TradeMode == tradecost.TradeModeHedgelessDoubleCollateral {
		spotBTC = btc + float64(req.TakerFeeSats+req.MiningFeeSats+req.FundingFeeCapSats)/1e8
	}
	feeRate := bd.CexTakerFeeBTC / btc
	if feeRate < 0 || feeRate >= 1 {
		return nil, bad
	}
	spotBTC = (spotBTC + bd.WithdrawalFeeBTC) / (1 - feeRate)
	route.BTCAmount = btc
	route.FiatAmount = float64(req.VolumeAsLong) / 1e4
	route.SecurityDepositBTC = float64(req.SecurityDepositSats) / 1e8
	route.RequiredBTCLiquidity = fundingBTC + float64(req.FundingFeeCapSats)/1e8
	route.CEXWithdrawalFeeBTC = bd.WithdrawalFeeBTC
	route.CostInputs.SpotTakerFeeBTC = spotBTC * feeRate
	route.CostInputs.DEXFeeBTC = float64(req.TakerFeeSats) / 1e8
	route.CostInputs.MinerFeeBTC = float64(req.MiningFeeSats+req.FundingFeeCapSats) / 1e8
	route.RequiredCapital = math.Ceil((spotBTC*offer.ReferencePrice+bd.Capital.HedgeCollateral+bd.Hedge.OpenFeeBTC*offer.ReferencePrice)*100) / 100
	return route, nil
}
