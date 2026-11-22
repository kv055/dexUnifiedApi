package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dexunifiedapi/internal/testdb"
)

// Bisq 2 is only ingested with quote-side (fiat) amount specs, so its offers
// are fiat-denominated.
func TestFetchBisq2CurrencyMarksFiatDenomination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{
			"bisqEasyOffer": {
				"id": "b2-1",
				"direction": "BUY",
				"amountSpec": {"type": "QuoteSideRangeAmountSpec", "minAmount": 1000000, "maxAmount": 5000000},
				"priceSpec": {"type": "MarketPriceSpec"},
				"quoteSidePaymentMethodSpecs": [{"paymentMethod": "SEPA"}]
			}
		}]`))
	}))
	t.Cleanup(srv.Close)

	prevHost := bisq2RESTHost
	bisq2RESTHost = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { bisq2RESTHost = prevHost })

	res := fetchBisq2Currency(context.Background(), "bid", "GBP", 50000)
	if res == nil || len(res.Offers) != 1 {
		t.Fatalf("fetch result = %#v, want one offer", res)
	}
	if got := res.Offers[0].VolumeDenomination; got != "GBP" {
		t.Fatalf("VolumeDenomination = %q, want GBP", got)
	}
}

func TestSaveSnapshotPersistsVolumeDenomination(t *testing.T) {
	db := testdb.Open(t)
	btcMin := 0.002
	btcMax := 0.02
	fiatMin := 100
	fiatMax := 1000
	mk := func(denomination string) []offer {
		return []offer{{
			OfferID:            "hh-denomination-1",
			FiatCurrency:       "EUR",
			VolumeDenomination: denomination,
			Price:              50000,
			BTCMin:             &btcMin,
			BTCMax:             &btcMax,
			FiatMin:            &fiatMin,
			FiatMax:            &fiatMax,
			PaymentMethods:     []string{"SEPA"},
			PriceType:          priceTypeFloating(),
		}}
	}

	readDenomination := func(id int64) string {
		t.Helper()
		var got *string
		if err := db.QueryRow(`SELECT volume_denomination FROM offers WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("read volume_denomination: %v", err)
		}
		if got == nil {
			return ""
		}
		return *got
	}

	first := saveSnapshot(db, "hodlhodl", "EUR", "bid", 50000, mk("EUR"))
	if len(first) != 1 {
		t.Fatalf("insert live len = %d, want 1", len(first))
	}
	if first[0].VolumeDenomination != "EUR" {
		t.Fatalf("live payload denomination = %q, want EUR", first[0].VolumeDenomination)
	}
	if got := readDenomination(first[0].ID); got != "EUR" {
		t.Fatalf("inserted volume_denomination = %q, want EUR", got)
	}

	// An update from a writer that does not know the denomination keeps the
	// stored value instead of clearing it.
	second := saveSnapshot(db, "hodlhodl", "EUR", "bid", 50000, mk(""))
	if len(second) != 1 || second[0].ID != first[0].ID {
		t.Fatalf("update live = %#v, want same id %d", second, first[0].ID)
	}
	if got := readDenomination(first[0].ID); got != "EUR" {
		t.Fatalf("updated volume_denomination = %q, want EUR kept", got)
	}
}
