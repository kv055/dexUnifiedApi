package main

import (
	"net/http/httptest"
	"testing"
)

func TestSameOriginWS(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin (non-browser client) allowed", "dash.example.com", "", true},
		{"matching origin allowed", "dash.example.com", "https://dash.example.com", true},
		{"matching origin with port", "dash.example.com:5443", "https://dash.example.com:5443", true},
		{"cross origin rejected", "dash.example.com", "https://evil.example.com", false},
		{"unparseable origin rejected", "dash.example.com", "://not a url", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws/live_offers", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := sameOriginWS(req); got != tc.want {
				t.Fatalf("sameOriginWS(host=%q, origin=%q) = %v, want %v", tc.host, tc.origin, got, tc.want)
			}
		})
	}
}
