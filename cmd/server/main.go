package main

import (
	"commons/exchangecredentials"
	"commons/fiat"
	"commons/logging"
	"commons/metrics"
	"commons/mmcapabilities"
	"commons/prices"
	"commons/session"
	"commons/tracing"
	"commons/tradecost"
	"context"
	"crypto/subtle"
	"dexunifiedapi/db"
	"dexunifiedapi/internal/accounthealth"
	"dexunifiedapi/internal/config"
	"dexunifiedapi/internal/dbmetrics"
	"dexunifiedapi/internal/lifecycle"
	"dexunifiedapi/internal/paymentnotifications"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// serviceName is the OpenTelemetry/logging identity for this binary.
const serviceName = "unified-api"

// sessionProtected reports whether a path is a browser-facing endpoint that
// requires a valid dashboard login session. Only the routes nginx exposes to the
// browser are gated; internal service-to-service endpoints
// (/api/accounts/health_check, /internal/*, /health, /metrics) carry no user
// cookie and must stay open. The payment_notifications READ endpoint is
// login-gated: it returns payment amounts/tx IDs and has no service consumer
// (the /internal/payment_notifications webhook ingest is separate, secret-gated).
func sessionProtected(path string) bool {
	return strings.HasPrefix(path, "/api/live_offers") ||
		strings.HasPrefix(path, "/api/cex_permissions") ||
		strings.HasPrefix(path, "/api/trade_cost_config") ||
		strings.HasPrefix(path, "/api/payment_notifications") ||
		strings.HasPrefix(path, "/ws/live_offers")
}

var (
	adminDatabaseURL          string
	databaseURL               string
	snapshotIntervalMin       int
	listenAddr                string
	paymentNotificationSecret string
	internalAPISecret         string
	// allowInsecureInternal permits the internal ingest endpoints to run without a
	// shared secret (local dev / tests). In prod it stays false, so an unset secret
	// fails CLOSED instead of leaving the endpoints open. Set via ALLOW_INSECURE_INTERNAL.
	allowInsecureInternal bool
	bisqGRPCHost          string
	bisqAPIPassword       string
	bisq2RESTHost         string
	bisq2APIPassword      string
	accountHealthInterval time.Duration
	capabilityMaxAge      time.Duration
	db                    *dbutil.DB
	capabilityStore       *mmcapabilities.Store
	priceManager          *prices.Manager
	lifecycleRepo         lifecycle.Repository
	paymentNotifications  *paymentnotifications.Service
)

var (
	cexPricesMu sync.RWMutex
	cexPrices   = map[string]float64{}
)

const (
	defaultCEX = "kraken"
)

var supportedCEXes = tradecost.SupportedCEXes()

var tradeCostEstimators map[string]*tradecost.TradeCostEstimator
var tradeRouteOptimizer *tradecost.TradeRouteOptimizer

func coreTablesReady() bool {
	var ready bool
	if err := db.QueryRow(`
		SELECT
			to_regclass('public.offers') IS NOT NULL
			AND to_regclass('public.snapshot_log') IS NOT NULL
	`).Scan(&ready); err != nil {
		return false
	}
	return ready
}

func getCexPrice(cex string) float64 {
	if priceManager != nil {
		return priceManager.Get(cex)
	}
	cexPricesMu.RLock()
	defer cexPricesMu.RUnlock()
	return cexPrices[cex]
}

func setCexPrice(cex string, p float64) {
	if priceManager != nil {
		priceManager.Set(cex, p)
		return
	}
	cexPricesMu.Lock()
	cexPrices[cex] = p
	cexPricesMu.Unlock()
	hub.broadcastForCex(cex)
}

// --- Init & Main ---

func init() {
	// Under tests we run without a configured secret; allow insecure so the internal
	// handler tests exercise the empty-secret path. In prod this is opt-in only.
	allowInsecureInternal = runningUnderGoTest() ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_INSECURE_INTERNAL")), "true")
	if err := config.LoadRequiredDotEnv(".env"); err != nil {
		if runningUnderGoTest() {
			listenAddr = ":5010"
			paymentNotificationSecret = strings.TrimSpace(os.Getenv("PAYMENT_NOTIFICATIONS_SHARED_SECRET"))
			internalAPISecret = strings.TrimSpace(os.Getenv("INTERNAL_API_SECRET"))
			bisqGRPCHost = "localhost:9998"
			accountHealthInterval = time.Minute
			return
		}
		panic(err)
	}
	config.RequireEnvSet(
		"POSTGRES_UNIFIED_API_PASSWORD",
		"POSTGRES_MONITOR_PASSWORD",
		"POSTGRES_MM_CONTROLLER_PASSWORD",
		"POSTGRES_FIAT_CONTROLLER_PASSWORD",
	)
	adminDatabaseURL = config.PostgresAdminURLFromEnv()
	databaseURL = config.PostgresURLFromEnv()
	interval := os.Getenv("SNAPSHOT_INTERVAL_MIN")
	if interval == "" {
		snapshotIntervalMin = 1
	} else {
		v, err := strconv.Atoi(interval)
		if err != nil {
			snapshotIntervalMin = 1
		} else {
			snapshotIntervalMin = v
		}
	}

	listenAddr = os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":5010"
	}
	paymentNotificationSecret = strings.TrimSpace(os.Getenv("PAYMENT_NOTIFICATIONS_SHARED_SECRET"))
	internalAPISecret = strings.TrimSpace(os.Getenv("INTERNAL_API_SECRET"))
	bisqGRPCHost = strings.TrimSpace(os.Getenv("BISQ_GRPC_HOST"))
	if bisqGRPCHost == "" {
		bisqGRPCHost = "localhost:9998"
	}
	bisqAPIPassword = os.Getenv("BISQ_API_PASSWORD")
	bisq2RESTHost = strings.TrimSpace(os.Getenv("BISQ2_REST_HOST"))
	bisq2APIPassword = os.Getenv("BISQ2_API_PASSWORD")
	accountHealthInterval = accounthealth.ParseInterval(os.Getenv("ACCOUNT_HEALTH_INTERVAL"))
	capabilityMaxAge = mmcapabilities.ParseMaxAge(os.Getenv("MM_CEX_CAPABILITY_MAX_AGE"))

}

