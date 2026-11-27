package main

import (
	"log/slog"
	"net/http"
	"time"

	"commons/mmcapabilities"
	"commons/tracing"
)

// The public monitor (the dexmonitor-public service behind the public nginx
// entry) shows live offers without a dashboard login. Opening those routes on
// the main listener would also open them on the operator dashboard, so they
// are served on a second listener instead (PUBLIC_LISTEN_ADDR, e.g. :5011).
// It has only the read-only routes below and no session at all. Nothing
// publishes the port; only the public nginx server block proxies to it.

// registerPublicRoutes lists every route of the public listener.
func registerPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/live_offers", handleLiveOffersREST)
	mux.HandleFunc("GET /api/live_offers/counts", handleLiveOffersCountsREST)
	mux.HandleFunc("GET /api/live_offers/{id}/lifecycle", handleLiveOfferLifecycleREST)
	mux.HandleFunc("GET /ws/live_offers", handleLiveOffersWS)
	mux.HandleFunc("GET /api/trade_cost_config", handleTradeCostConfig)
	mux.HandleFunc("GET /api/cex_permissions", handlePublicCEXPermissions)
	mux.HandleFunc("GET /health", handleHealth)
}

func newPublicHandler(metricsMiddleware func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	registerPublicRoutes(mux)
	var handler http.Handler = tracing.RouteNamer(limitRequestBody(mux))
	if metricsMiddleware != nil {
		handler = metricsMiddleware(handler)
	}
	return tracing.WrapHandler(handler, serviceName)
}

func startPublicListener(addr string, metricsMiddleware func(http.Handler) http.Handler) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           newPublicHandler(metricsMiddleware),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		slog.Info("starting public read-only listener", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("public listener error", "err", err)
		}
	}()
	return srv
}

// handlePublicCEXPermissions serves the routing view of the CEX permissions:
// which venues can price, buy and hedge, and the fees routing uses. Account
// ids, key counts, permission check times, blocker messages and fee refresh
// errors stay on the operator route.
func handlePublicCEXPermissions(w http.ResponseWriter, r *http.Request) {
	store := currentCapabilityStore()
	if store == nil {
		http.Error(w, "exchange permissions are unavailable", http.StatusServiceUnavailable)
		return
	}
	states, err := store.States(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "public CEX permissions unavailable", "err", err)
		http.Error(w, "exchange permissions are unavailable", http.StatusServiceUnavailable)
		return
	}
	maxAge := capabilityMaxAge
	if maxAge <= 0 {
		maxAge = mmcapabilities.DefaultMaxAge
	}
	exchanges := make([]cexCapabilityExchange, 0, len(states))
	for _, state := range states {
		exchanges = append(exchanges, publicCEXCapabilityExchange(buildCEXCapabilityExchange(r.Context(), state)))
	}
	writeJSON(w, cexCapabilitiesResponse{Exchanges: exchanges, MaxAgeSec: int64(maxAge / time.Second)})
}

func publicCEXCapabilityExchange(exchange cexCapabilityExchange) cexCapabilityExchange {
	exchange.AccountID = 0
	exchange.ActiveKeyCount = 0
	exchange.PermissionsCheckedAt = nil
	exchange.Spot = publicLegState(exchange.Spot)
	exchange.Hedge = publicLegState(exchange.Hedge)
	exchange.FeeSnapshot.LastError = ""
	exchange.FeeSnapshot.LastAttemptedAt = nil
	for _, component := range []*cexFeeComponentStatus{
		&exchange.FeeSnapshot.Spot, &exchange.FeeSnapshot.Withdrawal, &exchange.FeeSnapshot.Hedge,
	} {
		component.LastError = ""
		component.LastAttemptedAt = nil
	}
	return exchange
}

func publicLegState(leg mmcapabilities.LegState) mmcapabilities.LegState {
	leg.BlockingPermissions = []string{}
	leg.Blockers = []mmcapabilities.Blocker{}
	return leg
}
