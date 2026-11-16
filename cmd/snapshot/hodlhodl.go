package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	hodlHodlPageSize = 100
	hodlHodlMaxPages = 100
)

type hodlHodlOfferJSON struct {
	ID                        string                           `json:"id"`
	AssetCode                 string                           `json:"asset_code"`
	AssetLayer                string                           `json:"asset_layer"`
	Side                      string                           `json:"side"`
	CurrencyCode              string                           `json:"currency_code"`
	Price                     string                           `json:"price"`
	PriceSource               string                           `json:"price_source"`
	MinAmount                 string                           `json:"min_amount"`
	MaxAmount                 string                           `json:"max_amount"`
	MinAmountSats             string                           `json:"min_amount_sats"`
	MaxAmountSats             string                           `json:"max_amount_sats"`
	PaymentMethods            []hodlHodlPaymentMethodJSON      `json:"payment_methods"`
	PaymentMethodInstructions []hodlHodlPaymentInstructionJSON `json:"payment_method_instructions"`
	Trader                    hodlHodlTrader                   `json:"trader"`
}

type hodlHodlPaymentMethodJSON struct {
	Name string `json:"name"`
}

type hodlHodlPaymentInstructionJSON struct {
	PaymentMethodName string `json:"payment_method_name"`
}

type hodlHodlMapStatus int

const (
	hodlHodlMapped hodlHodlMapStatus = iota
	hodlHodlSkipUnsupported
	hodlHodlSkipMalformed
	hodlHodlSkipMismatch
)

type hodlHodlFetchStats struct {
	pages       int
	raw         int
	accepted    int
	duplicates  int
	unsupported int
	malformed   int
	mismatches  int
}

func fetchHodlHodlCurrency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult {
	return fetchHodlHodlCurrencyFrom(ctx, clearnetClient(), "https://hodlhodl.com", direction, currency, btcPrice)
}

func fetchHodlHodlCurrencyFrom(ctx context.Context, client *http.Client, baseURL, direction, currency string, btcPrice int) *fetchResult {
	var (
		offers  []offer
		seenIDs = make(map[string]struct{})
		stats   hodlHodlFetchStats
	)
	complete := false

	for page := 0; page < hodlHodlMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			fmt.Printf("[HodlHodl] %s %s cancelled: %v\n", currency, direction, err)
			return hodlHodlFetchResult(currency, direction, btcPrice, offers, stats, false)
		}

		offset := page * hodlHodlPageSize
		pageOffers, err := fetchHodlHodlPage(ctx, client, baseURL, direction, currency, offset)
		stats.pages++
		if err != nil {
			fmt.Printf("[HodlHodl] %s %s page offset=%d: %v\n", currency, direction, offset, err)
			return hodlHodlFetchResult(currency, direction, btcPrice, offers, stats, false)
		}

		stats.raw += len(pageOffers)
		newSourceIDs := 0
		for _, raw := range pageOffers {
			id := strings.TrimSpace(raw.ID)
			if id != "" {
				if _, dup := seenIDs[id]; dup {
					stats.duplicates++
					continue
				}
				seenIDs[id] = struct{}{}
				newSourceIDs++
			}
			mapped, status := mapHodlHodlOffer(raw, direction, currency)
			switch status {
			case hodlHodlMapped:
				offers = append(offers, mapped)
				stats.accepted++
			case hodlHodlSkipUnsupported:
				stats.unsupported++
			case hodlHodlSkipMismatch:
				stats.mismatches++
			default:
				stats.malformed++
			}
		}

		if len(pageOffers) < hodlHodlPageSize {
			complete = stats.mismatches == 0
			break
		}
		if newSourceIDs == 0 {
			fmt.Printf("[HodlHodl] %s %s repeated full page with no new IDs at offset=%d\n", currency, direction, offset)
			break
		}
	}

	return hodlHodlFetchResult(currency, direction, btcPrice, offers, stats, complete)
}

