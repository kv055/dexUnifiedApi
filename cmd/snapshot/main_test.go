package main

import (
	"encoding/json"
	"testing"

	"dexunifiedapi/internal/testdb"
)

func TestNormalizeHodlHodlPriceType(t *testing.T) {
	tests := []struct {
		name        string
		priceSource string
		want        string
	}{
		{
			name:        "fixed value",
			priceSource: "fixed_value",
			want:        "fixed",
		},
		{
			name:        "exchange rate",
			priceSource: "exchange_rate",
			want:        "floating",
		},
		{
			name:        "missing public price source",
			priceSource: "",
			want:        "unknown",
		},
		{
			name:        "unrecognized public price source",
			priceSource: "unexpected",
			want:        "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeHodlHodlPriceType(tc.priceSource)
			if got == nil {
				t.Fatalf("price type = nil, want %q", tc.want)
			}
			if *got != tc.want {
				t.Fatalf("price type = %q, want %q", *got, tc.want)
			}
		})
	}
}

func TestNonHodlHodlPriceTypeHelpersStayProviderSpecific(t *testing.T) {
	fixed := priceTypeFixed()
	if fixed == nil || *fixed != "fixed" {
		t.Fatalf("fixed price type = %#v, want fixed", fixed)
	}

	floating := priceTypeFloating()
	if floating == nil || *floating != "floating" {
		t.Fatalf("floating price type = %#v, want floating", floating)
	}
}

func TestRecordFetchResultTracksZeroOfferExchange(t *testing.T) {
	exchangeData := make(exchangeSnapshotData)

	if ok := recordFetchResult(exchangeData, "bisq", &fetchResult{}); !ok {
		t.Fatal("recordFetchResult returned false for successful empty result")
	}
	if ids, ok := exchangeData["bisq"]; !ok {
		t.Fatal("bisq exchange missing from snapshot data")
	} else if len(ids) != 0 {
		t.Fatalf("bisq ids len = %d, want 0", len(ids))
	}

	payload, total, err := buildSnapshotLogPayload(exchangeData)
	if err != nil {
		t.Fatalf("buildSnapshotLogPayload: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}

	var got map[string]struct {
		Count int      `json:"count"`
		IDs   []string `json:"ids"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["bisq"].Count != 0 {
		t.Fatalf("bisq count = %d, want 0", got["bisq"].Count)
	}
	if got["bisq"].IDs == nil {
		t.Fatal("bisq ids = nil, want empty JSON array")
	}
	if len(got["bisq"].IDs) != 0 {
		t.Fatalf("bisq ids len = %d, want 0", len(got["bisq"].IDs))
	}
}

func TestRecordFetchResultTreatsNilAsOffline(t *testing.T) {
	exchangeData := make(exchangeSnapshotData)

	if ok := recordFetchResult(exchangeData, "bisq", nil); ok {
		t.Fatal("recordFetchResult returned true for nil result")
	}
	if _, ok := exchangeData["bisq"]; ok {
		t.Fatal("bisq exchange should not be tracked for nil result")
	}

	fetchers := []exchFetcher{
		{name: "bisq"},
		{name: "hodlhodl"},
	}
	recordFetchResult(exchangeData, "hodlhodl", &fetchResult{})

	offline := offlineExchangeNames(fetchers, exchangeData)
	if len(offline) != 1 || offline[0] != "bisq" {
		t.Fatalf("offline exchanges = %#v, want [bisq]", offline)
	}
}

func TestRecordFetchResultTracksOfferIDs(t *testing.T) {
	exchangeData := make(exchangeSnapshotData)
	result := &fetchResult{Offers: []offer{
		{OfferID: "b"},
		{OfferID: "a"},
		{OfferID: ""},
	}}

	if ok := recordFetchResult(exchangeData, "bisq", result); !ok {
		t.Fatal("recordFetchResult returned false for successful result")
	}
	payload, total, err := buildSnapshotLogPayload(exchangeData)
	if err != nil {
		t.Fatalf("buildSnapshotLogPayload: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}

	var got map[string]struct {
		Count int      `json:"count"`
		IDs   []string `json:"ids"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["bisq"].Count != 2 {
		t.Fatalf("bisq count = %d, want 2", got["bisq"].Count)
	}
	wantIDs := []string{"a", "b"}
	for i, want := range wantIDs {
		if got["bisq"].IDs[i] != want {
			t.Fatalf("bisq ids[%d] = %q, want %q", i, got["bisq"].IDs[i], want)
		}
	}
}

func TestSaveSnapshotPersistsBisqOffer(t *testing.T) {
	db := testdb.Open(t)
	btcMin := 0.01
	btcMax := 0.02
	fiatMin := 600
	fiatMax := 1200
	priceType := priceTypeFixed()

	live := saveSnapshot(db, "bisq", "EUR", "sell", 60000, []offer{{
		OfferID:       "bisq-offer-1",
		FiatCurrency:  "EUR",
		Price:         61000,
		BTCMin:        &btcMin,
		BTCMax:        &btcMax,
		FiatMin:       &fiatMin,
		FiatMax:       &fiatMax,
		PaymentMethod: "SEPA",
		PriceType:     priceType,
	}})

	if len(live) != 1 {
		t.Fatalf("live offers len = %d, want 1", len(live))
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM offers WHERE exchange = ? AND offer_id = ?`, "bisq", "bisq-offer-1").Scan(&count); err != nil {
		t.Fatalf("count persisted bisq offer: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted bisq offers = %d, want 1", count)
	}
}
