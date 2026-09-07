package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"commons/nostrreq"
	"dexunifiedapi/internal/sellerrating"
)

func TestMapNIP69EventFixedAndFloating(t *testing.T) {
	fixed, ok := mapNIP69Event(nostrreq.Event{
		Kind: nip69OrderKind,
		Tags: [][]string{
			{"d", "ord-fixed"},
			{"k", "sell"},
			{"f", "EUR"},
			{"s", "pending"},
			{"amt", "100000"},
			{"fa", "50"},
			{"pm", "SEPA", "Revolut"},
			{"premium", "2"},
			{"y", "mostro"},
			{"z", "order"},
			{"network", "mainnet"},
			{"layer", "lightning"},
		},
	}, "mostro", "sell", "EUR", 50000)
	if !ok {
		t.Fatal("fixed offer rejected")
	}
	if fixed.OfferID != "ord-fixed" {
		t.Fatalf("offer_id = %q", fixed.OfferID)
	}
	if fixed.Price != 50/(100000/1e8) {
		t.Fatalf("fixed price = %v, want 50000", fixed.Price)
	}
	if fixed.PriceType == nil || *fixed.PriceType != "fixed" {
		t.Fatalf("price type = %v, want fixed", fixed.PriceType)
	}
	if len(fixed.PaymentMethods) != 2 {
		t.Fatalf("methods = %#v", fixed.PaymentMethods)
	}

	floating, ok := mapNIP69Event(nostrreq.Event{
		Kind: nip69OrderKind,
		Tags: [][]string{
			{"d", "ord-float"},
			{"k", "buy"},
			{"f", "USD"},
			{"s", "pending"},
			{"amt", "0"},
			{"fa", "100", "200"},
			{"pm", "bank transfer"},
			{"premium", "5"},
			{"y", "mostro"},
			{"z", "order"},
		},
	}, "mostro", "buy", "USD", 60000)
	if !ok {
		t.Fatal("floating offer rejected")
	}
	if floating.Price != 60000*1.05 {
		t.Fatalf("floating price = %v, want 63000", floating.Price)
	}
	if floating.PriceType == nil || *floating.PriceType != "floating" {
		t.Fatalf("price type = %v, want floating", floating.PriceType)
	}
	if floating.FiatMin == nil || *floating.FiatMin != 100 || floating.FiatMax == nil || *floating.FiatMax != 200 {
		t.Fatalf("fiat range = %v-%v", floating.FiatMin, floating.FiatMax)
	}
}

func TestMapNIP69EventFiltersWrongMarket(t *testing.T) {
	event := nostrreq.Event{
		Kind: nip69OrderKind,
		Tags: [][]string{
			{"d", "x"},
			{"k", "sell"},
			{"f", "EUR"},
			{"s", "pending"},
			{"amt", "1000"},
			{"fa", "10"},
			{"y", "mostro"},
			{"z", "order"},
			{"network", "testnet"},
		},
	}
	if _, ok := mapNIP69Event(event, "mostro", "sell", "EUR", 50000); ok {
		t.Fatal("testnet order should be skipped")
	}
	event.Tags[8] = []string{"network", "mainnet"}
	if _, ok := mapNIP69Event(event, "robosats", "sell", "EUR", 50000); ok {
		t.Fatal("wrong platform should be skipped")
	}
	if _, ok := mapNIP69Event(event, "mostro", "buy", "EUR", 50000); ok {
		t.Fatal("wrong side should be skipped")
	}
	if _, ok := mapNIP69Event(event, "mostro", "sell", "USD", 50000); ok {
		t.Fatal("wrong fiat should be skipped")
	}
}

