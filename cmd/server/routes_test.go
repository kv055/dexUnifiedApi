package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterRoutes(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)

	tests := []struct {
		method  string
		path    string
		pattern string
	}{
		{http.MethodGet, "/api/live_offers", "GET /api/live_offers"},
		{http.MethodGet, "/api/live_offers/42/lifecycle", "GET /api/live_offers/{id}/lifecycle"},
		{http.MethodGet, "/api/payment_notifications", "GET /api/payment_notifications"},
		{http.MethodGet, "/ws/live_offers", "GET /ws/live_offers"},
		{http.MethodPost, "/api/accounts/health_check", "POST /api/accounts/health_check"},
		{http.MethodPost, "/internal/live_offers", "POST /internal/live_offers"},
		{http.MethodPost, "/internal/payment_notifications", "POST /internal/payment_notifications"},
		{http.MethodGet, "/internal/cex_permissions", "GET /internal/cex_permissions"},
		{http.MethodPut, "/api/cex_permissions/kraken", "PUT /api/cex_permissions/{provider}"},
		{http.MethodGet, "/internal/ws/live_offers", "GET /internal/ws/live_offers"},
		{http.MethodGet, "/health", "GET /health"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			_, got := mux.Handler(req)
			if got != tt.pattern {
				t.Fatalf("matched pattern %q, want %q", got, tt.pattern)
			}
		})
	}
}

// TestOpenAPIRegisteredLiveOfferResponses exercises the established handlers
// through the generated strict router. Handler-only tests cannot catch a valid
// response whose top-level JSON shape disagrees with the OpenAPI contract.
func TestOpenAPIRegisteredLiveOfferResponses(t *testing.T) {
	previousHub := hub
	t.Cleanup(func() { hub = previousHub })
	hub = &LiveHub{
		lifecycle:   map[int64][]LifecyclePoint{},
		subscribers: map[chan []byte]liveSubscription{},
	}

	mux := http.NewServeMux()
	registerRoutes(mux)

	tests := []struct {
		name string
		path string
	}{
		{name: "live offers", path: "/api/live_offers?cex=kraken&fiat=EUR"},
		{name: "offer lifecycle", path: "/api/live_offers/42/lifecycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("registered route status = %d, want %d (body=%s)", response.Code, http.StatusOK, response.Body.String())
			}
			var payload []json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("registered route response is not a JSON array: %v (body=%s)", err, response.Body.String())
			}
		})
	}
}
