package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dexunifiedapi/internal/sellerrating"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFetchRobosatsFallsBackAcrossCoordinatorFailures(t *testing.T) {
	var failedHits, unknownHits, successHits atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "failed.test":
			failedHits.Add(1)
			return jsonResponse(http.StatusBadGateway, `{"detail":"upstream unavailable"}`), nil
		case "unknown.test":
			unknownHits.Add(1)
			return jsonResponse(http.StatusOK, `{"detail":"temporary coordinator error"}`), nil
		default:
			successHits.Add(1)
			return jsonResponse(http.StatusOK, `[{
			"id": 42,
			"price": 60000,
			"premium": 0,
			"amount": 600,
			"min_amount": 0,
			"max_amount": 0,
			"payment_method": "SEPA",
			"bond_size": 3
		}]`), nil
		}
	})}

	result := fetchRobosatsCurrencyFrom(
		context.Background(),
		"ask",
		"EUR",
		60000,
		client,
		[]robosatsCoordinator{{Alias: "temple", Onion: "http://failed.test"}, {Alias: "lake", Onion: "http://unknown.test"}, {Alias: "bazaar", Onion: "http://success.test"}},
	)
	if result == nil {
		t.Fatal("result = nil, want fallback coordinator result")
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "bazaar:42" {
		t.Fatalf("offers = %#v, want namespaced order bazaar:42", result.Offers)
	}
	if result.Offers[0].VenueNodeName != "bazaar" {
		t.Fatalf("VenueNodeName = %q, want bazaar", result.Offers[0].VenueNodeName)
	}
	if result.status() != fetchStatusIncomplete {
		t.Fatalf("status = %q, want incomplete after coordinator failures", result.status())
	}
	if failedHits.Load() != 1 || unknownHits.Load() != 1 || successHits.Load() != 1 {
		t.Fatalf("hits failed=%d unknown=%d success=%d, want 1 each", failedHits.Load(), unknownHits.Load(), successHits.Load())
	}
}

func TestFetchRobosatsNotFoundObjectIsEmptyBook(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"not_found":"No orders found"}`), nil
	})}
	result := fetchRobosatsCurrencyFrom(context.Background(), "bid", "GBP", 50000, client, []robosatsCoordinator{{Onion: "http://empty.test"}})
	if result == nil {
		t.Fatal("result = nil, want successful empty book")
	}
	if len(result.Offers) != 0 {
		t.Fatalf("offers = %d, want 0", len(result.Offers))
	}
	if result.status() != fetchStatusComplete {
		t.Fatalf("status = %q, want complete empty book", result.status())
	}
}

func TestFetchRobosatsUnionsCoordinatorsAndKeepsGoingAfterNotFound(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "empty.test":
			return jsonResponse(http.StatusOK, `{"not_found":"No orders found"}`), nil
		case "book.test":
			return jsonResponse(http.StatusOK, `[{
			"id": 7,
			"price": 61000,
			"premium": 0,
			"amount": 400,
			"min_amount": 0,
			"max_amount": 0,
			"payment_method": "SEPA",
			"bond_size": 3
		}]`), nil
		default:
			t.Fatalf("unexpected host: %s", req.URL.Host)
			return nil, errors.New("unexpected host")
		}
	})}

	result := fetchRobosatsCurrencyFrom(
		context.Background(),
		"ask",
		"EUR",
		60000,
		client,
		[]robosatsCoordinator{{Alias: "temple", Onion: "http://empty.test"}, {Alias: "lake", Onion: "http://book.test"}},
	)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete union", result)
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "lake:7" {
		t.Fatalf("offers = %#v, want namespaced order lake:7 from second coordinator", result.Offers)
	}
}

func TestFetchRobosatsNotFoundThenFailureIsIncomplete(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "empty.test":
			return jsonResponse(http.StatusOK, `{"not_found":"No orders found"}`), nil
		default:
			return jsonResponse(http.StatusBadGateway, `{"detail":"down"}`), nil
		}
	})}

	result := fetchRobosatsCurrencyFrom(
		context.Background(),
		"bid",
		"EUR",
		60000,
		client,
		[]robosatsCoordinator{{Onion: "http://empty.test"}, {Onion: "http://down.test"}},
	)
	if result == nil || result.status() != fetchStatusIncomplete {
		t.Fatalf("result = %#v, want incomplete empty book", result)
	}
	if len(result.Offers) != 0 {
		t.Fatalf("offers = %d, want 0", len(result.Offers))
	}
}

func TestFetchRobosatsFederationUsesOnlyValidFederatedMainnetOnions(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
			"temple": {"federated": true, "shortAlias": "temple", "mainnet": {"onion": "http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}},
			"bazaar": {"federated": true, "mainnet": {"onion": "http://librebazovfmmkyi2jekraxsuso3mh622avuuzqpejixdl5dhuhb4tid.onion"}},
			"duplicate": {"federated": true, "mainnet": {"onion": "http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}},
			"not-federated": {"federated": false, "mainnet": {"onion": "http://alice7bqexhtnkiqhtgkuwgtzzfkishw23ac4sfwpznrwlmnipxlomyd.onion"}},
			"invalid": {"federated": true, "mainnet": {"onion": "https://example.com"}}
		}`)
	}))
	defer server.Close()

	coordinators, err := fetchRobosatsFederation(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchRobosatsFederation: %v", err)
	}
	// Sorted by onion: bazaar's onion sorts before temple's. The duplicate
	// onion collapses to one entry; bazaar takes its alias from the registry
	// key because it carries no shortAlias.
	if len(coordinators) != 2 {
		t.Fatalf("coordinators = %#v, want 2 after onion dedup", coordinators)
	}
	if coordinators[0] != (robosatsCoordinator{Alias: "bazaar", Onion: "http://librebazovfmmkyi2jekraxsuso3mh622avuuzqpejixdl5dhuhb4tid.onion"}) {
		t.Fatalf("coordinators[0] = %#v, want bazaar", coordinators[0])
	}
	templeOnion := "http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"
	if coordinators[1].Onion != templeOnion {
		t.Fatalf("coordinators[1] = %#v, want temple onion %q", coordinators[1], templeOnion)
	}
	// Duplicate-onion alias selection must be stable, not map-order luck: the
	// winner may be either claimant, but refetches must agree.
	if coordinators[1].Alias != "duplicate" && coordinators[1].Alias != "temple" {
		t.Fatalf("coordinators[1].Alias = %q, want duplicate or temple", coordinators[1].Alias)
	}
	again, err := fetchRobosatsFederation(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchRobosatsFederation: %v", err)
	}
	for i := range coordinators {
		if again[i] != coordinators[i] {
			t.Fatalf("refetch coordinators[%d] = %#v, want stable %#v", i, again[i], coordinators[i])
		}
	}
}