func TestMapNIP69EventBondAndRating(t *testing.T) {
	ratingJSON := `{"total_reviews":21,"total_rating":4.5,"max_rate":5}`
	got, ok := mapNIP69Event(nostrreq.Event{
		Kind: nip69OrderKind,
		Tags: [][]string{
			{"d", "bonded"},
			{"k", "sell"},
			{"f", "EUR"},
			{"s", "pending"},
			{"amt", "100000"},
			{"fa", "50"},
			{"pm", "SEPA"},
			{"bond", "0.01"},
			{"rating", ratingJSON},
			{"y", "mostro"},
			{"z", "order"},
			{"network", "mainnet"},
			{"layer", "lightning"},
		},
	}, "mostro", "sell", "EUR", 50000)
	if !ok {
		t.Fatal("bonded offer rejected")
	}
	if got.SecurityDepositPct == nil || *got.SecurityDepositPct != 0.01 {
		t.Fatalf("deposit pct = %v, want 0.01", got.SecurityDepositPct)
	}
	if got.SellerRatingConfidence == nil || *got.SellerRatingConfidence != string(sellerrating.ConfidenceFeedbackVerified) {
		t.Fatalf("rating confidence = %v", got.SellerRatingConfidence)
	}
	if got.SellerRatingScore == nil {
		t.Fatal("expected a feedback score")
	}
}

func TestFetchNIP69CurrencyUsesSharedBookAndStatus(t *testing.T) {
	resetNIP69Cache()
	t.Cleanup(resetNIP69Cache)

	events := []nostrreq.Event{
		{
			Kind: nip69OrderKind,
			Tags: [][]string{
				{"d", "eur-sell"},
				{"k", "sell"},
				{"f", "EUR"},
				{"s", "pending"},
				{"amt", "100000"},
				{"fa", "50"},
				{"pm", "SEPA"},
				{"y", "mostro"},
				{"z", "order"},
			},
		},
		{
			Kind: nip69OrderKind,
			Tags: [][]string{
				{"d", "usd-buy"},
				{"k", "buy"},
				{"f", "USD"},
				{"s", "pending"},
				{"amt", "0"},
				{"fa", "80"},
				{"pm", "Zelle"},
				{"premium", "1"},
				{"y", "mostro"},
				{"z", "order"},
			},
		},
	}
	queries := 0
	query := func(context.Context, []string, map[string]any) (nostrreq.QueryResult, error) {
		queries++
		return nostrreq.QueryResult{Events: events, Complete: true, Connected: 1, EOSE: 1}, nil
	}

	ask := fetchNIP69CurrencyFrom(context.Background(), "mostro", []string{"wss://example"}, "ask", "EUR", 50000, query)
	bid := fetchNIP69CurrencyFrom(context.Background(), "mostro", []string{"wss://example"}, "bid", "USD", 60000, query)
	if queries != 1 {
		t.Fatalf("NIP-69 queries = %d, want 1 shared census", queries)
	}
	if ask == nil || ask.status() != fetchStatusComplete || len(ask.Offers) != 1 || ask.Offers[0].OfferID != "eur-sell" {
		t.Fatalf("ask result = %#v", ask)
	}
	if bid == nil || len(bid.Offers) != 1 || bid.Offers[0].OfferID != "usd-buy" {
		t.Fatalf("bid result = %#v", bid)
	}
}

func TestFetchNIP69IncompleteEmptyIsFailed(t *testing.T) {
	resetNIP69Cache()
	t.Cleanup(resetNIP69Cache)
	query := func(context.Context, []string, map[string]any) (nostrreq.QueryResult, error) {
		return nostrreq.QueryResult{Complete: false}, nil
	}
	if got := fetchNIP69CurrencyFrom(context.Background(), "mostro", nil, "ask", "EUR", 50000, query); got != nil {
		t.Fatalf("incomplete empty book = %#v, want nil", got)
	}
}

func TestMapNIP69RatingEnvelope(t *testing.T) {
	inner, _ := json.Marshal(map[string]any{"total_reviews": 3, "total_rating": 4.0, "max_rate": 5})
	envelope, _ := json.Marshal([]any{"rating", json.RawMessage(inner)})
	score := mapNIP69Rating([]string{string(envelope)})
	if score.Confidence != sellerrating.ConfidenceFeedbackNew || score.Value == nil {
		t.Fatalf("envelope rating = %+v", score)
	}
}

func TestFetchNIP69CurrencyFailedIncludesError(t *testing.T) {
	resetNIP69Cache()
	t.Cleanup(resetNIP69Cache)
	query := func(context.Context, []string, map[string]any) (nostrreq.QueryResult, error) {
		return nostrreq.QueryResult{}, fmt.Errorf("all relays failed")
	}
	if got := fetchNIP69CurrencyFrom(context.Background(), "mostro", nil, "ask", "EUR", 50000, query); got != nil {
		t.Fatalf("failed census = %#v, want nil", got)
	}
}
