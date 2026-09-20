package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type roboNativeTerms struct {
	CoordinatorID string    `json:"venue_coordinator_id"`
	OrderID       string    `json:"offer_id"`
	FiatAmount    string    `json:"fiat_amount"`
	Currency      string    `json:"fiat_currency"`
	PaymentMethod string    `json:"payment_method"`
	Price         string    `json:"price"`
	BondPercent   string    `json:"bond_percent"`
	TakerFee      string    `json:"taker_fee"`
	PrincipalSat  int64     `json:"principal_sat"`
	MaxBondSat    int64     `json:"max_bond_sat"`
	MaxEscrowSat  int64     `json:"max_escrow_sat"`
	NodePubKey    string    `json:"node_pubkey"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// MM owns robot credentials and Tor transport; Unified owns route calculation.
// Only this fixed internal endpoint may supply executable RoboSats terms.
func loadRoboNativeTerms(ctx context.Context, req executionQuoteRequest, offer *LiveOffer, method string) (*roboNativeTerms, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MM_CONTROLLER_API_URL")), "/")
	if base == "" {
		base = "http://localhost:5001"
	}
	if strings.TrimSpace(internalAPISecret) == "" {
		return nil, fmt.Errorf("RoboSats quote transport is not configured")
	}
	body, err := json.Marshal(map[string]string{"venue_coordinator_id": offer.VenueCoordinatorID, "offer_id": *offer.OfferID, "amount": req.Amount, "unit": req.Unit, "fiat_currency": offer.FiatCurrency, "payment_method": method})
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/robosats/quote", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Internal-Secret", internalAPISecret)
	response, err := (&http.Client{Timeout: 25 * time.Second}).Do(r)
	if err != nil {
		return nil, fmt.Errorf("native RoboSats quote unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("native RoboSats quote rejected (HTTP %d)", response.StatusCode)
	}
	var terms roboNativeTerms
	if err := json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&terms); err != nil {
		return nil, err
	}
	if terms.OrderID != *offer.OfferID || terms.CoordinatorID != offer.VenueCoordinatorID || terms.Currency != offer.FiatCurrency || terms.PaymentMethod != method || terms.PrincipalSat <= 0 || terms.MaxBondSat <= 0 || terms.MaxEscrowSat < terms.PrincipalSat || !terms.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return nil, fmt.Errorf("native RoboSats quote has inconsistent terms")
	}
	return &terms, nil
}
