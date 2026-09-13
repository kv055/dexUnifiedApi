package main

import (
	"commons/fiat"
	"commons/logging"
	"commons/paymentmethods"
	"commons/tracing"
	"context"
	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/config"
	"dexunifiedapi/internal/lifecycle"
	"dexunifiedapi/internal/sellerrating"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bisqgrpc "dexunifiedapi/bisq_grpc/go"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// serviceName is the OpenTelemetry/logging identity for this binary.
const serviceName = "unified-snapshot"

// stage runs fn inside a child span of ctx so each phase of a snapshot run shows
// up as its own timed span under snapshot.run.
func stage(ctx context.Context, name string, fn func()) {
	_, span := tracing.Tracer("snapshot").Start(ctx, name)
	defer span.End()
	fn()
}

var (
	adminDatabaseURL string
	databaseURL      string
	bisqGRPCHost     string
	bisqAPIPassword  string
	bisq2RESTHost    string
	bisq2APIPassword string
	priceProvider    string
	torProxy         string
	robosatsOnions   = []string{
		"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion",
		"http://mmhaqzuirth5rx7gl24d4773lknltjhik57k7ahec5iefktezv4b3uid.onion",
		"http://4t4jxmivv6uqej6xzx2jx3fxh75gtt65v3szjoqmc4ugdlhipzdat6yd.onion",
		"http://otmoonrndnrddqdlhu6b36heunmbyw3cgvadqo2oqeau3656wfv7fwad.onion",
	}
)

const (
	// defaultRobosatsFederationURL is the upstream registry used by the
	// RoboSats client to discover the current federated coordinators.
	defaultRobosatsFederationURL             = "https://raw.githubusercontent.com/RoboSats/robosats/main/frontend/static/federation.json"
	defaultRobosatsFederationRefreshInterval = 6 * time.Hour
	defaultRobosatsFederationCachePath       = "/tmp/robosats-federation.json"
)

func init() {
	if runningUnderGoTest() {
		_ = config.LoadRequiredDotEnv(".env")
		return
	}
	if err := config.LoadRequiredDotEnv(".env"); err != nil {
		panic(err)
	}
	config.RequireEnvSet(
		"POSTGRES_UNIFIED_API_PASSWORD",
		"POSTGRES_MONITOR_PASSWORD",
		"POSTGRES_MM_CONTROLLER_PASSWORD",
		"POSTGRES_FIAT_CONTROLLER_PASSWORD",
	)
	adminDatabaseURL = config.PostgresAdminURLFromEnv()
	databaseURL = config.PostgresURLFromEnv()
	bisqGRPCHost = os.Getenv("BISQ_GRPC_HOST")
	if bisqGRPCHost == "" {
		bisqGRPCHost = "localhost:9998"
	}
	bisqAPIPassword = os.Getenv("BISQ_API_PASSWORD")
	bisq2RESTHost = os.Getenv("BISQ2_REST_HOST")
	bisq2APIPassword = os.Getenv("BISQ2_API_PASSWORD")
	priceProvider = os.Getenv("PRICE_PROVIDER")
	if priceProvider == "" {
		priceProvider = "bisq_tor"
	}
	torProxy = os.Getenv("TOR_PROXY_URL")
	if torProxy == "" {
		torProxy = "socks5h://127.0.0.1:9050"
	}
}

func runningUnderGoTest() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}

// bisqCreds injects the Bisq API password header on every gRPC call.
type bisqCreds struct{ password string }

// GetRequestMetadata retrieves a request metadata.
func (c bisqCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"password": c.password}, nil
}

// RequireTransportSecurity reports whether the credentials require transport security.
func (c bisqCreds) RequireTransportSecurity() bool { return false }

// --- Offer type ---

type offer struct {
	OfferID        string   `json:"offer_id,omitempty"`
	FiatCurrency   string   `json:"fiat_currency,omitempty"`
	Price          float64  `json:"price"`
	PriceDiffPct   *float64 `json:"price_diff_percent,omitempty"`
	BTCMin         *float64 `json:"btc_min,omitempty"`
	BTCMax         *float64 `json:"btc_max,omitempty"`
	FiatMin        *int     `json:"fiat_min,omitempty"`
	FiatMax        *int     `json:"fiat_max,omitempty"`
	PaymentMethods []string `json:"payment_methods,omitempty"`
	PriceType      *string  `json:"price_type,omitempty"`
	// SecurityDepositBTC/Pct are the deposit or bond the taker of this offer
	// must lock, in BTC and as a fraction of the trade amount. Nil for
	// exchanges with no deposit/bond concept (bisq2, hodlhodl, peachbitcoin).
	SecurityDepositBTC *float64 `json:"security_deposit_btc,omitempty"`
	SecurityDepositPct *float64 `json:"security_deposit_pct,omitempty"`
	// CounterpartyStakeBTC/Pct are the maker's lock on this offer (Bisq 1
	// maker deposit, RoboSats fidelity bond). Nil when the venue has no lock.
	CounterpartyStakeBTC *float64 `json:"counterparty_stake_btc,omitempty"`
	CounterpartyStakePct *float64 `json:"counterparty_stake_pct,omitempty"`
	// SellerRating* are the maker's reputation normalized across exchanges by
	// internal/sellerrating. Score is 0-100 (nil when no signal is obtainable,
	// e.g. RoboSats, unstaked Bisq 2, empty feedback); Confidence records which
	// methodology produced it; Raw preserves the exchange-specific inputs.
	// Set via applyRating.
	SellerRatingScore      *float64        `json:"seller_rating_score,omitempty"`
	SellerRatingConfidence *string         `json:"seller_rating_confidence,omitempty"`
	SellerRatingRaw        json.RawMessage `json:"seller_rating_raw,omitempty"`
	// SellerRatingSignals holds venue extras that are not inputs to the 0-100
	// mapper (account age, payment times, Bisq 2 sell-limit, etc.). Nil when
	// the venue has nothing extra (RoboSats, Bisq 1).
	SellerRatingSignals json.RawMessage `json:"seller_rating_signals,omitempty"`
	// MakerID is the exchange's seller identity, sent to the unified-api server
	// so it can look up a per-maker reputation for live Peach offers (the
	// listing omits the rating).
	MakerID              string    `json:"maker_id,omitempty"`
	VenueNodePubKey      string    `json:"venue_node_pubkey,omitempty"`
	VenueNodeName        string    `json:"venue_node_name,omitempty"`
	SourceEventID        string    `json:"source_event_id,omitempty"`
	SourceEventCreatedAt time.Time `json:"source_event_created_at,omitempty"`
	ProtocolVersion      int       `json:"protocol_version,omitempty"`
}

// applyRating copies a normalized sellerrating.Score onto an offer. Every
// fetcher calls this once per offer so the three rating fields are populated
// consistently (including the bisq v1 Unavailable() case).
func applyRating(o *offer, s sellerrating.Score) {
	o.SellerRatingScore = s.Value
	o.SellerRatingConfidence = s.ConfidencePtr()
	o.SellerRatingRaw = s.Raw
}

// fetchStatus is whether a market census can be used to close absent offers.
// Complete is the only status that may confirm a market empty or missing ids.
type fetchStatus string

const (
	fetchStatusComplete   fetchStatus = "complete"
	fetchStatusIncomplete fetchStatus = "incomplete"
	fetchStatusFailed     fetchStatus = "failed"
)

type fetchResult struct {
	BTCPrice float64
	Offers   []offer
	Status   fetchStatus
}

func completeFetch(price float64, offers []offer) *fetchResult {
	return &fetchResult{BTCPrice: price, Offers: offers, Status: fetchStatusComplete}
}

func incompleteFetch(price float64, offers []offer) *fetchResult {
	return &fetchResult{BTCPrice: price, Offers: offers, Status: fetchStatusIncomplete}
}

func (r *fetchResult) status() fetchStatus {
	if r == nil || r.Status == fetchStatusFailed {
		return fetchStatusFailed
	}
	if r.Status == fetchStatusIncomplete {
		return fetchStatusIncomplete
	}
	return fetchStatusComplete
}

// externalOfferDirection translates the canonical stored side to the legacy
// buy/sell vocabulary required by DEX APIs that have not adopted bid/ask.
func externalOfferDirection(direction string) string {
	if strings.EqualFold(strings.TrimSpace(direction), "ask") {
		return "sell"
	}
	return "buy"
}

type exchFetcher struct {
	name string
	fn   func(context.Context, string, string, int) *fetchResult
}

type exchangeSnapshotData map[string]map[string]bool

// liveMarket identifies the smallest independently fetchable offer book. A
// successful snapshot of one market can prove offers absent only from that
// market; failures elsewhere must not change its live state.
type liveMarket struct {
	exchange  string
	fiat      string
	direction string
}

type livePayload struct {
	ID                 int64    `json:"id"`
	Exchange           string   `json:"exchange"`
	FiatCurrency       string   `json:"fiat_currency"`
	Direction          string   `json:"direction"`
	Price              float64  `json:"price"`
	ReferencePrice     float64  `json:"reference_price"`
	BTCMin             *float64 `json:"btc_min"`
	BTCMax             *float64 `json:"btc_max"`
	FiatMin            *int64   `json:"fiat_min"`
	FiatMax            *int64   `json:"fiat_max"`
	PaymentMethods     []string `json:"payment_methods"`
	OfferID            *string  `json:"offer_id"`
	FirstSeen          string   `json:"first_seen"`
	LastSeen           string   `json:"last_seen"`
	PriceType          *string  `json:"price_type"`
	SecurityDepositBTC *float64 `json:"security_deposit_btc"`
	SecurityDepositPct *float64 `json:"security_deposit_pct"`
	// CounterpartyStake* is the maker's lock, independent of the taker deposit
	// used for capital. Nil when the venue has no lock.
	CounterpartyStakeBTC *float64 `json:"counterparty_stake_btc"`
	CounterpartyStakePct *float64 `json:"counterparty_stake_pct"`
	// SellerRating* carry the normalized maker reputation to the live feed so
	// the frontend can show it alongside each offer (see internal/sellerrating).
	SellerRatingScore      *float64        `json:"seller_rating_score"`
	SellerRatingConfidence *string         `json:"seller_rating_confidence"`
	SellerRatingRaw        json.RawMessage `json:"seller_rating_raw,omitempty"`
	SellerRatingSignals    json.RawMessage `json:"seller_rating_signals,omitempty"`
	// MakerID lets the server look up a per-maker reputation for live Peach
	// offers (its listing omits the rating).
	MakerID              string     `json:"maker_id,omitempty"`
	VenueNodePubKey      string     `json:"venue_node_pubkey,omitempty"`
	VenueNodeName        string     `json:"venue_node_name,omitempty"`
	SourceEventID        string     `json:"source_event_id,omitempty"`
	SourceEventCreatedAt *time.Time `json:"source_event_created_at,omitempty"`
	ProtocolVersion      int        `json:"protocol_version,omitempty"`
}

func priceTypeFixed() *string    { s := "fixed"; return &s }
func priceTypeFloating() *string { s := "floating"; return &s }
func priceTypeUnknown() *string  { s := "unknown"; return &s }

func normalizeHodlHodlPriceType(priceSource string) *string {
	switch priceSource {
	case "fixed_value":
		return priceTypeFixed()
	case "exchange_rate":
		return priceTypeFloating()
	default:
		return priceTypeUnknown()
	}
}

