package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"dexunifiedapi/internal/sellerrating"
)

// Noones is a KYC-light fiat P2P marketplace of the Paxful/LocalBitcoins
// lineage. Like Peach it is integrated snapshot-only: offer ingestion plus the
// maker reputation that ships inline with every listed offer. There is no
// taker connector yet — the point is watching for premium offers.
//
// Wire format: an OAuth2 client-credentials JWT from auth.noones.com (default
// lifetime 10 days), then POST offer/all under api.noones.com/noones/v1. The
// marketplace caches offer/all results for one minute, so one cycle's pages
// are stable. App-level errors return HTTP 200 with status:"error" — the
// envelope must be checked, not the status code.

const (
	// noonesAPIBaseURL carries the versioned /noones/v1 prefix (Paxful's was
	// /paxful/v1): the gateway answers the unprefixed /offer/all with 404
	// resource_not_found and the prefixed route with 401 without a token.
	noonesAPIBaseURL  = "https://api.noones.com/noones/v1"
	noonesAuthBaseURL = "https://auth.noones.com"

	// noonesPageSize is the page size we request. The response echoes the size
	// the server actually applied as data.limit, which paging trusts instead,
	// so an ignored or capped count can neither skip rows nor end the census
	// early on a page that only looks short.
	noonesPageSize = 50
	// noonesMaxPages bounds a lying totalCount so a stuck book neither loops
	// forever nor publishes a truncated result as authoritative.
	noonesMaxPages = 100
	// noonesDefaultTokenTTL is the documented client-credentials lifetime.
	noonesDefaultTokenTTL = 10 * 24 * time.Hour
	// noonesTokenExpiryMargin refreshes the JWT slightly before it expires so
	// a snapshot never races the deadline.
	noonesTokenExpiryMargin = time.Hour
)

// Snapshot-side OAuth credentials, read from the environment in init. Ingestion
// is opt-in: without them the venue is not registered in the fetcher list at
// all (see main), so an unconfigured deployment logs nothing rather than a
// failure per market.
var (
	noonesClientID     string
	noonesClientSecret string
)

func noonesConfigured() bool {
	return strings.TrimSpace(noonesClientID) != "" && strings.TrimSpace(noonesClientSecret) != ""
}

// --- OAuth2 client-credentials token ---

// noonesTokenSource mints a bearer token. A non-empty rejected is a token the
// API just answered 401 to: the source must not hand it out again. The
// indirection keeps the fetcher unit-testable without the auth endpoint.
type noonesTokenSource func(ctx context.Context, rejected string) (string, error)

// noonesTokenCache memoizes the JWT across the many markets of a cycle (and
// across cycles, given the 10-day lifetime).
type noonesTokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// get returns the cached JWT, minting one when none is cached, it is near
// expiry, or it is the rejected token. Comparing against rejected (rather than
// clearing unconditionally) means concurrent markets that all saw the same
// 401 re-mint once, not once each.
func (c *noonesTokenCache) get(ctx context.Context, client *http.Client, authBaseURL, rejected string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rejected != "" && c.token == rejected {
		c.token = ""
	}
	if c.token != "" && time.Now().Before(c.expiresAt) {
		return c.token, nil
	}
	token, ttl, err := requestNoonesToken(ctx, client, authBaseURL)
	if err != nil {
		return "", err
	}
	c.token = token
	// A short-lived token must still be cached for most of its life: a fixed
	// one-hour margin would expire anything under an hour on arrival.
	c.expiresAt = time.Now().Add(ttl - min(noonesTokenExpiryMargin, ttl/10))
	return c.token, nil
}

func requestNoonesToken(ctx context.Context, client *http.Client, authBaseURL string) (string, time.Duration, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {noonesClientID},
		"client_secret": {noonesClientSecret},
	}
	reqURL := strings.TrimRight(authBaseURL, "/") + "/oauth2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request failed: %w", err)
	}
	body, readErr := readAllLimited(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return "", 0, fmt.Errorf("response read error: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var payload struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", 0, fmt.Errorf("parse error: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return "", 0, fmt.Errorf("missing access_token")
	}
	ttl := noonesDefaultTokenTTL
	if payload.ExpiresIn > 0 {
		ttl = time.Duration(payload.ExpiresIn * float64(time.Second))
	}
	return payload.AccessToken, ttl, nil
}

var noonesTokens = &noonesTokenCache{}

// --- Offer listing ---

// noonesNumber tolerates the API family's mixed number/string encodings: the
// spec renders limits and feedback counts as bare numbers, but this Paxful
// lineage has shipped both quoted and unquoted values across deployments. An
// empty or unparsable value decodes to 0; field-level validation skips
// malformed rows.
type noonesNumber float64

func (n *noonesNumber) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*n = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			*n = 0
			return nil
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			*n = 0
			return nil
		}
		*n = noonesNumber(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		*n = 0
		return nil
	}
	*n = noonesNumber(v)
	return nil
}

