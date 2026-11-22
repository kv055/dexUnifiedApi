package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"commons/nostrreq"
	"dexunifiedapi/internal/sellerrating"
)

const (
	nip69OrderKind = 38383
	nip69InfoKind  = 38385
)

var mostroRelays = nostrreq.MostroRelays

type nip69QueryFunc func(ctx context.Context, relays []string, filter map[string]any) (nostrreq.QueryResult, error)

var queryNIP69 nip69QueryFunc = nostrreq.QueryWithStats

type nip69Book struct {
	events   []nostrreq.Event
	complete bool
	failed   bool
	err      error
}

type nip69Cache struct {
	mu       sync.Mutex
	books    map[string]*nip69Book
	inflight map[string]chan struct{}
}

func newNIP69Cache() *nip69Cache {
	return &nip69Cache{
		books:    make(map[string]*nip69Book),
		inflight: make(map[string]chan struct{}),
	}
}

var nip69Books = newNIP69Cache()

func resetNIP69Cache() {
	nip69Books = newNIP69Cache()
}

func fetchMostroCurrency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult {
	return fetchNIP69Currency(ctx, "mostro", mostroRelays, direction, currency, btcPrice)
}

func fetchNIP69Currency(ctx context.Context, platform string, relays []string, direction, currency string, btcPrice int) *fetchResult {
	return fetchNIP69CurrencyFrom(ctx, platform, relays, direction, currency, btcPrice, queryNIP69)
}

func fetchNIP69CurrencyFrom(ctx context.Context, platform string, relays []string, direction, currency string, btcPrice int, query nip69QueryFunc) *fetchResult {
	if btcPrice <= 0 {
		slog.WarnContext(ctx, "NIP-69 market reference price unavailable", "exchange", platform, "currency", currency, "direction", direction)
		return nil
	}
	book := loadNIP69Book(ctx, platform, relays, query)
	if book == nil || book.failed {
		return nil
	}

	wantKind := externalOfferDirection(direction)
	var offers []offer
	for _, event := range book.events {
		mapped, ok := mapNIP69Event(event, platform, wantKind, currency, btcPrice)
		if !ok {
			continue
		}
		offers = append(offers, mapped)
	}
	fmt.Printf("[%s] Found %d %s %s offers\n", displayNIP69(platform), len(offers), currency, direction)
	if !book.complete && len(offers) == 0 {
		return nil
	}
	if book.complete {
		return completeFetch(float64(btcPrice), offers)
	}
	return incompleteFetch(float64(btcPrice), offers)
}

func loadNIP69Book(ctx context.Context, platform string, relays []string, query nip69QueryFunc) *nip69Book {
	return nip69Books.load(platform, func() *nip69Book {
		if query == nil {
			query = queryNIP69
		}
		// Keep the REQ small: public relays often CLOSED complex tag sets.
		// Currency, document type, and network are filtered in mapNIP69Event.
		filter := map[string]any{
			"kinds": []int{nip69OrderKind},
			"#s":    []string{"pending"},
			"#y":    []string{platform},
		}
		result, err := query(ctx, relays, filter)
		book := &nip69Book{
			events:   result.Events,
			complete: result.Complete,
			failed:   err != nil && len(result.Events) == 0,
			err:      err,
		}
		logNIP69Census(platform, relays, result)
		if book.failed {
			fmt.Printf("[%s] NIP-69 census failed: %v\n", displayNIP69(platform), err)
		}
		return book
	})
}

func (c *nip69Cache) load(platform string, query func() *nip69Book) *nip69Book {
	c.mu.Lock()
	if book, ok := c.books[platform]; ok {
		c.mu.Unlock()
		return book
	}
	if wait, ok := c.inflight[platform]; ok {
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
		book := c.books[platform]
		c.mu.Unlock()
		return book
	}
	done := make(chan struct{})
	c.inflight[platform] = done
	c.mu.Unlock()

	book := query()

	c.mu.Lock()
	c.books[platform] = book
	delete(c.inflight, platform)
	close(done)
	c.mu.Unlock()
	return book
}