// --- HTTP clients ---

func torClient() *http.Client {
	proxyURL, _ := url.Parse(torProxy)
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: tracing.Transport(&http.Transport{
			Proxy:       http.ProxyURL(proxyURL),
			DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		}),
	}
}

func clearnetClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: tracing.Transport(nil)}
}

// --- Exchange fetchers ---

func fetchBisq(ctx context.Context, direction string, btcPrice int) *fetchResult {
	return fetchBisqCurrency(ctx, direction, "EUR", btcPrice)
}

func fetchBisqCurrency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult {
	if btcPrice == 0 {
		fmt.Println("[Bisq] Could not get reference price")
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(bisqGRPCHost,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bisqCreds{password: bisqAPIPassword}),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		fmt.Printf("[Bisq] Connection error: %v\n", err)
		return nil
	}
	defer conn.Close()

	stub := bisqgrpc.NewOffersClient(conn)
	reply, err := stub.GetOffers(ctx, &bisqgrpc.GetOffersRequest{
		Direction:    strings.ToUpper(externalOfferDirection(direction)),
		CurrencyCode: currency,
	})
	if err != nil {
		fmt.Printf("[Bisq] Error getting %s offers: %v\n", direction, err)
		return nil
	}

	var offers []offer
	for _, o := range reply.Offers {
		mapped := mapBisqOffer(o, direction, time.Now())
		mapped.FiatCurrency = currency
		offers = append(offers, mapped)
	}

	fmt.Printf("[Bisq] Found %d %s offers\n", len(offers), direction)
	return completeFetch(float64(btcPrice), offers)
}

// mapBisqOffer converts a bisq v1 gRPC OfferInfo into our offer struct.
// Pure — no I/O — so the field mapping is unit-testable without a daemon.
func mapBisqOffer(o *bisqgrpc.OfferInfo, direction string, now time.Time) offer {
	price, _ := strconv.ParseFloat(o.Price, 64)
	btcMin := float64(o.MinAmount) / 1e8
	btcMax := float64(o.Amount) / 1e8
	fiatMin := int(parseFloatStr(o.MinVolume))
	fiatMax := int(parseFloatStr(o.Volume))
	pt := priceTypeFixed()
	if o.UseMarketBasedPrice {
		pt = priceTypeFloating()
	}
	// The taker plays the opposite side of the maker: on an "ask" offer
	// (maker sells BTC) the taker buys, so they post the buyer deposit;
	// on a "bid" offer the taker sells, so they post the seller deposit.
	// Mirrors the isBid mapping in tradecost.computeCapitalRequirement.
	// Counterparty stake is the maker's lock — the other deposit field.
	takerSats := o.BuyerSecurityDeposit
	makerSats := o.SellerSecurityDeposit
	if direction == "bid" {
		takerSats = o.SellerSecurityDeposit
		makerSats = o.BuyerSecurityDeposit
	}
	depositBTCPtr, depositPctPtr := securityDeposit(takerSats, btcMax)
	stakeBTCPtr, stakePctPtr := securityDeposit(makerSats, btcMax)
	newOffer := offer{
		OfferID:              o.Id,
		FiatCurrency:         "EUR",
		Price:                price,
		BTCMin:               &btcMin,
		BTCMax:               &btcMax,
		FiatMin:              &fiatMin,
		FiatMax:              &fiatMax,
		PaymentMethods:       []string{o.PaymentMethodShortName},
		PriceType:            pt,
		SecurityDepositBTC:   depositBTCPtr,
		SecurityDepositPct:   depositPctPtr,
		CounterpartyStakeBTC: stakeBTCPtr,
		CounterpartyStakePct: stakePctPtr,
	}
	// Bisq v1's trust signal is its account-signing web of trust (sign-state
	// tier + account age). Stock gRPC OfferInfo strips it (it lives in
	// OfferPayload.extra_data); our bisq-patches/offerinfo-witness.patch adds
	// the two fields below. An unpatched daemon sends "" for the sign-state,
	// which NormalizeBisqAccountSigning maps to Unavailable() — so this stays
	// safe before/during the patched-image rollout.
	applyRating(&newOffer, sellerrating.NormalizeBisqAccountSigning(
		o.GetAccountAgeWitnessSignState(), o.GetAccountAgeWitnessDate(), now))
	return newOffer
}

func fetchBisq2Currency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult {
	if btcPrice == 0 {
		fmt.Println("[Bisq2] Could not get reference price")
		return nil
	}
	if bisq2RESTHost == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf("http://%s/api/v1/offerbook/markets/%s/offers", bisq2RESTHost, url.PathEscape(currency))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		fmt.Printf("[Bisq2] Build request error: %v\n", err)
		return nil
	}
	if bisq2APIPassword != "" {
		req.Header.Set("Authorization", "Bearer "+bisq2APIPassword)
	}

	resp, err := clearnetClient().Do(req)
	if err != nil {
		fmt.Printf("[Bisq2] Error fetching offers: %v\n", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[Bisq2] Unexpected status %d\n", resp.StatusCode)
		return nil
	}

	body, _ := readAllLimited(resp.Body)

	// Offer fields live under bisqEasyOffer in the offerbook listing response.
	// All quote-side amounts are integer fiat units in 4-decimal precision
	// (e.g. 1000000 == 100.0000 EUR). Fixed prices use priceQuote.value with
	// the same 4-decimal precision.
	var raw []struct {
		BisqEasyOffer struct {
			ID         string `json:"id"`
			Direction  string `json:"direction"`
			AmountSpec struct {
				Type      string `json:"type"`
				Amount    int64  `json:"amount"`
				MinAmount int64  `json:"minAmount"`
				MaxAmount int64  `json:"maxAmount"`
			} `json:"amountSpec"`
			PriceSpec struct {
				Type       string  `json:"type"`
				Percentage float64 `json:"percentage"`
				PriceQuote struct {
					Value int64 `json:"value"`
				} `json:"priceQuote"`
			} `json:"priceSpec"`
			QuoteSidePaymentMethodSpecs []struct {
				PaymentMethod string `json:"paymentMethod"`
			} `json:"quoteSidePaymentMethodSpecs"`
		} `json:"bisqEasyOffer"`
		// reputationScore is a sibling of bisqEasyOffer in each list item; the
		// Bisq2 node computes it with the same ReputationService the desktop UI
		// uses (burned/bonded BSQ + imported bisq1 account age). fiveSystemScore
		// is the 0-5 star value shown to users. Present in the response body we
		// already read — previously discarded because the struct omitted it.
		ReputationScore struct {
			TotalScore      int64   `json:"totalScore"`
			FiveSystemScore float64 `json:"fiveSystemScore"`
			Ranking         int     `json:"ranking"`
		} `json:"reputationScore"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		fmt.Printf("[Bisq2] Parse error: %v\n", err)
		return nil
	}

	wantDir := strings.ToUpper(externalOfferDirection(direction))
	var offers []offer
	for _, item := range raw {
		o := item.BisqEasyOffer
		if strings.ToUpper(o.Direction) != wantDir {
			continue
		}

		var fiatMinUnits, fiatMaxUnits int64
		switch o.AmountSpec.Type {
		case "QuoteSideFixedAmountSpec":
			fiatMinUnits = o.AmountSpec.Amount
			fiatMaxUnits = o.AmountSpec.Amount
		case "QuoteSideRangeAmountSpec":
			fiatMinUnits = o.AmountSpec.MinAmount
			fiatMaxUnits = o.AmountSpec.MaxAmount
		default:
			continue
		}

		var price float64
		var pt *string
		switch o.PriceSpec.Type {
		case "FixPriceSpec":
			price = float64(o.PriceSpec.PriceQuote.Value) / 10000.0
			pt = priceTypeFixed()
		case "FloatPriceSpec":
			price = float64(btcPrice) * (1 + o.PriceSpec.Percentage)
			pt = priceTypeFloating()
		case "MarketPriceSpec":
			price = float64(btcPrice)
			pt = priceTypeFloating()
		default:
			continue
		}

		if price == 0 {
			continue
		}

		fiatMin := int(fiatMinUnits / 10000)
		fiatMax := int(fiatMaxUnits / 10000)
		btcMin := float64(fiatMin) / price
		btcMax := float64(fiatMax) / price

		var methods []string
		for _, spec := range o.QuoteSidePaymentMethodSpecs {
			if m := strings.TrimSpace(spec.PaymentMethod); m != "" {
				methods = append(methods, m)
			}
		}

		newOffer := offer{
			OfferID:        o.ID,
			FiatCurrency:   currency,
			Price:          price,
			BTCMin:         &btcMin,
			BTCMax:         &btcMax,
			FiatMin:        &fiatMin,
			FiatMax:        &fiatMax,
			PaymentMethods: methods,
			PriceType:      pt,
		}
		rep := item.ReputationScore
		repRaw, _ := json.Marshal(map[string]any{
			"total_score":       rep.TotalScore,
			"five_system_score": rep.FiveSystemScore,
			"ranking":           rep.Ranking,
		})
		applyRating(&newOffer, sellerrating.NormalizeBisq2TotalScore(rep.TotalScore, repRaw))
		newOffer.SellerRatingSignals = mapBisq2Signals(bisq2Reputation{
			TotalScore:      rep.TotalScore,
			FiveSystemScore: rep.FiveSystemScore,
			Ranking:         rep.Ranking,
		}, &fiatMax)
		offers = append(offers, newOffer)
	}

	fmt.Printf("[Bisq2] Found %d %s offers\n", len(offers), direction)
	return completeFetch(float64(btcPrice), offers)
}

// bisq2Reputation is the reputationScore sibling on a Bisq 2 offerbook item.
type bisq2Reputation struct {
	TotalScore      int64
	FiveSystemScore float64
	Ranking         int
}

// bisq2OfficialFiveSystemScore is ReputationService.getFiveSystemScore. Used
// only to verify the node's fiveSystemScore against the published buckets —
// not as an input to our 0-100 mapper.
func bisq2OfficialFiveSystemScore(totalScore int64) float64 {
	switch {
	case totalScore < 1200:
		return 0
	case totalScore < 5000:
		return 0.5
	case totalScore < 15000:
		return 1
	case totalScore < 20000:
		return 1.5
	case totalScore < 25000:
		return 2
	case totalScore < 30000:
		return 2.5
	case totalScore < 35000:
		return 3
	case totalScore < 40000:
		return 3.5
	case totalScore < 60000:
		return 4
	case totalScore < 100000:
		return 4.5
	default:
		return 5
	}
}

func mapBisq2Signals(rep bisq2Reputation, offerFiatMax *int) json.RawMessage {
	maxSellUSD := float64(rep.TotalScore) / 200
	official := bisq2OfficialFiveSystemScore(rep.TotalScore)
	blob := map[string]any{
		"source":                      "bisq2",
		"ranking":                     rep.Ranking,
		"five_system_score":           rep.FiveSystemScore,
		"max_sell_usd":                maxSellUSD,
		"five_system_matches_buckets": math.Abs(official-rep.FiveSystemScore) < 1e-9,
	}
	if offerFiatMax != nil {
		blob["offer_fiat_max"] = *offerFiatMax
		blob["within_reputation_limit"] = float64(*offerFiatMax) <= maxSellUSD
	}
	return marshalSignals(blob)
}

// hodlHodlTrader is the maker reputation nested per-offer in the HodlHodl offer
// listing. Rating is a 0-1 fraction and is null for makers with no rating yet,
// so it MUST stay a pointer — a bare float64 would decode null to 0.0,
// indistinguishable from a genuinely terrible rating.
type hodlHodlTrader struct {
	TradesCount               int      `json:"trades_count"`
	Rating                    *float64 `json:"rating"`
	StrongHodler              bool     `json:"strong_hodler"`
	DaysSinceLastTrade        int      `json:"days_since_last_trade"`
	AveragePaymentTimeMinutes int      `json:"average_payment_time_minutes"`
	AverageReleaseTimeMinutes int      `json:"average_release_time_minutes"`
	BlockedBy                 int      `json:"blocked_by"`
	Verified                  bool     `json:"verified"`
	VerifiedBy                string   `json:"verified_by"`
	OnlineStatus              string   `json:"online_status"`
	ratingMalformed           bool
}

// UnmarshalJSON accepts the rating variants observed in HodlHodl's book:
// number, numeric string, and null. An invalid rating is isolated to that
// maker's reputation instead of rejecting the entire order book.
func (t *hodlHodlTrader) UnmarshalJSON(data []byte) error {
	var raw struct {
		TradesCount               int             `json:"trades_count"`
		Rating                    json.RawMessage `json:"rating"`
		StrongHodler              bool            `json:"strong_hodler"`
		DaysSinceLastTrade        int             `json:"days_since_last_trade"`
		AveragePaymentTimeMinutes int             `json:"average_payment_time_minutes"`
		AverageReleaseTimeMinutes int             `json:"average_release_time_minutes"`
		BlockedBy                 int             `json:"blocked_by"`
		Verified                  bool            `json:"verified"`
		VerifiedBy                string          `json:"verified_by"`
		OnlineStatus              string          `json:"online_status"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.TradesCount = raw.TradesCount
	t.StrongHodler = raw.StrongHodler
	t.DaysSinceLastTrade = raw.DaysSinceLastTrade
	t.AveragePaymentTimeMinutes = raw.AveragePaymentTimeMinutes
	t.AverageReleaseTimeMinutes = raw.AverageReleaseTimeMinutes
	t.BlockedBy = raw.BlockedBy
	t.Verified = raw.Verified
	t.VerifiedBy = raw.VerifiedBy
	t.OnlineStatus = raw.OnlineStatus
	t.Rating = nil
	t.ratingMalformed = false
	if len(raw.Rating) == 0 || string(raw.Rating) == "null" {
		return nil
	}
	var number float64
	if err := json.Unmarshal(raw.Rating, &number); err == nil {
		if !math.IsNaN(number) && !math.IsInf(number, 0) && number >= 0 && number <= 1 {
			t.Rating = &number
			return nil
		}
	}
	var numericString string
	if err := json.Unmarshal(raw.Rating, &numericString); err == nil {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(numericString), 64); err == nil {
			if !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed >= 0 && parsed <= 1 {
				t.Rating = &parsed
				return nil
			}
		}
	}
	t.ratingMalformed = true
	return nil
}

