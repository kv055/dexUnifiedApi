package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCEXPermissionsAuthenticationPolicies(t *testing.T) {
	if !sessionProtected("/api/cex_permissions") {
		t.Fatal("browser CEX permissions route must require a dashboard session")
	}
	if sessionProtected("/internal/cex_permissions") {
		t.Fatal("internal CEX permissions route must use the internal-secret gate, not a browser session")
	}

	previousSecret := internalAPISecret
	previousInsecure := allowInsecureInternal
	t.Cleanup(func() {
		internalAPISecret = previousSecret
		allowInsecureInternal = previousInsecure
	})
	internalAPISecret = "test-secret"
	allowInsecureInternal = false

	called := false
	handler := internalSecretGate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/internal/cex_permissions", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || called {
		t.Fatalf("missing secret: status=%d called=%v", response.Code, called)
	}

	request = httptest.NewRequest(http.MethodGet, "/internal/cex_permissions", nil)
	request.Header.Set("X-Internal-Secret", "test-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("valid secret: status=%d called=%v", response.Code, called)
	}
}

func TestCategoryExclusionsIncludeLegacyValues(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet,
		"/api/live_offers?exclude_cexes=binance&exclude_spot_cexes=kraken,binance&exclude_hedge_cexes=coinbase", nil)
	spot := queryExcludedSpotCEXes(request)
	hedge := queryExcludedHedgeCEXes(request)
	if len(spot) != 2 || spot[0] != "binance" || spot[1] != "kraken" {
		t.Fatalf("spot exclusions = %v", spot)
	}
	if len(hedge) != 2 || hedge[0] != "binance" || hedge[1] != "coinbase" {
		t.Fatalf("hedge exclusions = %v", hedge)
	}
}
