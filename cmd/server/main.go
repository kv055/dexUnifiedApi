package main

import (
	"context"
	"database/sql"
	"dexunifiedapi/internal/config"
	"dexunifiedapi/internal/dbutil"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	gorillaws "github.com/gorilla/websocket"
)

var (
	adminDatabaseURL          string
	databaseURL               string
	snapshotIntervalMin       int
	listenAddr                string
	paymentNotificationSecret string
	db                        *dbutil.DB
)

var (
	cexPricesMu sync.RWMutex
	cexPrices   = map[string]float64{}
)

const (
	defaultCEX     = "kraken"
	defaultCEXPair = "BTC/EUR"
)

var supportedCEXes = []string{"kraken", "binance", "bitstamp", "coinbase"}

var tradeCostEstimators map[string]*TradeCostEstimator
var tradeRouteOptimizer *TradeRouteOptimizer

func seedHubFromDB() {
	// Live offers are now sourced from snapshot memory and pushed into the hub
	// directly, so we intentionally avoid reconstructing them from persisted DB
	// rows on startup.
	hub.setOffers(nil)
	fmt.Printf("[Hub] waiting for next snapshot push to seed live offers\n")
}

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
	cexPricesMu.RLock()
	defer cexPricesMu.RUnlock()
	return cexPrices[cex]
}

func setCexPrice(cex string, p float64) {
	cexPricesMu.Lock()
	cexPrices[cex] = p
	cexPricesMu.Unlock()
	hub.broadcastForCex(cex)
}

// --- CEX WebSocket price feeds ---

func runCexPriceFeed(ctx context.Context, cex string) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var err error
		switch cex {
		case "kraken":
			err = runKrakenFeed(ctx, cex)
		case "binance":
			err = runBinanceFeed(ctx, cex)
		case "bitstamp":
			err = runBitstampFeed(ctx, cex)
		case "coinbase":
			err = runCoinbaseFeed(ctx, cex)
		default:
			return
		}

		if err != nil {
			fmt.Printf("[%sFeed] error: %v, retrying in %s\n", cex, err, backoff)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		} else {
			backoff = time.Second
		}
	}
}

func runKrakenFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://ws.kraken.com/v2", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	sub := fmt.Sprintf(`{"method":"subscribe","params":{"channel":"ticker","symbol":["%s"]}}`, defaultCEXPair)
	if err := ws.WriteMessage(gorillaws.TextMessage, []byte(sub)); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var envelope struct {
			Channel string `json:"channel"`
			Data    []struct {
				Last float64 `json:"last"`
			} `json:"data"`
		}
		if json.Unmarshal(msgBytes, &envelope) == nil &&
			envelope.Channel == "ticker" && len(envelope.Data) > 0 && envelope.Data[0].Last > 0 {
			setCexPrice(cex, envelope.Data[0].Last)
		}
	}
}

func runBinanceFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://stream.binance.com:9443/ws/btceur@miniTicker", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			EventType string `json:"e"`
			Close     string `json:"c"`
		}
		if json.Unmarshal(msgBytes, &msg) == nil && msg.EventType == "24hrMiniTicker" {
			if p, err := strconv.ParseFloat(msg.Close, 64); err == nil && p > 0 {
				setCexPrice(cex, p)
			}
		}
	}
}

func runBitstampFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://ws.bitstamp.net", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	sub := `{"event":"bts:subscribe","data":{"channel":"live_trades_btceur"}}`
	if err := ws.WriteMessage(gorillaws.TextMessage, []byte(sub)); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			Event   string `json:"event"`
			Channel string `json:"channel"`
			Data    struct {
				Price float64 `json:"price"`
			} `json:"data"`
		}
		if json.Unmarshal(msgBytes, &msg) == nil &&
			msg.Event == "trade" && msg.Data.Price > 0 {
			setCexPrice(cex, msg.Data.Price)
		}
	}
}

func runCoinbaseFeed(ctx context.Context, cex string) error {
	ws, _, err := gorillaws.DefaultDialer.DialContext(ctx, "wss://advanced-trade-ws.coinbase.com", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	fmt.Printf("[%sFeed] connected\n", cex)

	sub := `{"type":"subscribe","product_ids":["BTC-EUR"],"channel":"ticker"}`
	if err := ws.WriteMessage(gorillaws.TextMessage, []byte(sub)); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			Channel string `json:"channel"`
			Events  []struct {
				Tickers []struct {
					Price string `json:"price"`
				} `json:"tickers"`
			} `json:"events"`
		}
		if json.Unmarshal(msgBytes, &msg) == nil && msg.Channel == "ticker" {
			for _, ev := range msg.Events {
				for _, t := range ev.Tickers {
					if p, err := strconv.ParseFloat(t.Price, 64); err == nil && p > 0 {
						setCexPrice(cex, p)
					}
				}
			}
		}
	}
}

