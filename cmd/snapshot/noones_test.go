package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"dexunifiedapi/internal/sellerrating"
)

func staticNoonesToken(token string) noonesTokenSource {
	return func(context.Context, string) (string, error) { return token, nil }
}

// noonesOfferJSON builds one listing row. Prices and margins are quoted and
// limits/counts bare on purpose: the tolerant number decoder must accept the
// API family's mixed encodings.
func noonesOfferJSON(id string) string {
	return fmt.Sprintf(`{"offer_hash":%q,"offer_type":"sell","fiat_currency_code":"EUR","crypto_currency_code":"BTC","fiat_price_per_btc":"63000.5","margin":"1.5","fiat_amount_range_min":50,"fiat_amount_range_max":500,"payment_method_slug":"wise","payment_method_name":"Wise","offer_owner_username":"maker-a","offer_owner_country_iso":"DE","offer_owner_feedback_positive":10,"offer_owner_feedback_negative":1,"payment_window":90,"fee_percentage":0.5,"last_seen_timestamp":1455032576}`, id)
}

func noonesListJSON(totalCount int, offers ...string) string {
	return fmt.Sprintf(`{"status":"success","data":{"count":%d,"limit":50,"offset":0,"totalCount":%d,"offers":[%s]}}`,
		len(offers), totalCount, strings.Join(offers, ","))
}

// noonesFullPageIDs returns a full page's worth of unique offer ids so a test
// fixture exercises real offset-paging semantics (only the final page is
// allowed to come back short).
func noonesFullPageIDs(prefix string, start, end int) []string {
	offers := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		offers = append(offers, noonesOfferJSON(fmt.Sprintf("%s-%d", prefix, i)))
	}
	return offers
}

func noonesOfferForTest(id string) noonesOffer {
	var o noonesOffer
	if err := json.Unmarshal([]byte(noonesOfferJSON(id)), &o); err != nil {
		panic(err)
	}
	return o
}

