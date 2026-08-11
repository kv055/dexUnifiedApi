package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"commons/tradecost"
	"dexunifiedapi/internal/lifecycle"

	"github.com/gorilla/websocket"
)

// upgrader restricts the origin to same-origin: /ws/live_offers is gated by the
// shared dashboard session cookie, so allowing any origin would let a page on
// another site open the stream with a logged-in operator's cookie (cross-site
// WebSocket hijacking). A missing Origin (non-browser clients) is allowed.
var upgrader = websocket.Upgrader{
	CheckOrigin: sameOriginWS,
}

func sameOriginWS(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// LiveOffer holds a single live offer pushed by the snapshot tool.
type LiveOffer struct {
	// ID identifies the live offer.
	ID int64 `json:"id"`
	// Exchange is the exchange value for the live offer.
	Exchange string `json:"exchange"`
	// FiatCurrency is a fiat-denominated fiat currency value.
	FiatCurrency string `json:"fiat_currency"`
	// Direction is the direction value for the live offer.
	Direction string `json:"direction"`
	// Price is the price value used by the live offer.
	Price float64 `json:"price"`
	// ReferencePrice is the same-currency BTC reference captured by the snapshot
	// worker. It is used for non-EUR offers until live CEX feeds are currency-aware.
	ReferencePrice float64 `json:"reference_price"`
	// BTCMin is a BTC-denominated btcmin value.
	BTCMin *float64 `json:"btc_min"`
	// BTCMax is a BTC-denominated btcmax value.
	BTCMax *float64 `json:"btc_max"`
	// FiatMin is a fiat-denominated fiat min value.
	FiatMin *int64 `json:"fiat_min"`
	// FiatMax is a fiat-denominated fiat max value.
	FiatMax *int64 `json:"fiat_max"`
	// PaymentMethods lists the payment methods accepted by this offer.
	PaymentMethods []string `json:"payment_methods"`
	// OfferID is the offer id value for the live offer.
	OfferID *string `json:"offer_id"`
	// FirstSeen is the first seen value for the live offer.
	FirstSeen string `json:"first_seen"`
	// LastSeen is the last seen value for the live offer.
	LastSeen string `json:"last_seen"`
	// PriceType is the price value used by the live offer.
	PriceType *string `json:"price_type"`
	// SecurityDepositPct is the real per-offer security deposit/bond fraction
	// (offers.security_deposit_pct), already picked for the taker's side at
	// snapshot time. Nil for exchanges with no deposit concept, or offers
	// snapshotted before this was persisted.
	SecurityDepositPct *float64 `json:"security_deposit_pct"`
	// SellerRating* are the maker's normalized reputation (internal/sellerrating).
	// Score is 0-100, nil when no signal is obtainable; Confidence records the
	// methodology ('unavailable' when there is none); Raw holds the source inputs.
	SellerRatingScore      *float64        `json:"seller_rating_score"`
	SellerRatingConfidence *string         `json:"seller_rating_confidence"`
	SellerRatingRaw        json.RawMessage `json:"seller_rating_raw"`
	// MakerID is the exchange's seller identity, used to look up a per-maker
	// reputation for profitable offers (Peach only; its listing omits the rating).
	MakerID string `json:"maker_id,omitempty"`
}

// LifecyclePoint is a single price observation from the order_lifecycle table.
type LifecyclePoint struct {
	// Time records the relevant time for the lifecycle point.
	Time string `json:"time"`
	// Price is the price value used by the lifecycle point.
	Price float64 `json:"price"`
	// NetPremiumPct is a percentage value.
	NetPremiumPct *float64 `json:"net_premium_pct,omitempty"`
}

type liveOfferStatus struct {
	IsLive    bool   `json:"is_live"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

type liveOfferPricing struct {
	PriceType         string  `json:"price_type"`
	FiatCurrency      string  `json:"fiat_currency"`
	OfferPrice        float64 `json:"offer_price"`
	RefPrice          float64 `json:"ref_price"`
	RawPremiumPercent float64 `json:"raw_premium_percent"`
}

type liveOfferVolumeLimits struct {
	BTCMin  *float64 `json:"btc_min"`
	BTCMax  *float64 `json:"btc_max"`
	FiatMin *int64   `json:"fiat_min"`
	FiatMax *int64   `json:"fiat_max"`
}

type liveOfferBTCNetworkFee struct {
	BTC                     float64                        `json:"btc"`
	Method                  string                         `json:"method"`
	Tier                    string                         `json:"tier"`
	Confidence              float64                        `json:"confidence"`
	NumTxs                  int                            `json:"num_txs"`
	RequiredConfirmations   int                            `json:"required_confirmations"`
	EstimatedArrivalMinutes int                            `json:"estimated_arrival_minutes"`
	Detail                  *tradecost.BTCNetworkFeeDetail `json:"detail,omitempty"`
}

type liveOfferRoute struct {
	CEX        string `json:"cex"`
	Settlement string `json:"settlement"`
}

// resolvedRoute is the executable route selected by Unified API. Consumers pass
// it to MM controller unchanged, avoiding a second route calculation there.
type resolvedRoute struct {
	ConfigVersion          int64   `json:"config_version"`
	TradeMode              string  `json:"trade_mode"`
	TargetDirection        string  `json:"target_direction"`
	MarketSide             string  `json:"market_side"`
	Provider               string  `json:"provider"`
	HedgeVenue             string  `json:"hedge_venue,omitempty"`
	HedgeSide              string  `json:"hedge_side,omitempty"`
	HedgeNotional          float64 `json:"hedge_notional_btc,omitempty"`
	Leverage               float64 `json:"leverage,omitempty"`
	Settlement             string  `json:"settlement"`
	NeedsHub               bool    `json:"needs_hub"`
	NeedsLightning         bool    `json:"needs_lightning"`
	FiatCurrency           string  `json:"fiat_currency"`
	FiatAmount             float64 `json:"fiat_amount"`
	BTCAmount              float64 `json:"btc_amount"`
	RequiredCapital        float64 `json:"required_capital"`
	RequiredBTCLiquidity   float64 `json:"required_btc_liquidity"`
	SecurityDepositBTC     float64 `json:"security_deposit_btc"`
	RoutingFeeAllowanceSat int64   `json:"routing_fee_allowance_sat"`
	CEXWithdrawalFeeBTC    float64 `json:"cex_withdrawal_fee_btc"`
	SwapSafetyHeadroomBTC  float64 `json:"swap_safety_headroom_btc"`
	TotalCostBTC           float64 `json:"total_cost_btc"`
	TotalCostPercent       float64 `json:"total_cost_percent"`
	NetPremiumPercent      float64 `json:"net_premium_percent"`
}

type liveOfferMetadata struct {
	ResolvedRoute *resolvedRoute `json:"resolved_route,omitempty"`
}

// transactionalRoutingSettings is the routing-relevant projection of the
// singleton settings row. It is cached in the hub so a websocket fan-out uses
// one coherent policy snapshot for every offer in the payload.
type transactionalRoutingSettings struct {
	FiatCurrency    string
	CustomAmount    *float64
	TradeMode       string
	TargetDirection string
	ConfigVersion   int64
}

type liveOfferPremium struct {
	TradeCostPercent float64 `json:"trade_cost_percent"`
	NetPercent       float64 `json:"net_percent"`
}

type liveOfferProfit struct {
	BTC  float64 `json:"btc"`
	Fiat float64 `json:"fiat"`
}

type liveOfferCostComponent struct {
	Pct float64 `json:"pct"`
	BTC float64 `json:"btc"`
}

type liveOfferNetworkCost struct {
	Pct float64 `json:"pct"`
	BTC float64 `json:"btc"`
}

type liveOfferScenarioCosts struct {
	CexTaker   liveOfferCostComponent        `json:"cex_taker"`
	Withdrawal liveOfferCostComponent        `json:"withdrawal"`
	DexFee     liveOfferCostComponent        `json:"dex_fee"`
	Network    liveOfferNetworkCost          `json:"network"`
	Conversion tradecost.ConversionBreakdown `json:"conversion"`
	Hedge      tradecost.HedgeBreakdown      `json:"hedge"`
	Total      liveOfferCostComponent        `json:"total"`
}

// liveOfferModeVariant is the economics for an alternate trade mode on the
// same spot route (currently Hedgeless Double Collateral).
type liveOfferModeVariant struct {
	Route            liveOfferRoute               `json:"route"`
	NumNetworkTxs    int                          `json:"num_network_txs"`
	NetworkFeeMethod string                       `json:"network_fee_method"`
	Premium          liveOfferPremium             `json:"premium"`
	Profit           liveOfferProfit              `json:"profit"`
	Costs            liveOfferScenarioCosts       `json:"costs"`
	AbortCost        tradecost.AbortCostBreakdown `json:"abort_cost"`
	Capital          tradecost.CapitalRequirement `json:"capital"`
}

type liveOfferScenario struct {
	CapFiat                   *float64                     `json:"cap_fiat,omitempty"`
	TradeSizeBTC              float64                      `json:"trade_size_btc"`
	TradeSizeFiat             float64                      `json:"trade_size_fiat"`
	Route                     liveOfferRoute               `json:"route"`
	Premium                   liveOfferPremium             `json:"premium"`
	Profit                    liveOfferProfit              `json:"profit"`
	Costs                     liveOfferScenarioCosts       `json:"costs"`
	AbortCost                 tradecost.AbortCostBreakdown `json:"abort_cost"`
	Capital                   tradecost.CapitalRequirement `json:"capital"` // hedgefull single-collateral capital
	ManualRouteOptions        tradecost.ManualRouteOptions `json:"manual_route_options"`
	HedgelessDoubleCollateral *liveOfferModeVariant        `json:"hedgeless_double_collateral,omitempty"`
}

type liveOfferAnalysis struct {
	FullVolume   liveOfferScenario  `json:"full_volume"`
	CappedVolume *liveOfferScenario `json:"capped_volume,omitempty"`
	MinVolume    *liveOfferScenario `json:"min_volume,omitempty"`
}

type liveOfferAPI struct {
	ID                     int64                  `json:"id"`
	Exchange               string                 `json:"exchange"`
	OfferID                string                 `json:"offer_id"`
	PaymentMethods         []string               `json:"payment_methods"`
	Direction              string                 `json:"direction"`
	Status                 liveOfferStatus        `json:"status"`
	Pricing                liveOfferPricing       `json:"pricing"`
	VolumeLimits           liveOfferVolumeLimits  `json:"volume_limits"`
	BTCNetworkFee          liveOfferBTCNetworkFee `json:"btc_network_fee"`
	Analysis               liveOfferAnalysis      `json:"analysis"`
	Metadata               liveOfferMetadata      `json:"metadata"`
	SellerRatingScore      *float64               `json:"seller_rating_score"`
	SellerRatingConfidence *string                `json:"seller_rating_confidence"`
	SellerRatingRaw        json.RawMessage        `json:"seller_rating_raw,omitempty"`
}

// liveOutOffer is the shape sent to clients — includes computed premium fields.
type liveOutOffer struct {
	ID                        int64                         `json:"id"`
	Exchange                  string                        `json:"exchange"`
	FiatCurrency              string                        `json:"fiat_currency"`
	Direction                 string                        `json:"direction"`
	Price                     float64                       `json:"price"`
	RawPremiumPct             float64                       `json:"raw_premium_percent"` // explicit alias for PriceDiffPct
	PriceDiffPct              float64                       `json:"price_diff_percent"`
	RefPrice                  float64                       `json:"ref_price"`
	TradeCostPct              float64                       `json:"trade_cost_percent"`              // estimated CEX→DEX arbitrage cost as % of trade value
	NetPremiumPct             float64                       `json:"net_premium_percent"`             // PriceDiffPct minus TradeCostPct
	EstimatedProfitBTC        float64                       `json:"estimated_profit_btc"`            // estimated BTC profit for the offer's full trade size
	EstimatedProfitFiat       float64                       `json:"estimated_profit_fiat"`           // estimated fiat profit for the offer's full trade size
	CostBreakdown             tradecost.CostBreakdown       `json:"cost_breakdown"`                  // per-component cost detail (full size)
	CostBreakdownCapped       *tradecost.CostBreakdown      `json:"cost_breakdown_capped,omitempty"` // capped to max fiat limit (only set when limit < offer size)
	AbortCost                 tradecost.AbortCostBreakdown  `json:"abort_cost"`
	AbortCostCapped           *tradecost.AbortCostBreakdown `json:"abort_cost_capped,omitempty"`
	TradeCostPctCapped        *float64                      `json:"trade_cost_percent_capped,omitempty"`
	NetPremiumPctCapped       *float64                      `json:"net_premium_percent_capped,omitempty"`
	EstimatedProfitBTCCapped  *float64                      `json:"estimated_profit_btc_capped,omitempty"`  // estimated BTC profit at the max fiat cap
	EstimatedProfitFiatCapped *float64                      `json:"estimated_profit_fiat_capped,omitempty"` // estimated fiat profit at the max fiat cap
	CostBreakdownMin          *tradecost.CostBreakdown      `json:"cost_breakdown_min,omitempty"`           // at the offer's min amount (only set when min < full size)
	HedgelessOnlyBreakdown    *tradecost.CostBreakdown      `json:"-"`                                      // spot-only route when hedgefull cannot be priced
	HedgelessOnlyCapped       *tradecost.CostBreakdown      `json:"-"`
	HedgelessOnlyMin          *tradecost.CostBreakdown      `json:"-"`
	ManualRouteOptions        tradecost.ManualRouteOptions  `json:"-"`
	ManualRouteOptionsCapped  *tradecost.ManualRouteOptions `json:"-"`
	ManualRouteOptionsMin     *tradecost.ManualRouteOptions `json:"-"`
	TradeCostPctMin           *float64                      `json:"trade_cost_percent_min,omitempty"`
	NetPremiumPctMin          *float64                      `json:"net_premium_percent_min,omitempty"`
	BTCMin                    *float64                      `json:"btc_min"`
	BTCMax                    *float64                      `json:"btc_max"`
	FiatMin                   *int64                        `json:"fiat_min"`
	FiatMax                   *int64                        `json:"fiat_max"`
	PaymentMethods            []string                      `json:"payment_methods"`
	OfferID                   *string                       `json:"offer_id"`
	FirstSeen                 string                        `json:"first_seen"`
	LastSeen                  string                        `json:"last_seen"`
	IsLive                    bool                          `json:"is_live"`
	PriceType                 *string                       `json:"price_type"`
	SellerRatingScore         *float64                      `json:"seller_rating_score"`
	SellerRatingConfidence    *string                       `json:"seller_rating_confidence"`
	SellerRatingRaw           json.RawMessage               `json:"seller_rating_raw,omitempty"`
	Lifecycle                 []LifecyclePoint              `json:"lifecycle,omitempty"`
	ResolvedRoute             *resolvedRoute                `json:"-"`
}

func normalizePriceType(priceType *string) string {
	if priceType == nil {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(*priceType)) {
	case "fixed":
		return "fixed"
	case "floating":
		return "floating"
	default:
		return "unknown"
	}
}

func derefStringOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func routingSettlement(bd tradecost.CostBreakdown) string {
	if strings.TrimSpace(bd.RoutingProfile) != "" {
		return bd.RoutingProfile
	}
	return bd.RouteMethod
}

func scenarioCostsFromBreakdown(bd tradecost.CostBreakdown) liveOfferScenarioCosts {
	return liveOfferScenarioCosts{
		CexTaker: liveOfferCostComponent{
			Pct: bd.CexTakerFeePct,
			BTC: bd.CexTakerFeeBTC,
		},
		Withdrawal: liveOfferCostComponent{
			Pct: bd.WithdrawalFeePct,
			BTC: bd.WithdrawalFeeBTC,
		},
		DexFee: liveOfferCostComponent{
			Pct: bd.DexFeePct,
			BTC: bd.DexFeeBTC,
		},
		Network: liveOfferNetworkCost{
			Pct: bd.NetworkFeePct,
			BTC: bd.NetworkFeeBTC,
		},
		Conversion: bd.Conversion,
		Hedge:      bd.Hedge,
		Total: liveOfferCostComponent{
			Pct: bd.TotalCostPct,
			BTC: bd.TotalCostBTC,
		},
	}
}

func buildModeVariantFromBreakdown(bd tradecost.CostBreakdown, refPrice float64) liveOfferModeVariant {
	return liveOfferModeVariant{
		Route:            liveOfferRoute{CEX: bd.RouteCEX, Settlement: routingSettlement(bd)},
		NumNetworkTxs:    bd.NumNetworkTxs,
		NetworkFeeMethod: bd.NetworkFeeMethod,
		Premium: liveOfferPremium{
			TradeCostPercent: bd.TotalCostPct,
			NetPercent:       bd.EstimatedProfitPct,
		},
		Profit: liveOfferProfit{
			BTC:  bd.EstimatedProfitBTC,
			Fiat: tradecost.Round2(bd.EstimatedProfitBTC * refPrice),
		},
		Costs:     scenarioCostsFromBreakdown(bd),
		AbortCost: tradecost.AbortCostFromBreakdown(bd),
		Capital:   bd.Capital,
	}
}

func buildScenarioFromBreakdown(bd tradecost.CostBreakdown, refPrice float64, capFiat *float64, tradeCostPct float64, netPremiumPct float64, manualOptions tradecost.ManualRouteOptions) liveOfferScenario {
	scenario := liveOfferScenario{
		CapFiat:       capFiat,
		TradeSizeBTC:  bd.BTCAmount,
		TradeSizeFiat: tradecost.Round2(bd.BTCAmount * refPrice),
		Route: liveOfferRoute{
			CEX:        bd.RouteCEX,
			Settlement: routingSettlement(bd),
		},
		Premium: liveOfferPremium{
			TradeCostPercent: tradeCostPct,
			NetPercent:       netPremiumPct,
		},
		Profit: liveOfferProfit{
			BTC:  bd.EstimatedProfitBTC,
			Fiat: tradecost.Round2(bd.EstimatedProfitBTC * refPrice),
		},
		Costs:              scenarioCostsFromBreakdown(bd),
		AbortCost:          tradecost.AbortCostFromBreakdown(bd),
		Capital:            bd.Capital,
		ManualRouteOptions: manualOptions,
	}
	if bd.TradeDirection == "bid" {
		if hedgeless := tradecost.DeriveHedgelessDoubleCollateral(bd); hedgeless.RouteCEX != "" {
			variant := buildModeVariantFromBreakdown(hedgeless, refPrice)
			scenario.HedgelessDoubleCollateral = &variant
		}
	}
	return scenario
}

func buildBTCLiveNetworkFee(bd tradecost.CostBreakdown, requiredConfirmations int) liveOfferBTCNetworkFee {
	etaMinutes := 0
	if bd.NetworkFeeDetail != nil {
		etaMinutes = bd.NetworkFeeDetail.SelectedFullConfirmationMinutes
		if bd.NetworkFeeDetail.RequiredConfirmations > 0 {
			requiredConfirmations = bd.NetworkFeeDetail.RequiredConfirmations
		}
	}
	return liveOfferBTCNetworkFee{
		BTC:                     bd.NetworkFeeBTC,
		Method:                  bd.NetworkFeeMethod,
		Tier:                    bd.NetworkFeeTier,
		Confidence:              bd.NetworkFeeConfidence,
		NumTxs:                  bd.NumNetworkTxs,
		RequiredConfirmations:   requiredConfirmations,
		EstimatedArrivalMinutes: etaMinutes,
		Detail:                  bd.NetworkFeeDetail,
	}
}

func buildLiveOfferAPI(o liveOutOffer, sub liveSubscription) liveOfferAPI {
	requiredConfirmations := tradecost.ResolveRequiredConfirmations(o.Direction, sub.requiredConfirmations)
	api := liveOfferAPI{
		ID:             o.ID,
		Exchange:       o.Exchange,
		OfferID:        derefStringOrEmpty(o.OfferID),
		PaymentMethods: o.PaymentMethods,
		Direction:      o.Direction,
		Status: liveOfferStatus{
			IsLive:    o.IsLive,
			FirstSeen: o.FirstSeen,
			LastSeen:  o.LastSeen,
		},
		Pricing: liveOfferPricing{
			PriceType:         normalizePriceType(o.PriceType),
			FiatCurrency:      o.FiatCurrency,
			OfferPrice:        o.Price,
			RefPrice:          o.RefPrice,
			RawPremiumPercent: o.RawPremiumPct,
		},
		VolumeLimits: liveOfferVolumeLimits{
			BTCMin:  o.BTCMin,
			BTCMax:  o.BTCMax,
			FiatMin: o.FiatMin,
			FiatMax: o.FiatMax,
		},
		BTCNetworkFee: buildBTCLiveNetworkFee(o.CostBreakdown, requiredConfirmations),
		Analysis: liveOfferAnalysis{
			FullVolume: buildScenarioFromBreakdown(o.CostBreakdown, o.RefPrice, nil, o.TradeCostPct, o.NetPremiumPct, o.ManualRouteOptions),
		},
		Metadata:               liveOfferMetadata{ResolvedRoute: o.ResolvedRoute},
		SellerRatingScore:      o.SellerRatingScore,
		SellerRatingConfidence: o.SellerRatingConfidence,
		SellerRatingRaw:        o.SellerRatingRaw,
	}
	attachHedgelessOnly(&api.Analysis.FullVolume, o.HedgelessOnlyBreakdown, o.RefPrice)
	if api.BTCNetworkFee.Method == "" && o.HedgelessOnlyBreakdown != nil {
		api.BTCNetworkFee = buildBTCLiveNetworkFee(*o.HedgelessOnlyBreakdown, requiredConfirmations)
	}
	if o.CostBreakdownCapped != nil && o.TradeCostPctCapped != nil && o.NetPremiumPctCapped != nil {
		capFiat := tradecost.Round2(o.CostBreakdownCapped.BTCAmount * o.RefPrice)
		manualOptions := tradecost.ManualRouteOptions{}
		if o.ManualRouteOptionsCapped != nil {
			manualOptions = *o.ManualRouteOptionsCapped
		}
		api.Analysis.CappedVolume = ptrScenario(buildScenarioFromBreakdown(*o.CostBreakdownCapped, o.RefPrice, &capFiat, *o.TradeCostPctCapped, *o.NetPremiumPctCapped, manualOptions))
		attachHedgelessOnly(api.Analysis.CappedVolume, o.HedgelessOnlyCapped, o.RefPrice)
	}
	if o.CostBreakdownMin != nil && o.TradeCostPctMin != nil && o.NetPremiumPctMin != nil {
		manualOptions := tradecost.ManualRouteOptions{}
		if o.ManualRouteOptionsMin != nil {
			manualOptions = *o.ManualRouteOptionsMin
		}
		api.Analysis.MinVolume = ptrScenario(buildScenarioFromBreakdown(*o.CostBreakdownMin, o.RefPrice, nil, *o.TradeCostPctMin, *o.NetPremiumPctMin, manualOptions))
		attachHedgelessOnly(api.Analysis.MinVolume, o.HedgelessOnlyMin, o.RefPrice)
	}
	return api
}

func ptrScenario(s liveOfferScenario) *liveOfferScenario {
	return &s
}

func computeDirectionalPremiumPct(direction string, offerPrice, refPrice float64) float64 {
	return lifecycle.DirectionalPremiumPct(direction, offerPrice, refPrice)
}

func selectBTCTradeSize(btcMax, btcMin *float64) float64 {
	if btcMax != nil && *btcMax > 0 {
		return *btcMax
	}
	if btcMin != nil && *btcMin > 0 {
		return *btcMin
	}
	return tradecost.DefaultBTCTradeSize()
}

func routeSizeForSettings(o LiveOffer, refPrice float64, settings transactionalRoutingSettings) (float64, bool) {
	btcSize := selectBTCTradeSize(o.BTCMax, o.BTCMin)
	if btcSize <= 0 {
		return 0, false
	}
	if settings.CustomAmount != nil && *settings.CustomAmount > 0 && refPrice > 0 && strings.EqualFold(o.FiatCurrency, settings.FiatCurrency) {
		btcSize = math.Min(btcSize, *settings.CustomAmount/refPrice)
	}
	if o.BTCMin != nil && btcSize+1e-12 < *o.BTCMin {
		return 0, false
	}
	return btcSize, true
}

func resolvedRouteFromBreakdown(bd tradecost.CostBreakdown, fiatCurrency string, settings transactionalRoutingSettings) *resolvedRoute {
	if bd.RouteCEX == "" || bd.BTCAmount <= 0 {
		return nil
	}
	route := &resolvedRoute{
		ConfigVersion: settings.ConfigVersion, TradeMode: settings.TradeMode,
		TargetDirection: settings.TargetDirection, MarketSide: bd.TradeDirection,
		Provider: bd.RouteCEX, Settlement: bd.RouteMethod,
		FiatCurrency: fiatCurrency, BTCAmount: bd.BTCAmount,
		FiatAmount:           tradecost.Round2(bd.BTCAmount * bd.Capital.BTCPrice),
		RequiredCapital:      bd.Capital.TotalFiat,
		RequiredBTCLiquidity: bd.Capital.BTCLiquidityBTC,
		SecurityDepositBTC:   bd.Capital.SecurityDepositBTC,
		TotalCostBTC:         bd.TotalCostBTC, TotalCostPercent: bd.TotalCostPct,
		NetPremiumPercent: bd.EstimatedProfitPct,
	}
	route.NeedsHub = route.Settlement == "onchain_hub" || route.Settlement == "onchain_swap"
	route.NeedsLightning = route.Settlement == "onchain_swap"
	if route.NeedsLightning {
		route.RoutingFeeAllowanceSat = int64(math.Ceil(bd.Conversion.RoutingFeeBTC * 1e8))
		if route.RoutingFeeAllowanceSat <= 0 {
			route.RoutingFeeAllowanceSat = 100
		}
		route.CEXWithdrawalFeeBTC = bd.WithdrawalFeeBTC
		route.SwapSafetyHeadroomBTC = 0.00001
	}
	if settings.TradeMode == tradecost.TradeModeHedgefullSingleCollateral {
		route.HedgeVenue = bd.Hedge.Venue
		route.HedgeSide = bd.Hedge.Side
		route.HedgeNotional = bd.BTCAmount
		if bd.Hedge.Position != nil {
			route.HedgeNotional = bd.BTCAmount * bd.Hedge.Position.HedgeRatio
			route.Leverage = bd.Hedge.Position.LeverageUsed
		}
	}
	return route
}

func computeNetPremiumFieldsFull(premiumPct float64, btcSize float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedSpotCEXes, excludedHedgeCEXes []string, feeTier string, coupledHedge bool, realDepositPct *float64) (float64, float64, tradecost.CostBreakdown) {
	// Always preserve the offer's executable trade size on the breakdown, even when
	// we skip cost routing (non-positive premium / no optimizer / no route). Otherwise
	// analysis.full_volume.trade_size_* becomes 0 and the live Size column shows 0€
	// despite volume_limits still carrying a real btc_max/fiat_max.
	emptySize := tradecost.CostBreakdown{BTCAmount: btcSize}
	if tradeRouteOptimizer == nil || premiumPct <= 0 {
		return 0, 0, emptySize
	}

	requiredConfirmations = tradecost.ResolveRequiredConfirmations(direction, requiredConfirmations)
	bd := tradeRouteOptimizer.BestBreakdownFullWithExclusions(btcSize, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, feeTier, coupledHedge, realDepositPct)
	if bd.RouteCEX == "" {
		return 0, 0, emptySize
	}
	netPremium := math.Round((premiumPct-bd.TotalCostPct)*100) / 100
	bd.EstimatedProfitPct = netPremium
	bd.EstimatedProfitBTC = math.Round((bd.BTCAmount*(netPremium/100))*1e8) / 1e8
	return bd.TotalCostPct, netPremium, bd
}

// computeHedgelessOnlyBreakdown prices a spot-only route when hedgefull routing
// cannot find any hedge venue. Bid offers are converted to hedgeless double-
// collateral economics; ask offers keep the spot-only cost shape.
func computeHedgelessOnlyBreakdown(premiumPct float64, btcSize float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedSpotCEXes, excludedHedgeCEXes []string, feeTier string, coupledHedge bool, realDepositPct *float64) *tradecost.CostBreakdown {
	if tradeRouteOptimizer == nil || premiumPct <= 0 || btcSize <= 0 {
		return nil
	}
	requiredConfirmations = tradecost.ResolveRequiredConfirmations(direction, requiredConfirmations)
	spot := tradeRouteOptimizer.BestBreakdownSpotOnlyWithExclusions(btcSize, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, feeTier, coupledHedge, realDepositPct)
	if spot.RouteCEX == "" {
		return nil
	}
	bd := spot
	if spot.TradeDirection == "bid" {
		if hedgeless := tradecost.DeriveHedgelessDoubleCollateral(spot); hedgeless.RouteCEX != "" {
			bd = hedgeless
		}
	}
	netPremium := math.Round((premiumPct-bd.TotalCostPct)*100) / 100
	bd.EstimatedProfitPct = netPremium
	bd.EstimatedProfitBTC = math.Round((bd.BTCAmount*(netPremium/100))*1e8) / 1e8
	return &bd
}

func attachHedgelessOnly(scenario *liveOfferScenario, hedgelessOnly *tradecost.CostBreakdown, refPrice float64) {
	if scenario == nil || hedgelessOnly == nil || hedgelessOnly.RouteCEX == "" {
		return
	}
	if scenario.HedgelessDoubleCollateral != nil {
		return
	}
	variant := buildModeVariantFromBreakdown(*hedgelessOnly, refPrice)
	scenario.HedgelessDoubleCollateral = &variant
	// Keep size metadata usable even when the hedgefull parent route is empty.
	if scenario.TradeSizeBTC <= 0 {
		scenario.TradeSizeBTC = hedgelessOnly.BTCAmount
		scenario.TradeSizeFiat = tradecost.Round2(hedgelessOnly.BTCAmount * refPrice)
	}
}

func computeManualRouteOptions(btcSize float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedSpotCEXes, excludedHedgeCEXes []string, feeTier string) tradecost.ManualRouteOptions {
	if tradeRouteOptimizer == nil || btcSize <= 0 {
		return tradecost.ManualRouteOptions{}
	}
	return tradeRouteOptimizer.ManualRouteOptionsForTradeWithExclusions(btcSize, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, feeTier)
}

// LiveHub holds live offers in memory and broadcasts to WebSocket subscribers
// on every CEX price tick.
type LiveHub struct {
	offersMu  sync.RWMutex
	offers    []LiveOffer
	lifecycle map[int64][]LifecyclePoint // keyed by offer DB ID; protected by offersMu

	subsMu      sync.Mutex
	subscribers map[chan []byte]liveSubscription

	capabilitiesMu     sync.RWMutex
	excludedSpotCEXes  string
	excludedHedgeCEXes string
	routingSettingsMu  sync.RWMutex
	routingSettings    transactionalRoutingSettings
}

var liveRefreshMu sync.Mutex

type liveSubscription struct {
	cex                   string
	fiat                  string
	requiredConfirmations int
	excludedSpotCEXes     string // comma-joined for map key comparability
	excludedHedgeCEXes    string
	feeTier               string
	coupledHedge          bool
	maxFiatLimitEUR       float64 // 0 = disabled
}

var hub = &LiveHub{
	lifecycle:   make(map[int64][]LifecyclePoint),
	subscribers: make(map[chan []byte]liveSubscription),
}

// setOffers replaces the in-memory offer list and broadcasts to all subscribers.
func (h *LiveHub) setOffers(offers []LiveOffer) {
	h.offersMu.Lock()
	h.offers = offers
	h.offersMu.Unlock()
	appMetrics.setOffersInHub(len(offers))
	h.broadcastAll()
}

// refreshLiveHub atomically replaces the cached book only after PostgreSQL has
// returned a complete result. A failed refresh deliberately leaves the previous
// book visible.
func refreshLiveHub(ctx context.Context, pushed []LiveOffer) error {
	if db == nil {
		return nil
	}
	liveRefreshMu.Lock()
	defer liveRefreshMu.Unlock()

	offers, err := newLiveOfferRepository(db).Load(ctx)
	if err != nil {
		return err
	}
	if len(pushed) > 0 {
		makers := make(map[int64]string, len(pushed))
		for _, offer := range pushed {
			if offer.MakerID != "" {
				makers[offer.ID] = offer.MakerID
			}
		}
		for i := range offers {
			offers[i].MakerID = makers[offers[i].ID]
		}
	}
	hub.setOffers(offers)
	return nil
}

func (h *LiveHub) getOffers() []LiveOffer {
	h.offersMu.RLock()
	defer h.offersMu.RUnlock()
	cp := make([]LiveOffer, len(h.offers))
	copy(cp, h.offers)
	return cp
}

// setCapabilityExclusions updates the server-authoritative routing overlay.
// Keeping it separate from subscription preferences means policy and credential
// changes also affect clients that already have a WebSocket connection open.
func (h *LiveHub) setCapabilityExclusions(spot, hedge []string) {
	spotJoined := strings.Join(spot, ",")
	hedgeJoined := strings.Join(hedge, ",")
	h.capabilitiesMu.Lock()
	changed := h.excludedSpotCEXes != spotJoined || h.excludedHedgeCEXes != hedgeJoined
	h.excludedSpotCEXes = spotJoined
	h.excludedHedgeCEXes = hedgeJoined
	h.capabilitiesMu.Unlock()
	if changed {
		h.broadcastAll()
	}
}

func (h *LiveHub) capabilityExclusions() (spot, hedge []string) {
	h.capabilitiesMu.RLock()
	defer h.capabilitiesMu.RUnlock()
	return parseExcludedCEXes(h.excludedSpotCEXes), parseExcludedCEXes(h.excludedHedgeCEXes)
}

func (h *LiveHub) setTransactionalRoutingSettings(settings transactionalRoutingSettings) {
	h.routingSettingsMu.Lock()
	changed := !routingSettingsEqual(h.routingSettings, settings)
	h.routingSettings = settings
	h.routingSettingsMu.Unlock()
	if changed {
		h.broadcastAll()
	}
}

func routingSettingsEqual(a, b transactionalRoutingSettings) bool {
	if a.FiatCurrency != b.FiatCurrency || a.TradeMode != b.TradeMode || a.TargetDirection != b.TargetDirection || a.ConfigVersion != b.ConfigVersion {
		return false
	}
	if a.CustomAmount == nil || b.CustomAmount == nil {
		return a.CustomAmount == nil && b.CustomAmount == nil
	}
	return *a.CustomAmount == *b.CustomAmount
}

func (h *LiveHub) transactionalRoutingSettings() transactionalRoutingSettings {
	h.routingSettingsMu.RLock()
	defer h.routingSettingsMu.RUnlock()
	return h.routingSettings
}

// subscribe registers a new WebSocket client with a CEX preference.
func (h *LiveHub) subscribe(sub liveSubscription) chan []byte {
	ch := make(chan []byte, 8)
	h.subsMu.Lock()
	h.subscribers[ch] = sub
	h.subsMu.Unlock()
	appMetrics.incWSClient()
	return ch
}

// unsubscribe removes a client and closes its channel.
func (h *LiveHub) unsubscribe(ch chan []byte) {
	h.subsMu.Lock()
	delete(h.subscribers, ch)
	h.subsMu.Unlock()
	appMetrics.decWSClient()
	close(ch)
}

// broadcastForCex broadcasts to all subscribers that use the given CEX,
// regardless of their other settings. Called on every price tick.
func (h *LiveHub) broadcastForCex(cex string) {
	h.subsMu.Lock()
	configSet := make(map[liveSubscription]struct{}, 4)
	for _, sub := range h.subscribers {
		if sub.cex == cex {
			configSet[sub] = struct{}{}
		}
	}
	h.subsMu.Unlock()

	for sub := range configSet {
		h.broadcastForConfig(sub)
	}
}

// broadcastForConfig computes premiums for the given config and pushes to matching subscribers.
func (h *LiveHub) broadcastForConfig(sub liveSubscription) {
	price := getCexPrice(sub.cex)
	data := h.computePayload(price, sub)
	if data == nil {
		return
	}
	appMetrics.observeBroadcast()
	h.subsMu.Lock()
	for ch, s := range h.subscribers {
		if s == sub {
			select {
			case ch <- data:
			default: // drop if subscriber is too slow
			}
		}
	}
	h.subsMu.Unlock()
}

// broadcastAll broadcasts to every subscriber grouped by their config.
// Called after offer data changes (new snapshot).
func (h *LiveHub) broadcastAll() {
	// Collect unique configs among active subscribers
	h.subsMu.Lock()
	configSet := make(map[liveSubscription]struct{}, 4)
	for _, sub := range h.subscribers {
		configSet[sub] = struct{}{}
	}
	h.subsMu.Unlock()

	// Always include the default so the first push broadcasts even before any subscriber
	if len(configSet) == 0 {
		configSet[liveSubscription{cex: defaultCEX}] = struct{}{}
	}

	for sub := range configSet {
		h.broadcastForConfig(sub)
	}
}

// parseExcludedCEXes splits the comma-joined excluded CEX string back into a slice.
func parseExcludedCEXes(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

// computeOutOffers returns the enriched offer slice for a given price and subscription config.
// If the selected CEX has no price yet, falls back to the default CEX price so
// that price_diff_percent is always meaningful and offers remain visible.
func (h *LiveHub) computeOutOffers(price float64, sub liveSubscription) []liveOutOffer {
	if price <= 0 {
		price = getCexPrice(defaultCEX)
	}
	offers := h.getOffers()
	h.offersMu.RLock()
	lc := h.lifecycle
	h.offersMu.RUnlock()
	policySpotCEXes, policyHedgeCEXes := h.capabilityExclusions()
	settings := h.transactionalRoutingSettings()
	excludedSpotCEXes := mergeCEXLists(parseExcludedCEXes(sub.excludedSpotCEXes), policySpotCEXes)
	excludedHedgeCEXes := mergeCEXLists(parseExcludedCEXes(sub.excludedHedgeCEXes), policyHedgeCEXes)
	result := make([]liveOutOffer, 0, len(offers))
	for _, o := range offers {
		offerFiat := o.FiatCurrency
		if offerFiat == "" {
			offerFiat = "EUR" // compatibility with pre-multi-fiat snapshot payloads
		}
		if sub.fiat != "" && !strings.EqualFold(offerFiat, sub.fiat) {
			continue
		}
		offerPrice := price
		if !strings.EqualFold(offerFiat, "EUR") && o.ReferencePrice > 0 {
			offerPrice = o.ReferencePrice
		}
		if offerPrice <= 0 {
			continue
		}
		pct := computeDirectionalPremiumPct(o.Direction, o.Price, offerPrice)
		btcSize := selectBTCTradeSize(o.BTCMax, o.BTCMin)
		tradeCostPct, netPremium, bd := computeNetPremiumFieldsFull(pct, btcSize, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)
		manualOptions := computeManualRouteOptions(btcSize, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier)
		var selectedRoute *resolvedRoute
		if pct > 0 && settings.ConfigVersion > 0 {
			if routeSize, ok := routeSizeForSettings(o, offerPrice, settings); ok {
				var routeBD tradecost.CostBreakdown
				if settings.TradeMode == tradecost.TradeModeHedgelessDoubleCollateral {
					if hedgeless := computeHedgelessOnlyBreakdown(pct, routeSize, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct); hedgeless != nil {
						routeBD = *hedgeless
					}
				} else {
					_, _, routeBD = computeNetPremiumFieldsFull(pct, routeSize, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)
				}
				selectedRoute = resolvedRouteFromBreakdown(routeBD, offerFiat, settings)
			}
		}
		var hedgelessOnly *tradecost.CostBreakdown
		if bd.RouteCEX == "" {
			if only := computeHedgelessOnlyBreakdown(pct, btcSize, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct); only != nil {
				if only.TradeDirection == "ask" {
					// Asks have no hedgeless trade mode; spot-only is the priced route.
					bd = *only
					tradeCostPct = only.TotalCostPct
					netPremium = only.EstimatedProfitPct
				} else {
					hedgelessOnly = only
				}
			}
		}

		// If a max fiat limit is set and the offer's full size exceeds it, compute a second
		// breakdown capped to that fiat amount so the frontend can show both side by side.
		var bdCapped *tradecost.CostBreakdown
		var hedgelessOnlyCapped *tradecost.CostBreakdown
		var manualOptionsCapped *tradecost.ManualRouteOptions
		var abortCapped *tradecost.AbortCostBreakdown
		var tcCapped, npCapped *float64
		var profitBTCCapped, profitFiatCapped *float64
		if sub.maxFiatLimitEUR > 0 && offerPrice > 0 {
			btcCap := sub.maxFiatLimitEUR / offerPrice
			if btcSize > btcCap {
				tc2, np2, bd2 := computeNetPremiumFieldsFull(pct, btcCap, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)
				bdCapped = &bd2
				if bd2.RouteCEX == "" {
					hedgelessOnlyCapped = computeHedgelessOnlyBreakdown(pct, btcCap, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)
				}
				options := computeManualRouteOptions(btcCap, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier)
				manualOptionsCapped = &options
				abort2 := tradecost.AbortCostFromBreakdown(bd2)
				abortCapped = &abort2
				tcCapped = &tc2
				npCapped = &np2
				profitBTC := bd2.EstimatedProfitBTC
				profitFiat := tradecost.Round2(profitBTC * offerPrice)
				profitBTCCapped = &profitBTC
				profitFiatCapped = &profitFiat
			}
		}

		// If the offer has a minimum amount below its full size, compute a third
		// breakdown at that minimum so the frontend can show the smallest viable trade.
		var bdMin *tradecost.CostBreakdown
		var hedgelessOnlyMin *tradecost.CostBreakdown
		var manualOptionsMin *tradecost.ManualRouteOptions
		var tcMin, npMin *float64
		if o.BTCMin != nil && *o.BTCMin > 0 && *o.BTCMin < btcSize {
			tc3, np3, bd3 := computeNetPremiumFieldsFull(pct, *o.BTCMin, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)
			bdMin = &bd3
			if bd3.RouteCEX == "" {
				hedgelessOnlyMin = computeHedgelessOnlyBreakdown(pct, *o.BTCMin, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)
			}
			options := computeManualRouteOptions(*o.BTCMin, offerPrice, o.Exchange, o.Direction, sub.requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, sub.feeTier)
			manualOptionsMin = &options
			tcMin = &tc3
			npMin = &np3
		}

		result = append(result, liveOutOffer{
			ID:                        o.ID,
			Exchange:                  o.Exchange,
			FiatCurrency:              o.FiatCurrency,
			Direction:                 o.Direction,
			Price:                     o.Price,
			RawPremiumPct:             pct,
			PriceDiffPct:              pct,
			RefPrice:                  offerPrice,
			TradeCostPct:              tradeCostPct,
			NetPremiumPct:             netPremium,
			EstimatedProfitBTC:        bd.EstimatedProfitBTC,
			EstimatedProfitFiat:       tradecost.Round2(bd.EstimatedProfitBTC * offerPrice),
			CostBreakdown:             bd,
			CostBreakdownCapped:       bdCapped,
			AbortCost:                 tradecost.AbortCostFromBreakdown(bd),
			AbortCostCapped:           abortCapped,
			TradeCostPctCapped:        tcCapped,
			NetPremiumPctCapped:       npCapped,
			EstimatedProfitBTCCapped:  profitBTCCapped,
			EstimatedProfitFiatCapped: profitFiatCapped,
			CostBreakdownMin:          bdMin,
			HedgelessOnlyBreakdown:    hedgelessOnly,
			HedgelessOnlyCapped:       hedgelessOnlyCapped,
			HedgelessOnlyMin:          hedgelessOnlyMin,
			ManualRouteOptions:        manualOptions,
			ManualRouteOptionsCapped:  manualOptionsCapped,
			ManualRouteOptionsMin:     manualOptionsMin,
			TradeCostPctMin:           tcMin,
			NetPremiumPctMin:          npMin,
			BTCMin:                    o.BTCMin,
			BTCMax:                    o.BTCMax,
			FiatMin:                   o.FiatMin,
			FiatMax:                   o.FiatMax,
			PaymentMethods:            o.PaymentMethods,
			OfferID:                   o.OfferID,
			FirstSeen:                 o.FirstSeen,
			LastSeen:                  o.LastSeen,
			IsLive:                    true,
			PriceType:                 o.PriceType,
			SellerRatingScore:         o.SellerRatingScore,
			SellerRatingConfidence:    o.SellerRatingConfidence,
			SellerRatingRaw:           o.SellerRatingRaw,
			Lifecycle:                 lc[o.ID],
			ResolvedRoute:             selectedRoute,
		})
	}
	return result
}

// computePayload builds the JSON payload for a given subscription config.
// Returns nil on marshal error.
func (h *LiveHub) computePayload(price float64, sub liveSubscription) []byte {
	internal := h.computeOutOffers(price, sub)
	result := make([]liveOfferAPI, 0, len(internal))
	for _, offer := range internal {
		result = append(result, buildLiveOfferAPI(offer, sub))
	}
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Printf("[LiveHub] marshal error: %v\n", err)
		return nil
	}
	return data
}

func handleLiveOfferLifecycleREST(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid offer id", http.StatusBadRequest)
		return
	}

	hub.offersMu.RLock()
	if pts, ok := hub.lifecycle[id]; ok {
		hub.offersMu.RUnlock()
		if pts == nil {
			pts = []LifecyclePoint{}
		}
		writeJSON(w, pts)
		return
	}
	hub.offersMu.RUnlock()

	lifecycle := fetchOfferLifecycle(r.Context(), []int64{id})
	pts := lifecycle[id]
	if pts == nil {
		pts = []LifecyclePoint{}
	}
	writeJSON(w, pts)
}

// handleLiveOffersWS is the WebSocket handler. It sends the current state
// immediately on connect, then streams every subsequent broadcast.
// Accepts optional ?cex=, ?fee_tier=, ?exclude_cexes= query params.
func handleLiveOffersWS(w http.ResponseWriter, r *http.Request) {
	if err := refreshLiveHub(r.Context(), nil); err != nil {
		fmt.Printf("[LiveWS] live book refresh failed; serving cached book: %v\n", err)
	}
	if err := refreshLiveCapabilityPolicy(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "refresh MM CEX policy; routing fail-closed", "err", err)
	}
	spotExclusions := queryExcludedSpotCEXes(r)
	hedgeExclusions := queryExcludedHedgeCEXes(r)
	sub := liveSubscription{
		cex:                   queryCex(r),
		fiat:                  queryFiat(r),
		requiredConfirmations: queryConfirmations(r),
		excludedSpotCEXes:     strings.Join(spotExclusions, ","),
		excludedHedgeCEXes:    strings.Join(hedgeExclusions, ","),
		feeTier:               queryFeeTier(r),
		coupledHedge:          queryCoupledHedge(r),
		maxFiatLimitEUR:       queryMaxFiatLimitEUR(r),
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[LiveWS] upgrade error: %v\n", err)
		return
	}
	defer ws.Close()

	ch := hub.subscribe(sub)
	defer hub.unsubscribe(ch)

	// Respond to pings so the browser doesn't drop the connection.
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Read loop — needed to process control frames (ping/close) from the client.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Send current state immediately on connect.
	if payload := hub.computePayload(getCexPrice(sub.cex), sub); payload != nil {
		if err := ws.WriteMessage(websocket.TextMessage, payload); err != nil {
			return
		}
	}

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-done:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
