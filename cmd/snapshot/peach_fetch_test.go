package main

import (
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestFetchPeachRejectsNonOKResponse(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"error":"temporarily unavailable"}`), nil
			})}

			if result := fetchPeachBitcoinCurrencyFrom(client, "https://peach.test", "ask", "EUR", 60000); result != nil {
				t.Fatalf("result = %#v, want nil for HTTP %d", result, status)
			}
		})
	}
}

func TestFetchPeachRejectsMidPaginationFailure(t *testing.T) {
	page0 := `{"offers":[{"id":"p-1","amount":30000,"meansOfPayment":{"EUR":["revolut"]},"prices":{"EUR":17}}],"remaining":1}`

	t.Run("transport error", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("page") == "0" {
				return jsonResponse(http.StatusOK, page0), nil
			}
			return nil, errors.New("upstream reset")
		})}
		if result := fetchPeachBitcoinCurrencyFrom(client, "https://peach.test", "ask", "EUR", 60000); result == nil || result.status() != fetchStatusIncomplete {
			t.Fatalf("result = %#v, want incomplete after page-1 transport error", result)
		} else if len(result.Offers) != 1 || result.Offers[0].OfferID != "p-1" {
			t.Fatalf("offers = %#v, want page-0 offer kept", result.Offers)
		}
	})

	t.Run("parse error", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("page") == "0" {
				return jsonResponse(http.StatusOK, page0), nil
			}
			return jsonResponse(http.StatusOK, `{not-json`), nil
		})}
		if result := fetchPeachBitcoinCurrencyFrom(client, "https://peach.test", "ask", "EUR", 60000); result == nil || result.status() != fetchStatusIncomplete {
			t.Fatalf("result = %#v, want incomplete after page-1 parse error", result)
		} else if len(result.Offers) != 1 || result.Offers[0].OfferID != "p-1" {
			t.Fatalf("offers = %#v, want page-0 offer kept", result.Offers)
		}
	})
}

func TestFetchPeachRejectsResponseReadFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(failingReader{}),
		}, nil
	})}

	if result := fetchPeachBitcoinCurrencyFrom(client, "https://peach.test", "ask", "EUR", 60000); result != nil {
		t.Fatalf("result = %#v, want nil after body read failure", result)
	}
}

func TestFetchPeachAcceptsConfirmedEmptyBook(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"offers":[],"remaining":0}`), nil
	})}

	result := fetchPeachBitcoinCurrencyFrom(client, "https://peach.test", "bid", "GBP", 50000)
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

func TestFetchPeachMapsOfferFromSecondPage(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Query().Get("page") {
		case "0":
			return jsonResponse(http.StatusOK, `{"offers":[],"remaining":1}`), nil
		case "1":
			return jsonResponse(http.StatusOK, `{"offers":[{"id":"p-2","amount":30000,"meansOfPayment":{"EUR":["revolut"]},"prices":{"EUR":17}}],"remaining":0}`), nil
		default:
			t.Fatalf("unexpected page: %s", req.URL.RawQuery)
			return nil, errors.New("unexpected page")
		}
	})}

	result := fetchPeachBitcoinCurrencyFrom(client, "https://peach.test/", "ask", "EUR", 60000)
	if result == nil || len(result.Offers) != 1 {
		t.Fatalf("result = %#v, want one offer from page 1", result)
	}
	if result.Offers[0].OfferID != "p-2" {
		t.Fatalf("offer id = %q, want p-2", result.Offers[0].OfferID)
	}
}

func TestFetchPeachCurrencyMissIsEmptyBook(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"offers":[{"id":"p-eur","amount":30000,"meansOfPayment":{"EUR":["revolut"]},"prices":{"EUR":17}}],"remaining":0}`), nil
	})}

	result := fetchPeachBitcoinCurrencyFrom(client, "https://peach.test", "ask", "CHF", 60000)
	if result == nil {
		t.Fatal("result = nil, want successful empty book for currency miss")
	}
	if len(result.Offers) != 0 {
		t.Fatalf("offers = %d, want 0 after CHF filter on EUR-only book", len(result.Offers))
	}
	if result.status() != fetchStatusComplete {
		t.Fatalf("status = %q, want complete empty currency miss", result.status())
	}
}
