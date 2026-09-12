package main

import (
	"context"
	"dexunifiedapi/internal/testdb"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

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
	if math.Abs(floating.Price-60000/0.95) > 1e-8 {
		t.Fatalf("floating price = %v, want 63157.8947368421", floating.Price)
	}
	if floating.PriceType == nil || *floating.PriceType != "floating" {
		t.Fatalf("price type = %v, want floating", floating.PriceType)
	}
	if floating.FiatMin == nil || *floating.FiatMin != 100 || floating.FiatMax == nil || *floating.FiatMax != 200 {
		t.Fatalf("fiat range = %v-%v", floating.FiatMin, floating.FiatMax)
	}
}

func TestMapNIP69EventRangeIgnoresTakeQuoteAmt(t *testing.T) {
	// Offer 3984: Mostro left amt set to the sat quote for one slice of a
	// still-pending 50–300 EUR range. midFiat/amt produced ~232k EUR/BTC
	// (~249% fake premium). These inputs alone do not reproduce EUR 729.
	const spot = 66451
	got, ok := mapNIP69Event(nostrreq.Event{
		Kind: nip69OrderKind,
		Tags: [][]string{
			{"d", "d9d56531-382e-4bda-986d-f903c90d0298"},
			{"k", "buy"},
			{"f", "EUR"},
			{"s", "pending"},
			{"amt", "75497"},
			{"fa", "50", "300"},
			{"pm", "Bizum", "Efectivo Móvil", "SEPA", "con envio del código a mi número"},
			{"premium", "0"},
			{"y", "mostro"},
			{"z", "order"},
			{"network", "mainnet"},
			{"layer", "lightning"},
		},
	}, "mostro", "buy", "EUR", spot)
	if !ok {
		t.Fatal("range take-quote offer rejected")
	}
	if got.Price != float64(spot) {
		t.Fatalf("price = %v, want spot %d (ignore amt on range orders)", got.Price, spot)
	}
	if got.PriceType == nil || *got.PriceType != "floating" {
		t.Fatalf("price type = %v, want floating", got.PriceType)
	}
	if got.FiatMin == nil || *got.FiatMin != 50 || got.FiatMax == nil || *got.FiatMax != 300 {
		t.Fatalf("fiat range = %v-%v, want 50-300", got.FiatMin, got.FiatMax)
	}
	wantBTCMax := round8(300 / float64(spot))
	if got.BTCMax == nil || *got.BTCMax != wantBTCMax {
		t.Fatalf("btc_max = %v, want %v from fiat/spot", got.BTCMax, wantBTCMax)
	}
}

func nip69PricingEvent(kind, amt, premium string, fiat ...string) nostrreq.Event {
	tags := [][]string{{"d", "pricing-order"}, {"k", kind}, {"f", "EUR"}, {"s", "pending"}, {"amt", amt}, {"y", "mostro"}, {"pm", "SEPA"}}
	tags = append(tags, append([]string{"fa"}, fiat...))
	if premium != "" {
		tags = append(tags, []string{"premium", premium})
	}
	return nostrreq.Event{Kind: nip69OrderKind, Tags: tags}
}

func TestMapNIP69FloatingQuoteMath(t *testing.T) {
	for _, kind := range []string{"buy", "sell"} {
		for _, amt := range []string{"0", "75497"} {
			for _, tc := range []struct {
				premium         string
				price, min, max float64
			}{
				{"", 60000, 0.001, 0.005},
				{"0", 60000, 0.001, 0.005},
				{"20", 75000, 0.0008, 0.004},
				{"-20", 50000, 0.0012, 0.006},
			} {
				t.Run(kind+"/"+amt+"/"+tc.premium, func(t *testing.T) {
					got, ok := mapNIP69Event(nip69PricingEvent(kind, amt, tc.premium, "60", "300"), "mostro", kind, "EUR", 60000)
					if !ok {
						t.Fatal("valid range rejected")
					}
					if got.Price != tc.price || *got.BTCMin != tc.min || *got.BTCMax != tc.max || *got.PriceType != "floating" {
						t.Fatalf("quote = %+v, BTC %v..%v, want price %v BTC %v..%v", got, *got.BTCMin, *got.BTCMax, tc.price, tc.min, tc.max)
					}
				})
			}
		}
	}
	// A single amount with amt=0 uses the same market quote.
	got, ok := mapNIP69Event(nip69PricingEvent("sell", "0", "20", "300"), "mostro", "sell", "EUR", 60000)
	if !ok || got.Price != 75000 || *got.BTCMax != 0.004 {
		t.Fatalf("single floating quote = %+v, accepted=%v", got, ok)
	}
}