func TestMapNoonesOffer(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*noonesOffer)
		direction string
		currency  string
		status    noonesMapStatus
		check     func(*testing.T, offer)
	}{
		{
			name:      "valid sell offer",
			direction: "ask",
			currency:  "EUR",
			status:    noonesMapped,
			check: func(t *testing.T, got offer) {
				if got.OfferID != "hash-1" {
					t.Fatalf("OfferID = %q, want hash-1", got.OfferID)
				}
				if got.FiatCurrency != "EUR" {
					t.Fatalf("FiatCurrency = %q, want EUR", got.FiatCurrency)
				}
				if got.Price != 63000.5 {
					t.Fatalf("Price = %v, want 63000.5", got.Price)
				}
				if got.FiatMin == nil || *got.FiatMin != 50 || got.FiatMax == nil || *got.FiatMax != 500 {
					t.Fatalf("fiat range = %v..%v, want 50..500", got.FiatMin, got.FiatMax)
				}
				if got.BTCMin == nil || got.BTCMax == nil || *got.BTCMax < *got.BTCMin {
					t.Fatalf("btc range = %v..%v, want ordered", got.BTCMin, got.BTCMax)
				}
				if len(got.PaymentMethods) != 1 || got.PaymentMethods[0] != "Wise" {
					t.Fatalf("PaymentMethods = %v, want [Wise]", got.PaymentMethods)
				}
				if got.PriceType == nil || *got.PriceType != "floating" {
					t.Fatalf("PriceType = %v, want floating (non-zero margin)", got.PriceType)
				}
				if got.MakerID != "maker-a" {
					t.Fatalf("MakerID = %q, want maker-a", got.MakerID)
				}
			},
		},
		{
			name: "fixed price flag wins over margin",
			mutate: func(o *noonesOffer) {
				fixed := true
				o.IsFixedPrice = &fixed
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesMapped,
			check: func(t *testing.T, got offer) {
				if got.PriceType == nil || *got.PriceType != "fixed" {
					t.Fatalf("PriceType = %v, want fixed", got.PriceType)
				}
			},
		},
		{
			name: "quoted limits and counts decode",
			mutate: func(o *noonesOffer) {
				o.FiatAmountRangeMin = noonesNumber(100)
				o.FiatAmountRangeMax = noonesNumber(900)
				o.FeedbackPositive = noonesNumber(7)
				o.FeedbackNegative = noonesNumber(3)
				o.Margin = 0
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesMapped,
			check: func(t *testing.T, got offer) {
				if got.FiatMin == nil || *got.FiatMin != 100 || got.FiatMax == nil || *got.FiatMax != 900 {
					t.Fatalf("fiat range = %v..%v, want 100..900", got.FiatMin, got.FiatMax)
				}
				if got.PriceType == nil || *got.PriceType != "fixed" {
					t.Fatalf("PriceType = %v, want fixed (zero margin)", got.PriceType)
				}
			},
		},
		{
			name: "numeric offer_id is a fallback identity",
			mutate: func(o *noonesOffer) {
				o.OfferHash = ""
				o.OfferID = "12345"
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesMapped,
			check: func(t *testing.T, got offer) {
				if got.OfferID != "12345" {
					t.Fatalf("OfferID = %q, want 12345", got.OfferID)
				}
			},
		},
		{
			name: "currency mismatch skipped",
			mutate: func(o *noonesOffer) {
				o.FiatCurrencyCode = "USD"
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipMismatch,
		},
		{
			name: "side mismatch skipped",
			mutate: func(o *noonesOffer) {
				o.OfferType = "buy"
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipMismatch,
		},
		{
			name: "non-btc crypto unsupported",
			mutate: func(o *noonesOffer) {
				o.CryptoCurrencyCode = "USDT"
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipUnsupported,
		},
		{
			name: "no identity malformed",
			mutate: func(o *noonesOffer) {
				o.OfferHash = ""
				o.OfferID = ""
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipMalformed,
		},
		{
			name: "zero fiat range malformed",
			mutate: func(o *noonesOffer) {
				o.FiatAmountRangeMax = 0
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipMalformed,
		},
		{
			name: "inactive offer skipped as inactive",
			mutate: func(o *noonesOffer) {
				active := false
				o.Active = &active
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipInactive,
		},
		{
			name: "blocked offer skipped as inactive",
			mutate: func(o *noonesOffer) {
				blocked := true
				o.IsBlocked = &blocked
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipInactive,
		},
		{
			name: "missing payment method malformed",
			mutate: func(o *noonesOffer) {
				o.PaymentMethodName = ""
				o.PaymentMethodSlug = ""
			},
			direction: "ask",
			currency:  "EUR",
			status:    noonesSkipMalformed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := noonesOfferForTest("hash-1")
			if tc.mutate != nil {
				tc.mutate(&raw)
			}
			got, status := mapNoonesOffer(raw, tc.direction, tc.currency)
			if status != tc.status {
				t.Fatalf("status = %d, want %d", status, tc.status)
			}
			if tc.check != nil && status == noonesMapped {
				tc.check(t, got)
			}
		})
	}
}

func TestNoonesNumberDecodesMixedEncodings(t *testing.T) {
	var decoded struct {
		Bare   noonesNumber `json:"bare"`
		Quoted noonesNumber `json:"quoted"`
		Empty  noonesNumber `json:"empty"`
		Junk   noonesNumber `json:"junk"`
		Null   noonesNumber `json:"null_field"`
	}
	if err := json.Unmarshal([]byte(`{"bare":50,"quoted":"63000.5","empty":"","junk":"oops","null_field":null}`), &decoded); err != nil {
		t.Fatalf("decode mixed row: %v", err)
	}
	if decoded.Bare.Float() != 50 || decoded.Quoted.Float() != 63000.5 {
		t.Fatalf("bare/quoted = %v/%v, want 50/63000.5", decoded.Bare, decoded.Quoted)
	}
	if decoded.Empty.Float() != 0 || decoded.Junk.Float() != 0 || decoded.Null.Float() != 0 {
		t.Fatalf("empty/junk/null = %v/%v/%v, want zeros", decoded.Empty, decoded.Junk, decoded.Null)
	}
}

func TestMapNoonesOfferReputation(t *testing.T) {
	raw := noonesOfferForTest("hash-1")
	got, status := mapNoonesOffer(raw, "ask", "EUR")
	if status != noonesMapped {
		t.Fatalf("status = %d, want mapped", status)
	}

	// 10 positive + 1 negative: fraction 10/11, sample 11 — the same shared
	// feedback methodology HodlHodl uses, so the score is derived, not bespoke.
	frac := 10.0 / 11.0
	want := sellerrating.NormalizeFeedbackRating(
		&frac, 11,
		sellerrating.DefaultFeedbackPriorMean, sellerrating.DefaultFeedbackPriorWeight, sellerrating.DefaultFeedbackVerifiedThreshold,
		json.RawMessage(`{"feedback_negative":1,"feedback_positive":10}`),
	)
	if got.SellerRatingScore == nil || *got.SellerRatingScore != *want.Value {
		t.Fatalf("SellerRatingScore = %v, want %v", got.SellerRatingScore, want.Value)
	}
	if got.SellerRatingConfidence == nil || *got.SellerRatingConfidence != string(sellerrating.ConfidenceFeedbackNew) {
		t.Fatalf("SellerRatingConfidence = %v, want feedback_new", got.SellerRatingConfidence)
	}
	var signals map[string]any
	if err := json.Unmarshal(got.SellerRatingSignals, &signals); err != nil {
		t.Fatalf("parse signals: %v", err)
	}
	if signals["username"] != "maker-a" || signals["country_iso"] != "DE" {
		t.Fatalf("signals = %v, want maker identity", signals)
	}

	// Zero feedback never mints a prior-only score.
	raw.FeedbackPositive = 0
	raw.FeedbackNegative = 0
	got, status = mapNoonesOffer(raw, "ask", "EUR")
	if status != noonesMapped {
		t.Fatalf("status = %d, want mapped", status)
	}
	if got.SellerRatingScore != nil {
		t.Fatalf("SellerRatingScore = %v, want nil", got.SellerRatingScore)
	}
	if got.SellerRatingConfidence == nil || *got.SellerRatingConfidence != string(sellerrating.ConfidenceUnavailable) {
		t.Fatalf("SellerRatingConfidence = %v, want unavailable", got.SellerRatingConfidence)
	}
}

func TestFetchNoonesCurrencyFromPaginatesUntilTotalCount(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/offer/all" {
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
		if got := req.Header.Get("Content-Type"); got != "text/plain" {
			t.Errorf("Content-Type = %q, want text/plain", got)
		}
		if got := req.Header.Get("Accept"); got != "application/json; version=1" {
			t.Errorf("Accept = %q, want application/json; version=1", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		body, _ := io.ReadAll(req.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("parse form body: %v", err)
			return jsonResponse(http.StatusBadRequest, `{}`), nil
		}
		for key, want := range map[string]string{
			"type":                 "sell",
			"currency_code":        "EUR",
			"country_code":         "WORLDWIDE",
			"crypto_currency_code": "BTC",
			"count":                "50",
		} {
			if got := form.Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		// A book of exactly two full pages: totalCount must end the census
		// without a third, empty request.
		switch form.Get("offset") {
		case "0":
			calls++
			return jsonResponse(http.StatusOK, noonesListJSON(100, noonesFullPageIDs("hash", 1, 50)...)), nil
		case "50":
			calls++
			return jsonResponse(http.StatusOK, noonesListJSON(100, noonesFullPageIDs("hash", 51, 100)...)), nil
		default:
			t.Errorf("unexpected offset %q", form.Get("offset"))
			return jsonResponse(http.StatusBadRequest, `{}`), nil
		}
	})}

	result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", staticNoonesToken("test-token"), "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete fetch", result)
	}
	if len(result.Offers) != 100 {
		t.Fatalf("offers = %d, want 100", len(result.Offers))
	}
	if calls != 2 {
		t.Fatalf("pages fetched = %d, want 2", calls)
	}
}

func TestFetchNoonesCurrencyFromShortPageCompletes(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, noonesListJSON(1, noonesOfferJSON("hash-1"))), nil
	})}
	result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", staticNoonesToken("test-token"), "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete fetch", result)
	}
	if len(result.Offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(result.Offers))
	}
}

func TestFetchNoonesCurrencyFromDeduplicatesRepeatedRows(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		calls++
		if form.Get("offset") == "0" {
			return jsonResponse(http.StatusOK, noonesListJSON(100, noonesFullPageIDs("hash", 1, 50)...)), nil
		}
		// A source that repeats a full page must not double-count rows.
		return jsonResponse(http.StatusOK, noonesListJSON(100, noonesFullPageIDs("hash", 1, 50)...)), nil
	})}
	result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", staticNoonesToken("test-token"), "ask", "EUR", 60000)
	if result == nil {
		t.Fatal("result = nil, want an incomplete fetch")
	}
	if result.status() != fetchStatusIncomplete {
		t.Fatalf("status = %q, want incomplete (no new ids on the repeated page)", result.status())
	}
	if len(result.Offers) != 50 {
		t.Fatalf("offers = %d, want 50 unique rows", len(result.Offers))
	}
	if calls != 2 {
		t.Fatalf("pages fetched = %d, want 2", calls)
	}
}

