package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
