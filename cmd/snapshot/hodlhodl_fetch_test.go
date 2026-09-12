package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestFetchHodlHodlRejectsNonOKResponse(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"status":"error","error_code":"temporarily unavailable"}`), nil
			})}

			if result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000); result != nil {
				t.Fatalf("result = %#v, want nil for HTTP %d", result, status)
			}
		})
	}
}

func TestFetchHodlHodlAcceptsConfirmedEmptyBook(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"status":"success","offers":[]}`), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "bid", "GBP", 50000)
	if result == nil {
		t.Fatal("result = nil, want successful empty book")
	}
	if len(result.Offers) != 0 {
		t.Fatalf("offers = %d, want 0", len(result.Offers))
	}
	if result.status() != fetchStatusComplete {
		t.Fatalf("status = %q, want complete", result.status())
	}
}

func TestFetchHodlHodlMapsSuccessfulOffer(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		if q.Get("filters[side]") != "sell" || q.Get("filters[currency_code]") != "EUR" {
			t.Fatalf("unexpected query: %s", req.URL.RawQuery)
		}
		if q.Get("filters[include_global]") != "true" || q.Get("filters[only_working_now]") != "true" || q.Get("sort[by]") != "price" {
			t.Fatalf("unexpected filters: %s", req.URL.RawQuery)
		}
		if q.Get("pagination[limit]") != "100" || q.Get("pagination[offset]") != "0" {
			t.Fatalf("unexpected pagination: %s", req.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, []map[string]any{hodlHodlSampleOffer("hh-1")})), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test/", "ask", "EUR", 60000)
	if result == nil || len(result.Offers) != 1 {
		t.Fatalf("result = %#v, want one offer", result)
	}
	if result.status() != fetchStatusComplete {
		t.Fatalf("status = %q, want complete", result.status())
	}
	got := result.Offers[0]
	if got.OfferID != "hh-1" || got.Price != 60000 || len(got.PaymentMethods) != 1 || got.PaymentMethods[0] != "SEPA" {
		t.Fatalf("offer = %#v, want mapped hh-1 offer", got)
	}
	if got.BTCMin == nil || got.BTCMax == nil || *got.BTCMin != 0.005 || *got.BTCMax != 0.01 {
		t.Fatalf("btc range = %v-%v, want 0.005-0.01 from sats", got.BTCMin, got.BTCMax)
	}
	if got.FiatMin == nil || got.FiatMax == nil || *got.FiatMin != 300 || *got.FiatMax != 600 {
		t.Fatalf("fiat range = %v-%v, want 300-600", got.FiatMin, got.FiatMax)
	}
	var signals map[string]any
	if err := json.Unmarshal(got.SellerRatingSignals, &signals); err != nil {
		t.Fatalf("unmarshal seller_rating_signals: %v", err)
	}
	if signals["source"] != "hodlhodl" || signals["trades_count"] != float64(10) || signals["online_status"] != "online" {
		t.Fatalf("seller_rating_signals = %s, want hodlhodl extras", got.SellerRatingSignals)
	}
}

func TestFetchHodlHodlRejectsResponseReadFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(failingReader{}),
		}, nil
	})}

	if result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000); result != nil {
		t.Fatalf("result = %#v, want nil after body read failure", result)
	}
}

func TestFetchHodlHodlPaginationCounts(t *testing.T) {
	tests := []struct {
		name      string
		pages     []int
		wantCount int
	}{
		{name: "empty", pages: []int{0}, wantCount: 0},
		{name: "fifty", pages: []int{50}, wantCount: 50},
		{name: "exactly 100 then empty", pages: []int{100, 0}, wantCount: 100},
		{name: "over 100", pages: []int{100, 25}, wantCount: 125},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := hodlHodlPagedClient(t, tc.pages)
			result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
			if result == nil {
				t.Fatal("result = nil")
			}
			if result.status() != fetchStatusComplete {
				t.Fatalf("status = %q, want complete", result.status())
			}
			if len(result.Offers) != tc.wantCount {
				t.Fatalf("offers = %d, want %d", len(result.Offers), tc.wantCount)
			}
		})
	}
}

