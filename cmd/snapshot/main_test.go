package main

import (
	"context"
	"encoding/json"
	"testing"

	"commons/paymentmethods"
	"dexunifiedapi/internal/sellerrating"
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
		{"sepa", "Sepa"},
		{"instantSepa", "Instant Sepa"},
		{"nationalTransferDE", "National Transfer"},
		{"nationalTransferFR", "National Transfer"},
		{"revolut", "Revolut"},
		{"wise", "Wise"},
		{"paypal", "Paypal"},
		{"n26", "N26"},
		{"mbWay", "Mb Way"},
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

	payload, total, err := buildSnapshotLogPayload(exchangeData, nil, newFetchTimings())
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

// A fetcher that fails hard (nil result: daemon unreachable, gRPC error, no
// reference price) must still appear in the snapshot log with count 0.
// dbmetrics derives dex_exchange_up from this payload; if the exchange key is
// missing the Prometheus series vanishes and the down-alert sees NoData
// instead of 0 — the exact blind spot that hid a dead bisq feed.
func TestSnapshotLogKeepsHardFailedExchangeAtZero(t *testing.T) {
	exchangeData := make(exchangeSnapshotData)
	fetchers := []exchFetcher{
		{name: "bisq"},
		{name: "hodlhodl"},
	}

	// bisq fails hard, hodlhodl succeeds with offers.
	recordFetchResult(exchangeData, "bisq", nil)
	recordFetchResult(exchangeData, "hodlhodl", &fetchResult{Offers: []offer{{OfferID: "h1"}}})

	offline := offlineExchangeNames(fetchers, exchangeData)
	payload, total, err := buildSnapshotLogPayload(exchangeData, offline, newFetchTimings())
	if err != nil {
		t.Fatalf("buildSnapshotLogPayload: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}

	var got map[string]struct {
		Count   int      `json:"count"`
		IDs     []string `json:"ids"`
		Offline bool     `json:"offline"`
		Status  string   `json:"status"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	bisq, ok := got["bisq"]
	if !ok {
		t.Fatal("bisq missing from snapshot log payload — dex_exchange_up series would vanish")
	}
	if bisq.Count != 0 {
		t.Fatalf("bisq count = %d, want 0", bisq.Count)
	}
	if !bisq.Offline {
		t.Fatal("bisq offline flag = false, want true")
	}
	if bisq.Status != "failed" {
		t.Fatalf("bisq status = %q, want failed", bisq.Status)
	}
	if bisq.IDs == nil || len(bisq.IDs) != 0 {
		t.Fatalf("bisq ids = %#v, want empty JSON array", bisq.IDs)
	}
	if got["hodlhodl"].Count != 1 || got["hodlhodl"].Offline {
		t.Fatalf("hodlhodl = %+v, want count 1 online", got["hodlhodl"])
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
	payload, total, err := buildSnapshotLogPayload(exchangeData, nil, newFetchTimings())
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

func TestSaveSnapshotUpdatesBTCAndFiatLimitsWithoutChangingIdentity(t *testing.T) {
	db := testdb.Open(t)
	btcMin := 0.01
	btcMax := 0.02
	fiatMin := 600
	fiatMax := 1200
	priceType := priceTypeFixed()

	first := saveSnapshot(db, "hodlhodl", "EUR", "ask", 60000, []offer{{
		OfferID:        "hh-persist-1",
		FiatCurrency:   "EUR",
		Price:          61000,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		FiatMin:        &fiatMin,
		FiatMax:        &fiatMax,
		PaymentMethods: []string{"SEPA"},
		PriceType:      priceType,
	}})
	if len(first) != 1 {
		t.Fatalf("first live len = %d, want 1", len(first))
	}

	btcMin, btcMax = 0.03, 0.08
	fiatMin, fiatMax = 1800, 4800
	second := saveSnapshot(db, "hodlhodl", "EUR", "ask", 60000, []offer{{
		OfferID:        "hh-persist-1",
		FiatCurrency:   "EUR",
		Price:          62500,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		FiatMin:        &fiatMin,
		FiatMax:        &fiatMax,
		PaymentMethods: []string{"SEPA"},
		PriceType:      priceType,
	}})
	if len(second) != 1 {
		t.Fatalf("second live len = %d, want 1", len(second))
	}
	if second[0].ID != first[0].ID {
		t.Fatalf("id = %d, want preserved %d", second[0].ID, first[0].ID)
	}
	if second[0].FirstSeen != first[0].FirstSeen {
		t.Fatalf("first_seen = %q, want preserved %q", second[0].FirstSeen, first[0].FirstSeen)
	}
	if second[0].BTCMin == nil || second[0].BTCMax == nil || *second[0].BTCMin != 0.03 || *second[0].BTCMax != 0.08 {
		t.Fatalf("live btc = %v-%v, want 0.03-0.08", second[0].BTCMin, second[0].BTCMax)
	}
	if second[0].FiatMin == nil || second[0].FiatMax == nil || *second[0].FiatMin != 1800 || *second[0].FiatMax != 4800 {
		t.Fatalf("live fiat = %v-%v, want 1800-4800", second[0].FiatMin, second[0].FiatMax)
	}
	if second[0].Price != 62500 {
		t.Fatalf("live price = %v, want 62500", second[0].Price)
	}

	var (
		gotPrice, gotBTCMin, gotBTCMax float64
		gotFiatMin, gotFiatMax         int64
		gotFirstSeen                   string
		gotID                          int64
	)
	if err := db.QueryRow(`
		SELECT id, price, btc_min, btc_max, fiat_min, fiat_max,
			to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM offers WHERE exchange = ? AND offer_id = ?`,
		"hodlhodl", "hh-persist-1",
	).Scan(&gotID, &gotPrice, &gotBTCMin, &gotBTCMax, &gotFiatMin, &gotFiatMax, &gotFirstSeen); err != nil {
		t.Fatalf("load persisted offer: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM offers WHERE exchange = ? AND offer_id = ?`, "hodlhodl", "hh-persist-1").Scan(&count); err != nil {
		t.Fatalf("count persisted offer: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted rows = %d, want 1", count)
	}
	if gotID != first[0].ID || gotFirstSeen != first[0].FirstSeen {
		t.Fatalf("db identity id=%d first_seen=%q, want id=%d first_seen=%q", gotID, gotFirstSeen, first[0].ID, first[0].FirstSeen)
	}
	if gotPrice != 62500 || gotBTCMin != 0.03 || gotBTCMax != 0.08 || gotFiatMin != 1800 || gotFiatMax != 4800 {
		t.Fatalf("db limits price=%v btc=%v-%v fiat=%d-%d, want updated values", gotPrice, gotBTCMin, gotBTCMax, gotFiatMin, gotFiatMax)
	}
}

func TestSaveSnapshotPersistsBisqOffer(t *testing.T) {
	db := testdb.Open(t)
	btcMin := 0.01
	btcMax := 0.02
	fiatMin := 600
	fiatMax := 1200
	priceType := priceTypeFixed()
	depositBTC := 0.003
	depositPct := 0.15
	stakeBTC := 0.001
	stakePct := 0.05

	live := saveSnapshot(db, "bisq", "EUR", "ask", 60000, []offer{{
		OfferID:              "bisq-offer-1",
		FiatCurrency:         "EUR",
		Price:                61000,
		BTCMin:               &btcMin,
		BTCMax:               &btcMax,
		FiatMin:              &fiatMin,
		FiatMax:              &fiatMax,
		PaymentMethods:       []string{"SEPA"},
		PriceType:            priceType,
		SecurityDepositBTC:   &depositBTC,
		SecurityDepositPct:   &depositPct,
		CounterpartyStakeBTC: &stakeBTC,
		CounterpartyStakePct: &stakePct,
	}})

	if len(live) != 1 {
		t.Fatalf("live offers len = %d, want 1", len(live))
	}

	var count int
	var gotDepositBTC, gotDepositPct, gotStakeBTC, gotStakePct float64
	if err := db.QueryRow(`SELECT count(*), security_deposit_btc, security_deposit_pct, counterparty_stake_btc, counterparty_stake_pct FROM offers WHERE exchange = ? AND offer_id = ? GROUP BY security_deposit_btc, security_deposit_pct, counterparty_stake_btc, counterparty_stake_pct`, "bisq", "bisq-offer-1").Scan(&count, &gotDepositBTC, &gotDepositPct, &gotStakeBTC, &gotStakePct); err != nil {
		t.Fatalf("count persisted bisq offer: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted bisq offers = %d, want 1", count)
	}
	if gotDepositBTC != depositBTC {
		t.Fatalf("security_deposit_btc = %v, want %v", gotDepositBTC, depositBTC)
	}
	if gotDepositPct != depositPct {
		t.Fatalf("security_deposit_pct = %v, want %v", gotDepositPct, depositPct)
	}
	if gotStakeBTC != stakeBTC {
		t.Fatalf("counterparty_stake_btc = %v, want %v", gotStakeBTC, stakeBTC)
	}
	if gotStakePct != stakePct {
		t.Fatalf("counterparty_stake_pct = %v, want %v", gotStakePct, stakePct)
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
		FiatCurrency:   "EUR",
		Price:          51899,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		PaymentMethods: []string{"SEPA"},
		PriceType:      priceType,
	}
	live := saveSnapshot(db, "peachbitcoin", "EUR", "bid", 60000, []offer{dup, dup, dup})

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

func TestMapHodlHodlTrader(t *testing.T) {
	t.Parallel()
	t.Run("no rating yet (nil) is unavailable", func(t *testing.T) {
		t.Parallel()
		s := mapHodlHodlTrader(hodlHodlTrader{TradesCount: 0, Rating: nil})
		if s.Value != nil || s.Confidence != sellerrating.ConfidenceUnavailable {
			t.Fatalf("got value=%v conf=%q, want nil/unavailable", s.Value, s.Confidence)
		}
	})

	tests := []struct {
		name     string
		trader   hodlHodlTrader
		wantConf sellerrating.Confidence
		// wantMin/Max bound the expected score (shrinkage makes exact values noisy).
		wantMin, wantMax float64
	}{
		{
			name:     "few good trades stay new near the conservative prior",
			trader:   hodlHodlTrader{TradesCount: 3, Rating: floatPtr(1.0)},
			wantConf: sellerrating.ConfidenceFeedbackNew,
			wantMin:  50, wantMax: 60,
		},
		{
			name:     "many good trades verified",
			trader:   hodlHodlTrader{TradesCount: 200, Rating: floatPtr(0.98)},
			wantConf: sellerrating.ConfidenceFeedbackVerified,
			wantMin:  90, wantMax: 100,
		},
		{
			name:     "strong hodler bonus applied, capped at 100",
			trader:   hodlHodlTrader{TradesCount: 500, Rating: floatPtr(1.0), StrongHodler: true},
			wantConf: sellerrating.ConfidenceFeedbackVerified,
			wantMin:  99, wantMax: 100,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := mapHodlHodlTrader(tc.trader)
			if s.Value == nil {
				t.Fatal("Value = nil, want a number")
			}
			if *s.Value < tc.wantMin || *s.Value > tc.wantMax {
				t.Fatalf("Value = %v, want in [%v,%v]", *s.Value, tc.wantMin, tc.wantMax)
			}
			if s.Confidence != tc.wantConf {
				t.Fatalf("Confidence = %q, want %q", s.Confidence, tc.wantConf)
			}
		})
	}
}

func TestHodlHodlTraderRatingDecoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		ratingJSON      string
		wantRating      *float64
		wantUnavailable bool
	}{
		{name: "number", ratingJSON: `0.96`, wantRating: floatPtr(0.96)},
		{name: "numeric string", ratingJSON: `"0.91"`, wantRating: floatPtr(0.91)},
		{name: "null", ratingJSON: `null`, wantRating: nil, wantUnavailable: true},
		{name: "malformed", ratingJSON: `{"bad":true}`, wantUnavailable: true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var trader hodlHodlTrader
			payload := []byte(`{"trades_count":12,"rating":` + tc.ratingJSON + `,"strong_hodler":false}`)
			if err := json.Unmarshal(payload, &trader); err != nil {
				t.Fatalf("unmarshal trader: %v", err)
			}
			if tc.wantRating == nil {
				if trader.Rating != nil {
					t.Fatalf("rating = %v, want nil", *trader.Rating)
				}
			} else if trader.Rating == nil || *trader.Rating != *tc.wantRating {
				t.Fatalf("rating = %v, want %v", trader.Rating, *tc.wantRating)
			}
			score := mapHodlHodlTrader(trader)
			if tc.wantUnavailable && score.Confidence != sellerrating.ConfidenceUnavailable {
				t.Fatalf("confidence = %q, want unavailable", score.Confidence)
			}
			if !tc.wantUnavailable && score.Confidence == sellerrating.ConfidenceUnavailable {
				t.Fatal("valid rating unexpectedly unavailable")
			}
		})
	}
}

func TestDecodeRobosatsBookShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		wantLen  int
		wantType string
		wantErr  bool
	}{
		{name: "populated array", body: `[{"id":1}]`, wantLen: 1, wantType: "array"},
		{name: "empty array", body: `[]`, wantLen: 0, wantType: "array"},
		{name: "not found object", body: `{"not_found":"No orders found"}`, wantLen: 0, wantType: "not_found"},
		{name: "unknown error object", body: `{"detail":"coordinator failed"}`, wantType: "error_object", wantErr: true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			offers, responseType, err := decodeRobosatsBook([]byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if responseType != tc.wantType {
				t.Fatalf("response type = %q, want %q", responseType, tc.wantType)
			}
			if !tc.wantErr && len(offers) != tc.wantLen {
				t.Fatalf("offers = %d, want %d", len(offers), tc.wantLen)
			}
		})
	}
}

func TestSaveSnapshotPersistsSellerRating(t *testing.T) {
	db := testdb.Open(t)
	btcMin := 0.01
	btcMax := 0.02

	// A populated feedback score and a bisq-style unavailable (all-NULL) score
	// must both round-trip through the new columns and the ?::jsonb cast.
	rated := offer{
		OfferID:      "hh-rated-1",
		FiatCurrency: "EUR",
		Price:        61000,
		BTCMin:       &btcMin,
		BTCMax:       &btcMax,
		PriceType:    priceTypeFixed(),
	}
	applyRating(&rated, mapHodlHodlTrader(hodlHodlTrader{TradesCount: 42, Rating: floatPtr(0.98), StrongHodler: true}))
	rated.SellerRatingSignals = mapHodlHodlSignals(hodlHodlTrader{
		TradesCount: 42, StrongHodler: true, DaysSinceLastTrade: 3, Verified: true, OnlineStatus: "online",
	})
	saveSnapshot(db, "hodlhodl", "EUR", "ask", 60000, []offer{rated})

	var gotScore *float64
	var gotConf *string
	var gotRaw *string
	var gotSignals *string
	if err := db.QueryRow(`SELECT seller_rating_score, seller_rating_confidence, seller_rating_raw::text, seller_rating_signals::text
		FROM offers WHERE exchange = ? AND offer_id = ?`, "hodlhodl", "hh-rated-1").Scan(&gotScore, &gotConf, &gotRaw, &gotSignals); err != nil {
		t.Fatalf("read rated offer: %v", err)
	}
	if gotScore == nil || *gotScore <= 0 || *gotScore > 100 {
		t.Fatalf("seller_rating_score = %v, want a value in (0,100]", gotScore)
	}
	if gotConf == nil || *gotConf != string(sellerrating.ConfidenceFeedbackVerified) {
		t.Fatalf("seller_rating_confidence = %v, want %q", gotConf, sellerrating.ConfidenceFeedbackVerified)
	}
	if gotRaw == nil {
		t.Fatal("seller_rating_raw = NULL, want a JSON blob")
	}
	if gotSignals == nil {
		t.Fatal("seller_rating_signals = NULL, want a hodlhodl extras blob")
	}
	var signals map[string]any
	if err := json.Unmarshal([]byte(*gotSignals), &signals); err != nil {
		t.Fatalf("seller_rating_signals = %s, want JSON: %v", *gotSignals, err)
	}
	if signals["source"] != "hodlhodl" {
		t.Fatalf("seller_rating_signals = %s, want source=hodlhodl", *gotSignals)
	}

	// Unavailable path stores NULL score/raw and the literal 'unavailable'.
	unrated := offer{
		OfferID:      "bisq-unrated-1",
		FiatCurrency: "EUR",
		Price:        61000,
		BTCMin:       &btcMin,
		BTCMax:       &btcMax,
		PriceType:    priceTypeFixed(),
	}
	applyRating(&unrated, sellerrating.Unavailable())
	saveSnapshot(db, "bisq", "EUR", "ask", 60000, []offer{unrated})

	var uScore *float64
	var uConf *string
	var uRaw *string
	if err := db.QueryRow(`SELECT seller_rating_score, seller_rating_confidence, seller_rating_raw::text
		FROM offers WHERE exchange = ? AND offer_id = ?`, "bisq", "bisq-unrated-1").Scan(&uScore, &uConf, &uRaw); err != nil {
		t.Fatalf("read unrated offer: %v", err)
	}
	if uScore != nil {
		t.Fatalf("seller_rating_score = %v, want NULL", *uScore)
	}
	if uRaw != nil {
		t.Fatalf("seller_rating_raw = %v, want NULL", *uRaw)
	}
	if uConf == nil || *uConf != string(sellerrating.ConfidenceUnavailable) {
		t.Fatalf("seller_rating_confidence = %v, want %q", uConf, sellerrating.ConfidenceUnavailable)
	}
}

