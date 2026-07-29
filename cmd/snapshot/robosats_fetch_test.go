package main

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFetchRobosatsFallsBackAcrossCoordinatorFailures(t *testing.T) {
	var failedHits, unknownHits, successHits atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "failed.test":
			failedHits.Add(1)
			return jsonResponse(http.StatusBadGateway, `{"detail":"upstream unavailable"}`), nil
		case "unknown.test":
			unknownHits.Add(1)
			return jsonResponse(http.StatusOK, `{"detail":"temporary coordinator error"}`), nil
		default:
			successHits.Add(1)
			return jsonResponse(http.StatusOK, `[{
			"id": 42,
			"price": 60000,
			"premium": 0,
			"amount": 600,
			"min_amount": 0,
			"max_amount": 0,
			"payment_method": "SEPA",
			"bond_size": 3
		}]`), nil
		}
	})}

	result := fetchRobosatsCurrencyFrom(
		"sell",
		"EUR",
		60000,
		client,
		[]string{"http://failed.test", "http://unknown.test", "http://success.test"},
	)
	if result == nil {
		t.Fatal("result = nil, want fallback coordinator result")
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "42" {
		t.Fatalf("offers = %#v, want order 42", result.Offers)
	}
	if failedHits.Load() != 1 || unknownHits.Load() != 1 || successHits.Load() != 1 {
		t.Fatalf("hits failed=%d unknown=%d success=%d, want 1 each", failedHits.Load(), unknownHits.Load(), successHits.Load())
	}
}

func TestFetchRobosatsNotFoundObjectIsEmptyBook(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"not_found":"No orders found"}`), nil
	})}
	result := fetchRobosatsCurrencyFrom("buy", "GBP", 50000, client, []string{"http://empty.test"})
	if result == nil {
		t.Fatal("result = nil, want successful empty book")
	}
	if len(result.Offers) != 0 {
		t.Fatalf("offers = %d, want 0", len(result.Offers))
	}
}