func TestLoadRobosatsCoordinatorsFallsBackOnRegistryFailure(t *testing.T) {
	fallback := []robosatsCoordinator{{Onion: "http://fallbackaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion"}}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("registry unavailable")
	})}

	got := loadRobosatsCoordinators(context.Background(), client, "https://registry.test/federation.json", filepath.Join(t.TempDir(), "federation.json"), time.Hour, fallback)
	if len(got) != 1 || got[0] != fallback[0] {
		t.Fatalf("coordinators = %v, want fallback %v", got, fallback)
	}
	// Alias-less fallback coordinators keep bare numeric offer ids.
	if got[0].Alias != "" {
		t.Fatalf("fallback alias = %q, want empty", got[0].Alias)
	}
	got[0].Onion = "changed"
	if fallback[0].Onion == "changed" {
		t.Fatal("fallback slice was not copied")
	}
}

func TestLoadRobosatsCoordinatorsUsesFreshCacheWithoutRequest(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "federation.json")
	cached := []robosatsCoordinator{{Alias: "temple", Onion: "http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}}
	if err := writeRobosatsFederationCache(cachePath, robosatsFederationCache{FetchedAt: time.Now().UTC(), Coordinators: cached}); err != nil {
		t.Fatalf("writeRobosatsFederationCache: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("fresh cache should not make a registry request")
		return nil, nil
	})}

	got := loadRobosatsCoordinators(context.Background(), client, "https://registry.test/federation.json", cachePath, time.Hour, nil)
	if len(got) != 1 || got[0] != cached[0] {
		t.Fatalf("coordinators = %v, want cache %v", got, cached)
	}
}

func TestLoadRobosatsCoordinatorsRevalidatesStaleCacheWithETag(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "federation.json")
	cached := []robosatsCoordinator{{Alias: "temple", Onion: "http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}}
	if err := writeRobosatsFederationCache(cachePath, robosatsFederationCache{FetchedAt: time.Now().UTC().Add(-2 * time.Hour), ETag: `"old-etag"`, Coordinators: cached}); err != nil {
		t.Fatalf("writeRobosatsFederationCache: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("If-None-Match"); got != `"old-etag"` {
			t.Fatalf("If-None-Match = %q, want old ETag", got)
		}
		return jsonResponse(http.StatusNotModified, ""), nil
	})}

	got := loadRobosatsCoordinators(context.Background(), client, "https://registry.test/federation.json", cachePath, time.Hour, nil)
	if len(got) != 1 || got[0] != cached[0] {
		t.Fatalf("coordinators = %v, want revalidated cache %v", got, cached)
	}
	refreshed, err := readRobosatsFederationCache(cachePath)
	if err != nil || refreshed.FetchedAt.Before(time.Now().UTC().Add(-time.Minute)) {
		t.Fatalf("cache was not refreshed: cache=%+v err=%v", refreshed, err)
	}
}