func (n noonesNumber) Float() float64 { return float64(n) }

// noonesOffer is one offer/all row. Field names follow the public API
// reference. The listing's crypto_min/crypto_max limits are deliberately not
// mapped: their unit is undocumented, while the fiat range is the offer's
// authoritative trade limit.
type noonesOffer struct {
	OfferHash            string       `json:"offer_hash"`
	OfferID              string       `json:"offer_id"`
	OfferType            string       `json:"offer_type"`
	FiatCurrencyCode     string       `json:"fiat_currency_code"`
	CryptoCurrencyCode   string       `json:"crypto_currency_code"`
	FiatPricePerBTC      noonesNumber `json:"fiat_price_per_btc"`
	Margin               noonesNumber `json:"margin"`
	IsFixedPrice         *bool        `json:"is_fixed_price"`
	FiatAmountRangeMin   noonesNumber `json:"fiat_amount_range_min"`
	FiatAmountRangeMax   noonesNumber `json:"fiat_amount_range_max"`
	PaymentMethodSlug    string       `json:"payment_method_slug"`
	PaymentMethodName    string       `json:"payment_method_name"`
	OfferOwnerUsername   string       `json:"offer_owner_username"`
	OfferOwnerCountryISO string       `json:"offer_owner_country_iso"`
	FeedbackPositive     noonesNumber `json:"offer_owner_feedback_positive"`
	FeedbackNegative     noonesNumber `json:"offer_owner_feedback_negative"`
	PaymentWindow        noonesNumber `json:"payment_window"`
	FeePercentage        noonesNumber `json:"fee_percentage"`
	LastSeenTimestamp    noonesNumber `json:"last_seen_timestamp"`
	Active               *bool        `json:"active"`
	IsBlocked            *bool        `json:"is_blocked"`
}

type noonesAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *noonesAPIError) Error() string {
	return fmt.Sprintf("api error %d: %s", e.Code, e.Message)
}

type noonesListResponse struct {
	Status string          `json:"status"`
	Error  *noonesAPIError `json:"error"`
	// The envelope's counters use the same tolerant decoding as offer rows: a
	// quoted "50" here would otherwise fail the whole page, not just one row.
	Data struct {
		Count      noonesNumber  `json:"count"`
		Limit      noonesNumber  `json:"limit"`
		Offset     noonesNumber  `json:"offset"`
		TotalCount noonesNumber  `json:"totalCount"`
		Offers     []noonesOffer `json:"offers"`
	} `json:"data"`
}

// errNoonesUnauthorized marks an HTTP 401 from the offer API: the cached JWT
// was revoked or expired early, so the fetcher re-mints once and retries.
var errNoonesUnauthorized = errors.New("unauthorized (HTTP 401)")

type noonesMapStatus int

const (
	noonesMapped noonesMapStatus = iota
	noonesSkipUnsupported
	noonesSkipMalformed
	noonesSkipMismatch
	// noonesSkipInactive is a well-formed row the listing flags as inactive or
	// blocked: not takeable, but not a data-quality problem either.
	noonesSkipInactive
)

type noonesFetchStats struct {
	pages       int
	raw         int
	accepted    int
	duplicates  int
	unsupported int
	malformed   int
	mismatches  int
	inactive    int
}

func fetchNoonesCurrency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult {
	if !noonesConfigured() {
		fmt.Println("[Noones] API credentials not configured (NOONES_CLIENT_ID / NOONES_CLIENT_SECRET)")
		return nil
	}
	return fetchNoonesCurrencyFrom(ctx, clearnetClient(), noonesAPIBaseURL, func(ctx context.Context, rejected string) (string, error) {
		return noonesTokens.get(ctx, clearnetClient(), noonesAuthBaseURL, rejected)
	}, direction, currency, btcPrice)
}

