package main

import (
	"commons/logging"
	"commons/tracing"
	"context"
	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/config"
	"dexunifiedapi/internal/lifecycle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
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
	OfferID       string   `json:"offer_id,omitempty"`
	FiatCurrency  string   `json:"fiat_currency,omitempty"`
	Price         float64  `json:"price"`
	PriceDiffPct  *float64 `json:"price_diff_percent,omitempty"`
	BTCMin        *float64 `json:"btc_min,omitempty"`
	BTCMax        *float64 `json:"btc_max,omitempty"`
	FiatMin       *int     `json:"fiat_min,omitempty"`
	FiatMax       *int     `json:"fiat_max,omitempty"`
	PaymentMethod string   `json:"payment_method,omitempty"`
	PriceType     *string  `json:"price_type,omitempty"`
}

type fetchResult struct {
	BTCPrice float64
	Offers   []offer
}

type exchFetcher struct {
	name string
	fn   func(string, int) *fetchResult
}

type exchangeSnapshotData map[string]map[string]bool

type livePayload struct {
	ID            int64    `json:"id"`
	Exchange      string   `json:"exchange"`
	FiatCurrency  string   `json:"fiat_currency"`
	Direction     string   `json:"direction"`
	Price         float64  `json:"price"`
	BTCMin        *float64 `json:"btc_min"`
	BTCMax        *float64 `json:"btc_max"`
	FiatMin       *int64   `json:"fiat_min"`
	FiatMax       *int64   `json:"fiat_max"`
	PaymentMethod *string  `json:"payment_method"`
	OfferID       *string  `json:"offer_id"`
	FirstSeen     string   `json:"first_seen"`
	LastSeen      string   `json:"last_seen"`
	PriceType     *string  `json:"price_type"`
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

// --- Reference price providers ---

// bisqPriceNodes are the known Bisq price node onion addresses, tried in order.
var bisqPriceNodes = []string{
	"http://runbtcpn7gmbj5rgqeyfyvepqokrijem6rbw7o5wgqbguimuoxrmcdyd.onion",
	"http://ro7nv73awqs3ga2qtqeqawrjpbxwarsazznszvr6whv7tes5ehffopid.onion",
}

func priceBisqTor(fiat string) (int, error) {
	var lastErr error
	for _, node := range bisqPriceNodes {
		resp, err := torClient().Get(node + "/getAllMarketPrices")
		if err != nil {
			lastErr = err
			fmt.Printf("[RefPrice] bisq_tor: %s failed: %v\n", node, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			Data []struct {
				CurrencyCode string  `json:"currencyCode"`
				Price        float64 `json:"price"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			lastErr = err
			continue
		}
		for _, c := range result.Data {
			if strings.EqualFold(c.CurrencyCode, fiat) {
				return int(c.Price), nil
			}
		}
		lastErr = fmt.Errorf("currency %s not found", fiat)
	}
	return 0, lastErr
}

func priceKraken(fiat string) (int, error) {
	pair := "XBT" + strings.ToUpper(fiat)
	resp, err := clearnetClient().Get("https://api.kraken.com/0/public/Ticker?pair=" + pair)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Result map[string]struct {
			C []string `json:"c"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}
	for _, ticker := range data.Result {
		if len(ticker.C) > 0 {
			v, _ := strconv.ParseFloat(ticker.C[0], 64)
			return int(v), nil
		}
	}
	return 0, fmt.Errorf("no ticker data for %s", pair)
}

func priceCoinGecko(fiat string) (int, error) {
	resp, err := clearnetClient().Get("https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=" + fiat)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}
	if btc, ok := data["bitcoin"]; ok {
		if price, ok := btc[fiat]; ok {
			return int(price), nil
		}
	}
	return 0, fmt.Errorf("no price for %s", fiat)
}

func fetchReferencePrice(fiat string) int {
	allProviders := []struct {
		name string
		fn   func(string) (int, error)
	}{
		{"bisq_tor", priceBisqTor},
		{"kraken", priceKraken},
		{"coingecko", priceCoinGecko},
	}

	// Build the ordered list: configured provider first, then the rest as fallbacks.
	var ordered []struct {
		name string
		fn   func(string) (int, error)
	}
	for _, p := range allProviders {
		if p.name == priceProvider {
			ordered = append([]struct {
				name string
				fn   func(string) (int, error)
			}{p}, ordered...)
		} else {
			ordered = append(ordered, p)
		}
	}
	if len(ordered) == 0 || ordered[0].name != priceProvider {
		fmt.Printf("[RefPrice] Unknown provider '%s', falling back to all\n", priceProvider)
		ordered = allProviders
	}

	for _, p := range ordered {
		price, err := p.fn(fiat)
		if err != nil {
			fmt.Printf("[RefPrice] Error from %s: %v\n", p.name, err)
			continue
		}
		fmt.Printf("[RefPrice] %s: %d %s\n", p.name, price, strings.ToUpper(fiat))
		return price
	}
	return 0
}

// --- Exchange fetchers ---

func fetchBisq(direction string, btcPrice int) *fetchResult {
	if btcPrice == 0 {
		fmt.Println("[Bisq] Could not get reference price")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		Direction:    strings.ToUpper(direction),
		CurrencyCode: "EUR",
	})
	if err != nil {
		fmt.Printf("[Bisq] Error getting %s offers: %v\n", direction, err)
		return nil
	}

	var offers []offer
	for _, o := range reply.Offers {
		price, _ := strconv.ParseFloat(o.Price, 64)
		btcMin := float64(o.MinAmount) / 1e8
		btcMax := float64(o.Amount) / 1e8
		fiatMin := int(parseFloatStr(o.MinVolume))
		fiatMax := int(parseFloatStr(o.Volume))
		pt := priceTypeFixed()
		if o.UseMarketBasedPrice {
			pt = priceTypeFloating()
		}
		offers = append(offers, offer{
			OfferID:       o.Id,
			FiatCurrency:  "EUR",
			Price:         price,
			BTCMin:        &btcMin,
			BTCMax:        &btcMax,
			FiatMin:       &fiatMin,
			FiatMax:       &fiatMax,
			PaymentMethod: o.PaymentMethodShortName,
			PriceType:     pt,
		})
	}

	fmt.Printf("[Bisq] Found %d %s offers\n", len(offers), direction)
	return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
}

func fetchBisq2(direction string, btcPrice int) *fetchResult {
	if btcPrice == 0 {
		fmt.Println("[Bisq2] Could not get reference price")
		return nil
	}
	if bisq2RESTHost == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf("http://%s/api/v1/offerbook/markets/EUR/offers", bisq2RESTHost)
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

	body, _ := io.ReadAll(resp.Body)

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
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		fmt.Printf("[Bisq2] Parse error: %v\n", err)
		return nil
	}

	wantDir := strings.ToUpper(direction)
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

		var paymentMethod string
		if len(o.QuoteSidePaymentMethodSpecs) > 0 {
			paymentMethod = o.QuoteSidePaymentMethodSpecs[0].PaymentMethod
		}

		offers = append(offers, offer{
			OfferID:       o.ID,
			FiatCurrency:  "EUR",
			Price:         price,
			BTCMin:        &btcMin,
			BTCMax:        &btcMax,
			FiatMin:       &fiatMin,
			FiatMax:       &fiatMax,
			PaymentMethod: paymentMethod,
			PriceType:     pt,
		})
	}

	fmt.Printf("[Bisq2] Found %d %s offers\n", len(offers), direction)
	return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
}