const (
	// strongHodlerBonus is a small bounded credit for the community "Strong
	// Hodler" vouch, capped so Value never exceeds 100. Only applied when a
	// real rating exists.
	hodlHodlStrongHodlerBonus = 2.0
)

// mapHodlHodlTrader normalizes a HodlHodl maker's reputation into a Score. Kept
// separate from the network call so it is unit-testable. Rating is already a
// 0-1 fraction, so no rescale is needed before the shared feedback formula.
// Nil or malformed ratings are Unavailable() — we do not mint a prior-only score.
func mapHodlHodlTrader(t hodlHodlTrader) sellerrating.Score {
	raw, _ := json.Marshal(map[string]any{
		"rating":        t.Rating,
		"trades_count":  t.TradesCount,
		"strong_hodler": t.StrongHodler,
	})
	if t.ratingMalformed || t.Rating == nil {
		return sellerrating.Unavailable()
	}
	s := sellerrating.NormalizeFeedbackRating(
		t.Rating, t.TradesCount,
		sellerrating.DefaultFeedbackPriorMean, sellerrating.DefaultFeedbackPriorWeight, sellerrating.DefaultFeedbackVerifiedThreshold,
		raw,
	)
	if t.StrongHodler && s.Value != nil {
		v := *s.Value + hodlHodlStrongHodlerBonus
		if v > 100 {
			v = 100
		}
		s.Value = &v
	}
	return s
}

func mapHodlHodlSignals(t hodlHodlTrader) json.RawMessage {
	return marshalSignals(map[string]any{
		"source":                       "hodlhodl",
		"trades_count":                 t.TradesCount,
		"days_since_last_trade":        t.DaysSinceLastTrade,
		"average_payment_time_minutes": t.AveragePaymentTimeMinutes,
		"average_release_time_minutes": t.AverageReleaseTimeMinutes,
		"blocked_by":                   t.BlockedBy,
		"verified":                     t.Verified,
		"verified_by":                  t.VerifiedBy,
		"strong_hodler":                t.StrongHodler,
		"online_status":                t.OnlineStatus,
	})
}

// normalizePeachMethod converts a Peach Bitcoin payment-method identifier
// (e.g. "sepa", "instantSepa", "nationalTransferDE", "mbWay") into a
// human-readable form. This is an exchange-specific PRE-normalizer (camelCase
// splitting, per-country nationalTransferXX collapse), not a second registry:
// its output still flows through paymentmethods.CanonicalizeAll before the
// offers upsert, so the payment_methods table remains the source of truth for
// merging and categorization.
func normalizePeachMethod(raw string) string {
	if raw == "" {
		return ""
	}
	// Collapse nationalTransferXX (trailing 2-letter country code) into a
	// single "nationalTransfer" so every per-country variant lands on one row.
	const ntPrefix = "nationalTransfer"
	if strings.HasPrefix(raw, ntPrefix) && len(raw) == len(ntPrefix)+2 {
		suffix := raw[len(ntPrefix):]
		if suffix[0] >= 'A' && suffix[0] <= 'Z' && suffix[1] >= 'A' && suffix[1] <= 'Z' {
			raw = ntPrefix
		}
	}
	// Split camelCase: insert a space before each upper that follows a lower.
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if i > 0 && c >= 'A' && c <= 'Z' {
			prev := raw[i-1]
			if prev >= 'a' && prev <= 'z' {
				b.WriteByte(' ')
			}
		}
		b.WriteByte(c)
	}
	// Title-case each word: first char upper, rest lower.
	parts := strings.Fields(b.String())
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	out := strings.Join(parts, " ")
	return out
}

// normalizeAllPeachMethods normalizes every Peach method string in the slice.
func normalizeAllPeachMethods(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if m := normalizePeachMethod(r); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// mapPeachOffer converts one Peach API offer into our internal offer schema.
// Peach uses fixed-size orders: amount is either a single satoshi value (sell) or
// [min, max] with min == max (buy). For sells, prices.EUR is the total fiat volume
// of the trade, not EUR/BTC.
func mapPeachOffer(o peachOffer, direction string, btcPrice int) (offer, bool) {
	return mapPeachOfferCurrency(o, direction, "EUR", btcPrice)
}

func mapPeachOfferCurrency(o peachOffer, direction, currency string, btcPrice int) (offer, bool) {
	methods, hasCurrency := o.MeansOfPayment[currency]
	if !hasCurrency || len(methods) == 0 {
		return offer{}, false
	}

	var btcMin, btcMax float64
	var arr []int64
	if err := json.Unmarshal(o.Amount, &arr); err == nil && len(arr) >= 1 {
		btcMin = round8(float64(arr[0]) / 1e8)
		if len(arr) >= 2 {
			btcMax = round8(float64(arr[1]) / 1e8)
		} else {
			btcMax = btcMin
		}
	} else {
		var single int64
		if err := json.Unmarshal(o.Amount, &single); err != nil {
			return offer{}, false
		}
		btcMin = round8(float64(single) / 1e8)
		btcMax = btcMin
	}
	if btcMax <= 0 {
		return offer{}, false
	}

	var fiatMin, fiatMax int
	var price float64

	switch direction {
	case "ask":
		currencyFiat, ok := o.Prices[currency]
		if !ok || currencyFiat <= 0 {
			return offer{}, false
		}
		fiatMin = int(math.Round(currencyFiat))
		fiatMax = fiatMin
		price = currencyFiat / btcMax
	default: // buy
		if btcPrice <= 0 {
			return offer{}, false
		}
		price = float64(btcPrice)
		fiat := int(math.Round(btcMax * price))
		fiatMin = fiat
		fiatMax = fiat
	}

	result := offer{
		OfferID:        o.ID,
		FiatCurrency:   currency,
		Price:          price,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		FiatMin:        &fiatMin,
		FiatMax:        &fiatMax,
		PaymentMethods: normalizeAllPeachMethods(methods),
		PriceType:      priceTypeFixed(),
		// Peach's offer listing has no seller rating; the maker id is carried to
		// the unified-api server, which looks the rating up for live Peach offers
		// (see cmd/server/peach_reputation.go).
		MakerID: o.User.ID,
	}
	return result, true
}

type peachOffer struct {
	ID             string              `json:"id"`
	Amount         json.RawMessage     `json:"amount"` // int64 (sell) or [min,max] int64 array (buy)
	MeansOfPayment map[string][]string `json:"meansOfPayment"`
	Prices         map[string]float64  `json:"prices"`
	// User carries only the maker id inline; reputation lives behind a separate
	// GET /v1/user/:id call, done by the server for live Peach offers.
	User struct {
		ID string `json:"id"`
	} `json:"user"`
}

const (
	peachSearchPageSize = 100
	// peachSearchMaxPages bounds a stuck remaining>0 book so we neither loop
	// forever nor publish a truncated result as authoritative.
	peachSearchMaxPages = 50
)

func fetchPeachBitcoinCurrency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult {
	return fetchPeachBitcoinCurrencyFrom(ctx, clearnetClient(), "https://api.peachbitcoin.com", direction, currency, btcPrice)
}

func fetchPeachBitcoinCurrencyFrom(ctx context.Context, client *http.Client, baseURL, direction, currency string, btcPrice int) *fetchResult {
	if btcPrice == 0 {
		fmt.Println("[PeachBitcoin] Could not get reference price")
		return nil
	}

	type peachResponse struct {
		Offers    []peachOffer `json:"offers"`
		Total     int          `json:"total"`
		Remaining int          `json:"remaining"`
	}

	var allOffers []peachOffer
	complete := false
	for page := 0; page < peachSearchMaxPages; page++ {
		reqURL := fmt.Sprintf("%s/v1/offer/search?page=%d&size=%d&sortBy=lowestPremium",
			strings.TrimRight(baseURL, "/"), page, peachSearchPageSize)
		body, err := json.Marshal(map[string]any{"type": direction})
		if err != nil {
			fmt.Printf("[PeachBitcoin] marshal error: %v\n", err)
			return peachResult(allOffers, direction, currency, btcPrice, false)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(body)))
		if err != nil {
			fmt.Printf("[PeachBitcoin] Request error: %v\n", err)
			return peachResult(allOffers, direction, currency, btcPrice, false)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("[PeachBitcoin] Error: %v\n", err)
			return peachResult(allOffers, direction, currency, btcPrice, false)
		}
		respBody, readErr := readAllLimited(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			fmt.Printf("[PeachBitcoin] Response read error: %v\n", readErr)
			return peachResult(allOffers, direction, currency, btcPrice, false)
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("[PeachBitcoin] HTTP %d: %s\n", resp.StatusCode, string(respBody))
			return peachResult(allOffers, direction, currency, btcPrice, false)
		}

		var pr peachResponse
		if err := json.Unmarshal(respBody, &pr); err != nil {
			fmt.Printf("[PeachBitcoin] Parse error: %v — body: %s\n", err, string(respBody))
			return peachResult(allOffers, direction, currency, btcPrice, false)
		}
		allOffers = append(allOffers, pr.Offers...)
		if pr.Remaining <= 0 {
			complete = true
			break
		}
	}
	if !complete {
		fmt.Printf("[PeachBitcoin] pagination incomplete after %d pages\n", peachSearchMaxPages)
	}
	return peachResult(allOffers, direction, currency, btcPrice, complete)
}