func TestFetchNoonesCurrencyFromMismatchKeepsIncomplete(t *testing.T) {
	// The server-side type/currency filters lied: a short final page containing
	// a foreign-currency row keeps the census incomplete rather than confirming
	// a market it did not fully verify.
	mismatched := noonesOfferForTest("hash-x")
	mismatched.FiatCurrencyCode = "USD"
	mismatchedRaw, _ := json.Marshal(mismatched)
	one := noonesOfferForTest("hash-1")
	oneRaw, _ := json.Marshal(one)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"status":"success","data":{"count":2,"limit":50,"offset":0,"totalCount":2,"offers":[%s,%s]}}`, oneRaw, mismatchedRaw)), nil
	})}
	result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", staticNoonesToken("test-token"), "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusIncomplete {
		t.Fatalf("result = %#v, want incomplete fetch", result)
	}
	if len(result.Offers) != 1 {
		t.Fatalf("offers = %d, want the single EUR row", len(result.Offers))
	}
}

func TestFetchNoonesCurrencyFromAPIErrorEnvelope(t *testing.T) {
	// App-level errors return HTTP 200 with status:"error" — the envelope, not
	// the status code, decides.
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"status":"error","timestamp":1455032576,"error":{"code":404,"message":"Not Found"}}`), nil
	})}
	if result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", staticNoonesToken("test-token"), "ask", "EUR", 60000); result != nil {
		t.Fatalf("result = %#v, want failed fetch", result)
	}
}

