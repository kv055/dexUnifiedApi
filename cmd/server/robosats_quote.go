package main

import (
	"bytes"
	"commons/apierror"
	"context"
	"encoding/json"
	"errors"
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

// relayControllerError passes the Taker Controller's own explanation through,
// so the operator sees why a quote was refused instead of a bare status code.
func relayControllerError(response *http.Response) error {
	upstream := apierror.FromResponse(response, "the Taker Controller")
	status := response.StatusCode
	if status < 400 {
		status = http.StatusBadGateway
	}
	if upstream.Reason == "" {
		return apierror.Wrapf(upstream, "robosats_quote_failed",
			"The Taker Controller refused the RoboSats quote (HTTP %d) without saying why. Check its log for \"RoboSats quote rejected\".", response.StatusCode).
			WithStatus(status)
	}
	code := upstream.Code
	if code == "" {
		code = "robosats_quote_failed"
	}
	var cause error = upstream
	if upstream.Detail != "" {
		cause = errors.New(upstream.Detail)
	}
	return &apierror.Error{Code: code, Message: upstream.Reason, Status: status, Err: cause}
}

// MM owns robot credentials and Tor transport; Unified owns route calculation.
// Only this fixed internal endpoint may supply executable RoboSats terms.
func loadRoboNativeTerms(ctx context.Context, req executionQuoteRequest, offer *LiveOffer, method string) (*roboNativeTerms, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MM_CONTROLLER_API_URL")), "/")
	if base == "" {
		base = "http://localhost:5001"
	}
	if strings.TrimSpace(internalAPISecret) == "" {
		return nil, apierror.New("robosats_quote_unconfigured",
			"The Unified API can't ask the Taker Controller for RoboSats quotes because INTERNAL_API_SECRET is not set. Set the same secret for both services and restart them.").
			WithStatus(http.StatusServiceUnavailable)
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
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			return nil, apierror.Wrap(err, "robosats_quote_timeout",
				"Pricing this RoboSats offer took longer than 25 seconds (the coordinator is reached over Tor and may be slow). Try again in a moment.").
				WithStatus(http.StatusGatewayTimeout)
		}
		return nil, apierror.Wrap(err, "taker_controller_unreachable",
			"The Unified API couldn't reach the Taker Controller to price this RoboSats offer. It may be restarting; try again in a moment.").
			WithStatus(http.StatusBadGateway)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, relayControllerError(response)
	}
	var terms roboNativeTerms
	if err := json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&terms); err != nil {
		return nil, apierror.Wrap(err, "robosats_quote_unreadable",
			"The Taker Controller sent a RoboSats quote the Unified API can't read. Make sure both services run the same release.").
			WithStatus(http.StatusBadGateway)
	}
	if terms.OrderID != *offer.OfferID || terms.CoordinatorID != offer.VenueCoordinatorID || terms.Currency != offer.FiatCurrency || terms.PaymentMethod != method || terms.PrincipalSat <= 0 || terms.MaxBondSat <= 0 || terms.MaxEscrowSat < terms.PrincipalSat || !terms.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return nil, apierror.Wrap(fmt.Errorf("native RoboSats quote has inconsistent terms: %+v", terms), "robosats_terms_changed",
			"The RoboSats quote doesn't match this offer any more (it changed or is about to expire while it was being priced). Try again; if it keeps happening, refresh the offer list.").
			WithStatus(http.StatusConflict)
	}
	return &terms, nil
}