func peachResult(all []peachOffer, direction, currency string, btcPrice int, complete bool) *fetchResult {
	if !complete && len(all) == 0 {
		return nil
	}
	var offers []offer
	for _, o := range all {
		mapped, ok := mapPeachOfferCurrency(o, direction, currency, btcPrice)
		if !ok {
			continue
		}
		offers = append(offers, mapped)
	}
	fmt.Printf("[PeachBitcoin] Found %d %s offers\n", len(offers), direction)
	if complete {
		return completeFetch(float64(btcPrice), offers)
	}
	return incompleteFetch(float64(btcPrice), offers)
}

func fetchRobosatsCurrency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult {
	return fetchRobosatsCurrencyFrom(ctx, direction, currency, btcPrice, torClient(), robosatsOnions)
}

// robosatsFederationEntry is the subset of the upstream federation registry
// needed to discover mainnet coordinator order books.
type robosatsFederationEntry struct {
	Federated bool `json:"federated"`
	Mainnet   struct {
		Onion string `json:"onion"`
	} `json:"mainnet"`
}

type robosatsFederationCache struct {
	FetchedAt time.Time `json:"fetched_at"`
	ETag      string    `json:"etag,omitempty"`
	Onions    []string  `json:"onions"`
}

// fetchRobosatsFederation returns the current federated mainnet coordinator
// onions. The registry is downloaded over HTTPS; coordinator APIs themselves
// remain onion-only and are fetched through Tor.
func fetchRobosatsFederation(ctx context.Context, client *http.Client, registryURL string) ([]string, error) {
	onions, _, _, err := fetchRobosatsFederationFrom(ctx, client, registryURL, "")
	return onions, err
}

// fetchRobosatsFederationFrom conditionally fetches the registry. notModified
// is true only when the caller's cached ETag remains current.
func fetchRobosatsFederationFrom(ctx context.Context, client *http.Client, registryURL, etag string) ([]string, string, bool, error) {
	registry, err := url.Parse(strings.TrimSpace(registryURL))
	if err != nil || registry.Scheme != "https" || registry.Host == "" {
		return nil, "", false, fmt.Errorf("invalid RoboSats federation URL")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registry.String(), nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("build federation request: %w", err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("fetch federation registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("federation registry returned HTTP %d", resp.StatusCode)
	}
	body, err := readAllLimited(resp.Body)
	if err != nil {
		return nil, "", false, fmt.Errorf("read federation registry: %w", err)
	}

	var entries map[string]robosatsFederationEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, "", false, fmt.Errorf("decode federation registry: %w", err)
	}

	unique := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.Federated {
			continue
		}
		onion, ok := validRobosatsOnion(entry.Mainnet.Onion)
		if ok {
			unique[onion] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return nil, "", false, fmt.Errorf("federation registry contains no valid federated mainnet onions")
	}

	onions := make([]string, 0, len(unique))
	for onion := range unique {
		onions = append(onions, onion)
	}
	sort.Strings(onions)
	return onions, resp.Header.Get("ETag"), false, nil
}

func validRobosatsOnion(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if u.Port() != "" || !strings.HasSuffix(host, ".onion") || len(host) != 62 {
		return "", false
	}
	return "http://" + host, true
}

func readRobosatsFederationCache(path string) (*robosatsFederationCache, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := readAllLimited(f)
	if err != nil {
		return nil, err
	}
	var cache robosatsFederationCache
	if err := json.Unmarshal(body, &cache); err != nil {
		return nil, err
	}
	unique := make(map[string]struct{}, len(cache.Onions))
	for _, raw := range cache.Onions {
		onion, ok := validRobosatsOnion(raw)
		if ok {
			unique[onion] = struct{}{}
		}
	}
	if cache.FetchedAt.IsZero() || len(unique) == 0 {
		return nil, fmt.Errorf("cache has no valid coordinator list")
	}
	cache.Onions = make([]string, 0, len(unique))
	for onion := range unique {
		cache.Onions = append(cache.Onions, onion)
	}
	sort.Strings(cache.Onions)
	return &cache, nil
}

func writeRobosatsFederationCache(path string, cache robosatsFederationCache) error {
	body, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".robosats-federation-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func robosatsFederationRefreshInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ROBOSATS_FEDERATION_REFRESH_INTERVAL"))
	if raw == "" {
		return defaultRobosatsFederationRefreshInterval
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		slog.Warn("invalid ROBOSATS_FEDERATION_REFRESH_INTERVAL; using default", "value", raw, "default", defaultRobosatsFederationRefreshInterval)
		return defaultRobosatsFederationRefreshInterval
	}
	return interval
}

// loadRobosatsCoordinators uses a cached registry between refreshes. A failed
// refresh retains the last known-good cache; only an empty cache uses fallback.
func loadRobosatsCoordinators(ctx context.Context, client *http.Client, registryURL, cachePath string, refreshInterval time.Duration, fallback []string) []string {
	cache, cacheErr := readRobosatsFederationCache(cachePath)
	if cacheErr == nil && time.Since(cache.FetchedAt) < refreshInterval {
		slog.InfoContext(ctx, "using cached RoboSats federation coordinators", "coordinator_count", len(cache.Onions), "cached_at", cache.FetchedAt)
		return cache.Onions
	}

	etag := ""
	if cache != nil {
		etag = cache.ETag
	}
	onions, newETag, notModified, err := fetchRobosatsFederationFrom(ctx, client, registryURL, etag)
	if err != nil {
		if cache != nil {
			slog.WarnContext(ctx, "RoboSats federation refresh failed; retaining cached coordinators", "err", err, "coordinator_count", len(cache.Onions), "cached_at", cache.FetchedAt)
			return cache.Onions
		}
		slog.WarnContext(ctx, "RoboSats federation discovery failed; using fallback coordinators", "err", err, "fallback_count", len(fallback))
		return append([]string(nil), fallback...)
	}
	if notModified {
		onions = cache.Onions
	}
	updated := robosatsFederationCache{FetchedAt: time.Now().UTC(), ETag: newETag, Onions: onions}
	if err := writeRobosatsFederationCache(cachePath, updated); err != nil {
		slog.WarnContext(ctx, "could not cache RoboSats federation coordinators", "err", err)
	}
	slog.InfoContext(ctx, "RoboSats federation discovery succeeded", "coordinator_count", len(onions), "registry_url", registryURL, "not_modified", notModified)
	return onions
}

func decodeRobosatsBook(body []byte) ([]json.RawMessage, string, error) {
	var rawOffers []json.RawMessage
	if err := json.Unmarshal(body, &rawOffers); err == nil {
		return rawOffers, "array", nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, "invalid", err
	}
	if _, ok := object["not_found"]; ok {
		return []json.RawMessage{}, "not_found", nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return nil, "error_object", fmt.Errorf("unknown response object keys: %s", strings.Join(keys, ","))
}

func fetchRobosatsCurrencyFrom(ctx context.Context, direction, currency string, btcPrice int, client *http.Client, coordinators []string) *fetchResult {
	currencyID := fiat.RoboSatsCurrencyID(currency)
	if currencyID == 0 {
		return nil
	}
	offerType := 1 // sell
	if direction == "bid" {
		offerType = 0
	}

	byID := make(map[string]offer)
	okCount := 0
	failCount := 0
	for _, onion := range coordinators {
		reqURL := fmt.Sprintf("%s/api/book/?currency=%d&type=%d", onion, currencyID, offerType)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			fmt.Printf("[Robosats] coordinator=%s request build failed: %v, trying next...\n", onion, err)
			failCount++
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("[Robosats] coordinator=%s request failed: %v, trying next...\n", onion, err)
			failCount++
			continue
		}
		body, readErr := readAllLimited(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			fmt.Printf("[Robosats] coordinator=%s status=%d response_type=read_error: %v, trying next...\n", onion, resp.StatusCode, readErr)
			failCount++
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			fmt.Printf("[Robosats] coordinator=%s status=%d response_type=http_error, trying next...\n", onion, resp.StatusCode)
			failCount++
			continue
		}

		rawOffers, responseType, err := decodeRobosatsBook(body)
		if err != nil {
			fmt.Printf("[Robosats] coordinator=%s status=%d response_type=%s: %v, trying next...\n", onion, resp.StatusCode, responseType, err)
			failCount++
			continue
		}

		offers := parseRobosatsOffers(rawOffers, currency, btcPrice)
		for _, o := range offers {
			if o.OfferID == "" {
				continue
			}
			byID[o.OfferID] = o
		}
		okCount++
		fmt.Printf("[Robosats] Found %d %s offers (coordinator=%s status=%d response_type=%s)\n", len(offers), direction, onion, resp.StatusCode, responseType)
	}

	if okCount == 0 {
		fmt.Println("[Robosats] All onion addresses failed")
		return nil
	}

	offers := make([]offer, 0, len(byID))
	for _, o := range byID {
		offers = append(offers, o)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].OfferID < offers[j].OfferID })
	if failCount > 0 {
		return incompleteFetch(float64(btcPrice), offers)
	}
	return completeFetch(float64(btcPrice), offers)
}

func parseRobosatsOffers(rawOffers []json.RawMessage, currency string, btcPrice int) []offer {
	var offers []offer
	for _, raw := range rawOffers {
		var s string
		if json.Unmarshal(raw, &s) == nil && s == "not_found" {
			break
		}

		var line struct {
			ID            json.Number  `json:"id"`
			Price         json.Number  `json:"price"`
			Premium       json.Number  `json:"premium"`
			Amount        *json.Number `json:"amount"`
			MinAmount     json.Number  `json:"min_amount"`
			MaxAmount     json.Number  `json:"max_amount"`
			PaymentMethod string       `json:"payment_method"`
			BondSize      json.Number  `json:"bond_size"`
		}
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}

		price, err := line.Price.Float64()
		if err != nil {
			continue
		}
		pt := priceTypeFixed()
		if price == 0 {
			premium, _ := line.Premium.Float64()
			if premium == 0 || btcPrice == 0 {
				continue
			}
			price = float64(btcPrice) * (1 + premium/100)
			pt = priceTypeFloating()
		}

		var fiatMin, fiatMax int
		if line.Amount != nil {
			v, _ := line.Amount.Float64()
			fiatMin = int(v)
			fiatMax = int(v)
		} else {
			minV, _ := line.MinAmount.Float64()
			maxV, _ := line.MaxAmount.Float64()
			fiatMin = int(minV)
			fiatMax = int(maxV)
		}

		btcMin := round8(float64(fiatMin) / price)
		btcMax := round8(float64(fiatMax) / price)

		bondPct, _ := line.BondSize.Float64()
		depositPct := bondPct / 100
		depositBTC := round8(depositPct * btcMax)
		stakeBTC := depositBTC
		stakePct := depositPct

		newOffer := offer{
			OfferID:              line.ID.String(),
			FiatCurrency:         currency,
			Price:                price,
			BTCMin:               &btcMin,
			BTCMax:               &btcMax,
			FiatMin:              &fiatMin,
			FiatMax:              &fiatMax,
			PaymentMethods:       []string{line.PaymentMethod},
			PriceType:            pt,
			SecurityDepositBTC:   &depositBTC,
			SecurityDepositPct:   &depositPct,
			CounterpartyStakeBTC: &stakeBTC,
			CounterpartyStakePct: &stakePct,
		}
		// Bond is skin-in-the-game on this offer (security_deposit_*), not a
		// maker reputation track record. Leave the rating Unavailable().
		applyRating(&newOffer, sellerrating.Unavailable())
		offers = append(offers, newOffer)
	}
	return offers
}

