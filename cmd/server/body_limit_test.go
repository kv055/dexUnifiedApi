package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitRequestBodyCapsOversizedPOST(t *testing.T) {
	var gotLen int
	var gotErr error
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		gotLen, gotErr = len(b), err
	})
	body := strings.NewReader(strings.Repeat("x", maxInternalBodyBytes+1024))
	req := httptest.NewRequest(http.MethodPost, "/api/execution-quotes", body)
	limitRequestBody(inner).ServeHTTP(httptest.NewRecorder(), req)
	if gotErr == nil {
		t.Fatalf("expected read error past the cap, got %d bytes without error", gotLen)
	}
	if gotLen != maxInternalBodyBytes {
		t.Fatalf("read %d bytes, want cap %d", gotLen, maxInternalBodyBytes)
	}
}

func TestLimitRequestBodyPassesSmallPOST(t *testing.T) {
	var got string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/bisq/requote", strings.NewReader(`{"ok":true}`))
	limitRequestBody(inner).ServeHTTP(httptest.NewRecorder(), req)
	if got != `{"ok":true}` {
		t.Fatalf("body = %q, want unchanged payload", got)
	}
}
