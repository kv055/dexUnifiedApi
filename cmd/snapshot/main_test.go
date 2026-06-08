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

func TestNormalizePeachMethod(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"sepa", "SEPA"},
		{"instantSepa", "Instant SEPA"},
		{"nationalTransferDE", "National Transfer"},
		{"nationalTransferFR", "National Transfer"},
		{"revolut", "Revolut"},
		{"wise", "Wise"},
		{"paypal", "PayPal"},
		{"n26", "N26"},
		{"mbWay", "MB Way"},
		{"bizum", "Bizum"},
		{"twint", "Twint"},
		{"vipps", "Vipps"},
		{"lydia", "Lydia"},
		{"applePay", "Apple Pay"},
		{"googlePay", "Google Pay"},
		{"cashTrade", "Cash Trade"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			if got := normalizePeachMethod(tc.raw); got != tc.want {
				t.Fatalf("normalizePeachMethod(%q) = %q, want %q", tc.raw, got, tc.want)
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

func TestSaveSnapshotCollapsesIdlessOffersByFingerprint(t *testing.T) {
	db := testdb.Open(t)
	btcMin := 0.01
	btcMax := 0.02
	priceType := priceTypeFixed()

	// Offers with NO stable id fall back to fingerprint identity, so identical-terms
	// id-less offers collapse onto one row and emit one baseline/hub entry per cycle.
	dup := offer{
		FiatCurrency:  "EUR",
		Price:         51899,
		BTCMin:        &btcMin,
		BTCMax:        &btcMax,
		PaymentMethod: "SEPA",
		PriceType:     priceType,
	}
	live := saveSnapshot(db, "peachbitcoin", "EUR", "buy", 60000, []offer{dup, dup, dup})

	if len(live) != 1 {
		t.Fatalf("live offers len = %d, want 1 (id-less duplicates collapse by fingerprint)", len(live))
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM offers WHERE exchange = ?`, "peachbitcoin").Scan(&count); err != nil {
		t.Fatalf("count offers: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted offers = %d, want 1", count)
	}
}

func intPtr(v int) *int { return &v }
func floatPtr(v float64) *float64 { return &v }

func TestMapPeachOffer(t *testing.T) {
	t.Parallel()

	sellAmount, _ := json.Marshal(int64(30000))
	buyAmount, _ := json.Marshal([]int64{598730, 598730})

	tests := []struct {
		name      string
		in        peachOffer
		direction string
		btcPrice  int
		wantOK    bool
		wantFiat  int
		wantBTC   float64
	}{
		{
			name: "sell uses prices.EUR as fiat volume",
			in: peachOffer{
				ID:             "361418",
				Amount:         sellAmount,
				MeansOfPayment: map[string][]string{"EUR": {"revolut"}},
				Prices:         map[string]float64{"EUR": 17},
			},
			direction: "sell",
			btcPrice:  55359,
			wantOK:    true,
			wantFiat:  17,
			wantBTC:   0.0003,
		},
		{
			name: "buy derives fiat from market price",
			in: peachOffer{
				ID:             "353910",
				Amount:         buyAmount,
				MeansOfPayment: map[string][]string{"EUR": {"sepa"}},
			},
			direction: "buy",
			btcPrice:  55359,
			wantOK:    true,
			wantFiat:  331, // round(0.0059873 * 55359)
			wantBTC:   0.0059873,
		},
		{
			name: "sell without EUR price skipped",
			in: peachOffer{
				ID:             "361885",
				Amount:         sellAmount,
				MeansOfPayment: map[string][]string{"EUR": {"sepa"}},
				Prices:         map[string]float64{"SAT": 66950},
			},
			direction: "sell",
			btcPrice:  55359,
			wantOK:    false,
		},
		{
			name: "non EUR payment skipped",
			in: peachOffer{
				ID:             "361885",
				Amount:         sellAmount,
				MeansOfPayment: map[string][]string{"SAT": {"lnurl"}},
				Prices:         map[string]float64{"EUR": 17},
			},
			direction: "sell",
			btcPrice:  55359,
			wantOK:    false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := mapPeachOffer(tc.in, tc.direction, tc.btcPrice)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.FiatMin == nil || got.FiatMax == nil || *got.FiatMin != *got.FiatMax {
				t.Fatalf("fiat min/max = %v/%v, want equal fixed size", got.FiatMin, got.FiatMax)
			}
			if *got.FiatMax != tc.wantFiat {
				t.Fatalf("fiat_max = %d, want %d", *got.FiatMax, tc.wantFiat)
			}
			if got.BTCMax == nil || *got.BTCMax != tc.wantBTC {
				t.Fatalf("btc_max = %v, want %f", got.BTCMax, tc.wantBTC)
			}
			if got.BTCMin == nil || *got.BTCMin != *got.BTCMax {
				t.Fatalf("btc_min = %v, btc_max = %v, want equal fixed size", got.BTCMin, got.BTCMax)
			}
			if tc.direction == "sell" && got.Price <= 1000 {
				t.Fatalf("sell price = %f, want EUR/BTC not total fiat", got.Price)
			}
		})
	}
}

func TestEffectiveFiatMax(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		offer   offer
		wantMax int
	}{
		{
			name:    "stored fiat max",
			offer:   offer{FiatMax: intPtr(500), BTCMax: floatPtr(0.01), Price: 60000},
			wantMax: 500,
		},
		{
			name:    "derive from btc when fiat zero",
			offer:   offer{FiatMax: intPtr(0), BTCMax: floatPtr(0.0002), Price: 60000},
			wantMax: 12,
		},
		{
			name:    "sub unit derived fiat rounds up",
			offer:   offer{FiatMax: intPtr(0), BTCMax: floatPtr(0.00001), Price: 60000},
			wantMax: 1, // round(0.6)
		},
		{
			name:    "sub unit derived fiat rounds down",
			offer:   offer{FiatMax: intPtr(0), BTCMax: floatPtr(0.000008), Price: 60000},
			wantMax: 0, // round(0.48)
		},
		{
			name:    "no volume",
			offer:   offer{Price: 60000},
			wantMax: 0,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := effectiveFiatMax(tc.offer); got != tc.wantMax {
				t.Fatalf("effectiveFiatMax() = %d, want %d", got, tc.wantMax)
			}
		})
	}
}

func TestFilterDustOffers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		offers    []offer
		wantCount int
		wantIDs   []string
	}{
		{
			name: "drops sub unit derived max",
			offers: []offer{
				{OfferID: "dust", FiatMax: intPtr(0), BTCMax: floatPtr(0.000008), Price: 60000},
			},
			wantCount: 0,
		},
		{
			name: "keeps bisq whole unit zero with derived max",
			offers: []offer{
				{OfferID: "ok", FiatMax: intPtr(0), BTCMax: floatPtr(0.0002), Price: 60000},
			},
			wantCount: 1,
			wantIDs:   []string{"ok"},
		},
		{
			name: "keeps normal stored fiat max",
			offers: []offer{
				{OfferID: "normal", FiatMax: intPtr(500), BTCMax: floatPtr(0.01), Price: 60000},
			},
			wantCount: 1,
			wantIDs:   []string{"normal"},
		},
		{
			name: "drops no volume",
			offers: []offer{
				{OfferID: "empty", Price: 60000},
			},
			wantCount: 0,
		},
		{
			name: "mixed batch",
			offers: []offer{
				{OfferID: "dust", FiatMax: intPtr(0), BTCMax: floatPtr(0.000008), Price: 60000},
				{OfferID: "ok", FiatMax: intPtr(100), BTCMax: floatPtr(0.01), Price: 60000},
			},
			wantCount: 1,
			wantIDs:   []string{"ok"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterDustOffers(tc.offers)
			if len(got) != tc.wantCount {
				t.Fatalf("filterDustOffers() len = %d, want %d", len(got), tc.wantCount)
			}
			for i, wantID := range tc.wantIDs {
				if got[i].OfferID != wantID {
					t.Fatalf("got[%d].OfferID = %q, want %q", i, got[i].OfferID, wantID)
				}
			}
		})
	}
}

func TestFilterDustOffersAllDustKeepsExchangeOnline(t *testing.T) {
	exchangeData := make(exchangeSnapshotData)
	dust := []offer{
		{OfferID: "d1", FiatMax: intPtr(0), BTCMax: floatPtr(0.000008), Price: 60000},
		{OfferID: "d2", FiatMax: intPtr(0), BTCMax: floatPtr(0.000005), Price: 60000},
	}
	filtered := filterDustOffers(dust)
	if len(filtered) != 0 {
		t.Fatalf("filtered len = %d, want 0", len(filtered))
	}
	result := &fetchResult{Offers: filtered}
	if !recordFetchResult(exchangeData, "peachbitcoin", result) {
		t.Fatal("recordFetchResult returned false for successful empty-after-filter result")
	}
	if _, ok := exchangeData["peachbitcoin"]; !ok {
		t.Fatal("peachbitcoin exchange missing from snapshot data after all-dust filter")
	}
}

func TestSaveSnapshotSkipsDustOffersWhenFiltered(t *testing.T) {
	db := testdb.Open(t)
	priceType := priceTypeFixed()
	dust := offer{
		OfferID:       "dust-offer",
		FiatCurrency:  "EUR",
		Price:         60000,
		FiatMax:       intPtr(0),
		BTCMax:        floatPtr(0.000008),
		PaymentMethod: "SEPA",
		PriceType:     priceType,
	}
	ok := offer{
		OfferID:       "ok-offer",
		FiatCurrency:  "EUR",
		Price:         60000,
		FiatMax:       intPtr(100),
		BTCMax:        floatPtr(0.01),
		PaymentMethod: "SEPA",
		PriceType:     priceType,
	}
	live := saveSnapshot(db, "bisq", "EUR", "sell", 60000, filterDustOffers([]offer{dust, ok}))
	if len(live) != 1 {
		t.Fatalf("live offers len = %d, want 1", len(live))
	}
	var dustCount int
	if err := db.QueryRow(`SELECT count(*) FROM offers WHERE offer_id = ?`, "dust-offer").Scan(&dustCount); err != nil {
		t.Fatalf("count dust offer: %v", err)
	}
	if dustCount != 0 {
		t.Fatalf("dust offer persisted = %d, want 0", dustCount)
	}
}

func TestSaveSnapshotKeepsDistinctIdOffersWithSameFingerprint(t *testing.T) {
	db := testdb.Open(t)
	btcMin := 0.0021
	btcMax := 0.0021
	priceType := priceTypeFixed()

	// The peach case: many distinct offers (each a stable id) share one fingerprint
	// (e.g. 91 makers all offering 0.0021 BTC via Revolut). Because they carry real
	// ids, each must get its own row — the fingerprint fallback must NOT merge them.
	mk := func(id string) offer {
		return offer{
			OfferID:       id,
			FiatCurrency:  "EUR",
			Price:         51899,
			BTCMin:        &btcMin,
			BTCMax:        &btcMax,
			PaymentMethod: "revolut",
			PriceType:     priceType,
		}
	}
	live := saveSnapshot(db, "peachbitcoin", "EUR", "buy", 60000, []offer{mk("p-1"), mk("p-2"), mk("p-3")})

	if len(live) != 3 {
		t.Fatalf("live offers len = %d, want 3 (distinct ids must not collapse)", len(live))
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM offers WHERE exchange = ?`, "peachbitcoin").Scan(&count); err != nil {
		t.Fatalf("count offers: %v", err)
	}
	if count != 3 {
		t.Fatalf("persisted offers = %d, want 3 (one row per distinct offer_id)", count)
	}
}