func hodlHodlFetchResult(currency, direction string, btcPrice int, offers []offer, stats hodlHodlFetchStats, complete bool) *fetchResult {
	if complete {
		logHodlHodlFetch(currency, direction, offers, stats, fetchStatusComplete)
		return completeFetch(float64(btcPrice), offers)
	}
	// A failed first page never parsed a valid offers array. Later-page
	// failures keep any offers already accepted and stay incomplete, including
	// an empty accepted set after skipped rows so we do not confirm the market.
	if stats.raw == 0 && stats.accepted == 0 {
		logHodlHodlFetch(currency, direction, offers, stats, fetchStatusFailed)
		return nil
	}
	logHodlHodlFetch(currency, direction, offers, stats, fetchStatusIncomplete)
	return incompleteFetch(float64(btcPrice), offers)
}

func logHodlHodlFetch(currency, direction string, offers []offer, stats hodlHodlFetchStats, status fetchStatus) {
	fmt.Printf("[HodlHodl] %s %s fetched=%d accepted=%d duplicates=%d unsupported=%d malformed=%d mismatches=%d pages=%d status=%s\n",
		currency, direction, stats.raw, len(offers), stats.duplicates, stats.unsupported, stats.malformed, stats.mismatches, stats.pages, status)
}

func fetchHodlHodlPage(ctx context.Context, client *http.Client, baseURL, direction, currency string, offset int) ([]hodlHodlOfferJSON, error) {
	reqURL := hodlHodlOffersURL(baseURL, direction, currency, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	body, readErr := readAllLimited(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("response read error: %w", readErr)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited (HTTP 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return parseHodlHodlOffersResponse(body)
}

func hodlHodlOffersURL(baseURL, direction, currency string, offset int) string {
	return fmt.Sprintf(
		"%s/api/v1/offers?filters[side]=%s&filters[include_global]=true&filters[currency_code]=%s&filters[only_working_now]=true&sort[by]=price&pagination[limit]=%d&pagination[offset]=%d",
		strings.TrimRight(baseURL, "/"),
		externalOfferDirection(direction),
		url.QueryEscape(currency),
		hodlHodlPageSize,
		offset,
	)
}

func parseHodlHodlOffersResponse(body []byte) ([]hodlHodlOfferJSON, error) {
	var env struct {
		Status string          `json:"status"`
		Offers json.RawMessage `json:"offers"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(env.Status), "success") {
		return nil, fmt.Errorf("api status %q", strings.TrimSpace(env.Status))
	}
	if len(env.Offers) == 0 || bytes.Equal(bytes.TrimSpace(env.Offers), []byte("null")) {
		return nil, fmt.Errorf("missing offers array")
	}
	var offers []hodlHodlOfferJSON
	if err := json.Unmarshal(env.Offers, &offers); err != nil {
		return nil, fmt.Errorf("malformed offers array: %w", err)
	}
	return offers, nil
}

func mapHodlHodlOffer(raw hodlHodlOfferJSON, direction, currency string) (offer, hodlHodlMapStatus) {
	id := strings.TrimSpace(raw.ID)
	if id == "" {
		return offer{}, hodlHodlSkipMalformed
	}

	assetCode := strings.ToUpper(strings.TrimSpace(raw.AssetCode))
	assetLayer := strings.ToUpper(strings.TrimSpace(raw.AssetLayer))
	if assetCode != "" && assetCode != "BTC" {
		return offer{}, hodlHodlSkipUnsupported
	}
	if assetLayer != "" && assetLayer != "BTC" {
		return offer{}, hodlHodlSkipUnsupported
	}

	if !strings.EqualFold(strings.TrimSpace(raw.CurrencyCode), strings.TrimSpace(currency)) ||
		!strings.EqualFold(strings.TrimSpace(raw.Side), externalOfferDirection(direction)) {
		return offer{}, hodlHodlSkipMismatch
	}

	price, ok := parseHodlHodlPositiveFloat(raw.Price)
	if !ok {
		return offer{}, hodlHodlSkipMalformed
	}

	btcMin, btcMax, ok := hodlHodlBTCRange(raw, price)
	if !ok {
		return offer{}, hodlHodlSkipMalformed
	}
	fiatMin, fiatMax, ok := hodlHodlFiatRange(raw, price, btcMin, btcMax)
	if !ok {
		return offer{}, hodlHodlSkipMalformed
	}

	mapped := offer{
		OfferID:        id,
		FiatCurrency:   currency,
		Price:          price,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		FiatMin:        &fiatMin,
		FiatMax:        &fiatMax,
		PaymentMethods: hodlHodlPaymentMethodNames(raw, direction),
		PriceType:      normalizeHodlHodlPriceType(raw.PriceSource),
	}
	applyRating(&mapped, mapHodlHodlTrader(raw.Trader))
	mapped.SellerRatingSignals = mapHodlHodlSignals(raw.Trader)
	mapped.MakerID = raw.Trader.Login
	return mapped, hodlHodlMapped
}

func hodlHodlBTCRange(raw hodlHodlOfferJSON, price float64) (float64, float64, bool) {
	satsMin, satsMinOK := parseHodlHodlSats(raw.MinAmountSats)
	satsMax, satsMaxOK := parseHodlHodlSats(raw.MaxAmountSats)
	if satsMinOK && satsMaxOK {
		if satsMax < satsMin || satsMin <= 0 || satsMax <= 0 {
			return 0, 0, false
		}
		return satsMin, satsMax, true
	}

	fiatMin, fiatMinOK := parseHodlHodlPositiveFloat(raw.MinAmount)
	fiatMax, fiatMaxOK := parseHodlHodlPositiveFloat(raw.MaxAmount)
	if !fiatMinOK || !fiatMaxOK || price <= 0 {
		return 0, 0, false
	}
	btcMin := round8(fiatMin / price)
	btcMax := round8(fiatMax / price)
	if !isPositiveFinite(btcMin) || !isPositiveFinite(btcMax) || btcMax < btcMin {
		return 0, 0, false
	}
	return btcMin, btcMax, true
}

func hodlHodlFiatRange(raw hodlHodlOfferJSON, price, btcMin, btcMax float64) (int, int, bool) {
	var fiatMinDec, fiatMaxDec float64
	var ok bool
	if fiatMinDec, ok = parseHodlHodlNonNegativeFloat(raw.MinAmount); !ok {
		fiatMinDec = btcMin * price
	}
	if fiatMaxDec, ok = parseHodlHodlNonNegativeFloat(raw.MaxAmount); !ok {
		fiatMaxDec = btcMax * price
	}
	fiatMin, minOK := truncateFiatAmount(fiatMinDec)
	fiatMax, maxOK := truncateFiatAmount(fiatMaxDec)
	if !minOK || !maxOK || fiatMax < fiatMin {
		return 0, 0, false
	}
	return fiatMin, fiatMax, true
}

func hodlHodlPaymentMethodNames(raw hodlHodlOfferJSON, direction string) []string {
	var names []string
	if direction == "bid" {
		for _, pm := range raw.PaymentMethods {
			names = append(names, pm.Name)
		}
	} else {
		for _, pmi := range raw.PaymentMethodInstructions {
			names = append(names, pmi.PaymentMethodName)
		}
	}
	return uniqueTrimmedStrings(names)
}

func uniqueTrimmedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func parseHodlHodlSats(raw string) (float64, bool) {
	sats, ok := parseHodlHodlPositiveFloat(raw)
	if !ok {
		return 0, false
	}
	btc := round8(sats / 1e8)
	if !isPositiveFinite(btc) {
		return 0, false
	}
	return btc, true
}

func parseHodlHodlPositiveFloat(raw string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || !isPositiveFinite(v) {
		return 0, false
	}
	return v, true
}

func parseHodlHodlNonNegativeFloat(raw string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || !isNonNegativeFinite(v) {
		return 0, false
	}
	return v, true
}

func truncateFiatAmount(v float64) (int, bool) {
	if !isNonNegativeFinite(v) || v >= float64(math.MaxInt) {
		return 0, false
	}
	return int(v), true
}

func isPositiveFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func isNonNegativeFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}