func TestFetchNoonesCurrencyFromAuthFailure(t *testing.T) {
	failingToken := func(context.Context, string) (string, error) { return "", fmt.Errorf("oauth down") }
	if result := fetchNoonesCurrencyFrom(context.Background(), clearnetClient(), "https://api.noones.test", failingToken, "ask", "EUR", 60000); result != nil {
		t.Fatalf("result = %#v, want failed fetch", result)
	}
}

func TestFetchNoonesCurrencyUsesVersionedEndpoint(t *testing.T) {
	// The live gateway 404s the unprefixed /offer/all; only the /noones/v1
	// route exists. Pin the production constant, not a test base URL.
	var gotURL string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResponse(http.StatusOK, noonesListJSON(1, noonesOfferJSON("hash-1"))), nil
	})}
	result := fetchNoonesCurrencyFrom(context.Background(), client, noonesAPIBaseURL, staticNoonesToken("test-token"), "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete fetch", result)
	}
	if want := "https://api.noones.com/noones/v1/offer/all"; gotURL != want {
		t.Fatalf("offer/all URL = %q, want %q", gotURL, want)
	}
}

func TestFetchNoonesCurrencyFromRemintsTokenAfter401(t *testing.T) {
	var rejectedSeen []string
	token := func(_ context.Context, rejected string) (string, error) {
		rejectedSeen = append(rejectedSeen, rejected)
		if rejected == "revoked" {
			return "fresh", nil
		}
		return "revoked", nil
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") == "Bearer revoked" {
			return jsonResponse(http.StatusUnauthorized, `{"error":"unauthorized_access"}`), nil
		}
		return jsonResponse(http.StatusOK, noonesListJSON(1, noonesOfferJSON("hash-1"))), nil
	})}
	result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", token, "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete || len(result.Offers) != 1 {
		t.Fatalf("result = %#v, want complete fetch with 1 offer after re-mint", result)
	}
	if len(rejectedSeen) != 2 || rejectedSeen[0] != "" || rejectedSeen[1] != "revoked" {
		t.Fatalf("token calls = %q, want [\"\" \"revoked\"]", rejectedSeen)
	}
}