// --- Database ---

// unknownPaymentMethods accumulates canonical atoms that have no
// payment_methods row yet; flushed once per run as Others/inactive rows so
// they surface in the categorization panel instead of only in logs.
var unknownPaymentMethods = struct {
	sync.Mutex
	names map[string]struct{}
}{names: make(map[string]struct{})}

func collectUnknownPaymentMethods(methods []string) {
	reg := paymentmethods.Default()
	unknownPaymentMethods.Lock()
	defer unknownPaymentMethods.Unlock()
	for _, m := range methods {
		m = strings.TrimSpace(m)
		// Length cap guards against junky free-text labels (RoboSats) becoming
		// unbounded catalog rows.
		if m == "" || len(m) > 64 || reg.IsRegisteredCanonical(m) {
			continue
		}
		unknownPaymentMethods.names[m] = struct{}{}
	}
}

func flushUnknownPaymentMethods(ctx context.Context, sdb *dbutil.DB) {
	unknownPaymentMethods.Lock()
	names := make([]string, 0, len(unknownPaymentMethods.names))
	for name := range unknownPaymentMethods.names {
		names = append(names, name)
	}
	unknownPaymentMethods.names = make(map[string]struct{})
	unknownPaymentMethods.Unlock()
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	for _, name := range names {
		// Re-check the live catalog at flush time. The registry is loaded once
		// per snapshot, so an alias added while offers are being fetched must
		// still win over auto-registering the raw name as a new Others row.
		res, err := sdb.Exec(`
			INSERT INTO payment_methods (name, category)
			SELECT ?, 'Others'
			WHERE NOT EXISTS (
				SELECT 1
				FROM payment_methods pm
				WHERE lower(trim(replace(pm.name, '_', ' '))) = lower(trim(replace(?, '_', ' ')))
				   OR EXISTS (
					SELECT 1
					FROM unnest(pm.alias_list) AS existing_alias
					WHERE lower(trim(replace(existing_alias, '_', ' '))) = lower(trim(replace(?, '_', ' ')))
				   )
			)
			ON CONFLICT (name) DO NOTHING`, name, name, name)
		if err != nil {
			slog.ErrorContext(ctx, "failed to register unknown payment method", "name", name, "err", err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			slog.InfoContext(ctx, "registered unknown payment method as Others/inactive", "name", name)
		}
	}
}

// paymentMethodTouch aggregates first-seen offer hits for one catalog name
// within a snapshot run. Applied after unknown methods are flushed so new
// Others rows still receive currencies/sources/popularity from that run.
type paymentMethodTouch struct {
	count      int
	currencies map[string]struct{}
	sources    map[string]struct{}
}

var paymentMethodTouches = struct {
	sync.Mutex
	byName map[string]*paymentMethodTouch
}{byName: make(map[string]*paymentMethodTouch)}

func queuePaymentMethodTouches(methods []string, exchange, fiat string) {
	paymentMethodTouches.Lock()
	defer paymentMethodTouches.Unlock()
	seen := make(map[string]struct{}, len(methods))
	for _, m := range methods {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		t := paymentMethodTouches.byName[m]
		if t == nil {
			t = &paymentMethodTouch{
				currencies: make(map[string]struct{}),
				sources:    make(map[string]struct{}),
			}
			paymentMethodTouches.byName[m] = t
		}
		t.count++
		if fiat != "" {
			t.currencies[fiat] = struct{}{}
		}
		if exchange != "" {
			t.sources[exchange] = struct{}{}
		}
	}
}

func flushPaymentMethodTouches(ctx context.Context, sdb *dbutil.DB) {
	paymentMethodTouches.Lock()
	byName := paymentMethodTouches.byName
	paymentMethodTouches.byName = make(map[string]*paymentMethodTouch)
	paymentMethodTouches.Unlock()
	if len(byName) == 0 {
		return
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := byName[name]
		currencies := sortedStringSet(t.currencies)
		sources := sortedStringSet(t.sources)
		res, err := sdb.Exec(`
			UPDATE payment_methods SET
				currencies = (
					SELECT COALESCE(array_agg(DISTINCT x), '{}'::fiat_currency[])
					FROM unnest(currencies || ?::fiat_currency[]) AS x
				),
				sources = (
					SELECT COALESCE(array_agg(DISTINCT x), '{}'::dex_provider[])
					FROM unnest(sources || ?::dex_provider[]) AS x
				),
				popularity = popularity + ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE name = ?`,
			paymentmethods.TextArray(currencies),
			paymentmethods.TextArray(sources),
			t.count,
			name,
		)
		if err != nil {
			slog.ErrorContext(ctx, "failed to update payment method popularity",
				"name", name, "err", err)
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			slog.WarnContext(ctx, "payment method popularity touch matched no row",
				"name", name)
		}
	}
}

func sortedStringSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// saveSnapshot persists one non-empty market snapshot. It remains as a small
// compatibility wrapper for tests and callers that only need its live payload.
func saveSnapshot(sdb *dbutil.DB, exchange, fiat, direction string, btcPrice float64, offers []offer) []livePayload {
	live, _ := saveSnapshotPersisted(sdb, exchange, fiat, direction, btcPrice, offers)
	return live
}

// saveSnapshotPersisted reports whether the database transaction committed.
// Callers must not use a fetched response to close prior offers unless this is
// true: a source response that we failed to persist is not authoritative.
func saveSnapshotPersisted(sdb *dbutil.DB, exchange, fiat, direction string, btcPrice float64, offers []offer) ([]livePayload, bool) {
	if len(offers) == 0 {
		fmt.Printf("[%s] No offers to save\n", exchange)
		return nil, true
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[%s] DB error: %v\n", exchange, err)
		return nil, false
	}
	defer tx.Rollback()

	if _, err := tx.Exec("INSERT INTO snapshots (exchange, fiat_currency, direction, btc_price) VALUES (?, ?, ?, ?)",
		exchange, fiat, direction, btcPrice); err != nil {
		// A failed first write poisons the whole transaction in Postgres, so the
		// offer upserts below would all fail too. Bail loudly rather than silently
		// pushing id=0 offers to the hub (the bug that hid bisq2/peach for so long).
		fmt.Printf("[%s] snapshot insert failed (%s): %v\n", exchange, direction, err)
		return nil, false
	}

	liveOffers := make([]livePayload, 0, len(offers))
	seen := make(map[int64]bool, len(offers))
	updated, inserted, failed := 0, 0, 0
	// Collect payment methods from newly inserted offers; queue catalog
	// touches only after a successful commit so a rolled-back tx never bumps.
	var insertedMethods [][]string
	for _, o := range offers {
		var existingID int64
		var found bool

		// Identity resolution. When the exchange gives us a stable offer id (all of
		// them currently do), that id IS the offer's identity: match on it and, if it
		// is new, fall through to INSERT. The fingerprint is only a fallback for
		// genuinely id-less offers — it must NOT collapse a distinct id-bearing offer
		// onto a same-terms row. Peach is full of identical-terms offers (e.g. 91
		// distinct makers all offering 0.0021 BTC via Revolut); the old code fell back
		// to fingerprint whenever an id wasn't yet in the DB, so every new peach offer
		// re-merged onto the first row of its group every cycle, writing N identical
		// order_lifecycle rows and pushing N duplicate hub entries — the bloat that
		// OOM-killed the server (offer #989 got 83 rows every snapshot).
		if o.OfferID != "" {
			query := "SELECT id FROM offers WHERE exchange = ? AND fiat_currency = ? AND offer_id = ?"
			args := []any{exchange, fiat, o.OfferID}
			if exchange == "mostro" && o.VenueNodePubKey != "" {
				query += " AND venue_node_pubkey = ?"
				args = append(args, o.VenueNodePubKey)
			}
			err := tx.QueryRow(query, args...).Scan(&existingID)
			found = err == nil
		} else {
			err := tx.QueryRow(`SELECT id FROM offers
				WHERE exchange = ? AND fiat_currency = ? AND direction = ?
				  AND btc_min = ? AND btc_max = ?
				  AND offer_id IS NULL`,
				exchange, fiat, direction, o.BTCMin, o.BTCMax).Scan(&existingID)
			found = err == nil
		}

		// Belt-and-suspenders: if the same identity resolves twice within one scrape
		// (e.g. a duplicate id from the source), capture it once per cycle.
		if found && seen[existingID] {
			continue
		}

		live := livePayload{
			Exchange:               exchange,
			FiatCurrency:           fiat,
			Direction:              direction,
			Price:                  o.Price,
			ReferencePrice:         btcPrice,
			BTCMin:                 o.BTCMin,
			BTCMax:                 o.BTCMax,
			PriceType:              o.PriceType,
			SecurityDepositBTC:     o.SecurityDepositBTC,
			SecurityDepositPct:     o.SecurityDepositPct,
			CounterpartyStakeBTC:   o.CounterpartyStakeBTC,
			CounterpartyStakePct:   o.CounterpartyStakePct,
			SellerRatingScore:      o.SellerRatingScore,
			SellerRatingConfidence: o.SellerRatingConfidence,
			SellerRatingRaw:        o.SellerRatingRaw,
			SellerRatingSignals:    o.SellerRatingSignals,
			MakerID:                o.MakerID,
			VenueNodePubKey:        o.VenueNodePubKey,
			VenueNodeName:          o.VenueNodeName,
			SourceEventID:          o.SourceEventID,
			ProtocolVersion:        o.ProtocolVersion,
		}
		if !o.SourceEventCreatedAt.IsZero() {
			v := o.SourceEventCreatedAt
			live.SourceEventCreatedAt = &v
		}
		if o.FiatMin != nil {
			v := int64(*o.FiatMin)
			live.FiatMin = &v
		}
		if o.FiatMax != nil {
			v := int64(*o.FiatMax)
			live.FiatMax = &v
		}
		if methods := paymentmethods.CanonicalizeAll(o.PaymentMethods); len(methods) > 0 {
			o.PaymentMethods = methods
			live.PaymentMethods = methods
			collectUnknownPaymentMethods(methods)
		}
		if o.OfferID != "" {
			offerID := o.OfferID
			live.OfferID = &offerID
		}

		var upsertErr error
		if found {
			upsertErr = tx.QueryRow(`UPDATE offers SET
				last_seen = CURRENT_TIMESTAMP,
				price_type = ?,
				price = ?,
				btc_min = ?,
				btc_max = ?,
				fiat_min = ?,
				fiat_max = ?,
				payment_methods = ?,
				offer_id = COALESCE(offer_id, ?),
				security_deposit_btc = ?,
				security_deposit_pct = ?,
				counterparty_stake_btc = ?,
				counterparty_stake_pct = ?,
				seller_rating_score = ?,
				seller_rating_confidence = ?,
				seller_rating_raw = ?::jsonb,
				seller_rating_signals = ?::jsonb,
				maker_id = COALESCE(?, maker_id),
				venue_node_pubkey = COALESCE(?, venue_node_pubkey),
				venue_node_name = COALESCE(?, venue_node_name),
				source_event_id = COALESCE(?, source_event_id),
				source_event_created_at = COALESCE(?, source_event_created_at),
				venue_protocol_version = ?
				WHERE id = ?
				RETURNING id,
					to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
					to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`,
				o.PriceType, o.Price, o.BTCMin, o.BTCMax, o.FiatMin, o.FiatMax, nilIfEmptySlice(o.PaymentMethods), nilIfEmpty(o.OfferID),
				o.SecurityDepositBTC, o.SecurityDepositPct,
				o.CounterpartyStakeBTC, o.CounterpartyStakePct,
				o.SellerRatingScore, o.SellerRatingConfidence, nilIfEmptyRawMessage(o.SellerRatingRaw),
				nilIfEmptyRawMessage(o.SellerRatingSignals),
				nilIfEmpty(o.MakerID), nilIfEmpty(o.VenueNodePubKey), nilIfEmpty(o.VenueNodeName), nilIfEmpty(o.SourceEventID), nilIfZeroTime(o.SourceEventCreatedAt), nilIfZeroInt(o.ProtocolVersion), existingID,
			).Scan(&live.ID, &live.FirstSeen, &live.LastSeen)
		} else {
			upsertErr = tx.QueryRow(`INSERT INTO offers
				(exchange, fiat_currency, direction, btc_min, btc_max,
				 fiat_min, fiat_max, payment_methods, offer_id, price_type, price,
				 security_deposit_btc, security_deposit_pct,
				 counterparty_stake_btc, counterparty_stake_pct,
				 seller_rating_score, seller_rating_confidence, seller_rating_raw, seller_rating_signals, maker_id,
				 venue_node_pubkey, venue_node_name, source_event_id, source_event_created_at, venue_protocol_version)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?, ?, ?, ?, ?)
				RETURNING id,
					to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
					to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`,
				exchange, fiat, direction, o.BTCMin, o.BTCMax,
				o.FiatMin, o.FiatMax, nilIfEmptySlice(o.PaymentMethods), nilIfEmpty(o.OfferID), o.PriceType, o.Price,
				o.SecurityDepositBTC, o.SecurityDepositPct,
				o.CounterpartyStakeBTC, o.CounterpartyStakePct,
				o.SellerRatingScore, o.SellerRatingConfidence, nilIfEmptyRawMessage(o.SellerRatingRaw),
				nilIfEmptyRawMessage(o.SellerRatingSignals),
				nilIfEmpty(o.MakerID), nilIfEmpty(o.VenueNodePubKey), nilIfEmpty(o.VenueNodeName), nilIfEmpty(o.SourceEventID), nilIfZeroTime(o.SourceEventCreatedAt), nilIfZeroInt(o.ProtocolVersion),
			).Scan(&live.ID, &live.FirstSeen, &live.LastSeen)
		}
		if upsertErr != nil {
			// Skip rather than push an id=0 offer to the hub. Once one upsert fails
			// the transaction is poisoned, so the rest will fail too.
			failed++
			if failed == 1 {
				fmt.Printf("[%s] offer upsert failed (%s): %v\n", exchange, direction, upsertErr)
			}
			continue
		}
		if found {
			updated++
		} else {
			inserted++
			if len(o.PaymentMethods) > 0 {
				methodsCopy := append([]string(nil), o.PaymentMethods...)
				insertedMethods = append(insertedMethods, methodsCopy)
			}
		}
		seen[live.ID] = true
		liveOffers = append(liveOffers, live)
	}

	if err := tx.Commit(); err != nil {
		fmt.Printf("[%s] snapshot commit failed (%s): %v\n", exchange, direction, err)
		return nil, false
	}
	for _, methods := range insertedMethods {
		queuePaymentMethodTouches(methods, exchange, fiat)
	}
	if failed > 0 {
		fmt.Printf("[%s] %d new, %d updated, %d failed (%s)\n", exchange, inserted, updated, failed, direction)
	} else {
		fmt.Printf("[%s] %d new, %d updated (%s)\n", exchange, inserted, updated, direction)
	}
	return liveOffers, true
}

func recordFetchResult(exchangeData exchangeSnapshotData, exchange string, result *fetchResult) bool {
	if result == nil {
		return false
	}
	if exchangeData[exchange] == nil {
		exchangeData[exchange] = make(map[string]bool)
	}
	for _, o := range result.Offers {
		if o.OfferID != "" {
			exchangeData[exchange][o.OfferID] = true
		}
	}
	return true
}

func offlineExchangeNames(fetchers []exchFetcher, exchangeData exchangeSnapshotData) []string {
	names := make([]string, 0)
	for _, f := range fetchers {
		if _, ok := exchangeData[f.name]; !ok {
			names = append(names, f.name)
		}
	}
	return names
}

const defaultLiveOfferStaleGrace = 5 * time.Minute

// liveOfferStaleGrace is the maximum time an offer remains visible when its
// market cannot be fetched. A successful market snapshot always wins and
// closes absent offers immediately.
func liveOfferStaleGrace() time.Duration {
	raw := strings.TrimSpace(os.Getenv("LIVE_OFFER_STALE_GRACE"))
	if raw == "" {
		return defaultLiveOfferStaleGrace
	}
	grace, err := time.ParseDuration(raw)
	if err != nil || grace <= 0 {
		slog.Warn("invalid LIVE_OFFER_STALE_GRACE; using default", "value", raw, "default", defaultLiveOfferStaleGrace)
		return defaultLiveOfferStaleGrace
	}
	return grace
}

const clearLiveOfferClosureFields = `
		historical_analysis_status = NULL,
		avg_ref_price = NULL,
		avg_premium_pct = NULL,
		avg_trade_cost_pct = NULL,
		avg_trade_cost_btc = NULL,
		avg_hedge_fee_sum_btc = NULL,
		avg_net_premium_pct = NULL,
		avg_estimated_profit_btc = NULL,
		avg_estimated_profit_fiat = NULL,
		avg_btc_amount = NULL,
		avg_min_trade_cost_pct = NULL,
		avg_min_trade_cost_btc = NULL,
		avg_min_net_premium_pct = NULL,
		avg_min_estimated_profit_btc = NULL,
		avg_min_estimated_profit_fiat = NULL,
		avg_min_btc_amount = NULL,
		min_volume_analyzed = FALSE`

// reconcileLiveOfferStatus updates live state from persisted market snapshots
// in a single transaction. Only a Complete committed market can immediately
// close offers missing from it. Incomplete and Failed markets keep their last
// known offers until no Complete observation falls inside stale grace.
func reconcileLiveOfferStatus(sdb *dbutil.DB, snapshotStart time.Time, confirmed map[liveMarket]struct{}, staleGrace time.Duration) error {
	tx, err := sdb.Begin()
	if err != nil {
		return fmt.Errorf("begin live-status reconcile: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE offers SET
		is_live = TRUE,
		duration = NULL,`+clearLiveOfferClosureFields+`
		WHERE last_seen >= ?`, snapshotStart); err != nil {
		return fmt.Errorf("mark refreshed offers live: %w", err)
	}

	for market := range confirmed {
		if _, err := tx.Exec(`INSERT INTO live_market_observation (exchange, fiat_currency, direction, last_complete_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (exchange, fiat_currency, direction)
			DO UPDATE SET last_complete_at = EXCLUDED.last_complete_at`,
			market.exchange, market.fiat, market.direction, snapshotStart); err != nil {
			return fmt.Errorf("record complete observation for %s/%s/%s: %w", market.exchange, market.fiat, market.direction, err)
		}
		if _, err := tx.Exec(`UPDATE offers SET
			is_live = FALSE,
			duration = EXTRACT(EPOCH FROM (last_seen - first_seen))::BIGINT
			WHERE is_live = TRUE
			  AND exchange = ? AND fiat_currency = ? AND direction = ?
			  AND last_seen < ?`,
			market.exchange, market.fiat, market.direction, snapshotStart); err != nil {
			return fmt.Errorf("close absent offers for %s/%s/%s: %w", market.exchange, market.fiat, market.direction, err)
		}
	}

	staleCutoff := snapshotStart.Add(-staleGrace)
	if _, err := tx.Exec(`UPDATE offers o SET
		is_live = FALSE,
		duration = EXTRACT(EPOCH FROM (o.last_seen - o.first_seen))::BIGINT
		WHERE o.is_live = TRUE
		  AND o.last_seen < ?
		  AND NOT EXISTS (
			SELECT 1 FROM live_market_observation m
			WHERE m.exchange = o.exchange
			  AND m.fiat_currency = o.fiat_currency
			  AND m.direction = o.direction
			  AND m.last_complete_at >= ?
		  )`, staleCutoff, staleCutoff); err != nil {
		return fmt.Errorf("expire stale live offers: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit live-status reconcile: %w", err)
	}
	return nil
}

// fetchTimings accumulates per-exchange fetch effort across all markets in one
// run: summed market duration, outcomes and hard failures. Concurrent market
// durations intentionally remain summed effort rather than cycle wall time;
// snapshot_log.cycle_ms records the latter.
type fetchTimings struct {
	durationMS   map[string]int64
	failures     map[string]int
	statusCounts map[string]*exchangeStatusCounts
}

type exchangeStatusCounts struct {
	complete   int
	incomplete int
	failed     int
}

func newFetchTimings() *fetchTimings {
	return &fetchTimings{
		durationMS:   make(map[string]int64),
		failures:     make(map[string]int),
		statusCounts: make(map[string]*exchangeStatusCounts),
	}
}

func (t *fetchTimings) addStatus(exchange string, status fetchStatus) {
	counts := t.statusCounts[exchange]
	if counts == nil {
		counts = &exchangeStatusCounts{}
		t.statusCounts[exchange] = counts
	}
	switch status {
	case fetchStatusComplete:
		counts.complete++
	case fetchStatusIncomplete:
		counts.incomplete++
	default:
		counts.failed++
	}
}

func (t *fetchTimings) captureStatus(exchange string) fetchStatus {
	counts := t.statusCounts[exchange]
	if counts == nil {
		return fetchStatusComplete
	}
	if counts.failed > 0 && counts.complete == 0 && counts.incomplete == 0 {
		return fetchStatusFailed
	}
	if counts.incomplete > 0 || counts.failed > 0 {
		return fetchStatusIncomplete
	}
	return fetchStatusComplete
}

func buildSnapshotLogPayload(exchangeData exchangeSnapshotData, offline []string, timings *fetchTimings) ([]byte, int, error) {
	exchanges := make(map[string]any)
	total := 0
	for ex, ids := range exchangeData {
		idList := make([]string, 0, len(ids))
		for id := range ids {
			idList = append(idList, id)
		}
		sort.Strings(idList)
		exchanges[ex] = map[string]any{
			"count":       len(ids),
			"ids":         idList,
			"duration_ms": timings.durationMS[ex],
			"status":      string(timings.captureStatus(ex)),
		}
		total += len(ids)
	}
	// Hard-failed fetchers must stay present with count 0: dbmetrics derives
	// dex_exchange_up from this payload, and a missing key makes the series
	// disappear entirely — the down-alert then evaluates to NoData and stays
	// green instead of firing for the dead exchange.
	for _, ex := range offline {
		if _, ok := exchanges[ex]; !ok {
			exchanges[ex] = map[string]any{
				"count":       0,
				"ids":         []string{},
				"offline":     true,
				"status":      string(fetchStatusFailed),
				"duration_ms": timings.durationMS[ex],
			}
		}
	}
	for ex, fails := range timings.failures {
		if entry, ok := exchanges[ex].(map[string]any); ok {
			entry["failed_fetches"] = fails
		}
	}
	jsonBytes, err := json.Marshal(exchanges)
	return jsonBytes, total, err
}

func writeSnapshotLog(sdb *dbutil.DB, exchangeData exchangeSnapshotData, offline []string, timings *fetchTimings, cycle time.Duration) {
	jsonBytes, total, err := buildSnapshotLogPayload(exchangeData, offline, timings)
	if err != nil {
		slog.Error("snapshot log encode error", "err", err)
		return
	}
	sdb.Exec("INSERT INTO snapshot_log (exchanges, offers_count, cycle_ms) VALUES (?::jsonb, ?, ?)",
		string(jsonBytes), total, cycle.Milliseconds())
}

// --- Main ---

func main() {
	cycleStart := time.Now()
	logging.Init(serviceName)
	tpShutdown, err := tracing.Init(context.Background(), serviceName)
	if err != nil {
		slog.Warn("tracing init failed; continuing without traces", "err", err)
	}
	// This binary runs one snapshot and exits, so flushing spans before exit is
	// essential — without it the batch processor may never export this run.
	defer func() { _ = tpShutdown(context.Background()) }()

	ctx, span := tracing.Tracer("snapshot").Start(context.Background(), "snapshot.run")
	defer span.End()

	slog.InfoContext(ctx, "starting orderbook snapshot")
	resetNIP69Cache()
	registryURL := strings.TrimSpace(os.Getenv("ROBOSATS_FEDERATION_URL"))
	if registryURL == "" {
		registryURL = defaultRobosatsFederationURL
	}
	robosatsOnions = loadRobosatsCoordinators(ctx, clearnetClient(), registryURL, defaultRobosatsFederationCachePath, robosatsFederationRefreshInterval(), robosatsOnions)

	refPrices := make(map[string]int, len(fiat.Enabled))
	for _, currency := range fiat.Enabled {
		if price := fetchReferencePrice(strings.ToLower(currency)); price > 0 {
			refPrices[currency] = price
			slog.InfoContext(ctx, "reference price", "fiat", currency, "price", price)
		} else {
			slog.WarnContext(ctx, "could not get reference price; skipping currency", "fiat", currency)
		}
	}
	if len(refPrices) == 0 {
		slog.ErrorContext(ctx, "could not get any reference price, aborting")
		os.Exit(1)
	}

	adminDB, err := dbutil.Open(adminDatabaseURL)
	if err != nil {
		slog.ErrorContext(ctx, "failed to open admin database", "err", err)
		os.Exit(1)
	}
	defer adminDB.Close()

	if err := adminDB.Ping(); err != nil {
		slog.ErrorContext(ctx, "failed to ping admin database", "err", err)
		os.Exit(1)
	}
	if err := dbutil.EnsureAppRoles(adminDB, dbutil.RoleBootstrapConfig{
		DatabaseName:           config.EnvOrDefault("POSTGRES_DB", "dexarb"),
		UnifiedAPIPassword:     config.RequireEnv("POSTGRES_UNIFIED_API_PASSWORD"),
		MonitorPassword:        config.RequireEnv("POSTGRES_MONITOR_PASSWORD"),
		MMControllerPassword:   config.RequireEnv("POSTGRES_MM_CONTROLLER_PASSWORD"),
		FiatControllerPassword: config.RequireEnv("POSTGRES_FIAT_CONTROLLER_PASSWORD"),
		GrafanaROPassword:      config.EnvOrDefault("POSTGRES_GRAFANA_RO_PASSWORD", ""),
	}); err != nil {
		slog.ErrorContext(ctx, "database admin setup error", "err", err)
		os.Exit(1)
	}

	sdb, err := dbutil.Open(databaseURL)
	if err != nil {
		slog.ErrorContext(ctx, "failed to open database", "err", err)
		os.Exit(1)
	}
	defer sdb.Close()

	if err := sdb.Ping(); err != nil {
		slog.ErrorContext(ctx, "failed to ping database", "err", err)
		os.Exit(1)
	}
	if err := dbutil.EnsureUnifiedSchema(sdb); err != nil {
		slog.ErrorContext(ctx, "failed to initialize schema", "err", err)
		os.Exit(1)
	}
	if err := dbutil.SeedPaymentMethods(sdb); err != nil {
		slog.ErrorContext(ctx, "failed to seed payment methods", "err", err)
		os.Exit(1)
	}
	// Canonicalization must run against the live payment_methods table, not the
	// built-in fallback — offers written with a stale registry would need a
	// re-snapshot to heal. Hard-fail like the schema step above.
	pmRows, err := paymentmethods.LoadRows(ctx, sdb.DB)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load payment methods registry", "err", err)
		os.Exit(1)
	}
	pmRegistry := paymentmethods.NewRegistry(pmRows)
	for _, w := range pmRegistry.Warnings() {
		slog.WarnContext(ctx, "payment methods registry", "warning", w)
	}
	paymentmethods.SetDefault(pmRegistry)

	// Fetch matching-currency Kraken candles before settlement. A missing market
	// only leaves that currency without historical settlement; it never reuses
	// EUR candles for a non-EUR offer.
	stage(ctx, "snapshot.fetch_ohlc", func() {
		for _, currency := range fiat.Enabled {
			if _, ok := refPrices[currency]; ok {
				fetchAndStoreKrakenOHLC(sdb, currency)
			}
		}
	})

	// Record start time; used after the run to mark which offers were seen
	snapshotStart := time.Now().UTC()

	fetchers := []exchFetcher{
		{"bisq", fetchBisqCurrency},
		{"bisq2", fetchBisq2Currency},
		{"hodlhodl", fetchHodlHodlCurrency},
		{"robosats", fetchRobosatsCurrency},
		{"peachbitcoin", fetchPeachBitcoinCurrency},
		{"mostro", fetchMostroCurrency},
	}

	exchangeData := make(exchangeSnapshotData)
	confirmedMarkets := make(map[liveMarket]struct{})
	liveOffers := make([]livePayload, 0)
	timings := newFetchTimings()
	serverURL := os.Getenv("SERVER_INTERNAL_URL")
	if serverURL == "" {
		serverURL = "http://localhost:5010"
	}

	stage(ctx, "snapshot.fetch_offers", func() {
		jobs := buildMarketFetchJobs(refPrices, fetchers)
		for _, fetched := range fetchMarkets(ctx, jobs) {
			job := fetched.job
			f := job.fetcher
			currency := job.market.fiat
			direction := job.market.direction
			refPrice := job.referencePrice
			result := fetched.result
			timings.durationMS[f.name] += fetched.duration.Milliseconds()
			status := result.status()
			if status == fetchStatusFailed {
				timings.failures[f.name]++
				timings.addStatus(f.name, fetchStatusFailed)
				continue
			}
			before := len(result.Offers)
			result.Offers = filterDustOffers(result.Offers)
			if dropped := before - len(result.Offers); dropped > 0 {
				slog.InfoContext(ctx, "dust offers filtered", "exchange", f.name, "fiat", currency, "direction", direction, "dropped", dropped)
			}
			if !recordFetchResult(exchangeData, f.name, result) {
				timings.addStatus(f.name, fetchStatusFailed)
				continue
			}
			confirm := false
			if len(result.Offers) == 0 {
				confirm = status == fetchStatusComplete
			} else {
				price := result.BTCPrice
				if price == 0 {
					price = float64(refPrice)
				}
				persisted, ok := saveSnapshotPersisted(sdb, f.name, currency, direction, price, result.Offers)
				if !ok {
					slog.WarnContext(ctx, "market snapshot was not persisted; retaining prior live offers", "exchange", f.name, "fiat", currency, "direction", direction)
					status = fetchStatusIncomplete
				} else {
					liveOffers = append(liveOffers, persisted...)
					confirm = status == fetchStatusComplete
				}
			}
			timings.addStatus(f.name, status)
			if confirm {
				confirmedMarkets[job.market] = struct{}{}
			}
		}
	})

	stage(ctx, "snapshot.register_unknown_payment_methods", func() { flushUnknownPaymentMethods(ctx, sdb) })
	stage(ctx, "snapshot.update_payment_method_popularity", func() { flushPaymentMethodTouches(ctx, sdb) })

	staleGrace := liveOfferStaleGrace()
	if err := reconcileLiveOfferStatus(sdb, snapshotStart, confirmedMarkets, staleGrace); err != nil {
		slog.ErrorContext(ctx, "failed to reconcile live offer state", "err", err)
	}

	// Keep failed exchanges in the snapshot log, but do not treat an outage as
	// proof that its offers closed. reconcileLiveOfferStatus retains them until
	// the configured stale grace expires.
	offline := offlineExchangeNames(fetchers, exchangeData)
	for _, name := range offline {
		slog.WarnContext(ctx, "exchange offline — retaining last known offers through stale grace", "exchange", name, "stale_grace", staleGrace)
	}

	// Always write a baseline lifecycle row for every live offer directly to the
	// DB, before the push. This guarantees a price-path point for each live offer
	// even when the server is down/unreachable; the server enriches these rows
	// with cost fields when it processes the push below.
	stage(ctx, "snapshot.write_baseline_lifecycle", func() { writeBaselineLifecycle(sdb, liveOffers, refPrices) })

	// Ask the API to reload the live hub from Postgres. Membership is is_live,
	// not this POST body; the body is only a refresh trigger.
	pushLiveRefreshToServer(ctx, serverURL)

	// Settle closed offers: compute avg_ref_price from historical OHLC.
	// Keep this after the live push so the server can persist the latest
	// lifecycle snapshot before we decide whether a closed offer has history.
	stage(ctx, "snapshot.settle_closed_offers", func() { settleClosedOffers(sdb) })
	stage(ctx, "snapshot.analyze_closed_offers", func() { analyzeClosedOffers(sdb) })
	stage(ctx, "snapshot.prune_order_lifecycle", func() { pruneOrderLifecycle(sdb) })

	writeSnapshotLog(sdb, exchangeData, offline, timings, time.Since(cycleStart))
	total := 0
	names := make([]string, 0, len(exchangeData))
	for name, ids := range exchangeData {
		total += len(ids)
		names = append(names, name)
	}
	slog.InfoContext(ctx, "snapshot complete", "offers", total, "exchanges", names)
}

// fetchAndStoreKrakenOHLC fetches 1-min Kraken OHLC candles and upserts them into ohlc.
func fetchAndStoreKrakenOHLC(sdb *dbutil.DB, currency string) {
	currency = strings.ToUpper(currency)
	resp, err := clearnetClient().Get("https://api.kraken.com/0/public/OHLC?pair=XBT" + url.QueryEscape(currency) + "&interval=1")
	if err != nil {
		fmt.Printf("[OHLC] fetch error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := readAllLimited(resp.Body)

	var data struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Printf("[OHLC] parse error: %v\n", err)
		return
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[OHLC] db error: %v\n", err)
		return
	}
	defer tx.Rollback()

	n := 0
	for key, raw := range data.Result {
		if key == "last" {
			continue
		}
		var rows [][]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			continue
		}
		for _, c := range rows {
			if len(c) < 5 {
				continue
			}
			var ts float64
			json.Unmarshal(c[0], &ts)
			var open, high, low, cls string
			json.Unmarshal(c[1], &open)
			json.Unmarshal(c[2], &high)
			json.Unmarshal(c[3], &low)
			json.Unmarshal(c[4], &cls)
			t := time.Unix(int64(ts), 0).UTC().Format("2006-01-02 15:04:05")
			tx.Exec(`INSERT INTO ohlc (time, fiat_currency, open, high, low, close) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(time, fiat_currency) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close`,
				t,
				currency,
				parseFloatStr(open), parseFloatStr(high), parseFloatStr(low), parseFloatStr(cls),
			)
			n++
		}
		break // only one non-"last" key
	}
	if err := tx.Commit(); err != nil {
		fmt.Printf("[OHLC] commit error (candles dropped this cycle): %v\n", err)
		return
	}
	fmt.Printf("[OHLC] upserted %d candles\n", n)
}

// settleClosedOffers computes avg_ref_price for offers that just closed and do
// not have a settled historical reference price yet.
func settleClosedOffers(sdb *dbutil.DB) {
	rows, err := sdb.Query(`
		SELECT id, fiat_currency,
		       to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
		       to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM offers
		WHERE is_live = FALSE AND avg_ref_price IS NULL
	`)
	if err != nil {
		fmt.Printf("[Settle] query error: %v\n", err)
		return
	}
	defer rows.Close()

	type toSettle struct {
		id        int64
		currency  string
		firstSeen string
		lastSeen  string
	}
	var batch []toSettle
	for rows.Next() {
		var o toSettle
		if err := rows.Scan(&o.id, &o.currency, &o.firstSeen, &o.lastSeen); err != nil {
			continue
		}
		batch = append(batch, o)
	}
	rows.Close()

	settled := 0
	for _, o := range batch {
		var avgClose float64
		sdb.QueryRow(`
			SELECT AVG(close) FROM ohlc
			WHERE fiat_currency = ? AND time >= ? AND time <= ?
		`, o.currency, o.firstSeen, o.lastSeen).Scan(&avgClose)

		// No candles in range — offer's lifetime may predate our cached OHLC window.
		// Fetch historical data from Kraken using the offer's first_seen as the
		// since parameter, upsert into ohlc, then retry.
		if avgClose == 0 {
			if firstSeenUnix := parseDBTimeUnix(o.firstSeen); firstSeenUnix > 0 {
				fetchAndStoreKrakenOHLCSince(sdb, o.currency, firstSeenUnix)
				sdb.QueryRow(`
					SELECT AVG(close) FROM ohlc
					WHERE fiat_currency = ? AND time >= ? AND time <= ?
				`, o.currency, o.firstSeen, o.lastSeen).Scan(&avgClose)
			}
		}

		// Last resort: nearest single candle to last_seen
		if avgClose == 0 {
			sdb.QueryRow(`
				SELECT close FROM ohlc
				WHERE fiat_currency = ?
				ORDER BY ABS(EXTRACT(EPOCH FROM (time - ?::timestamptz)))
				LIMIT 1
			`, o.currency, o.lastSeen).Scan(&avgClose)
		}

		if avgClose == 0 {
			continue
		}

		sdb.Exec(`UPDATE offers SET avg_ref_price = ? WHERE id = ?`,
			avgClose, o.id)
		settled++
	}
	if settled > 0 {
		fmt.Printf("[Settle] settled %d closed offers with historical avg ref price\n", settled)
	}
}

// writeBaselineLifecycle inserts a baseline order_lifecycle row for every live
// offer (price, ref_price, directional premium) straight to the DB, leaving the
// cost columns NULL for the server to enrich on the subsequent push. This
// decouples price-path capture from server availability: a live offer always
// gets a lifecycle point even if the server is down, unreachable, or rejects the
// push.
func writeBaselineLifecycle(sdb *dbutil.DB, offers []livePayload, refPrices map[string]int) {
	if len(offers) == 0 {
		return
	}
	repo := lifecycle.NewRepository(sdb)
	baselines := make([]lifecycle.Baseline, 0, len(offers))
	for _, o := range offers {
		refPrice := refPrices[strings.ToUpper(o.FiatCurrency)]
		if refPrice <= 0 {
			continue
		}
		baselines = append(baselines, lifecycle.Baseline{
			OfferID:    o.ID,
			Price:      o.Price,
			RefPrice:   float64(refPrice),
			PremiumPct: lifecycle.DirectionalPremiumPct(o.Direction, o.Price, float64(refPrice)),
		})
	}
	if err := repo.InsertBaseline(context.Background(), baselines); err != nil {
		fmt.Printf("[Lifecycle] baseline insert error: %v\n", err)
		return
	}
	fmt.Printf("[Lifecycle] wrote %d baseline rows\n", len(baselines))
}

// pushLiveRefreshToServer tells the unified-api to reload the live hub from
// Postgres after this run's persist and reconcile. The POST body is not the
// live book; membership is WHERE is_live = TRUE.
func pushLiveRefreshToServer(ctx context.Context, serverURL string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/internal/live_offers", strings.NewReader(`{}`))
	if err != nil {
		slog.ErrorContext(ctx, "live push: request build failed (non-fatal)", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if secret := strings.TrimSpace(os.Getenv("INTERNAL_API_SECRET")); secret != "" {
		req.Header.Set("X-Internal-Secret", secret)
	}

	resp, err := tracing.Client(15 * time.Second).Do(req)
	if err != nil {
		slog.ErrorContext(ctx, "live push: POST failed (non-fatal, baseline lifecycle already written)", "err", err)
		return
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		slog.ErrorContext(ctx, "live push: server rejected push: 401 unauthorized — check INTERNAL_API_SECRET parity between snapshot and unified-api (lifecycle enrichment skipped this cycle)")
	case resp.StatusCode >= 300:
		slog.ErrorContext(ctx, "live push: server returned error status (lifecycle enrichment skipped this cycle)", "status", resp.StatusCode)
	default:
		slog.InfoContext(ctx, "live push: requested live hub refresh from PostgreSQL")
	}
}

// fetchAndStoreKrakenOHLCSince fetches 1-min candles starting from sinceUnix
// and upserts them into ohlc. Used to backfill history for offers whose
// lifetime predates the standard OHLC cache window (~7.5 days).
func fetchAndStoreKrakenOHLCSince(sdb *dbutil.DB, currency string, sinceUnix int64) {
	currency = strings.ToUpper(currency)
	url := fmt.Sprintf("https://api.kraken.com/0/public/OHLC?pair=XBT%s&interval=1&since=%d", url.QueryEscape(currency), sinceUnix)
	resp, err := clearnetClient().Get(url)
	if err != nil {
		fmt.Printf("[OHLCSince] fetch error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := readAllLimited(resp.Body)

	var data struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Printf("[OHLCSince] parse error: %v\n", err)
		return
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[OHLCSince] db error: %v\n", err)
		return
	}
	defer tx.Rollback()

	n := 0
	for key, raw := range data.Result {
		if key == "last" {
			continue
		}
		var rows [][]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			continue
		}
		for _, c := range rows {
			if len(c) < 5 {
				continue
			}
			var ts float64
			json.Unmarshal(c[0], &ts)
			var open, high, low, cls string
			json.Unmarshal(c[1], &open)
			json.Unmarshal(c[2], &high)
			json.Unmarshal(c[3], &low)
			json.Unmarshal(c[4], &cls)
			t := time.Unix(int64(ts), 0).UTC().Format("2006-01-02 15:04:05")
			tx.Exec(`INSERT INTO ohlc (time, fiat_currency, open, high, low, close) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(time, fiat_currency) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close`,
				t,
				currency,
				parseFloatStr(open), parseFloatStr(high), parseFloatStr(low), parseFloatStr(cls),
			)
			n++
		}
		break
	}
	if err := tx.Commit(); err != nil {
		fmt.Printf("[OHLCSince] commit error (candles dropped this cycle): %v\n", err)
		return
	}
	fmt.Printf("[OHLCSince] upserted %d historical candles\n", n)
}

// parseDBTimeUnix parses a database timestamp string and returns its Unix timestamp.
// Returns 0 if parsing fails.
func parseDBTimeUnix(s string) int64 {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02T15:04:05Z"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC().Unix()
		}
	}
	return 0
}

// --- Helpers ---

// minIngestFiat is the minimum quote-currency trade size (whole units) for an
// offer to be ingested. Offers below this are dust/spam and are dropped before
// persistence or live-hub push.
const minIngestFiat = 1

func effectiveFiatMax(o offer) int {
	if o.FiatMax != nil && *o.FiatMax > 0 {
		return *o.FiatMax
	}
	if o.BTCMax != nil && *o.BTCMax > 0 && o.Price > 0 {
		return int(math.Round(*o.BTCMax * o.Price))
	}
	return 0
}

func filterDustOffers(offers []offer) []offer {
	if len(offers) == 0 {
		return offers
	}
	kept := make([]offer, 0, len(offers))
	for _, o := range offers {
		if effectiveFiatMax(o) >= minIngestFiat {
			kept = append(kept, o)
		}
	}
	return kept
}

// maxFetchBytes bounds how much we read from any exchange/price/onion response.
// A hostile or compromised endpoint — especially a Tor onion in the price/offer
// paths — could otherwise stream unbounded data straight into memory. 8 MiB is
// far above any real response (Kraken OHLC, a Peach page, a full HodlHodl book).
const maxFetchBytes = 8 << 20

func readAllLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxFetchBytes))
}

// securityDeposit returns the taker's deposit in BTC and as a fraction of the
// trade size, or (nil, nil) when btcMax <= 0. The guard matters: a zero-amount
// offer would make depositPct = deposit/0 = +Inf, and json.Marshal rejects
// non-finite floats — which failed the ENTIRE /internal/live_offers push for the
// cycle, not just the one offer.
func securityDeposit(depositSats uint64, btcMax float64) (*float64, *float64) {
	if btcMax <= 0 {
		return nil, nil
	}
	depositBTC := float64(depositSats) / 1e8
	depositPct := depositBTC / btcMax
	return &depositBTC, &depositPct
}

func round8(v float64) float64 {
	return math.Round(v*1e8) / 1e8
}

func parseFloatStr(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nilIfZeroTime(v time.Time) any {
	if v.IsZero() {
		return nil
	}
	return v
}

func nilIfZeroInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nilIfEmptySlice(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s
}

// nilIfEmptyRawMessage returns nil for an absent JSON blob so the ?::jsonb
// bind writes SQL NULL rather than an empty string (which is invalid jsonb).
func nilIfEmptyRawMessage(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func marshalSignals(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
