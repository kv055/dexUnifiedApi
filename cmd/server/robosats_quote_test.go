package main

import (
	"commons/apierror"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeQuoteUsesAuthenticatedMMBoundary(t *testing.T) {
	old := internalAPISecret
	internalAPISecret = "test-secret"
	defer func() { internalAPISecret = old }()
	bad := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/robosats/quote" || r.Header.Get("X-Internal-Secret") != "test-secret" {
			t.Errorf("wrong quote boundary")
		}
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["amount"] != "100" || input["fiat_currency"] != "EUR" || input["venue_coordinator_id"] != "lake" {
			t.Errorf("wrong quote input: %+v", input)
		}
		terms := roboNativeTerms{CoordinatorID: "lake", OrderID: "42", FiatAmount: "100.00", Currency: "EUR", PaymentMethod: "SEPA", PrincipalSat: 100000, MaxBondSat: 3000, MaxEscrowSat: 100100, ExpiresAt: time.Now().Add(time.Minute)}
		if bad {
			terms.OrderID = "43"
		}
		_ = json.NewEncoder(w).Encode(terms)
	}))
	defer upstream.Close()
	t.Setenv("MM_CONTROLLER_API_URL", upstream.URL)
	id := "42"
	offer := &LiveOffer{OfferID: &id, VenueCoordinatorID: "lake", FiatCurrency: "EUR"}
	request := executionQuoteRequest{Amount: "100", Unit: "fiat"}
	terms, err := loadRoboNativeTerms(context.Background(), request, offer, "SEPA")
	if err != nil || terms.MaxEscrowSat != 100100 {
		t.Fatalf("quote: %+v %v", terms, err)
	}
	bad = true
	if _, err := loadRoboNativeTerms(context.Background(), request, offer, "SEPA"); err == nil {
		t.Fatal("changed native order accepted")
	}
}

func TestNativeQuoteRelaysTheControllersReason(t *testing.T) {
	old := internalAPISecret
	internalAPISecret = "test-secret"
	defer func() { internalAPISecret = old }()
	id := "42"
	offer := &LiveOffer{OfferID: &id, VenueCoordinatorID: "lake", FiatCurrency: "EUR"}
	request := executionQuoteRequest{Amount: "100", Unit: "fiat"}

	answer := func(status int, body string) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(upstream.Close)
		t.Setenv("MM_CONTROLLER_API_URL", upstream.URL)
	}

	answer(http.StatusServiceUnavailable, `{"error":"RoboSats trading is turned off on this server.","code":"robosats_disabled"}`)
	_, err := loadRoboNativeTerms(context.Background(), request, offer, "SEPA")
	ae, ok := apierror.As(err)
	if !ok || ae.Code != "robosats_disabled" || ae.Message != "RoboSats trading is turned off on this server." || ae.Status != http.StatusServiceUnavailable {
		t.Fatalf("relayed error = %+v (%v)", ae, err)
	}

	answer(http.StatusConflict, "")
	_, err = loadRoboNativeTerms(context.Background(), request, offer, "SEPA")
	if ae, _ := apierror.As(err); ae == nil || !strings.Contains(ae.Message, "without saying why") {
		t.Fatalf("empty refusal = %v", err)
	}

	t.Setenv("MM_CONTROLLER_API_URL", "http://127.0.0.1:1")
	_, err = loadRoboNativeTerms(context.Background(), request, offer, "SEPA")
	if apierror.CodeOf(err) != "taker_controller_unreachable" {
		t.Fatalf("unreachable controller = %v", err)
	}
}

func TestExecutionQuoteErrorsAreJSONForTheOperator(t *testing.T) {
	rec := httptest.NewRecorder()
	handleExecutionQuotes(rec, httptest.NewRequest(http.MethodPost, "/api/execution-quotes", strings.NewReader(`{"amount":"0","unit":"fiat"}`)))
	var body apierror.Body
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.Code != http.StatusBadRequest || body.Code != "invalid_amount" || !strings.HasPrefix(body.Error, "Enter an amount greater than zero") {
		t.Fatalf("status %d body %+v", rec.Code, body)
	}
}
