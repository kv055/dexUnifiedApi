package main

import (
	"math"
	"testing"
)

func TestMapHodlHodlOfferPrefersSatoshiLimits(t *testing.T) {
	raw := mapTestHodlHodlOffer()
	raw.MinAmount = "300.9"
	raw.MaxAmount = "600.9"
	raw.MinAmountSats = "500000"
	raw.MaxAmountSats = "1000000"

	got, status := mapHodlHodlOffer(raw, "ask", "EUR")
	if status != hodlHodlMapped {
		t.Fatalf("status = %v, want mapped", status)
	}
	if got.BTCMin == nil || got.BTCMax == nil || *got.BTCMin != 0.005 || *got.BTCMax != 0.01 {
		t.Fatalf("btc = %v-%v, want satoshi 0.005-0.01", ptrVal(got.BTCMin), ptrVal(got.BTCMax))
	}
	if got.FiatMin == nil || got.FiatMax == nil || *got.FiatMin != 300 || *got.FiatMax != 600 {
		t.Fatalf("fiat = %v-%v, want truncated 300-600", ptrVal(got.FiatMin), ptrVal(got.FiatMax))
	}
}

func TestMapHodlHodlOfferDerivesBTCFromDecimalFiat(t *testing.T) {
	raw := mapTestHodlHodlOffer()
	raw.MinAmount = "300.9"
	raw.MaxAmount = "600.9"
	raw.MinAmountSats = ""
	raw.MaxAmountSats = ""

	got, status := mapHodlHodlOffer(raw, "ask", "EUR")
	if status != hodlHodlMapped {
		t.Fatalf("status = %v, want mapped", status)
	}
	wantMin := round8(300.9 / 60000)
	wantMax := round8(600.9 / 60000)
	if got.BTCMin == nil || got.BTCMax == nil || *got.BTCMin != wantMin || *got.BTCMax != wantMax {
		t.Fatalf("btc = %v-%v, want %v-%v from decimal fiat", ptrVal(got.BTCMin), ptrVal(got.BTCMax), wantMin, wantMax)
	}
	if *got.FiatMin != 300 || *got.FiatMax != 600 {
		t.Fatalf("fiat = %d-%d, want integer truncation 300-600", *got.FiatMin, *got.FiatMax)
	}
}

func TestMapHodlHodlOfferValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*hodlHodlOfferJSON)
		dir    string
		ccy    string
		want   hodlHodlMapStatus
	}{
		{
			name:   "currency mismatch",
			mutate: func(o *hodlHodlOfferJSON) { o.CurrencyCode = "USD" },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipMismatch,
		},
		{
			name:   "side mismatch",
			mutate: func(o *hodlHodlOfferJSON) { o.Side = "buy" },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipMismatch,
		},
		{
			name:   "unsupported layer",
			mutate: func(o *hodlHodlOfferJSON) { o.AssetLayer = "ARK" },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipUnsupported,
		},
		{
			name:   "unsupported asset",
			mutate: func(o *hodlHodlOfferJSON) { o.AssetCode = "LTC" },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipUnsupported,
		},
		{
			name:   "missing id",
			mutate: func(o *hodlHodlOfferJSON) { o.ID = "  " },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipMalformed,
		},
		{
			name:   "zero price",
			mutate: func(o *hodlHodlOfferJSON) { o.Price = "0" },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipMalformed,
		},
		{
			name:   "negative price",
			mutate: func(o *hodlHodlOfferJSON) { o.Price = "-1" },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipMalformed,
		},
		{
			name:   "non-finite price",
			mutate: func(o *hodlHodlOfferJSON) { o.Price = "inf" },
			dir:    "ask", ccy: "EUR", want: hodlHodlSkipMalformed,
		},
		{
			name: "inverted sats",
			mutate: func(o *hodlHodlOfferJSON) {
				o.MinAmountSats = "200000"
				o.MaxAmountSats = "100000"
			},
			dir: "ask", ccy: "EUR", want: hodlHodlSkipMalformed,
		},
		{
			name: "inverted fiat",
			mutate: func(o *hodlHodlOfferJSON) {
				o.MinAmountSats = ""
				o.MaxAmountSats = ""
				o.MinAmount = "600"
				o.MaxAmount = "300"
			},
			dir: "ask", ccy: "EUR", want: hodlHodlSkipMalformed,
		},
		{
			name:   "empty layer defaults to on-chain",
			mutate: func(o *hodlHodlOfferJSON) { o.AssetLayer = "" },
			dir:    "ask", ccy: "EUR", want: hodlHodlMapped,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := mapTestHodlHodlOffer()
			tc.mutate(&raw)
			_, status := mapHodlHodlOffer(raw, tc.dir, tc.ccy)
			if status != tc.want {
				t.Fatalf("status = %v, want %v", status, tc.want)
			}
		})
	}
}

func TestMapHodlHodlOfferAcceptsLargeLimits(t *testing.T) {
	raw := mapTestHodlHodlOffer()
	raw.MinAmount = "1000000000"
	raw.MaxAmount = "5000000000"
	raw.MinAmountSats = "1666666666667"
	raw.MaxAmountSats = "8333333333334"

	got, status := mapHodlHodlOffer(raw, "ask", "EUR")
	if status != hodlHodlMapped {
		t.Fatalf("status = %v, want mapped large offer", status)
	}
	if got.FiatMin == nil || *got.FiatMin != 1000000000 || got.FiatMax == nil || *got.FiatMax != 5000000000 {
		t.Fatalf("fiat = %v-%v, want billion-scale source limits", ptrVal(got.FiatMin), ptrVal(got.FiatMax))
	}
	if got.BTCMin == nil || got.BTCMax == nil || *got.BTCMax < *got.BTCMin || *got.BTCMax < 10 {
		t.Fatalf("btc = %v-%v, want large positive satoshi range", ptrVal(got.BTCMin), ptrVal(got.BTCMax))
	}
}

func TestTruncateFiatAmountRejectsOverflow(t *testing.T) {
	if _, ok := truncateFiatAmount(math.Inf(1)); ok {
		t.Fatal("accepted +Inf")
	}
	if _, ok := truncateFiatAmount(float64(math.MaxInt)); ok {
		t.Fatal("accepted MaxInt")
	}
	got, ok := truncateFiatAmount(16.57)
	if !ok || got != 16 {
		t.Fatalf("truncate 16.57 = %d ok=%v, want 16", got, ok)
	}
}

func mapTestHodlHodlOffer() hodlHodlOfferJSON {
	return hodlHodlOfferJSON{
		ID:            "hh-map",
		AssetCode:     "BTC",
		AssetLayer:    "BTC",
		Side:          "sell",
		CurrencyCode:  "EUR",
		Price:         "60000",
		PriceSource:   "fixed_value",
		MinAmount:     "300",
		MaxAmount:     "600",
		MinAmountSats: "500000",
		MaxAmountSats: "1000000",
		PaymentMethodInstructions: []hodlHodlPaymentInstructionJSON{
			{PaymentMethodName: "SEPA"},
		},
	}
}

func ptrVal[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}