func runningUnderGoTest() bool {
	return strings.HasSuffix(os.Args[0], ".test") || strings.HasSuffix(os.Args[0], ".test.exe")
}

func main() {
	logging.Init(serviceName)
	tpShutdown, err := tracing.Init(context.Background(), serviceName)
	if err != nil {
		slog.Warn("tracing init failed; continuing without traces", "err", err)
	}
	defer func() { _ = tpShutdown(context.Background()) }()

	adminDB, err := dbutil.Open(adminDatabaseURL)
	if err != nil {
		slog.Error("failed to open admin database", "err", err)
		os.Exit(1)
	}
	defer adminDB.Close()

	if err := adminDB.Ping(); err != nil {
		slog.Error("failed to ping admin database", "err", err)
		os.Exit(1)
	}

	if err := dbutil.EnsureAppRoles(adminDB, dbutil.RoleBootstrapConfig{
		DatabaseName:           config.EnvOrDefault("POSTGRES_DB", "dexarb"),
		UnifiedAPIPassword:     config.RequireEnv("POSTGRES_UNIFIED_API_PASSWORD"),
		MonitorPassword:        config.RequireEnv("POSTGRES_MONITOR_PASSWORD"),
		MMControllerPassword:   config.RequireEnv("POSTGRES_MM_CONTROLLER_PASSWORD"),
		FiatControllerPassword: config.RequireEnv("POSTGRES_FIAT_CONTROLLER_PASSWORD"),
		GrafanaROPassword:      config.EnvOrDefault("POSTGRES_GRAFANA_RO_PASSWORD", ""),
	}); err != nil {
		slog.Error("database admin setup error", "err", err)
		os.Exit(1)
	}

	db, err = dbutil.Open(databaseURL)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Shared by HTTP handlers, the snapshot-push ingest, account-health checks and the
	// dbmetrics poller. 4 was too small and caused contention; Postgres allows 100.
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)

	if err := db.Ping(); err != nil {
		slog.Error("failed to ping database", "err", err)
		os.Exit(1)
	}

	// One orchestrator serves every CEX: it picks the cheapest non-excluded CEX
	// internally via the cextrading engine. The map is kept (the shared instance
	// under each CEX key) only for CEX-name validation and optimizer adoption.
	credentialStore := exchangecredentials.NewStore(db.DB)
	orchestrator := tradecost.NewTradeCostEstimatorWithCredentials(credentialStore.Resolve)
	tradeCostEstimators = make(map[string]*tradecost.TradeCostEstimator, len(supportedCEXes))
	for _, c := range supportedCEXes {
		tradeCostEstimators[c] = orchestrator
		slog.Info("trade-cost CEX enabled",
			"cex", c,
			"taker_fallback_pct", tradecost.TakerFeeFallback(c)*100,
			"onchain_withdrawal_fallback_btc", tradecost.WithdrawalFallback(c))
	}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		slog.Error("schema init error", "err", err)
		os.Exit(1)
	}
	if err := dbutil.SeedPaymentMethods(db); err != nil {
		slog.Error("payment methods seed error", "err", err)
		os.Exit(1)
	}

	// Dashboard login session: unified-api only VALIDATES the shared dex_session
	// cookie (stored in the same Postgres `sessions` table dexMonitor creates);
	// dexMonitor owns creating/destroying sessions. Default Secure=true (prod is
	// HTTPS); set SESSION_COOKIE_SECURE=false for local HTTP dev.
	sessionCookieSecure := !strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "false")
	sessionMgr := session.NewManager(db.DB, session.Config{CookieSecure: sessionCookieSecure})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifecycleRepo = lifecycle.NewRepository(db)
	paymentNotifications = paymentnotifications.NewService(250)
	priceManager = prices.NewManager(supportedCEXes)
	priceManager.SetUpdateCallback(func(cex string) {
		hub.broadcastForCex(cex)
	})

	// Metrics registry — created early so price feeds, the live hub, account
	// health checks and the DB-backed business collector all report from start.
	metricsReg := metrics.NewServiceRegistry("unified-api")
	appMetrics = newServerMetrics(metricsReg.Registry)
	metricsReg.Registry.MustRegister(dbmetrics.NewCollector(db))
	priceManager.SetMetrics(prices.NewMetrics(metricsReg.Registry))

	if err := refreshLiveCapabilityPolicy(ctx); err != nil {
		slog.Error("initial MM CEX policy refresh failed; routing fail-closed", "err", err)
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := refreshLiveCapabilityPolicy(ctx); err != nil {
					slog.Error("periodic MM CEX policy refresh failed; routing fail-closed", "err", err)
				}
			}
		}
	}()

	recorder := accounthealth.NewDBRecorder(db)

	go accounthealth.RunPeriodicAccountHealthCheck(
		ctx,
		accountHealthInterval,
		recorder,
		accounthealth.BisqAccountProvider,
		appMetrics.instrumentCheck(accounthealth.BisqAccountProvider, func(ctx context.Context) error {
			return accounthealth.CheckBisqAccountHealth(ctx, accounthealth.NewBisqGRPCAccountChecker(bisqGRPCHost, bisqAPIPassword, 0))
		}),
	)

	go accounthealth.RunPeriodicAccountHealthCheck(
		ctx,
		accountHealthInterval,
		recorder,
		"Bisq2",
		appMetrics.instrumentCheck("Bisq2", func(ctx context.Context) error {
			if bisq2RESTHost == "" {
				return fmt.Errorf("bisq2 not configured (BISQ2_REST_HOST is unset)")
			}
			checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(checkCtx, http.MethodGet,
				"http://"+bisq2RESTHost+"/api/v1/settings/version", nil)
			if err != nil {
				return fmt.Errorf("bisq2 health: build request: %w", err)
			}
			if bisq2APIPassword != "" {
				req.Header.Set("Authorization", "Bearer "+bisq2APIPassword)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("bisq2 health: %w", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("bisq2 health: unexpected status %d", resp.StatusCode)
			}
			return nil
		}),
	)

	// Seed the cache before any REST or WebSocket traffic is served. PostgreSQL
	// remains authoritative; refresh failures later retain this complete book.
	startupRefreshCtx, cancelStartupRefresh := context.WithTimeout(ctx, 30*time.Second)
	if err := refreshLiveHub(startupRefreshCtx, nil); err != nil {
		slog.Error("initial live book hydration failed", "err", err)
	}
	cancelStartupRefresh()

	priceManager.Start(ctx)

	mux := http.NewServeMux()
	registerRoutes(mux)

	mux.Handle("GET /metrics", metricsReg.Handler())

	// Order matters: tracing.WrapHandler is outermost so it starts the server
	// span and continues any inbound trace context; RouteNamer is innermost so
	// it can read the matched route pattern and name the span accordingly.
	// LoadAndSave loads the shared session into context; Gate then rejects
	// unauthenticated requests to the browser-facing live-offers/trade-cost routes
	// (internal + health/metrics routes stay open — see sessionProtected).
	handler := tracing.WrapHandler(
		corsMiddleware(metricsReg.Middleware(
			sessionMgr.LoadAndSave(
				sessionMgr.Gate(sessionProtected)(tracing.RouteNamer(mux)),
			),
		)),
		serviceName,
	)

	// No WriteTimeout: the websocket handler hijacks the connection and manages its
	// own deadlines; a server-wide WriteTimeout would sever long-lived WS streams.
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		slog.Info("starting dexUnifiedApi server", "addr", listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Block until SIGINT/SIGTERM (e.g. docker stop), then drain in-flight
	// requests and let the deferred tracer shutdown flush buffered spans.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}

// --- Middleware ---

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Handlers ---

func handleLiveOffersREST(w http.ResponseWriter, r *http.Request) {
	if err := refreshLiveHub(r.Context(), nil); err != nil {
		slog.WarnContext(r.Context(), "live book refresh failed; serving cached book", "err", err)
	}
	if err := refreshLiveCapabilityPolicy(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "refresh MM CEX policy; routing fail-closed", "err", err)
	}
	spotExclusions := queryExcludedSpotCEXes(r)
	hedgeExclusions := queryExcludedHedgeCEXes(r)
	sub := liveSubscription{
		cex:                   queryCex(r),
		fiat:                  queryFiat(r),
		requiredConfirmations: queryConfirmations(r),
		excludedSpotCEXes:     strings.Join(spotExclusions, ","),
		excludedHedgeCEXes:    strings.Join(hedgeExclusions, ","),
		feeTier:               queryFeeTier(r),
		coupledHedge:          queryCoupledHedge(r),
		maxFiatLimitEUR:       queryMaxFiatLimitEUR(r),
	}
	payload := hub.computePayload(getCexPrice(sub.cex), sub)
	if payload == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(payload)
}

// fetchOfferLifecycle returns lifecycle (snapshot_time, price) points grouped by offer_db_id.
func fetchOfferLifecycle(ctx context.Context, ids []int64) map[int64][]LifecyclePoint {
	result := make(map[int64][]LifecyclePoint, len(ids))
	if len(ids) == 0 {
		return result
	}
	if lifecycleRepo == (lifecycle.Repository{}) && db != nil {
		lifecycleRepo = lifecycle.NewRepository(db)
	}
	points, err := lifecycleRepo.LoadByOfferIDs(ctx, ids)
	if err != nil {
		return result
	}
	for id, pts := range points {
		for _, pt := range pts {
			result[id] = append(result[id], LifecyclePoint{
				Time:          pt.Time,
				Price:         pt.Price,
				NetPremiumPct: pt.NetPremiumPct,
			})
		}
	}
	return result
}

// writeLifecycleEntries persists a computed snapshot of each live offer's
// metrics into the local order_lifecycle table, and updates best_route_cex /
// best_route_method on the offers table.
func writeLifecycleEntries(ctx context.Context, offers []liveOutOffer) {
	if len(offers) == 0 || db == nil {
		return
	}
	if lifecycleRepo == (lifecycle.Repository{}) {
		lifecycleRepo = lifecycle.NewRepository(db)
	}
	records := make([]lifecycle.Record, 0, len(offers))
	for _, o := range offers {
		records = append(records, lifecycle.Record{
			ID:               o.ID,
			Price:            o.Price,
			RefPrice:         o.RefPrice,
			PriceDiffPct:     o.PriceDiffPct,
			NetPremiumPct:    o.NetPremiumPct,
			CostBreakdown:    o.CostBreakdown,
			MinCostBreakdown: o.CostBreakdownMin,
		})
	}
	if err := lifecycleRepo.PersistSnapshot(ctx, records); err != nil {
		fmt.Printf("[Lifecycle] persist error: %v\n", err)
	}
}

// handleInternalLiveOffers receives the live offer list from the snapshot tool,
// updates the hub, and persists lifecycle to the DB.
// maxInternalBodyBytes bounds the internal ingest request bodies so a caller
// can't stream an unbounded payload into memory before decoding.
const maxInternalBodyBytes = 4 << 20 // 4 MiB

// internalSecretAuthorized reports whether an internal-ingest request is allowed.
// A configured secret must match in constant time; an empty secret is accepted
// only when allowInsecureInternal is set (dev/tests), so prod fails CLOSED rather
// than leaving the endpoint open when the secret is missing.
func internalSecretAuthorized(provided, expected string) bool {
	if expected == "" {
		return allowInsecureInternal
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// internalSecretGate wraps a handler with the X-Internal-Secret check used by
// the internal ingest endpoints. It lets a service-to-service caller (no
// session cookie) reach a handler that would otherwise be behind the browser
// session gate. An empty configured secret fails closed unless
// allowInsecureInternal is set (dev/tests), matching internalSecretAuthorized.
func internalSecretGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !internalSecretAuthorized(r.Header.Get("X-Internal-Secret"), internalAPISecret) {
			http.Error(w, "unauthorized internal secret", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleInternalLiveOffers(w http.ResponseWriter, r *http.Request) {
	if !internalSecretAuthorized(r.Header.Get("X-Internal-Secret"), internalAPISecret) {
		http.Error(w, "unauthorized internal secret", http.StatusUnauthorized)
		return
	}

	var offers []LiveOffer
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInternalBodyBytes)).Decode(&offers); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Bound the DB work to the request: the snapshot client's POST has a 15s deadline,
	// so if it gives up we stop too instead of running on under context.Background().
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	appMetrics.observePush()
	// We deliberately do NOT preload every live offer's full lifecycle history into the
	// hub here. That unbounded bulk load (all order_lifecycle rows for all live offers)
	// is what OOM-killed this service; the per-offer lifecycle endpoint lazy-loads on
	// demand instead.
	if err := refreshLiveHub(ctx, offers); err != nil {
		slog.WarnContext(ctx, "live book refresh failed; retaining cached book", "err", err)
		http.Error(w, "live book refresh failed", http.StatusServiceUnavailable)
		return
	}
	refreshedOffers := hub.getOffers()
	price := getCexPrice(defaultCEX)
	outOffers := hub.computeOutOffers(price, liveSubscription{cex: defaultCEX})
	writeLifecycleEntries(ctx, outOffers)
	// Look up Peach seller ratings only for net-profitable offers, in the
	// background — Peach's per-maker API is slow/rate-limited and must not block
	// the snapshot's push (which has its own short deadline).
	go resolveAndApplyPeachRatings(context.Background(), refreshedOffers, outOffers)
	fmt.Printf("[LiveHub] refreshed with %d live offers from PostgreSQL\n", len(refreshedOffers))
	w.WriteHeader(http.StatusNoContent)
}

func handleTradeCostConfig(w http.ResponseWriter, r *http.Request) {
	type cexInfo struct {
		Name string `json:"name"`
	}
	cexes := make([]cexInfo, 0, len(supportedCEXes))
	for _, name := range supportedCEXes {
		cexes = append(cexes, cexInfo{Name: name})
	}

	tiers := []map[string]string{
		{"value": "", "label": "Auto (congestion-based)"},
		{"value": "economic", "label": "Economic (cheapest, ~1h)"},
		{"value": "standard", "label": "Standard (balanced, ~30min)"},
		{"value": "fast", "label": "Fast (priority, ~10min)"},
		{"value": "express", "label": "Express (next block, ~2min)"},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"cexes":                cexes,
		"dex_routing_profiles": tradecost.DexRoutingProfiles(),
		"fee_tiers":            tiers,
	})
}

type cexCapabilitiesResponse struct {
	Exchanges []mmcapabilities.ProviderState `json:"exchanges"`
	MaxAgeSec int64                          `json:"max_age_seconds"`
}

func currentCapabilityStore() *mmcapabilities.Store {
	if capabilityStore == nil && db != nil {
		capabilityStore = mmcapabilities.NewStore(db, capabilityMaxAge)
	}
	return capabilityStore
}

func handleCEXPermissions(w http.ResponseWriter, r *http.Request) {
	store := currentCapabilityStore()
	if store == nil {
		writeError(w, fmt.Errorf("MM capability store is unavailable"))
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			SpotEnabled  *bool `json:"spot_enabled"`
			HedgeEnabled *bool `json:"hedge_enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, fmt.Sprintf("invalid policy: %v", err), http.StatusBadRequest)
			return
		}
		if input.SpotEnabled == nil || input.HedgeEnabled == nil {
			http.Error(w, "spot_enabled and hedge_enabled are required", http.StatusBadRequest)
			return
		}
		policy := mmcapabilities.Policy{SpotEnabled: *input.SpotEnabled, HedgeEnabled: *input.HedgeEnabled}
		state, err := store.UpdatePolicy(r.Context(), r.PathValue("provider"), policy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := refreshLiveCapabilityPolicy(r.Context()); err != nil {
			slog.ErrorContext(r.Context(), "refresh MM CEX policy after update; routing fail-closed", "err", err)
		}
		writeJSON(w, state)
		return
	}
	states, err := store.States(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	maxAge := capabilityMaxAge
	if maxAge <= 0 {
		maxAge = mmcapabilities.DefaultMaxAge
	}
	writeJSON(w, cexCapabilitiesResponse{Exchanges: states, MaxAgeSec: int64(maxAge / time.Second)})
}

func refreshLiveCapabilityPolicy(ctx context.Context) error {
	store := currentCapabilityStore()
	if store == nil {
		all := mmcapabilities.CostSupportedProviderIDs()
		hub.setCapabilityExclusions(all, all)
		return fmt.Errorf("MM capability store is unavailable")
	}
	states, err := store.States(ctx)
	if err != nil {
		all := mmcapabilities.CostSupportedProviderIDs()
		hub.setCapabilityExclusions(all, all)
		return err
	}
	policySpot, policyHedge := mmcapabilities.EffectiveExclusions(states)
	hub.setCapabilityExclusions(policySpot, policyHedge)
	return nil
}

type accountHealthResponse struct {
	Provider  string `json:"provider"`
	Status    string `json:"status"`
	Healthy   bool   `json:"healthy"`
	Message   string `json:"message"`
	CheckedAt string `json:"checked_at"`
}

func handleAccountHealthCheck(w http.ResponseWriter, r *http.Request) {
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider == "" {
		http.Error(w, "provider query parameter is required", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(provider, accounthealth.BisqAccountProvider) {
		http.Error(w, "unsupported provider "+provider+"; supported providers: Bisq", http.StatusBadRequest)
		return
	}

	recorder := accounthealth.NewDBRecorder(db)
	if err := recorder.EnsureAccount(r.Context(), accounthealth.BisqAccountProvider); err != nil {
		writeError(w, err)
		return
	}

	err := accounthealth.CheckBisqAccountHealth(
		r.Context(),
		accounthealth.NewBisqGRPCAccountChecker(bisqGRPCHost, bisqAPIPassword, 0),
	)
	healthy := err == nil
	if updateErr := recorder.UpdateAccountHealth(r.Context(), accounthealth.BisqAccountProvider, healthy); updateErr != nil {
		writeError(w, updateErr)
		return
	}

	status := "healthy"
	message := "Bisq payment accounts are reachable."
	if err != nil {
		status = "unhealthy"
		message = err.Error()
	}

	writeJSON(w, accountHealthResponse{
		Provider:  accounthealth.BisqAccountProvider,
		Status:    status,
		Healthy:   healthy,
		Message:   message,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if !coreTablesReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]string{
			"status":    "starting",
			"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05"),
		})
		return
	}

	writeJSON(w, map[string]string{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05"),
	})
}

// --- Query helpers ---

func queryCex(r *http.Request) string {
	cex := r.URL.Query().Get("cex")
	if tradeCostEstimators[cex] != nil {
		return cex
	}
	return defaultCEX
}

func queryFiat(r *http.Request) string {
	currency, ok := fiat.Normalize(r.URL.Query().Get("fiat"))
	if !ok {
		return "EUR"
	}
	return currency
}

func queryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

func queryConfirmations(r *http.Request) int {
	v := queryInt(r, "confirmations", 0)
	if v <= 0 {
		return 0
	}
	if v > 6 {
		return 6
	}
	return v
}

var validFeeTiers = map[string]bool{
	"economic": true,
	"standard": true,
	"fast":     true,
	"express":  true,
}

func queryFeeTier(r *http.Request) string {
	tier := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("fee_tier")))
	if validFeeTiers[tier] {
		return tier
	}
	return ""
}

func queryExcludedCEXes(r *http.Request) []string {
	return queryCEXList(r, "exclude_cexes")
}

func queryExcludedSpotCEXes(r *http.Request) []string {
	return mergeCEXLists(queryExcludedCEXes(r), queryCEXList(r, "exclude_spot_cexes"))
}

func queryExcludedHedgeCEXes(r *http.Request) []string {
	return mergeCEXLists(queryExcludedCEXes(r), queryCEXList(r, "exclude_hedge_cexes"))
}

func queryCEXList(r *http.Request, key string) []string {
	values := append([]string{}, r.URL.Query()[key]...)
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			cex := strings.TrimSpace(strings.ToLower(part))
			if cex == "" {
				continue
			}
			if _, ok := seen[cex]; ok {
				continue
			}
			seen[cex] = struct{}{}
			result = append(result, cex)
		}
	}
	return result
}

func mergeCEXLists(lists ...[]string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, list := range lists {
		for _, cex := range list {
			if _, ok := seen[cex]; ok {
				continue
			}
			seen[cex] = struct{}{}
			result = append(result, cex)
		}
	}
	return result
}

func queryCoupledHedge(r *http.Request) bool {
	v := r.URL.Query().Get("coupled_hedge")
	return v != "false" && v != "0"
}

func queryMaxFiatLimitEUR(r *http.Request) float64 {
	v := r.URL.Query().Get("max_fiat_limit")
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, err error) {
	// Log the real error server-side but return a generic message: raw err.Error()
	// can leak DB/driver internals (table names, SQL state, connection details).
	slog.Error("request failed", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