func fetchNoonesCurrencyFrom(ctx context.Context, client *http.Client, apiBaseURL string, token noonesTokenSource, direction, currency string, btcPrice int) *fetchResult {
	if btcPrice <= 0 {
		fmt.Println("[Noones] Could not get reference price")
		return nil
	}
	tokenValue, err := token(ctx, "")
	if err != nil {
		fmt.Printf("[Noones] %s %s auth failed: %v\n", currency, direction, err)
		return nil
	}

	var (
		offers   []offer
		seenIDs  = make(map[string]struct{})
		stats    noonesFetchStats
		complete bool
		offset   int
		reminted bool
	)
	for page := 0; page < noonesMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			fmt.Printf("[Noones] %s %s cancelled: %v\n", currency, direction, err)
			return noonesFetchResult(currency, direction, btcPrice, offers, stats, false)
		}

		list, err := fetchNoonesPage(ctx, client, apiBaseURL, tokenValue, direction, currency, offset)
		stats.pages++
		if errors.Is(err, errNoonesUnauthorized) && !reminted {
			// Without this the 10-day cache would keep serving a revoked JWT,
			// failing every market until the worker restarts. One re-mint per
			// market: a second 401 means the credentials themselves are bad.
			reminted = true
			fresh, tokenErr := token(ctx, tokenValue)
			if tokenErr != nil {
				fmt.Printf("[Noones] %s %s re-auth after 401 failed: %v\n", currency, direction, tokenErr)
				return noonesFetchResult(currency, direction, btcPrice, offers, stats, false)
			}
			tokenValue = fresh
			list, err = fetchNoonesPage(ctx, client, apiBaseURL, tokenValue, direction, currency, offset)
			stats.pages++
		}
		if err != nil {
			fmt.Printf("[Noones] %s %s page offset=%d: %v\n", currency, direction, offset, err)
			return noonesFetchResult(currency, direction, btcPrice, offers, stats, false)
		}

		stats.raw += len(list.Data.Offers)
		newSourceIDs := 0
		for _, raw := range list.Data.Offers {
			id := noonesOfferIdentity(raw)
			if id != "" {
				if _, dup := seenIDs[id]; dup {
					stats.duplicates++
					continue
				}
				seenIDs[id] = struct{}{}
				newSourceIDs++
			}
			mapped, status := mapNoonesOffer(raw, direction, currency)
			switch status {
			case noonesMapped:
				offers = append(offers, mapped)
				stats.accepted++
			case noonesSkipUnsupported:
				stats.unsupported++
			case noonesSkipMismatch:
				stats.mismatches++
			case noonesSkipInactive:
				stats.inactive++
			default:
				stats.malformed++
			}
		}

		// Page by what the server applied, not what we asked for: if it ignored
		// count and served a smaller default, a fixed 50 would read its first
		// full page as short and confirm a truncated book as complete.
		pageSize := noonesPageSize
		if limit := int(list.Data.Limit.Float()); limit > 0 {
			pageSize = limit
		}
		rows := len(list.Data.Offers)
		if rows < pageSize {
			// A short page is the end of the book. Mismatched rows keep the
			// census incomplete, as with HodlHodl.
			complete = stats.mismatches == 0
			break
		}
		if totalCount := int(list.Data.TotalCount.Float()); totalCount > 0 && len(seenIDs) >= totalCount {
			complete = stats.mismatches == 0
			break
		}
		if newSourceIDs == 0 {
			fmt.Printf("[Noones] %s %s repeated full page with no new offer ids at offset=%d\n", currency, direction, offset)
			break
		}
		offset += rows
	}

	return noonesFetchResult(currency, direction, btcPrice, offers, stats, complete)
}

func noonesFetchResult(currency, direction string, btcPrice int, offers []offer, stats noonesFetchStats, complete bool) *fetchResult {
	if complete {
		logNoonesFetch(currency, direction, offers, stats, fetchStatusComplete)
		return completeFetch(float64(btcPrice), offers)
	}
	// A failed first page never parsed a valid offers array. Later-page
	// failures keep any offers already accepted and stay incomplete, including
	// an empty accepted set after skipped rows so we do not confirm the market.
	if stats.raw == 0 && stats.accepted == 0 {
		logNoonesFetch(currency, direction, offers, stats, fetchStatusFailed)
		return nil
	}
	logNoonesFetch(currency, direction, offers, stats, fetchStatusIncomplete)
	return incompleteFetch(float64(btcPrice), offers)
}