func TestReadRobosatsFederationCacheRefetchesLegacyOnionList(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "federation.json")
	// Pre-alias cache format: bare onion strings under "onions".
	legacy := `{"fetched_at":"` + time.Now().UTC().Format(time.RFC3339) + `","onions":["http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"]}`
	if err := os.WriteFile(cachePath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy cache: %v", err)
	}
	if _, err := readRobosatsFederationCache(cachePath); err == nil {
		t.Fatal("legacy cache parsed without error, want refetch-triggering error")
	}
}

func TestFetchRobosatsKeepsSameOrderIDAcrossCoordinators(t *testing.T) {
	book := `[{
		"id": 7,
		"price": 61000,
		"premium": 0,
		"amount": 400,
		"min_amount": 0,
		"max_amount": 0,
		"payment_method": "SEPA",
		"bond_size": 3
	}]`
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, book), nil
	})}

	result := fetchRobosatsCurrencyFrom(
		context.Background(),
		"ask",
		"EUR",
		60000,
		client,
		[]robosatsCoordinator{{Alias: "temple", Onion: "http://temple.test"}, {Alias: "lake", Onion: "http://lake.test"}},
	)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete union", result)
	}
	// Same numeric order on two coordinators: per-coordinator sequences, so
	// both survive under namespaced ids instead of last-writer-wins.
	if len(result.Offers) != 2 {
		t.Fatalf("offers = %#v, want 2 namespaced orders", result.Offers)
	}
	ids := map[string]string{}
	for _, o := range result.Offers {
		ids[o.OfferID] = o.VenueNodeName
	}
	if ids["temple:7"] != "temple" || ids["lake:7"] != "lake" {
		t.Fatalf("ids = %#v, want temple:7 and lake:7 with matching venue names", ids)
	}
}

func TestFetchRobosatsFederationRejectsInvalidRegistryURL(t *testing.T) {
	if _, err := fetchRobosatsFederation(context.Background(), http.DefaultClient, "http://registry.test/federation.json"); err == nil {
		t.Fatal("expected HTTP registry URL to be rejected")
	}
}

func TestParseRobosatsOffersLeavesRatingUnavailable(t *testing.T) {
	t.Parallel()
	raw := []json.RawMessage{[]byte(`{
		"id": 42,
		"price": 60000,
		"premium": 0,
		"amount": 600,
		"min_amount": 0,
		"max_amount": 0,
		"payment_method": "SEPA",
		"bond_size": 3
	}`)}
	offers := parseRobosatsOffers(raw, "EUR", 60000, "temple")
	if len(offers) != 1 {
		t.Fatalf("len(offers) = %d, want 1", len(offers))
	}
	if offers[0].OfferID != "temple:42" {
		t.Fatalf("OfferID = %q, want namespaced temple:42", offers[0].OfferID)
	}
	if offers[0].VenueNodeName != "temple" {
		t.Fatalf("VenueNodeName = %q, want temple", offers[0].VenueNodeName)
	}
	bare := parseRobosatsOffers(raw, "EUR", 60000, "")
	if len(bare) != 1 || bare[0].OfferID != "42" || bare[0].VenueNodeName != "" {
		t.Fatalf("bare offers = %#v, want id 42 with no venue name", bare)
	}
	got := offers[0]
	if got.SellerRatingScore != nil {
		t.Fatalf("SellerRatingScore = %v, want nil (bond is not reputation)", *got.SellerRatingScore)
	}
	if got.SellerRatingConfidence == nil || *got.SellerRatingConfidence != string(sellerrating.ConfidenceUnavailable) {
		t.Fatalf("SellerRatingConfidence = %v, want unavailable", got.SellerRatingConfidence)
	}
	if got.SecurityDepositPct == nil || *got.SecurityDepositPct != 0.03 {
		t.Fatalf("SecurityDepositPct = %v, want 0.03 from bond_size 3", got.SecurityDepositPct)
	}
	if got.CounterpartyStakePct == nil || *got.CounterpartyStakePct != 0.03 {
		t.Fatalf("CounterpartyStakePct = %v, want 0.03 from bond_size 3", got.CounterpartyStakePct)
	}
	if got.CounterpartyStakeBTC == nil || *got.CounterpartyStakeBTC != *got.SecurityDepositBTC {
		t.Fatalf("CounterpartyStakeBTC = %v, want same as SecurityDepositBTC %v", got.CounterpartyStakeBTC, got.SecurityDepositBTC)
	}
}