// --- Init & Main ---

func init() {
	if err := config.LoadRequiredDotEnv(".env"); err != nil {
		if runningUnderGoTest() {
			listenAddr = ":5010"
			paymentNotificationSecret = strings.TrimSpace(os.Getenv("PAYMENT_NOTIFICATIONS_SHARED_SECRET"))
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

}

func runningUnderGoTest() bool {
	return strings.HasSuffix(os.Args[0], ".test") || strings.HasSuffix(os.Args[0], ".test.exe")
}

func main() {
	adminDB, err := dbutil.Open(adminDatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open admin database: %v\n", err)
		os.Exit(1)
	}
	defer adminDB.Close()

	if err := adminDB.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ping admin database: %v\n", err)
		os.Exit(1)
	}

	if err := dbutil.EnsureAppRoles(adminDB, dbutil.RoleBootstrapConfig{
		DatabaseName:           config.EnvOrDefault("POSTGRES_DB", "dexarb"),
		UnifiedAPIPassword:     config.RequireEnv("POSTGRES_UNIFIED_API_PASSWORD"),
		MonitorPassword:        config.RequireEnv("POSTGRES_MONITOR_PASSWORD"),
		MMControllerPassword:   config.RequireEnv("POSTGRES_MM_CONTROLLER_PASSWORD"),
		FiatControllerPassword: config.RequireEnv("POSTGRES_FIAT_CONTROLLER_PASSWORD"),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "database admin setup error: %v\n", err)
		os.Exit(1)
	}

	db, err = dbutil.Open(databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ping database: %v\n", err)
		os.Exit(1)
	}

	tradeCostEstimators = make(map[string]*TradeCostEstimator, len(supportedCEXes))
	for _, c := range supportedCEXes {
		tradeCostEstimators[c] = NewTradeCostEstimator(c)
		fmt.Printf("[TradeCost] initialized %s (taker fallback %.2f%%, onchain withdrawal fallback %.8f BTC)\n",
			c, takerFeeFallbacks[c]*100, withdrawalFallbacks[c][MethodOnchain])
	}
	tradeRouteOptimizer = NewTradeRouteOptimizer(tradeCostEstimators)

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "schema init error: %v\n", err)
		os.Exit(1)
	}

	// Seed hub from DB in the background
	go seedHubFromDB()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, c := range supportedCEXes {
		go runCexPriceFeed(ctx, c)
	}

	mux := http.NewServeMux()

	// API — live offers only
	mux.HandleFunc("GET /api/live_offers", handleLiveOffersREST)
	mux.HandleFunc("GET /api/payment_notifications", handlePaymentNotifications)
	mux.HandleFunc("GET /ws/live_offers", handleLiveOffersWS)
	mux.HandleFunc("GET /api/trade_cost_config", handleTradeCostConfig)
	mux.HandleFunc("GET /health", handleHealth)

	// Internal: snapshot tool pushes live offer data after each run
	mux.HandleFunc("POST /internal/live_offers", handleInternalLiveOffers)
	mux.HandleFunc("POST /internal/payment_notifications", handleInternalPaymentNotifications)

	handler := corsMiddleware(mux)

	fmt.Printf("Starting dexUnifiedApi server on %s\n", listenAddr)
	if err := http.ListenAndServe(listenAddr, handler); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// --- Middleware ---

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
	sub := liveSubscription{
		cex:                   queryCex(r),
		requiredConfirmations: queryConfirmations(r),
		excludedCEXes:         strings.Join(queryExcludedCEXes(r), ","),
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
func fetchOfferLifecycle(ids []int64) map[int64][]LifecyclePoint {
	result := make(map[int64][]LifecyclePoint, len(ids))
	if len(ids) == 0 {
		return result
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(fmt.Sprintf(
		`SELECT offer_db_id,
		        to_char(snapshot_time AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
		        price,
		        net_premium_pct
		   FROM order_lifecycle
		  WHERE offer_db_id IN (%s)
		  ORDER BY offer_db_id, snapshot_time`,
		placeholders,
	), args...)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var offerID int64
		var snapTime string
		var price float64
		var netPremiumPct sql.NullFloat64
		if err := rows.Scan(&offerID, &snapTime, &price, &netPremiumPct); err != nil {
			continue
		}
		pt := LifecyclePoint{Time: snapTime, Price: price}
		if netPremiumPct.Valid {
			pt.NetPremiumPct = &netPremiumPct.Float64
		}
		result[offerID] = append(result[offerID], pt)
	}
	return result
}

// writeLifecycleEntries persists a computed snapshot of each live offer's
// metrics into the local order_lifecycle table, and updates best_route_cex /
// best_route_method on the offers table.
func writeLifecycleEntries(offers []liveOutOffer) {
	if len(offers) == 0 || db == nil {
		return
	}
	tx, err := db.Begin()
	if err != nil {
		fmt.Printf("[Lifecycle] begin tx error: %v\n", err)
		return
	}
	insStmt, err := tx.Prepare(`
		INSERT INTO order_lifecycle
			(offer_db_id, price, ref_price, premium_pct,
			 trade_cost_pct, trade_cost_btc, cex_taker_fee_btc, cex_withdraw_fee_btc, network_fee_btc, dex_fee_btc,
			 net_premium_pct, net_premium_btc, btc_amount)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		fmt.Printf("[Lifecycle] prepare insert error: %v\n", err)
		return
	}
	defer insStmt.Close()

	updStmt, err := tx.Prepare(`
		UPDATE offers SET
			best_route_cex = ?,
			best_route_method = ?,
			hedge_open_fee_btc = ?,
			hedge_close_fee_btc = ?,
			hedge_funding_fee_btc = ?,
			hedge_fee_sum_btc = ?,
			hedge_venue = ?,
			hedge_instrument = ?,
			hedge_transfer_minutes = ?
		WHERE id = ?
	`)
	if err != nil {
		tx.Rollback()
		fmt.Printf("[Lifecycle] prepare update error: %v\n", err)
		return
	}
	defer updStmt.Close()

	for _, o := range offers {
		if o.RefPrice <= 0 {
			continue
		}
		bd := o.CostBreakdown
		var cexWithdrawFee any
		if bd.TradeDirection != "ask" {
			cexWithdrawFee = round8(bd.WithdrawalFeeBTC)
		}
		_, _ = insStmt.Exec(
			o.ID,
			round4(o.Price),
			round4(o.RefPrice),
			round4(o.PriceDiffPct),
			round4(bd.TotalCostPct),
			round8(bd.TotalCostBTC),
			round8(bd.CexTakerFeeBTC),
			cexWithdrawFee,
			round8(bd.NetworkFeeBTC),
			round8(bd.DexFeeBTC),
			round4(o.NetPremiumPct),
			round8(bd.EstimatedProfitBTC),
			round8(bd.BTCAmount),
		)
		if bd.RouteCEX != "" {
			_, _ = updStmt.Exec(
				bd.RouteCEX,
				bd.RouteMethod,
				round8(bd.Hedge.OpenFeeBTC),
				round8(bd.Hedge.CloseFeeBTC),
				round8(bd.Hedge.FundingFeeBTC),
				round8(bd.Hedge.FeeSumBTC),
				nullIfEmpty(bd.Hedge.Venue),
				nullIfEmpty(bd.Hedge.Instrument),
				nullIfZeroInt(bd.Hedge.TransferMinutes),
				o.ID,
			)
		}
	}

	if err := tx.Commit(); err != nil {
		fmt.Printf("[Lifecycle] commit error: %v\n", err)
	}
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullIfZeroInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// handleInternalLiveOffers receives the live offer list from the snapshot tool,
// updates the hub, and persists lifecycle to the DB.
func handleInternalLiveOffers(w http.ResponseWriter, r *http.Request) {
	var offers []LiveOffer
	if err := json.NewDecoder(r.Body).Decode(&offers); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := make([]int64, len(offers))
	for i, o := range offers {
		ids[i] = o.ID
	}
	hub.setLifecycle(fetchOfferLifecycle(ids))
	hub.setOffers(offers)
	price := getCexPrice(defaultCEX)
	outOffers := hub.computeOutOffers(price, liveSubscription{cex: defaultCEX})
	go writeLifecycleEntries(outOffers)
	fmt.Printf("[LiveHub] updated with %d live offers from snapshot\n", len(offers))
	w.WriteHeader(http.StatusNoContent)
}

func handleTradeCostConfig(w http.ResponseWriter, r *http.Request) {
	type cexInfo struct {
		Name         string   `json:"name"`
		Methods      []string `json:"methods"`
		HasLightning bool     `json:"has_lightning"`
	}
	cexes := make([]cexInfo, 0, len(supportedCEXes))
	for _, name := range supportedCEXes {
		profile := cexProfileFor(name)
		methods := make([]string, 0, len(profile.Methods))
		hasLN := false
		for _, m := range profile.Methods {
			methods = append(methods, string(m))
			if m == MethodLightning {
				hasLN = true
			}
		}
		cexes = append(cexes, cexInfo{Name: name, Methods: methods, HasLightning: hasLN})
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
		"cexes":     cexes,
		"fee_tiers": tiers,
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
	values := append([]string{}, r.URL.Query()["exclude_cexes"]...)
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

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
