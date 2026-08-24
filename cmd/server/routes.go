package main

import (
	"net/http"

	internalapi "dexunifiedapi/internal/api/gen/internalapi"
	publicapi "dexunifiedapi/internal/api/gen/public"
)

// registerRoutes defines the public and service-to-service HTTP contract.
// Dependency wiring, middleware, and lifecycle remain in main.
func registerRoutes(mux *http.ServeMux) {
	legacyMux := http.NewServeMux()
	registerLegacyRoutes(legacyMux)

	publicapi.RegisterLegacyStrictHandler(legacyMux, mux)
	internalapi.RegisterLegacyStrictHandler(legacyMux, mux)

	// Streaming endpoints intentionally remain outside generated response handling.
	mux.HandleFunc("GET /ws/live_offers", handleLiveOffersWS)
	mux.Handle("GET /internal/ws/live_offers", internalSecretGate(http.HandlerFunc(handleLiveOffersWS)))
}

// registerLegacyRoutes is the wire-compatible response implementation behind
// the generated strict request adapters. New operations should be implemented
// directly on the generated StrictServerInterface instead of being added here.
func registerLegacyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/live_offers", handleLiveOffersREST)
	mux.HandleFunc("GET /api/live_offers/{id}/lifecycle", handleLiveOfferLifecycleREST)
	mux.HandleFunc("GET /api/payment_notifications", handlePaymentNotifications)
	mux.HandleFunc("GET /ws/live_offers", handleLiveOffersWS)
	mux.HandleFunc("GET /api/trade_cost_config", handleTradeCostConfig)
	mux.HandleFunc("GET /api/cex_permissions", handleCEXPermissions)
	mux.HandleFunc("PUT /api/cex_permissions/{provider}", handleCEXPermissions)
	mux.HandleFunc("POST /api/accounts/health_check", handleAccountHealthCheck)
	mux.HandleFunc("GET /health", handleHealth)

	mux.HandleFunc("POST /internal/live_offers", handleInternalLiveOffers)
	mux.HandleFunc("POST /internal/payment_notifications", handleInternalPaymentNotifications)
	mux.Handle("GET /internal/cex_permissions", internalSecretGate(http.HandlerFunc(handleCEXPermissions)))
	mux.Handle("GET /internal/ws/live_offers", internalSecretGate(http.HandlerFunc(handleLiveOffersWS)))
}