func TestFetchNoonesCurrencyFromPersistent401Fails(t *testing.T) {
	// Bad credentials: one re-mint, then give up rather than loop.
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusUnauthorized, `{"error":"unauthorized_access"}`), nil
	})}
	if result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", staticNoonesToken("bad"), "ask", "EUR", 60000); result != nil {
		t.Fatalf("result = %#v, want failed fetch", result)
	}
	if calls != 2 {
		t.Fatalf("offer/all calls = %d, want 2 (original + one retry)", calls)
	}
}

func TestFetchNoonesCurrencyFromFollowsServerPageSize(t *testing.T) {
	// The server ignored count=50 and applied limit=20: a fixed page size
	// would read the first full 20-row page as short and confirm a truncated
	// book. Paging must follow data.limit and step the offset by rows served.
	pages := map[string]string{
		"0":  fmt.Sprintf(`{"status":"success","data":{"count":20,"limit":"20","offset":0,"totalCount":"45","offers":[%s]}}`, strings.Join(noonesFullPageIDs("hash", 1, 20), ",")),
		"20": fmt.Sprintf(`{"status":"success","data":{"count":20,"limit":"20","offset":20,"totalCount":"45","offers":[%s]}}`, strings.Join(noonesFullPageIDs("hash", 21, 40), ",")),
		"40": fmt.Sprintf(`{"status":"success","data":{"count":5,"limit":"20","offset":40,"totalCount":"45","offers":[%s]}}`, strings.Join(noonesFullPageIDs("hash", 41, 45), ",")),
	}
	var offsets []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		offsets = append(offsets, form.Get("offset"))
		page, ok := pages[form.Get("offset")]
		if !ok {
			t.Errorf("unexpected offset %q", form.Get("offset"))
			return jsonResponse(http.StatusBadRequest, `{}`), nil
		}
		return jsonResponse(http.StatusOK, page), nil
	})}
	result := fetchNoonesCurrencyFrom(context.Background(), client, "https://api.noones.test", staticNoonesToken("test-token"), "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete fetch", result)
	}
	if len(result.Offers) != 45 {
		t.Fatalf("offers = %d, want 45", len(result.Offers))
	}
	if strings.Join(offsets, ",") != "0,20,40" {
		t.Fatalf("offsets = %v, want [0 20 40]", offsets)
	}
}

func TestNoonesTokenCacheDropsOnlyTheRejectedToken(t *testing.T) {
	prevID, prevSecret := noonesClientID, noonesClientSecret
	noonesClientID, noonesClientSecret = "id", "secret"
	t.Cleanup(func() { noonesClientID, noonesClientSecret = prevID, prevSecret })

	var minted int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		if req.URL.Path != "/oauth2/token" || form.Get("grant_type") != "client_credentials" ||
			form.Get("client_id") != "id" || form.Get("client_secret") != "secret" {
			t.Errorf("unexpected token request %s %v", req.URL.Path, form)
		}
		minted++
		// A short-lived token: the expiry margin must not expire it on arrival.
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"access_token":"jwt-%d","expires_in":300}`, minted)), nil
	})}
	cache := &noonesTokenCache{}
	ctx := context.Background()
	get := func(rejected string) string {
		t.Helper()
		token, err := cache.get(ctx, client, "https://auth.noones.test", rejected)
		if err != nil {
			t.Fatalf("get(%q): %v", rejected, err)
		}
		return token
	}

	if got := get(""); got != "jwt-1" {
		t.Fatalf("first get = %q, want jwt-1", got)
	}
	if got := get(""); got != "jwt-1" || minted != 1 {
		t.Fatalf("cached get = %q after %d mints, want jwt-1 from cache", got, minted)
	}
	if got := get("jwt-1"); got != "jwt-2" {
		t.Fatalf("get after rejecting jwt-1 = %q, want jwt-2", got)
	}
	// A second market that saw the same 401 must not re-mint again.
	if got := get("jwt-1"); got != "jwt-2" || minted != 2 {
		t.Fatalf("get with stale rejection = %q after %d mints, want jwt-2 from cache", got, minted)
	}
}