func mapNIP69Event(event nostrreq.Event, platform, wantKind, currency string, btcPrice int) (offer, bool) {
	if event.Kind != nip69OrderKind {
		return offer{}, false
	}
	if status := strings.ToLower(event.TagValue("s")); status != "" && status != "pending" {
		return offer{}, false
	}
	if z := strings.ToLower(event.TagValue("z")); z != "" && z != "order" {
		return offer{}, false
	}
	y := strings.ToLower(event.TagValue("y"))
	if y != "" && y != platform {
		return offer{}, false
	}
	network := strings.ToLower(event.TagValue("network"))
	if network != "" && network != "mainnet" {
		return offer{}, false
	}
	layer := strings.ToLower(event.TagValue("layer"))
	if layer != "" && layer != "lightning" {
		return offer{}, false
	}
	if strings.ToLower(event.TagValue("k")) != wantKind {
		return offer{}, false
	}
	fiatCode := strings.ToUpper(event.TagValue("f"))
	if fiatCode != strings.ToUpper(currency) {
		return offer{}, false
	}
	offerID := strings.TrimSpace(event.TagValue("d"))
	if offerID == "" {
		return offer{}, false
	}

	reject := func(reason string) (offer, bool) {
		slog.Warn("NIP-69 quote rejected", "exchange", platform, "offer_id", offerID, "reason", reason)
		return offer{}, false
	}
	amtSats, err := parseNIP69Number(event.TagValue("amt"))
	if err != nil || amtSats < 0 || math.Trunc(amtSats) != amtSats {
		return reject("invalid sat amount")
	}
	fiatMin, fiatMax, ok := parseFiatAmount(event.TagValues("fa"))
	if !ok {
		return reject("invalid fiat limits")
	}
	var premium float64
	for _, tag := range event.Tags {
		if len(tag) == 0 || tag[0] != "premium" {
			continue
		}
		if len(tag) != 2 {
			return reject("invalid premium")
		}
		premium, err = parseNIP69Number(tag[1])
		if err != nil {
			return reject("invalid premium")
		}
		break
	}

	price, priceType, err := nip69Price(amtSats, fiatMin, fiatMax, premium, btcPrice)
	if err != nil {
		return reject(err.Error())
	}

	btcMin := round8(float64(fiatMin) / price)
	btcMax := round8(float64(fiatMax) / price)
	if !positiveFinite(btcMin) || !positiveFinite(btcMax) {
		return reject("invalid BTC limits")
	}
	methods := paymentMethodsFromPM(event.TagValues("pm"))

	out := offer{
		OfferID:              offerID,
		FiatCurrency:         fiatCode,
		VolumeDenomination:   fiatVolumeDenomination(fiatCode),
		Price:                price,
		BTCMin:               &btcMin,
		BTCMax:               &btcMax,
		FiatMin:              &fiatMin,
		FiatMax:              &fiatMax,
		PaymentMethods:       methods,
		PriceType:            priceType,
		VenueNodePubKey:      event.PubKey,
		VenueNodeName:        secondTagValue(event, "y"),
		SourceEventID:        event.ID,
		SourceEventCreatedAt: time.Unix(event.CreatedAt, 0).UTC(),
	}
	if pct, btc := securityDepositFromBond(event.TagValue("bond"), amtSats, btcMax); pct != nil {
		out.SecurityDepositPct = pct
		out.SecurityDepositBTC = btc
	}
	applyRating(&out, mapNIP69Rating(event.TagValues("rating")))
	out.SellerRatingSignals = mapNIP69Signals(event.TagValues("rating"))
	return out, true
}

func secondTagValue(event nostrreq.Event, name string) string {
	for _, tag := range event.Tags {
		if len(tag) > 2 && tag[0] == name {
			return strings.TrimSpace(tag[2])
		}
	}
	return ""
}

// nip69FixedPriceMaxDeviation is a product filter, not a protocol constraint:
// unusually priced fixed orders are excluded, never rewritten as floating.
const nip69FixedPriceMaxDeviation = 0.5

// nip69Price prefers a sat-implied fixed price only for a single fiat amount
// whose implied fiat/BTC is still near spot. Range orders use the floating
// quote because pending `amt` can describe one take slice, not the fiat range.
func nip69Price(amtSats float64, fiatMin, fiatMax int, premium float64, btcPrice int) (float64, *string, error) {
	if btcPrice <= 0 {
		return 0, nil, fmt.Errorf("reference price unavailable")
	}
	if amtSats > 0 && fiatMin == fiatMax {
		btc := amtSats / 1e8
		price := float64(fiatMin) / btc
		if !positiveFinite(price) || !impliedPriceNearSpot(price, btcPrice) {
			return 0, nil, fmt.Errorf("fixed price outside spot tolerance")
		}
		return price, priceTypeFixed(), nil
	}
	if premium >= 100 {
		return 0, nil, fmt.Errorf("floating premium must be below 100 percent")
	}
	// Mostro get_market_quote subtracts premium percent from the sats:
	// sats = fiat / spot * 1e8 * (1 - premium/100).
	price := float64(btcPrice) / (1 - premium/100)
	if !positiveFinite(price) {
		return 0, nil, fmt.Errorf("invalid floating price")
	}
	return price, priceTypeFloating(), nil
}

func positiveFinite(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func parseNIP69Number(raw string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid number")
	}
	return v, nil
}