func TestMapNIP69FixedQuoteFilter(t *testing.T) {
	for _, tc := range []struct {
		name, fiat string
		spot       int
		accepted   bool
	}{
		{"at spot", "100", 100000, true},
		{"lower boundary", "50", 100000, true},
		{"upper boundary", "150", 100000, true},
		{"below boundary", "49", 100000, false},
		{"above boundary", "151", 100000, false},
		{"missing spot", "100", 0, false},
		{"negative spot", "100", -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := mapNIP69Event(nip69PricingEvent("sell", "100000", "3.2", tc.fiat), "mostro", "sell", "EUR", tc.spot)
			if ok != tc.accepted {
				t.Fatalf("accepted=%v, want %v", ok, tc.accepted)
			}
			if ok && (*got.PriceType != "fixed" || *got.BTCMax != 0.001) {
				t.Fatalf("fixed terms rewritten: %+v", got)
			}
		})
	}
	if _, ok := mapNIP69Event(nip69PricingEvent("sell", "20000", "3.2", "300"), "mostro", "sell", "EUR", 66451); ok {
		t.Fatal("outlier must be rejected, not rewritten as floating")
	}
}

func TestMapNIP69RejectsInvalidPricingInputs(t *testing.T) {
	for _, tc := range []struct {
		name, amt, premium string
		fiat               []string
	}{
		{"missing amt", "", "0", []string{"50"}},
		{"malformed amt", "garbage", "0", []string{"50", "300"}},
		{"negative amt", "-1", "0", []string{"50"}},
		{"fractional sats", "1.5", "0", []string{"50"}},
		{"nan amt", "NaN", "0", []string{"50"}},
		{"infinite amt", "+Inf", "0", []string{"50"}},
		{"malformed premium", "0", "oops", []string{"50"}},
		{"blank premium", "0", " ", []string{"50"}},
		{"nan premium", "0", "NaN", []string{"50"}},
		{"infinite premium", "0", "-Inf", []string{"50"}},
		{"zero sats premium", "0", "100", []string{"50"}},
		{"negative sats premium", "0", "101", []string{"50"}},
		{"rounded zero BTC", "0", "99.99999999999999", []string{"50"}},
		{"missing fiat", "0", "0", nil},
		{"zero fiat", "0", "0", []string{"0", "300"}},
		{"bad endpoint", "0", "0", []string{"50", "oops"}},
		{"infinite fiat", "0", "0", []string{"Inf"}},
		{"nan fiat", "0", "0", []string{"NaN"}},
		{"overflow fiat", "0", "0", []string{"99999999999999999999999"}},
		{"fractional range", "75497", "0", []string{"50.1", "50.9"}},
		{"extra endpoint", "0", "0", []string{"50", "100", "300"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := mapNIP69Event(nip69PricingEvent("buy", tc.amt, tc.premium, tc.fiat...), "mostro", "buy", "EUR", 66451); ok {
				t.Fatal("invalid pricing accepted")
			}
		})
	}
}

func TestFetchNIP69MissingSpotFailsBeforeQuery(t *testing.T) {
	for _, spot := range []int{0, -1} {
		got := fetchNIP69CurrencyFrom(context.Background(), "mostro", nil, "bid", "EUR", spot,
			func(context.Context, []string, map[string]any) (nostrreq.QueryResult, error) {
				t.Fatal("queried without spot")
				return nostrreq.QueryResult{}, nil
			})
		if got != nil {
			t.Fatalf("missing spot returned authoritative book: %+v", got)
		}
	}
}

func TestFetchNIP69RejectedQuotePreservesBookCompleteness(t *testing.T) {
	for _, complete := range []bool{true, false} {
		for _, includeValid := range []bool{true, false} {
			t.Run(fmt.Sprintf("complete=%v/valid=%v", complete, includeValid), func(t *testing.T) {
				resetNIP69Cache()
				t.Cleanup(resetNIP69Cache)
				events := []nostrreq.Event{nip69PricingEvent("sell", "20000", "3.2", "300")}
				if includeValid {
					events = append(events, nip69PricingEvent("sell", "0", "0", "50", "300"))
				}
				got := fetchNIP69CurrencyFrom(context.Background(), "mostro", nil, "ask", "EUR", 66451,
					func(context.Context, []string, map[string]any) (nostrreq.QueryResult, error) {
						return nostrreq.QueryResult{Events: events, Complete: complete}, nil
					})
				if !complete && !includeValid {
					if got != nil {
						t.Fatal("incomplete empty book should fail")
					}
					return
				}
				wantStatus := fetchStatusIncomplete
				if complete {
					wantStatus = fetchStatusComplete
				}
				wantOffers := 0
				if includeValid {
					wantOffers = 1
				}
				if got == nil || got.status() != wantStatus || len(got.Offers) != wantOffers {
					t.Fatalf("result = %+v", got)
				}
			})
		}
	}
}

