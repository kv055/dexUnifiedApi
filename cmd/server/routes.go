package main

import "net/http"

// registerRoutes defines the public and service-to-service HTTP contract.
// Dependency wiring, middleware, and lifecycle remain in main.
func registerRoutes(mux *http.ServeMux) {
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
