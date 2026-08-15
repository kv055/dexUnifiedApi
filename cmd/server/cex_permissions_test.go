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
