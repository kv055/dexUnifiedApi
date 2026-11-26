package main

import (
	"commons/apierror"
	"encoding/json"
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
		apiFail(w, http.StatusBadRequest, "bisq_requote_failed", "the Bisq requote request couldn't be read (the Taker Controller and Unified API may run different releases)")
		return
	}
	route, err := hub.requoteBisq(req, time.Now().UTC())
	if err != nil {
		writeAPIError(w, http.StatusConflict, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(route)
}

func (h *LiveHub) requoteBisq(req bisqroute.Request, now time.Time) (*resolvedRoute, error) {
	const maxSats = 21_000_000 * 100_000_000
	if req.TradeAmountSats == 0 || req.TradeAmountSats > maxSats || req.TotalToPaySats > maxSats || req.SecurityDepositSats > maxSats || req.TakerFeeSats > maxSats || req.MiningFeeSats > maxSats || req.FundingFeeCapSats > maxSats || req.VolumeAsLong <= 0 ||
		req.TotalToPaySats != req.TradeAmountSats+req.SecurityDepositSats+req.TakerFeeSats+req.MiningFeeSats {
		return nil, requoteFailed("the Bisq daemon's quote doesn't add up (trade %d + deposit %d + taker fee %d + mining fee %d sats should equal the %d sats total)",
			req.TradeAmountSats, req.SecurityDepositSats, req.TakerFeeSats, req.MiningFeeSats, req.TotalToPaySats)
	}
	settings := h.transactionalRoutingSettings()
	switch {
	case settings.ConfigVersion <= 0:
		return nil, requoteFailed("no transactional liquidity target is saved yet; save one in the liquidity workspace")
	case settings.TargetDirection != "bid":
		return nil, requoteFailed("the liquidity target's direction is %q, but Bisq trades need bid", settings.TargetDirection)
	case settings.TradeMode != req.TradeMode:
		return nil, requoteFailed("the liquidity target is now in %s mode, but this quote was made for %s mode", tradeModeLabel(settings.TradeMode), tradeModeLabel(req.TradeMode))
	case !strings.EqualFold(settings.FiatCurrency, req.FiatCurrency):
		return nil, requoteFailed("the liquidity target's currency is %s, but this offer is in %s", settings.FiatCurrency, req.FiatCurrency)
	}
	h.bookMu.RLock()
	settings.SnapshotRevision = h.revision
	h.bookMu.RUnlock()
	if settings.SnapshotRevision <= 0 {
		return nil, requoteFailed("the live order book hasn't loaded yet")
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
	switch {
	case offer == nil:
		return nil, requoteFailed("the offer is no longer in the live order book")
	case marketSideForOffer(offer.Direction) != "bid":
		return nil, requoteFailed("the offer sells bitcoin, and only Bisq offers that buy bitcoin can be taken")
	case !strings.EqualFold(offer.FiatCurrency, req.FiatCurrency):
		return nil, requoteFailed("the offer is now in %s, not %s", offer.FiatCurrency, req.FiatCurrency)
	case offer.ReferencePrice <= 0 || math.IsNaN(offer.ReferencePrice) || math.IsInf(offer.ReferencePrice, 0):
		return nil, requoteFailed("there is no current exchange reference price for %s", req.FiatCurrency)
	}
	seen, err := time.Parse(time.RFC3339Nano, offer.LastSeen)
	if err != nil || now.Sub(seen) > 5*time.Minute || seen.After(now.Add(time.Second*10)) {
		age := "an unknown time"
		if err == nil {
			age = now.Sub(seen).Round(time.Second).String()
		}
		return nil, requoteFailed("the offer was last confirmed in the order book %s ago, longer than the 5 minutes allowed for trading", age)
	}
	btc := float64(req.TradeAmountSats) / 1e8
	maxSize, ok := routeSizeForSettings(*offer, offer.ReferencePrice, settings)
	switch {
	case !ok:
		return nil, requoteFailed("the liquidity target doesn't allow any trade size for this offer")
	case btc > maxSize+1e-12:
		return nil, requoteFailed("%s BTC is above the %s BTC this offer and the liquidity target allow", formatBTCAmount(btc), formatBTCAmount(maxSize))
	case offer.BTCMin != nil && btc < *offer.BTCMin-1e-12:
		return nil, requoteFailed("%s BTC is below the offer's minimum of %s BTC", formatBTCAmount(btc), formatBTCAmount(*offer.BTCMin))
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
			return nil, requoteFailed("no exchange can refill the pool for a hedgeless trade of %s BTC", formatBTCAmount(btc))
		}
		bd = *candidate
	default:
		return nil, requoteFailed("unknown trade mode %q", req.TradeMode)
	}
	route := resolvedRouteFromBreakdown(bd, req.FiatCurrency, settings)
	switch {
	case route == nil:
		return nil, requoteFailed("no exchange can carry this trade right now (check the exchange permissions under Trading Accounts)")
	case route.Settlement != "onchain_direct":
		return nil, requoteFailed("the cheapest route now settles via %s instead of a direct on-chain transfer", route.Settlement)
	case !strings.EqualFold(route.Provider, req.Provider):
		return nil, requoteFailed("%s is now the cheapest exchange for this trade instead of %s", route.Provider, req.Provider)
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
		return nil, requoteFailed("the fee data for %s is invalid (taker fee rate %.4f)", req.Provider, feeRate)
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

// requoteFailed is a Bisq requote refusal. The Taker Controller embeds the
// reason in its own message, so it is written as a clause.
func requoteFailed(format string, args ...any) error {
	return apierror.Newf("bisq_requote_failed", format, args...).WithStatus(http.StatusConflict)
}

func tradeModeLabel(mode string) string {
	switch mode {
	case tradecost.TradeModeHedgelessDoubleCollateral:
		return "hedgeless"
	case tradecost.TradeModeHedgefullSingleCollateral:
		return "hedgefull"
	}
	return mode
}
