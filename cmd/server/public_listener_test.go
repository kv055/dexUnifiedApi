package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"commons/mmcapabilities"
)

func TestPublicListenerServesOnlyReadRoutes(t *testing.T) {
	mux := http.NewServeMux()
	registerPublicRoutes(mux)
	for _, path := range []string{
		"/api/live_offers", "/api/live_offers/counts", "/api/live_offers/7/lifecycle",
		"/ws/live_offers", "/api/trade_cost_config", "/api/cex_permissions", "/health",
	} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); !strings.HasPrefix(pattern, "GET ") {
			t.Errorf("GET %s is not served by the public listener (pattern %q)", path, pattern)
		}
	}

	handler := newPublicHandler(nil)
	serve := func(method, path string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
		return rec.Code
	}
	if code := serve(http.MethodGet, "/api/trade_cost_config"); code != http.StatusOK {
		t.Errorf("GET /api/trade_cost_config = %d, want 200 without a session", code)
	}

	// Operator, internal and write routes do not exist on the public listener.
	blocked := []struct{ method, path string }{
		{http.MethodPut, "/api/cex_permissions/kraken"},
		{http.MethodPut, "/api/cex_permissions"},
		{http.MethodPost, "/api/execution-quotes"},
		{http.MethodGet, "/api/payment_notifications"},
		{http.MethodPost, "/api/accounts/health_check"},
		{http.MethodPost, "/internal/live_offers"},
		{http.MethodPost, "/internal/execution-quotes"},
		{http.MethodGet, "/internal/cex_permissions"},
		{http.MethodGet, "/internal/ws/live_offers"},
		{http.MethodPost, "/internal/payment_notifications"},
		{http.MethodGet, "/metrics"},
		{http.MethodPost, "/api/live_offers"},
	}
	for _, tc := range blocked {
		if code := serve(tc.method, tc.path); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 404 or 405 on the public listener", tc.method, tc.path, code)
		}
	}
}

func TestPublicCEXCapabilityExchangeStripsOperatorDetail(t *testing.T) {
	checked := time.Now().UTC()
	blocker := mmcapabilities.Blocker{Code: mmcapabilities.BlockStale, Message: "API-key permissions have not been verified recently."}
	exchange := cexCapabilityExchange{
		ProviderState: mmcapabilities.ProviderState{
			AccountID:            42,
			Provider:             "Kraken",
			Exchange:             "kraken",
			CostSupported:        true,
			ExecutionSupported:   true,
			ActiveKeyCount:       2,
			PermissionsCheckedAt: &checked,
			Spot: mmcapabilities.LegState{Supported: true, Available: true, EffectiveEnabled: true,
				BlockingPermissions: []string{"withdraw"}, Blockers: []mmcapabilities.Blocker{blocker}},
			Hedge: mmcapabilities.LegState{Supported: true, Blockers: []mmcapabilities.Blocker{blocker}},
		},
	}
	exchange.FeeSnapshot.SpotAvailable = true
	exchange.FeeSnapshot.LastError = "kraken: EAPI:Invalid key"
	exchange.FeeSnapshot.LastAttemptedAt = &checked
	exchange.FeeSnapshot.Spot.LastError = "kraken: EAPI:Invalid key"
	exchange.FeeSnapshot.Withdrawal.LastAttemptedAt = &checked

	public := publicCEXCapabilityExchange(exchange)

	if !public.Spot.EffectiveEnabled || !public.FeeSnapshot.SpotAvailable || public.Exchange != "kraken" {
		t.Fatalf("routing view lost: %+v", public)
	}
	raw, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, secret := range []string{`"account_id"`, `"permissions_checked_at"`, "EAPI", "verified recently", `"withdraw"`, `"last_attempted_at"`} {
		if strings.Contains(body, secret) {
			t.Errorf("public CEX permissions still contain %s: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"active_key_count":0`) {
		t.Errorf("public CEX permissions must not report key counts: %s", body)
	}
	// The operator view keeps everything.
	if exchange.AccountID != 42 || len(exchange.Spot.Blockers) != 1 {
		t.Fatal("publicCEXCapabilityExchange modified the operator view")
	}
}