func TestNIP69SnapshotUpdatesPriceAndSizeTogether(t *testing.T) {
	db := testdb.Open(t)
	resetPaymentMethodTouchesForTest()
	t.Cleanup(resetPaymentMethodTouchesForTest)
	var id int64
	for _, tc := range []struct {
		spot          int
		amt           string
		price, btcMax float64
	}{
		{60000, "0", 75000, 0.004},
		{64000, "75497", 80000, 0.00375},
	} {
		mapped, ok := mapNIP69Event(nip69PricingEvent("buy", tc.amt, "20", "60", "300"), "mostro", "buy", "EUR", tc.spot)
		if !ok {
			t.Fatal("mapping failed")
		}
		live, saved := saveSnapshotPersisted(db, "mostro", "EUR", "bid", float64(tc.spot), []offer{mapped})
		if !saved || len(live) != 1 {
			t.Fatal("snapshot failed")
		}
		if id != 0 && live[0].ID != id {
			t.Fatal("take quote changed offer identity")
		}
		id = live[0].ID
		var price, btcMax float64
		if err := db.QueryRow("SELECT price, btc_max FROM offers WHERE id = ?", id).Scan(&price, &btcMax); err != nil {
			t.Fatal(err)
		}
		if price != tc.price || btcMax != tc.btcMax || live[0].Price != price || *live[0].BTCMax != btcMax || live[0].ReferencePrice != float64(tc.spot) {
			t.Fatalf("inconsistent persisted/live quote: price=%v btc_max=%v payload=%+v", price, btcMax, live[0])
		}
		// At a 20% reduction in sats, effective premium is 25%; both snapshots
		// have EUR 240 spot notional and EUR 60 gross spread (before costs).
		gross := btcMax * (price - float64(tc.spot))
		if math.Abs(gross-60) > 1e-8 {
			t.Fatalf("gross spread=%v, want 60", gross)
		}
	}
	// A complete book rejecting this fixed quote closes its previous live row;
	// a missing reference fetch must leave reconciliation unconfirmed instead.
	start := time.Now().UTC()
	if _, err := db.Exec("UPDATE offers SET first_seen = ?, last_seen = ?, is_live = TRUE WHERE id = ?", start.Add(-2*time.Minute), start.Add(-time.Minute), id); err != nil {
		t.Fatal(err)
	}
	if err := reconcileLiveOfferStatus(db, start, nil, defaultLiveOfferStaleGrace); err != nil {
		t.Fatal(err)
	}
	if !offerIsLive(t, db, id) {
		t.Fatal("failed fetch closed prior quote within grace")
	}
	confirmed := map[liveMarket]struct{}{{exchange: "mostro", fiat: "EUR", direction: "bid"}: {}}
	if err := reconcileLiveOfferStatus(db, start, confirmed, defaultLiveOfferStaleGrace); err != nil {
		t.Fatal(err)
	}
	if offerIsLive(t, db, id) {
		t.Fatal("excluded quote remained live after complete fetch")
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

func TestMapNIP69Signals(t *testing.T) {
	t.Parallel()
	values := []string{`{"total_rating":4.5,"total_reviews":30,"last_rating":5,"min_rate":3,"max_rate":5,"days":400}`}
	score := mapNIP69Rating(values)
	if score.Value == nil || score.Confidence != sellerrating.ConfidenceFeedbackVerified {
		t.Fatalf("score = %#v, want a verified feedback score", score)
	}

	raw := mapNIP69Signals(values)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal signals: %v", err)
	}
	if got["source"] != "mostro" {
		t.Fatalf("source = %v, want mostro", got["source"])
	}
	if got["days"] != float64(400) {
		t.Fatalf("days = %v, want 400", got["days"])
	}
	if got["total_reviews"] != float64(30) {
		t.Fatalf("total_reviews = %v, want 30", got["total_reviews"])
	}
	if got["last_rating"] != float64(5) {
		t.Fatalf("last_rating = %v, want 5", got["last_rating"])
	}
	if got["min_rate"] != float64(3) {
		t.Fatalf("min_rate = %v, want 3", got["min_rate"])
	}
	if got["max_rate"] != float64(5) {
		t.Fatalf("max_rate = %v, want 5", got["max_rate"])
	}
}

func TestMapNIP69SignalsEmpty(t *testing.T) {
	t.Parallel()
	if got := mapNIP69Signals(nil); got != nil {
		t.Fatalf("empty rating tag signals = %s, want nil", got)
	}
	if got := mapNIP69Signals([]string{"not-json"}); got != nil {
		t.Fatalf("malformed rating tag signals = %s, want nil", got)
	}
}