func fetchHodlHodl(direction string, btcPrice int) *fetchResult {
	reqURL := fmt.Sprintf("https://hodlhodl.com/api/v1/offers?filters[side]=%s&filters[include_global]=true&filters[currency_code]=EUR&filters[only_working_now]=true&sort[by]=price", direction)
	resp, err := clearnetClient().Get(reqURL)
	if err != nil {
		fmt.Printf("[HodlHodl] Error: %v\n", err)
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Offers []struct {
			ID             string `json:"id"`
			Price          string `json:"price"`
			PriceSource    string `json:"price_source"`
			MinAmount      string `json:"min_amount"`
			MaxAmount      string `json:"max_amount"`
			PaymentMethods []struct {
				Name string `json:"name"`
			} `json:"payment_methods"`
			PaymentMethodInstructions []struct {
				PaymentMethodName string `json:"payment_method_name"`
			} `json:"payment_method_instructions"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Printf("[HodlHodl] Parse error: %v\n", err)
		return nil
	}

	var offers []offer
	for _, o := range data.Offers {
		price, err := strconv.ParseFloat(o.Price, 64)
		if err != nil || price == 0 {
			continue
		}
		fiatMin := int(parseFloatStr(o.MinAmount))
		fiatMax := int(parseFloatStr(o.MaxAmount))

		var method string
		if direction == "buy" && len(o.PaymentMethods) > 0 {
			method = o.PaymentMethods[0].Name
		} else if len(o.PaymentMethodInstructions) > 0 {
			method = o.PaymentMethodInstructions[0].PaymentMethodName
		}
		if strings.Contains(method, "SEPA") {
			method = "SEPA"
		} else if strings.Contains(method, "Any national bank") {
			method = "NATIONAL_BANK"
		}

		btcMin := round8(float64(fiatMin) / price)
		btcMax := round8(float64(fiatMax) / price)
		offers = append(offers, offer{
			OfferID:       o.ID,
			FiatCurrency:  "EUR",
			Price:         price,
			BTCMin:        &btcMin,
			BTCMax:        &btcMax,
			FiatMin:       &fiatMin,
			FiatMax:       &fiatMax,
			PaymentMethod: method,
			PriceType:     normalizeHodlHodlPriceType(o.PriceSource),
		})
	}

	fmt.Printf("[HodlHodl] Found %d %s offers\n", len(offers), direction)
	return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
}

// normalizePeachMethod converts a Peach Bitcoin payment-method identifier
// (e.g. "sepa", "instantSepa", "nationalTransferDE", "mbWay") into a
// human-readable form aligned with the keyword rules used by
// dexMonitor/internal/paymentmethods/groups.go.
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
	switch out {
	case "Sepa":
		return "SEPA"
	case "Instant Sepa":
		return "Instant SEPA"
	case "Mb Way":
		return "MB Way"
	case "Paypal":
		return "PayPal"
	}
	return out
}

func fetchPeachBitcoin(direction string, btcPrice int) *fetchResult {
	if btcPrice == 0 {
		fmt.Println("[PeachBitcoin] Could not get reference price")
		return nil
	}

	peachType := "ask" // sell offers (maker sells BTC)
	if direction == "buy" {
		peachType = "bid" // buy offers (maker buys BTC)
	}

	type peachOffer struct {
		ID             string              `json:"id"`
		Amount         json.RawMessage     `json:"amount"` // int64 (sell) or [min,max] int64 array (buy)
		MeansOfPayment map[string][]string `json:"meansOfPayment"`
		Prices         map[string]float64  `json:"prices"`
	}
	type peachResponse struct {
		Offers    []peachOffer `json:"offers"`
		Total     int          `json:"total"`
		Remaining int          `json:"remaining"`
	}

	var allOffers []peachOffer
	for page := 0; ; page++ {
		reqURL := fmt.Sprintf("https://api.peachbitcoin.com/v1/offer/search?page=%d&size=100&sortBy=lowestPremium", page)
		body, err := json.Marshal(map[string]any{"type": peachType})
		if err != nil {
			fmt.Printf("[PeachBitcoin] marshal error: %v\n", err)
			break
		}

		resp, err := clearnetClient().Post(reqURL, "application/json", strings.NewReader(string(body)))
		if err != nil {
			fmt.Printf("[PeachBitcoin] Error: %v\n", err)
			break
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			fmt.Printf("[PeachBitcoin] HTTP %d: %s\n", resp.StatusCode, string(respBody))
			break
		}

		var pr peachResponse
		if err := json.Unmarshal(respBody, &pr); err != nil {
			fmt.Printf("[PeachBitcoin] Parse error: %v — body: %s\n", err, string(respBody))
			break
		}
		allOffers = append(allOffers, pr.Offers...)
		if pr.Remaining <= 0 {
			break
		}
	}

	var offers []offer
	for _, o := range allOffers {
		// Sell offers have an explicit EUR price; buy offers don't — use market price.
		eurPrice, ok := o.Prices["EUR"]
		if !ok || eurPrice == 0 {
			eurPrice = float64(btcPrice)
		}
		if eurPrice == 0 {
			continue
		}

		// amount is either a plain int64 (sell offers) or [min, max] int64 array (buy offers).
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
				continue
			}
			btcMin = round8(float64(single) / 1e8)
			btcMax = btcMin
		}

		fiatMin := int(btcMin * eurPrice)
		fiatMax := int(btcMax * eurPrice)

		var method string
		if methods, ok := o.MeansOfPayment["EUR"]; ok && len(methods) > 0 {
			method = normalizePeachMethod(methods[0])
		}

		offers = append(offers, offer{
			OfferID:       o.ID,
			FiatCurrency:  "EUR",
			Price:         eurPrice,
			BTCMin:        &btcMin,
			BTCMax:        &btcMax,
			FiatMin:       &fiatMin,
			FiatMax:       &fiatMax,
			PaymentMethod: method,
			PriceType:     priceTypeFixed(),
		})
	}

	fmt.Printf("[PeachBitcoin] Found %d %s offers\n", len(offers), direction)
	return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
}

func fetchRobosats(direction string, btcPrice int) *fetchResult {
	offerType := 1 // sell
	if direction == "buy" {
		offerType = 0
	}

	client := torClient()
	for _, onion := range robosatsOnions {
		reqURL := fmt.Sprintf("%s/api/book/?currency=2&type=%d", onion, offerType)
		resp, err := client.Get(reqURL)
		if err != nil {
			fmt.Printf("[Robosats] %s failed: %v, trying next...\n", onion, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var rawOffers []json.RawMessage
		if err := json.Unmarshal(body, &rawOffers); err != nil {
			fmt.Printf("[Robosats] Parse error: %v, trying next...\n", err)
			continue
		}

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
			}
			if err := json.Unmarshal(raw, &line); err != nil {
				continue
			}

			price, err := line.Price.Float64()
			if err != nil {
				continue
			}
			// RoboSats sometimes returns price=0 with only a premium percentage.
			// Derive absolute price from the reference BTC price in that case.
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

			offers = append(offers, offer{
				OfferID:       line.ID.String(),
				FiatCurrency:  "EUR",
				Price:         price,
				BTCMin:        &btcMin,
				BTCMax:        &btcMax,
				FiatMin:       &fiatMin,
				FiatMax:       &fiatMax,
				PaymentMethod: line.PaymentMethod,
				PriceType:     pt,
			})
		}

		fmt.Printf("[Robosats] Found %d %s offers\n", len(offers), direction)
		return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
	}

	fmt.Println("[Robosats] All onion addresses failed")
	return nil
}

// --- Database ---

func saveSnapshot(sdb *dbutil.DB, exchange, fiat, direction string, btcPrice float64, offers []offer) []livePayload {
	if len(offers) == 0 {
		fmt.Printf("[%s] No offers to save\n", exchange)
		return nil
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[%s] DB error: %v\n", exchange, err)
		return nil
	}
	defer tx.Rollback()

	if _, err := tx.Exec("INSERT INTO snapshots (exchange, fiat_currency, direction, btc_price) VALUES (?, ?, ?, ?)",
		exchange, fiat, direction, btcPrice); err != nil {
		// A failed first write poisons the whole transaction in Postgres, so the
		// offer upserts below would all fail too. Bail loudly rather than silently
		// pushing id=0 offers to the hub (the bug that hid bisq2/peach for so long).
		fmt.Printf("[%s] snapshot insert failed (%s): %v\n", exchange, direction, err)
		return nil
	}

	liveOffers := make([]livePayload, 0, len(offers))
	seen := make(map[int64]bool, len(offers))
	updated, inserted, failed := 0, 0, 0
	for _, o := range offers {
		var existingID int64
		var found bool

		if o.OfferID != "" {
			err := tx.QueryRow("SELECT id FROM offers WHERE offer_id = ?", o.OfferID).Scan(&existingID)
			found = err == nil
		}
		if !found {
			err := tx.QueryRow(`SELECT id FROM offers
				WHERE exchange = ? AND fiat_currency = ? AND direction = ?
				  AND btc_min = ? AND btc_max = ? AND payment_method = ?`,
				exchange, fiat, direction, o.BTCMin, o.BTCMax, o.PaymentMethod).Scan(&existingID)
			found = err == nil
		}

		// Many scraped offers (e.g. peachbitcoin, which has no stable offer_id) share
		// one fingerprint and collapse onto the same offers row. Capture each distinct
		// offer once per cycle, otherwise we write N identical order_lifecycle baseline
		// rows and push N duplicate hub entries for it — the bloat that OOM-killed the
		// server (offer #989 was getting 83 identical rows every snapshot).
		if found && seen[existingID] {
			continue
		}

		live := livePayload{
			Exchange:     exchange,
			FiatCurrency: fiat,
			Direction:    direction,
			Price:        o.Price,
			BTCMin:       o.BTCMin,
			BTCMax:       o.BTCMax,
			PriceType:    o.PriceType,
		}
		if o.FiatMin != nil {
			v := int64(*o.FiatMin)
			live.FiatMin = &v
		}
		if o.FiatMax != nil {
			v := int64(*o.FiatMax)
			live.FiatMax = &v
		}
		if o.PaymentMethod != "" {
			method := o.PaymentMethod
			live.PaymentMethod = &method
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
				fiat_min = ?,
				fiat_max = ?,
				payment_method = ?,
				offer_id = COALESCE(offer_id, ?)
				WHERE id = ?
				RETURNING id,
					to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
					to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`,
				o.PriceType, o.Price, o.FiatMin, o.FiatMax, nilIfEmpty(o.PaymentMethod), nilIfEmpty(o.OfferID), existingID,
			).Scan(&live.ID, &live.FirstSeen, &live.LastSeen)
		} else {
			upsertErr = tx.QueryRow(`INSERT INTO offers
				(exchange, fiat_currency, direction, btc_min, btc_max,
				 fiat_min, fiat_max, payment_method, offer_id, price_type, price)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				RETURNING id,
					to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
					to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`,
				exchange, fiat, direction, o.BTCMin, o.BTCMax,
				o.FiatMin, o.FiatMax, nilIfEmpty(o.PaymentMethod), nilIfEmpty(o.OfferID), o.PriceType, o.Price,
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
		}
		seen[live.ID] = true
		liveOffers = append(liveOffers, live)
	}

	if err := tx.Commit(); err != nil {
		fmt.Printf("[%s] snapshot commit failed (%s): %v\n", exchange, direction, err)
		return nil
	}
	if failed > 0 {
		fmt.Printf("[%s] %d new, %d updated, %d failed (%s)\n", exchange, inserted, updated, failed, direction)
	} else {
		fmt.Printf("[%s] %d new, %d updated (%s)\n", exchange, inserted, updated, direction)
	}
	return liveOffers
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

func buildSnapshotLogPayload(exchangeData exchangeSnapshotData) ([]byte, int, error) {
	exchanges := make(map[string]any)
	total := 0
	for ex, ids := range exchangeData {
		idList := make([]string, 0, len(ids))
		for id := range ids {
			idList = append(idList, id)
		}
		sort.Strings(idList)
		exchanges[ex] = map[string]any{
			"count": len(ids),
			"ids":   idList,
		}
		total += len(ids)
	}
	jsonBytes, err := json.Marshal(exchanges)
	return jsonBytes, total, err
}

func writeSnapshotLog(sdb *dbutil.DB, exchangeData exchangeSnapshotData) {
	jsonBytes, total, err := buildSnapshotLogPayload(exchangeData)
	if err != nil {
		fmt.Printf("[SnapshotLog] encode error: %v\n", err)
		return
	}
	sdb.Exec("INSERT INTO snapshot_log (exchanges, offers_count) VALUES (?::jsonb, ?)", string(jsonBytes), total)
}

// --- Main ---

func main() {
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

	refPrice := fetchReferencePrice("eur")
	if refPrice == 0 {
		slog.ErrorContext(ctx, "could not get reference price, aborting")
		os.Exit(1)
	}
	slog.InfoContext(ctx, "reference price", "eur", refPrice)

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

	// Fetch and store Kraken OHLC candles before settlement
	stage(ctx, "snapshot.fetch_ohlc", func() { fetchAndStoreKrakenOHLC(sdb) })

	// Record start time; used after the run to mark which offers were seen
	snapshotStart := time.Now().UTC()

	fetchers := []exchFetcher{
		{"bisq", fetchBisq},
		{"bisq2", fetchBisq2},
		{"hodlhodl", fetchHodlHodl},
		{"robosats", fetchRobosats},
		{"peachbitcoin", fetchPeachBitcoin},
	}

	exchangeData := make(exchangeSnapshotData)
	liveOffers := make([]livePayload, 0)

	stage(ctx, "snapshot.fetch_offers", func() {
		for _, direction := range []string{"sell", "buy"} {
			slog.InfoContext(ctx, "fetching offers", "direction", direction)
			for _, f := range fetchers {
				result := f.fn(direction, refPrice)
				if !recordFetchResult(exchangeData, f.name, result) || len(result.Offers) == 0 {
					continue
				}
				price := result.BTCPrice
				if price == 0 {
					price = float64(refPrice)
				}
				liveOffers = append(liveOffers, saveSnapshot(sdb, f.name, "EUR", direction, price, result.Offers)...)
			}
		}
	})

	// Update is_live and duration in one pass:
	// - live offers: is_live=true, duration=NULL
	// - closed offers: is_live=false, duration=seconds between first_seen and last_seen
	sdb.Exec(`UPDATE offers SET
		is_live = (last_seen >= ?),
		duration = CASE WHEN last_seen < ? THEN EXTRACT(EPOCH FROM (last_seen - first_seen))::BIGINT ELSE NULL END`,
		snapshotStart, snapshotStart)

	// Explicitly close offers from any exchange that failed entirely this run
	for _, name := range offlineExchangeNames(fetchers, exchangeData) {
		res, _ := sdb.Exec(`UPDATE offers SET
				is_live = FALSE,
				duration = EXTRACT(EPOCH FROM (last_seen - first_seen))::BIGINT
				WHERE exchange = ?`, name)
		if n, _ := res.RowsAffected(); n > 0 {
			slog.WarnContext(ctx, "exchange offline — marked offers as closed", "exchange", name, "closed", n)
		}
	}

	// Always write a baseline lifecycle row for every live offer directly to the
	// DB, before the push. This guarantees a price-path point for each live offer
	// even when the server is down/unreachable; the server enriches these rows
	// with cost fields when it processes the push below.
	stage(ctx, "snapshot.write_baseline_lifecycle", func() { writeBaselineLifecycle(sdb, liveOffers, refPrice) })

	// Push live offers directly to the server's in-memory hub. ctx carries the
	// trace context so the POST continues this run's trace into unified-api.
	serverURL := os.Getenv("SERVER_INTERNAL_URL")
	if serverURL == "" {
		serverURL = "http://localhost:5010"
	}
	pushLiveOffersToServer(ctx, liveOffers, serverURL)

	// Settle closed offers: compute avg_ref_price from historical OHLC.
	// Keep this after the live push so the server can persist the latest
	// lifecycle snapshot before we decide whether a closed offer has history.
	stage(ctx, "snapshot.settle_closed_offers", func() { settleClosedOffers(sdb) })
	stage(ctx, "snapshot.analyze_closed_offers", func() { analyzeClosedOffers(sdb) })

	writeSnapshotLog(sdb, exchangeData)
	total := 0
	names := make([]string, 0, len(exchangeData))
	for name, ids := range exchangeData {
		total += len(ids)
		names = append(names, name)
	}
	slog.InfoContext(ctx, "snapshot complete", "offers", total, "exchanges", names)
}

// fetchAndStoreKrakenOHLC fetches 1-min Kraken OHLC candles and upserts them into ohlc.
func fetchAndStoreKrakenOHLC(sdb *dbutil.DB) {
	resp, err := clearnetClient().Get("https://api.kraken.com/0/public/OHLC?pair=XBTEUR&interval=1")
	if err != nil {
		fmt.Printf("[OHLC] fetch error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

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
			tx.Exec(`INSERT INTO ohlc (time, open, high, low, close) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(time) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close`,
				t,
				parseFloatStr(open), parseFloatStr(high), parseFloatStr(low), parseFloatStr(cls),
			)
			n++
		}
		break // only one non-"last" key
	}
	tx.Commit()
	fmt.Printf("[OHLC] upserted %d candles\n", n)
}

// settleClosedOffers computes avg_ref_price for offers that just closed and do
// not have a settled historical reference price yet.
func settleClosedOffers(sdb *dbutil.DB) {
	rows, err := sdb.Query(`
		SELECT id,
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
		firstSeen string
		lastSeen  string
	}
	var batch []toSettle
	for rows.Next() {
		var o toSettle
		if err := rows.Scan(&o.id, &o.firstSeen, &o.lastSeen); err != nil {
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
			WHERE time >= ? AND time <= ?
		`, o.firstSeen, o.lastSeen).Scan(&avgClose)

		// No candles in range — offer's lifetime may predate our cached OHLC window.
		// Fetch historical data from Kraken using the offer's first_seen as the
		// since parameter, upsert into ohlc, then retry.
		if avgClose == 0 {
			if firstSeenUnix := parseDBTimeUnix(o.firstSeen); firstSeenUnix > 0 {
				fetchAndStoreKrakenOHLCSince(sdb, firstSeenUnix)
				sdb.QueryRow(`
					SELECT AVG(close) FROM ohlc
					WHERE time >= ? AND time <= ?
				`, o.firstSeen, o.lastSeen).Scan(&avgClose)
			}
		}

		// Last resort: nearest single candle to last_seen
		if avgClose == 0 {
			sdb.QueryRow(`
				SELECT close FROM ohlc
				ORDER BY ABS(EXTRACT(EPOCH FROM (time - ?::timestamptz)))
				LIMIT 1
			`, o.lastSeen).Scan(&avgClose)
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
func writeBaselineLifecycle(sdb *dbutil.DB, offers []livePayload, refPrice int) {
	if len(offers) == 0 {
		return
	}
	repo := lifecycle.NewRepository(sdb)
	baselines := make([]lifecycle.Baseline, 0, len(offers))
	for _, o := range offers {
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

// pushLiveOffersToServer posts the freshly fetched live offers to the server's
// /internal/live_offers endpoint so the hub can serve them from memory without
// any DB reads.
func pushLiveOffersToServer(ctx context.Context, offers []livePayload, serverURL string) {
	body, err := json.Marshal(offers)
	if err != nil {
		slog.ErrorContext(ctx, "live push: marshal error", "err", err)
		return
	}

	// NewRequestWithContext + the traced client propagate this run's trace into
	// unified-api, so the snapshot run and the resulting ingest are one trace.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/internal/live_offers", strings.NewReader(string(body)))
	if err != nil {
		slog.ErrorContext(ctx, "live push: request build failed (non-fatal)", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Authenticate the internal ingest call when a shared secret is configured;
	// the server enforces it via X-Internal-Secret. Empty secret = open (dev).
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
		slog.InfoContext(ctx, "live push: pushed live offers to server", "count", len(offers))
	}
}

// fetchAndStoreKrakenOHLCSince fetches 1-min candles starting from sinceUnix
// and upserts them into ohlc. Used to backfill history for offers whose
// lifetime predates the standard OHLC cache window (~7.5 days).
func fetchAndStoreKrakenOHLCSince(sdb *dbutil.DB, sinceUnix int64) {
	url := fmt.Sprintf("https://api.kraken.com/0/public/OHLC?pair=XBTEUR&interval=1&since=%d", sinceUnix)
	resp, err := clearnetClient().Get(url)
	if err != nil {
		fmt.Printf("[OHLCSince] fetch error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

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
			tx.Exec(`INSERT INTO ohlc (time, open, high, low, close) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(time) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close`,
				t,
				parseFloatStr(open), parseFloatStr(high), parseFloatStr(low), parseFloatStr(cls),
			)
			n++
		}
		break
	}
	tx.Commit()
	fmt.Printf("[OHLCSince] upserted %d historical candles\n", n)
}

// parseDBTimeUnix parses a SQLite timestamp string and returns its Unix timestamp.
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

func round2(v float64) float64 {
	return math.Round(v*100) / 100
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