func logNoonesFetch(currency, direction string, offers []offer, stats noonesFetchStats, status fetchStatus) {
	fmt.Printf("[Noones] %s %s fetched=%d accepted=%d duplicates=%d unsupported=%d inactive=%d malformed=%d mismatches=%d pages=%d status=%s\n",
		currency, direction, stats.raw, len(offers), stats.duplicates, stats.unsupported, stats.inactive, stats.malformed, stats.mismatches, stats.pages, status)
}

func fetchNoonesPage(ctx context.Context, client *http.Client, apiBaseURL, token, direction, currency string, offset int) (*noonesListResponse, error) {
	form := url.Values{
		// `type` filters by the offer's own side. The alternative `offer_type`
		// filter inverts the perspective (offer_type=buy returns sell offers),
		// a needless foot-gun for a census.
		"type":          {externalOfferDirection(direction)},
		"currency_code": {currency},
		// WORLDWIDE opts out of the caller-country filter; the book must not
		// depend on where the snapshot worker egresses.
		"country_code":         {"WORLDWIDE"},
		"crypto_currency_code": {"BTC"},
		"count":                {strconv.Itoa(noonesPageSize)},
		"offset":               {strconv.Itoa(offset)},
	}
	reqURL := strings.TrimRight(apiBaseURL, "/") + "/offer/all"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	// Documented wire format: form-encoded body declared as text/plain, and
	// responses versioned via the Accept header.
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Accept", "application/json; version=1")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	body, readErr := readAllLimited(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("response read error: %w", readErr)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errNoonesUnauthorized
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited (HTTP 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return parseNoonesListResponse(body)
}

func parseNoonesListResponse(body []byte) (*noonesListResponse, error) {
	var list noonesListResponse
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(list.Status), "success") {
		if list.Error != nil {
			return nil, list.Error
		}
		return nil, fmt.Errorf("api status %q", strings.TrimSpace(list.Status))
	}
	return &list, nil
}

// noonesOfferIdentity picks the stable public id. Current listings carry a
// hash-like offer_hash; older Paxful-lineage listings only had the numeric
// offer_id, which is just as stable a row identity.
func noonesOfferIdentity(o noonesOffer) string {
	if id := strings.TrimSpace(o.OfferHash); id != "" {
		return id
	}
	return strings.TrimSpace(o.OfferID)
}