func TestFetchHodlHodlDeduplicatesByProviderID(t *testing.T) {
	first := hodlHodlSampleOffer("dup-1")
	first["price"] = "61000"
	page := append(hodlHodlOfferMaps(t, 99, 0), hodlHodlSampleOffer("dup-1"))
	page[0] = first
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Query().Get("pagination[offset]") {
		case "0":
			return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, page)), nil
		case "100":
			return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, []map[string]any{hodlHodlSampleOffer("dup-1")})), nil
		default:
			t.Fatalf("unexpected offset: %s", req.URL.RawQuery)
			return nil, errors.New("unexpected offset")
		}
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete", result)
	}
	var dups int
	for _, o := range result.Offers {
		if o.OfferID == "dup-1" {
			dups++
			if o.Price != 61000 {
				t.Fatalf("kept offer price = %v, want first occurrence 61000", o.Price)
			}
		}
	}
	if dups != 1 {
		t.Fatalf("dup-1 count = %d, want 1", dups)
	}
}

func TestFetchHodlHodlStopsOnRepeatedFullPage(t *testing.T) {
	same := hodlHodlOfferMaps(t, 100, 0)
	var hits int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		hits++
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, same)), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusIncomplete {
		t.Fatalf("result = %#v, want incomplete after repeated page", result)
	}
	if hits != 2 {
		t.Fatalf("pages fetched = %d, want 2", hits)
	}
	if len(result.Offers) != 100 {
		t.Fatalf("offers = %d, want 100 unique from first page", len(result.Offers))
	}
}

func TestFetchHodlHodlStopsOnCancellationAfterFirstPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("pagination[offset]") != "0" {
			t.Fatalf("second page requested after cancel: %s", req.URL.RawQuery)
		}
		defer cancel()
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, hodlHodlOfferMaps(t, 100, 0))), nil
	})}

	result := fetchHodlHodlCurrencyFrom(ctx, client, "https://hodlhodl.test", "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusIncomplete {
		t.Fatalf("result = %#v, want incomplete after cancel", result)
	}
	if len(result.Offers) != 100 {
		t.Fatalf("offers = %d, want first page kept", len(result.Offers))
	}
}

func TestFetchHodlHodlLaterPageFailureKeepsCollectedOffers(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("pagination[offset]") == "0" {
				return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, hodlHodlOfferMaps(t, 100, 0))), nil
			}
			return jsonResponse(http.StatusInternalServerError, `{"status":"error"}`), nil
		})}
		result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
		if result == nil || result.status() != fetchStatusIncomplete {
			t.Fatalf("result = %#v, want incomplete", result)
		}
		if len(result.Offers) != 100 {
			t.Fatalf("offers = %d, want first page kept", len(result.Offers))
		}
	})
	t.Run("json", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("pagination[offset]") == "0" {
				return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, hodlHodlOfferMaps(t, 100, 0))), nil
			}
			return jsonResponse(http.StatusOK, `{not-json`), nil
		})}
		result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
		if result == nil || result.status() != fetchStatusIncomplete || len(result.Offers) != 100 {
			t.Fatalf("result = %#v, want incomplete with first page kept", result)
		}
	})
}

func TestFetchHodlHodlProtocolErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "error envelope", body: `{"status":"error","error_code":"not_available","offers":[]}`},
		{name: "missing offers", body: `{"status":"success"}`},
		{name: "null offers", body: `{"status":"success","offers":null}`},
		{name: "malformed offers", body: `{"status":"success","offers":{}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, tc.body), nil
			})}
			if result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000); result != nil {
				t.Fatalf("result = %#v, want failed", result)
			}
		})
	}
}

func TestFetchHodlHodlCurrencyMismatchIsIncomplete(t *testing.T) {
	usd := hodlHodlSampleOffer("usd-1")
	usd["currency_code"] = "USD"
	eur := hodlHodlSampleOffer("eur-1")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, []map[string]any{usd, eur})), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusIncomplete {
		t.Fatalf("result = %#v, want incomplete after currency mismatch", result)
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "eur-1" || result.Offers[0].FiatCurrency != "EUR" {
		t.Fatalf("offers = %#v, want only the EUR offer", result.Offers)
	}
}

func TestFetchHodlHodlUnsupportedLayerStillComplete(t *testing.T) {
	ark := hodlHodlSampleOffer("ark-1")
	ark["asset_layer"] = "ARK"
	btc := hodlHodlSampleOffer("btc-1")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, []map[string]any{ark, btc})), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete after skipping ARK", result)
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "btc-1" {
		t.Fatalf("offers = %#v, want only on-chain BTC", result.Offers)
	}
}

func TestFetchHodlHodlMalformedOfferStillComplete(t *testing.T) {
	bad := hodlHodlSampleOffer("bad-1")
	bad["price"] = "0"
	good := hodlHodlSampleOffer("good-1")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, []map[string]any{bad, good})), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete after skipping malformed", result)
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "good-1" {
		t.Fatalf("offers = %#v, want only valid offer", result.Offers)
	}
}

func TestFetchHodlHodlBuySideReadsPaymentMethods(t *testing.T) {
	offer := hodlHodlSampleOffer("buy-1")
	offer["side"] = "buy"
	offer["payment_methods"] = []map[string]any{{"name": " Revolut "}, {"name": "Revolut"}, {"name": "SEPA"}}
	offer["payment_method_instructions"] = []map[string]any{{"payment_method_name": "ignored"}}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, []map[string]any{offer})), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "bid", "EUR", 60000)
	if result == nil || len(result.Offers) != 1 {
		t.Fatalf("result = %#v, want buy offer", result)
	}
	got := result.Offers[0].PaymentMethods
	if len(got) != 2 || got[0] != "Revolut" || got[1] != "SEPA" {
		t.Fatalf("payment methods = %v, want trimmed deduped buy-side names", got)
	}
}

func TestFetchHodlHodlAdvancesOffsetByRawPageSize(t *testing.T) {
	var offsets []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		offset := req.URL.Query().Get("pagination[offset]")
		offsets = append(offsets, offset)
		switch offset {
		case "0":
			page := hodlHodlOfferMaps(t, 100, 0)
			for i := range page {
				page[i]["asset_layer"] = "ARK"
			}
			return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, page)), nil
		case "100":
			return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, []map[string]any{hodlHodlSampleOffer("onchain-1")})), nil
		default:
			t.Fatalf("unexpected offset %q", offset)
			return nil, errors.New("unexpected offset")
		}
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000)
	if len(offsets) != 2 || offsets[0] != "0" || offsets[1] != "100" {
		t.Fatalf("offsets = %v, want [0 100] by raw page size", offsets)
	}
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete", result)
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "onchain-1" {
		t.Fatalf("offers = %#v, want on-chain offer from second page", result.Offers)
	}
}

func hodlHodlSampleOffer(id string) map[string]any {
	return map[string]any{
		"id":              id,
		"asset_code":      "BTC",
		"asset_layer":     "BTC",
		"side":            "sell",
		"currency_code":   "EUR",
		"price":           "60000",
		"price_source":    "fixed_value",
		"min_amount":      "300",
		"max_amount":      "600",
		"min_amount_sats": "500000",
		"max_amount_sats": "1000000",
		"payment_method_instructions": []map[string]any{
			{"payment_method_name": "SEPA"},
		},
		"trader": map[string]any{
			"trades_count":                 10,
			"rating":                       0.98,
			"strong_hodler":                true,
			"days_since_last_trade":        4,
			"average_payment_time_minutes": 12,
			"average_release_time_minutes": 8,
			"blocked_by":                   0,
			"verified":                     true,
			"verified_by":                  "hodlhodl",
			"online_status":                "online",
		},
	}
}

func hodlHodlOfferMaps(t *testing.T, n, start int) []map[string]any {
	t.Helper()
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = hodlHodlSampleOffer(fmt.Sprintf("hh-%d", start+i))
	}
	return out
}

func hodlHodlEnvelopeJSON(t *testing.T, offers []map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"status": "success", "offers": offers})
	if err != nil {
		t.Fatalf("marshal hodlhodl envelope: %v", err)
	}
	return string(raw)
}

func hodlHodlPagedClient(t *testing.T, pageSizes []int) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		offset, _ := strconv.Atoi(req.URL.Query().Get("pagination[offset]"))
		page := offset / hodlHodlPageSize
		if page < 0 || page >= len(pageSizes) {
			t.Fatalf("unexpected offset %d (pages=%d) query=%s", offset, len(pageSizes), req.URL.RawQuery)
		}
		start := 0
		for i := 0; i < page; i++ {
			start += pageSizes[i]
		}
		return jsonResponse(http.StatusOK, hodlHodlEnvelopeJSON(t, hodlHodlOfferMaps(t, pageSizes[page], start))), nil
	})}
}

func TestParseHodlHodlOffersResponseRejectsInvalidEnvelopes(t *testing.T) {
	offers, err := parseHodlHodlOffersResponse([]byte(`{"status":"success","offers":[]}`))
	if err != nil || offers == nil || len(offers) != 0 {
		t.Fatalf("empty array got %#v err=%v, want non-nil empty slice", offers, err)
	}
	if _, err := parseHodlHodlOffersResponse([]byte(`{"status":"error","offers":[]}`)); err == nil || !strings.Contains(err.Error(), "api status") {
		t.Fatalf("error envelope err = %v, want api status", err)
	}
	if _, err := parseHodlHodlOffersResponse([]byte(`{"status":"success"}`)); err == nil || !strings.Contains(err.Error(), "missing offers") {
		t.Fatalf("missing array err = %v", err)
	}
}
