package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestFetchHodlHodlRejectsNonOKResponse(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"error":"temporarily unavailable"}`), nil
			})}

			if result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "ask", "EUR", 60000); result != nil {
				t.Fatalf("result = %#v, want nil for HTTP %d", result, status)
			}
		})
	}
}

func TestFetchHodlHodlAcceptsConfirmedEmptyBook(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"offers":[]}`), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test", "bid", "GBP", 50000)
	if result == nil {
		t.Fatal("result = nil, want successful empty book")
	}
	if len(result.Offers) != 0 {
		t.Fatalf("offers = %d, want 0", len(result.Offers))
	}
}

func TestFetchHodlHodlMapsSuccessfulOffer(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("filters[side]") != "sell" || req.URL.Query().Get("filters[currency_code]") != "EUR" {
			t.Fatalf("unexpected query: %s", req.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, `{"offers":[{"id":"hh-1","price":"60000","price_source":"fixed_value","min_amount":"300","max_amount":"600","payment_method_instructions":[{"payment_method_name":"SEPA"}],"trader":{"trades_count":10,"rating":0.98}}]}`), nil
	})}

	result := fetchHodlHodlCurrencyFrom(context.Background(), client, "https://hodlhodl.test/", "ask", "EUR", 60000)
	if result == nil || len(result.Offers) != 1 {
		t.Fatalf("result = %#v, want one offer", result)
	}
	got := result.Offers[0]
	if got.OfferID != "hh-1" || got.Price != 60000 || len(got.PaymentMethods) != 1 || got.PaymentMethods[0] != "SEPA" {
		t.Fatalf("offer = %#v, want mapped hh-1 offer", got)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

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