// mapNoonesOffer converts one offer/all row into the internal offer schema.
// fiat_price_per_btc is the offer price in its fiat currency and
// fiat_amount_range_* are the trade limits; the BTC range is derived from the
// fiat range and that price instead of crypto_min/crypto_max, whose unit is
// undocumented in the listing.
func mapNoonesOffer(o noonesOffer, direction, currency string) (offer, noonesMapStatus) {
	id := noonesOfferIdentity(o)
	if id == "" {
		return offer{}, noonesSkipMalformed
	}
	if o.Active != nil && !*o.Active {
		return offer{}, noonesSkipInactive
	}
	if o.IsBlocked != nil && *o.IsBlocked {
		return offer{}, noonesSkipInactive
	}

	wantSide := externalOfferDirection(direction)
	if side := strings.ToLower(strings.TrimSpace(o.OfferType)); side != "" && side != wantSide {
		return offer{}, noonesSkipMismatch
	}
	if code := strings.ToUpper(strings.TrimSpace(o.FiatCurrencyCode)); code != "" && code != strings.ToUpper(strings.TrimSpace(currency)) {
		return offer{}, noonesSkipMismatch
	}
	if code := strings.ToUpper(strings.TrimSpace(o.CryptoCurrencyCode)); code != "" && code != "BTC" {
		return offer{}, noonesSkipUnsupported
	}

	price := o.FiatPricePerBTC.Float()
	if !isPositiveFinite(price) {
		return offer{}, noonesSkipMalformed
	}

	fiatMinDec := o.FiatAmountRangeMin.Float()
	fiatMaxDec := o.FiatAmountRangeMax.Float()
	if !isNonNegativeFinite(fiatMinDec) || !isNonNegativeFinite(fiatMaxDec) ||
		fiatMinDec <= 0 || fiatMaxDec <= 0 || fiatMaxDec < fiatMinDec {
		return offer{}, noonesSkipMalformed
	}
	fiatMin, minOK := truncateFiatAmount(fiatMinDec)
	fiatMax, maxOK := truncateFiatAmount(fiatMaxDec)
	if !minOK || !maxOK || fiatMin <= 0 || fiatMax < fiatMin {
		return offer{}, noonesSkipMalformed
	}

	btcMin := round8(float64(fiatMin) / price)
	btcMax := round8(float64(fiatMax) / price)
	if !isPositiveFinite(btcMin) || !isPositiveFinite(btcMax) || btcMax < btcMin {
		return offer{}, noonesSkipMalformed
	}

	method := strings.TrimSpace(o.PaymentMethodName)
	if method == "" {
		method = strings.TrimSpace(o.PaymentMethodSlug)
	}
	if method == "" {
		return offer{}, noonesSkipMalformed
	}

	mapped := offer{
		OfferID:        id,
		FiatCurrency:   currency,
		Price:          price,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		FiatMin:        &fiatMin,
		FiatMax:        &fiatMax,
		PaymentMethods: []string{method},
		PriceType:      noonesPriceType(o),
		// The maker's username is inert provenance: unlike Peach, the listing
		// already carries the reputation, so nothing looks this id up.
		MakerID: strings.TrimSpace(o.OfferOwnerUsername),
	}
	applyRating(&mapped, mapNoonesReputation(o))
	mapped.SellerRatingSignals = mapNoonesSignals(o)
	return mapped, noonesMapped
}

// noonesPriceType reports fixed vs market-relative pricing. is_fixed_price is
// authoritative when present; otherwise a non-zero margin implies a floating
// offer and a zero margin reads as a fixed one.
func noonesPriceType(o noonesOffer) *string {
	if o.IsFixedPrice != nil {
		if *o.IsFixedPrice {
			return priceTypeFixed()
		}
		return priceTypeFloating()
	}
	if o.Margin.Float() != 0 {
		return priceTypeFloating()
	}
	return priceTypeFixed()
}

// mapNoonesReputation normalizes the inline maker feedback counts. The
// marketplace reports positive/negative counts, not a percentage, so the
// rating fraction is positive/(positive+negative) with the total as the
// sample size — the same feedback methodology as HodlHodl. Zero feedback stays
// Unavailable(); we do not mint a prior-only score.
func mapNoonesReputation(o noonesOffer) sellerrating.Score {
	positive := o.FeedbackPositive.Float()
	negative := o.FeedbackNegative.Float()
	sample := positive + negative
	raw, _ := json.Marshal(map[string]any{
		"feedback_positive": positive,
		"feedback_negative": negative,
	})
	if sample <= 0 || sample > float64(math.MaxInt32) {
		return sellerrating.NormalizeFeedbackRating(nil, 0,
			sellerrating.DefaultFeedbackPriorMean, sellerrating.DefaultFeedbackPriorWeight, sellerrating.DefaultFeedbackVerifiedThreshold,
			raw,
		)
	}
	frac := positive / sample
	return sellerrating.NormalizeFeedbackRating(&frac, int(sample),
		sellerrating.DefaultFeedbackPriorMean, sellerrating.DefaultFeedbackPriorWeight, sellerrating.DefaultFeedbackVerifiedThreshold,
		raw,
	)
}

func mapNoonesSignals(o noonesOffer) json.RawMessage {
	return marshalSignals(map[string]any{
		"source":              "noones",
		"username":            strings.TrimSpace(o.OfferOwnerUsername),
		"country_iso":         strings.TrimSpace(o.OfferOwnerCountryISO),
		"feedback_positive":   o.FeedbackPositive.Float(),
		"feedback_negative":   o.FeedbackNegative.Float(),
		"payment_window_min":  o.PaymentWindow.Float(),
		"fee_percentage":      o.FeePercentage.Float(),
		"last_seen_timestamp": o.LastSeenTimestamp.Float(),
	})
}
