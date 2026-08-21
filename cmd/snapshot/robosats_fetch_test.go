package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
		"ask",
		"EUR",
		60000,
		client,
		[]string{"http://failed.test", "http://unknown.test", "http://success.test"},
	)
	if result == nil {
		t.Fatal("result = nil, want fallback coordinator result")
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "42" {
		t.Fatalf("offers = %#v, want order 42", result.Offers)
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
	result := fetchRobosatsCurrencyFrom("bid", "GBP", 50000, client, []string{"http://empty.test"})
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
		"ask",
		"EUR",
		60000,
		client,
		[]string{"http://empty.test", "http://book.test"},
	)
	if result == nil || result.status() != fetchStatusComplete {
		t.Fatalf("result = %#v, want complete union", result)
	}
	if len(result.Offers) != 1 || result.Offers[0].OfferID != "7" {
		t.Fatalf("offers = %#v, want order 7 from second coordinator", result.Offers)
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
		"bid",
		"EUR",
		60000,
		client,
		[]string{"http://empty.test", "http://down.test"},
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
			"temple": {"federated": true, "mainnet": {"onion": "http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}},
			"bazaar": {"federated": true, "mainnet": {"onion": "http://librebazovfmmkyi2jekraxsuso3mh622avuuzqpejixdl5dhuhb4tid.onion"}},
			"duplicate": {"federated": true, "mainnet": {"onion": "http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}},
			"not-federated": {"federated": false, "mainnet": {"onion": "http://alice7bqexhtnkiqhtgkuwgtzzfkishw23ac4sfwpznrwlmnipxlomyd.onion"}},
			"invalid": {"federated": true, "mainnet": {"onion": "https://example.com"}}
		}`)
	}))
	defer server.Close()

	onions, err := fetchRobosatsFederation(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchRobosatsFederation: %v", err)
	}
	want := "http://librebazovfmmkyi2jekraxsuso3mh622avuuzqpejixdl5dhuhb4tid.onion,http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"
	if got := strings.Join(onions, ","); got != want {
		t.Fatalf("onions = %q, want %q", got, want)
	}
}

func TestLoadRobosatsCoordinatorsFallsBackOnRegistryFailure(t *testing.T) {
	fallback := []string{"http://fallbackaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion"}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("registry unavailable")
	})}

	got := loadRobosatsCoordinators(context.Background(), client, "https://registry.test/federation.json", filepath.Join(t.TempDir(), "federation.json"), time.Hour, fallback)
	if strings.Join(got, ",") != strings.Join(fallback, ",") {
		t.Fatalf("coordinators = %v, want fallback %v", got, fallback)
	}
	got[0] = "changed"
	if fallback[0] == "changed" {
		t.Fatal("fallback slice was not copied")
	}
}

func TestLoadRobosatsCoordinatorsUsesFreshCacheWithoutRequest(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "federation.json")
	cached := []string{"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}
	if err := writeRobosatsFederationCache(cachePath, robosatsFederationCache{FetchedAt: time.Now().UTC(), Onions: cached}); err != nil {
		t.Fatalf("writeRobosatsFederationCache: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("fresh cache should not make a registry request")
		return nil, nil
	})}

	got := loadRobosatsCoordinators(context.Background(), client, "https://registry.test/federation.json", cachePath, time.Hour, nil)
	if strings.Join(got, ",") != strings.Join(cached, ",") {
		t.Fatalf("coordinators = %v, want cache %v", got, cached)
	}
}

func TestLoadRobosatsCoordinatorsRevalidatesStaleCacheWithETag(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "federation.json")
	cached := []string{"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion"}
	if err := writeRobosatsFederationCache(cachePath, robosatsFederationCache{FetchedAt: time.Now().UTC().Add(-2 * time.Hour), ETag: `"old-etag"`, Onions: cached}); err != nil {
		t.Fatalf("writeRobosatsFederationCache: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("If-None-Match"); got != `"old-etag"` {
			t.Fatalf("If-None-Match = %q, want old ETag", got)
		}
		return jsonResponse(http.StatusNotModified, ""), nil
	})}

	got := loadRobosatsCoordinators(context.Background(), client, "https://registry.test/federation.json", cachePath, time.Hour, nil)
	if strings.Join(got, ",") != strings.Join(cached, ",") {
		t.Fatalf("coordinators = %v, want revalidated cache %v", got, cached)
	}
	refreshed, err := readRobosatsFederationCache(cachePath)
	if err != nil || refreshed.FetchedAt.Before(time.Now().UTC().Add(-time.Minute)) {
		t.Fatalf("cache was not refreshed: cache=%+v err=%v", refreshed, err)
	}
}

func TestFetchRobosatsFederationRejectsInvalidRegistryURL(t *testing.T) {
	if _, err := fetchRobosatsFederation(context.Background(), http.DefaultClient, "http://registry.test/federation.json"); err == nil {
		t.Fatal("expected HTTP registry URL to be rejected")
	}
}