func impliedPriceNearSpot(price float64, btcPrice int) bool {
	if btcPrice <= 0 || price <= 0 {
		return false
	}
	ratio := price / float64(btcPrice)
	return ratio >= 1-nip69FixedPriceMaxDeviation && ratio <= 1+nip69FixedPriceMaxDeviation
}

func parseFiatAmount(values []string) (int, int, bool) {
	if len(values) == 0 || len(values) > 2 {
		return 0, 0, false
	}
	// Mostro publishes integer fiat amounts. Do not truncate malformed ranges
	// into a single amount and accidentally enable the fixed-price path.
	minV, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil || minV <= 0 {
		return 0, 0, false
	}
	maxV := minV
	if len(values) >= 2 {
		maxV, err = strconv.Atoi(strings.TrimSpace(values[1]))
		if err != nil || maxV <= 0 {
			return 0, 0, false
		}
	}
	if maxV < minV {
		minV, maxV = maxV, minV
	}
	return minV, maxV, true
}

func parseTagFloat(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return v
}

func paymentMethodsFromPM(values []string) []string {
	var out []string
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func securityDepositFromBond(raw string, amtSats, btcMax float64) (*float64, *float64) {
	bond := parseTagFloat(raw)
	if bond <= 0 {
		return nil, nil
	}
	var pct float64
	if bond <= 1 {
		pct = bond
	} else if amtSats > 0 {
		pct = bond / amtSats
	} else if btcMax > 0 {
		pct = (bond / 1e8) / btcMax
	} else {
		return nil, nil
	}
	if pct <= 0 {
		return nil, nil
	}
	depositBTC := round8(pct * btcMax)
	return &pct, &depositBTC
}

func mapNIP69Rating(values []string) sellerrating.Score {
	parsed, ok := parseNIP69RatingPayload(values)
	if !ok || parsed.TotalRating == nil {
		return sellerrating.Unavailable()
	}
	maxRate := parsed.MaxRate
	if maxRate <= 0 {
		maxRate = 5
	}
	frac := *parsed.TotalRating / maxRate
	raw, _ := json.Marshal(map[string]any{
		"total_rating":  *parsed.TotalRating,
		"total_reviews": parsed.TotalReviews,
		"max_rate":      maxRate,
	})
	return sellerrating.NormalizeFeedbackRating(
		&frac, parsed.TotalReviews,
		sellerrating.DefaultFeedbackPriorMean,
		sellerrating.DefaultFeedbackPriorWeight,
		sellerrating.DefaultFeedbackVerifiedThreshold,
		raw,
	)
}

type nip69RatingPayload struct {
	TotalRating  *float64 `json:"total_rating"`
	TotalReviews int      `json:"total_reviews"`
	MaxRate      float64  `json:"max_rate"`
	LastRating   *float64 `json:"last_rating"`
	MinRate      *float64 `json:"min_rate"`
	Days         *float64 `json:"days"`
}

func parseNIP69RatingPayload(values []string) (nip69RatingPayload, bool) {
	var parsed nip69RatingPayload
	rawText := strings.Join(values, "")
	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return parsed, false
	}
	payload := []byte(rawText)
	var envelope []json.RawMessage
	if json.Unmarshal(payload, &envelope) == nil && len(envelope) >= 2 {
		payload = envelope[len(envelope)-1]
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return parsed, false
	}
	return parsed, true
}

func mapNIP69Signals(values []string) json.RawMessage {
	parsed, ok := parseNIP69RatingPayload(values)
	if !ok {
		return nil
	}
	return marshalSignals(map[string]any{
		"source":        "mostro",
		"days":          parsed.Days,
		"total_reviews": parsed.TotalReviews,
		"last_rating":   parsed.LastRating,
		"min_rate":      parsed.MinRate,
		"max_rate":      parsed.MaxRate,
	})
}

func logNIP69Census(platform string, relays []string, result nostrreq.QueryResult) {
	label := displayNIP69(platform)
	fmt.Printf("[%s] NIP-69 census complete=%v relays=%d connected=%d eose=%d events=%d\n",
		label, result.Complete, len(relays), result.Connected, result.EOSE, len(result.Events))
	for _, relay := range result.Relays {
		if relay.Err != nil {
			fmt.Printf("[%s] relay=%s events=%d eose=%v err=%v\n", label, relay.URL, relay.Events, relay.EOSE, relay.Err)
			continue
		}
		fmt.Printf("[%s] relay=%s events=%d eose=%v\n", label, relay.URL, relay.Events, relay.EOSE)
	}
}

func displayNIP69(platform string) string {
	if platform == "mostro" {
		return "Mostro"
	}
	return platform
}
