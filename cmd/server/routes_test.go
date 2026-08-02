package main

import (
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