func intPtr(v int) *int           { return &v }
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
			direction: "ask",
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
			direction: "bid",
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
			direction: "ask",
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
			direction: "ask",
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
			// Peach offers carry the maker id (for the server's profit-gated
			// rating lookup) and no rating of their own at snapshot time.
			if got.MakerID != tc.in.User.ID {
				t.Fatalf("MakerID = %q, want %q", got.MakerID, tc.in.User.ID)
			}
			if got.SellerRatingScore != nil || got.SellerRatingConfidence != nil {
				t.Fatalf("Peach offer should have no rating at snapshot time, got score=%v conf=%v", got.SellerRatingScore, got.SellerRatingConfidence)
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
			if tc.direction == "ask" && got.Price <= 1000 {
				t.Fatalf("sell price = %f, want EUR/BTC not total fiat", got.Price)
			}
		})
	}
}

func TestMapPeachOfferCarriesMakerID(t *testing.T) {
	t.Parallel()
	amount, _ := json.Marshal(int64(30000))
	in := peachOffer{
		ID:             "361418",
		Amount:         amount,
		MeansOfPayment: map[string][]string{"EUR": {"revolut"}},
		Prices:         map[string]float64{"EUR": 17},
	}
	in.User.ID = "maker-9"

	got, ok := mapPeachOffer(in, "ask", 55359)
	if !ok {
		t.Fatal("mapPeachOffer returned ok=false")
	}
	if got.MakerID != "maker-9" {
		t.Fatalf("MakerID = %q, want %q", got.MakerID, "maker-9")
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

func TestFilterDustOffersDoesNotChangeFetchStatus(t *testing.T) {
	incomplete := incompleteFetch(60000, []offer{
		{OfferID: "d1", FiatMax: intPtr(0), BTCMax: floatPtr(0.000008), Price: 60000},
		{OfferID: "ok", FiatMax: intPtr(100), BTCMax: floatPtr(0.01), Price: 60000},
	})
	incomplete.Offers = filterDustOffers(incomplete.Offers)
	if incomplete.status() != fetchStatusIncomplete {
		t.Fatalf("status = %q after dust filter, want incomplete", incomplete.status())
	}
	completeAllDust := completeFetch(60000, []offer{
		{OfferID: "d1", FiatMax: intPtr(0), BTCMax: floatPtr(0.000008), Price: 60000},
	})
	completeAllDust.Offers = filterDustOffers(completeAllDust.Offers)
	if completeAllDust.status() != fetchStatusComplete || len(completeAllDust.Offers) != 0 {
		t.Fatalf("complete all-dust = status %q len %d, want complete empty", completeAllDust.status(), len(completeAllDust.Offers))
	}
}

func TestSaveSnapshotSkipsDustOffersWhenFiltered(t *testing.T) {
	db := testdb.Open(t)
	priceType := priceTypeFixed()
	dust := offer{
		OfferID:        "dust-offer",
		FiatCurrency:   "EUR",
		Price:          60000,
		FiatMax:        intPtr(0),
		BTCMax:         floatPtr(0.000008),
		PaymentMethods: []string{"SEPA"},
		PriceType:      priceType,
	}
	ok := offer{
		OfferID:        "ok-offer",
		FiatCurrency:   "EUR",
		Price:          60000,
		FiatMax:        intPtr(100),
		BTCMax:         floatPtr(0.01),
		PaymentMethods: []string{"SEPA"},
		PriceType:      priceType,
	}
	live := saveSnapshot(db, "bisq", "EUR", "ask", 60000, filterDustOffers([]offer{dust, ok}))
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
			OfferID:        id,
			FiatCurrency:   "EUR",
			Price:          51899,
			BTCMin:         &btcMin,
			BTCMax:         &btcMax,
			PaymentMethods: []string{"revolut"},
			PriceType:      priceType,
		}
	}
	live := saveSnapshot(db, "peachbitcoin", "EUR", "bid", 60000, []offer{mk("p-1"), mk("p-2"), mk("p-3")})

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

func resetPaymentMethodTouchesForTest() {
	paymentMethodTouches.Lock()
	paymentMethodTouches.byName = make(map[string]*paymentMethodTouch)
	paymentMethodTouches.Unlock()
	unknownPaymentMethods.Lock()
	unknownPaymentMethods.names = make(map[string]struct{})
	unknownPaymentMethods.Unlock()
}

func TestSaveSnapshotBumpsPaymentMethodPopularityOnInsertOnly(t *testing.T) {
	resetPaymentMethodTouchesForTest()
	t.Cleanup(resetPaymentMethodTouchesForTest)

	db := testdb.Open(t)
	ctx := context.Background()
	btcMin := 0.01
	btcMax := 0.02
	priceType := priceTypeFixed()

	o := offer{
		OfferID:        "pm-pop-1",
		FiatCurrency:   "EUR",
		Price:          61000,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		PaymentMethods: []string{"SEPA"},
		PriceType:      priceType,
	}
	if live := saveSnapshot(db, "bisq", "EUR", "ask", 60000, []offer{o}); len(live) != 1 {
		t.Fatalf("first insert live len = %d, want 1", len(live))
	}
	flushUnknownPaymentMethods(ctx, db)
	flushPaymentMethodTouches(ctx, db)

	var popularity int
	var sources, currencies paymentmethods.TextArray
	if err := db.QueryRow(`SELECT popularity, sources, currencies FROM payment_methods WHERE name = ?`, "SEPA").
		Scan(&popularity, &sources, &currencies); err != nil {
		t.Fatalf("load SEPA after insert: %v", err)
	}
	if popularity != 1 {
		t.Fatalf("popularity after insert = %d, want 1", popularity)
	}
	if len(sources) != 1 || sources[0] != "bisq" {
		t.Fatalf("sources after insert = %v, want [bisq]", []string(sources))
	}
	if len(currencies) != 1 || currencies[0] != "EUR" {
		t.Fatalf("currencies after insert = %v, want [EUR]", []string(currencies))
	}

	// Re-seeing the same offer_id must not bump again.
	o.Price = 62000
	if live := saveSnapshot(db, "bisq", "EUR", "ask", 60000, []offer{o}); len(live) != 1 {
		t.Fatalf("update live len = %d, want 1", len(live))
	}
	flushUnknownPaymentMethods(ctx, db)
	flushPaymentMethodTouches(ctx, db)

	if err := db.QueryRow(`SELECT popularity, sources, currencies FROM payment_methods WHERE name = ?`, "SEPA").
		Scan(&popularity, &sources, &currencies); err != nil {
		t.Fatalf("load SEPA after update: %v", err)
	}
	if popularity != 1 {
		t.Fatalf("popularity after re-see = %d, want 1", popularity)
	}
	if len(sources) != 1 || sources[0] != "bisq" {
		t.Fatalf("sources after re-see = %v, want [bisq]", []string(sources))
	}
	if len(currencies) != 1 || currencies[0] != "EUR" {
		t.Fatalf("currencies after re-see = %v, want [EUR]", []string(currencies))
	}
}

func TestSaveSnapshotTouchesUnknownPaymentMethodAfterFlush(t *testing.T) {
	resetPaymentMethodTouchesForTest()
	t.Cleanup(resetPaymentMethodTouchesForTest)

	db := testdb.Open(t)
	ctx := context.Background()
	btcMin := 0.01
	btcMax := 0.02
	priceType := priceTypeFixed()
	unknownName := "Zzz Snapshot Pop Pay"

	if live := saveSnapshot(db, "robosats", "USD", "bid", 60000, []offer{{
		OfferID:        "pm-pop-unknown-1",
		FiatCurrency:   "USD",
		Price:          61000,
		BTCMin:         &btcMin,
		BTCMax:         &btcMax,
		PaymentMethods: []string{unknownName},
		PriceType:      priceType,
	}}); len(live) != 1 {
		t.Fatalf("live len = %d, want 1", len(live))
	}

	// Before unknown flush the catalog row does not exist; touches are deferred.
	var preCount int
	if err := db.QueryRow(`SELECT count(*) FROM payment_methods WHERE name = ?`, unknownName).Scan(&preCount); err != nil {
		t.Fatalf("count before flush: %v", err)
	}
	if preCount != 0 {
		t.Fatalf("unknown method present before flush = %d, want 0", preCount)
	}

	flushUnknownPaymentMethods(ctx, db)
	flushPaymentMethodTouches(ctx, db)

	var popularity int
	var sources, currencies paymentmethods.TextArray
	if err := db.QueryRow(`SELECT popularity, sources, currencies FROM payment_methods WHERE name = ?`, unknownName).
		Scan(&popularity, &sources, &currencies); err != nil {
		t.Fatalf("load unknown method after flush: %v", err)
	}
	if popularity != 1 {
		t.Fatalf("unknown popularity = %d, want 1", popularity)
	}
	if len(sources) != 1 || sources[0] != "robosats" {
		t.Fatalf("unknown sources = %v, want [robosats]", []string(sources))
	}
	if len(currencies) != 1 || currencies[0] != "USD" {
		t.Fatalf("unknown currencies = %v, want [USD]", []string(currencies))
	}
}
