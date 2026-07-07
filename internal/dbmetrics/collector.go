// Package dbmetrics exposes business and data-freshness signal as Prometheus
// metrics by querying the database on scrape.
//
// The snapshot worker that produces all trading data is an ephemeral process
// (a fresh binary per cycle), so it cannot be scraped directly. Everything it
// writes, however, is persisted to offers / order_lifecycle / snapshot_log /
// ohlc / accounts. This collector runs a handful of cheap aggregate queries
// against those tables and emits them as gauges, hosted inside the long-lived
// unified-api that Prometheus already scrapes — no Pushgateway or extra
// container required.
//
// Results are cached briefly so concurrent scrapes never double-hit the DB, and
// every refresh runs under a short context timeout so a slow query can never
// stall a scrape.
package dbmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	dbutil "dexunifiedapi/db"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	cacheTTL = 5 * time.Second
	// queryTimeout bounds each aggregate query individually (applied per scan, not
	// shared across the whole refresh), so one slow query can't starve the others.
	queryTimeout = 8 * time.Second
)

// Collector implements prometheus.Collector against the unified-api database.
type Collector struct {
	db *dbutil.DB

	mu       sync.Mutex
	cached   *snapshot
	cachedAt time.Time

	// descriptors
	liveOffers     *prometheus.Desc
	liquidity      *prometheus.Desc
	netProfitable  *prometheus.Desc
	bestNetPremium *prometheus.Desc
	netPremiumStat *prometheus.Desc
	snapshotAge    *prometheus.Desc
	snapshotOffers *prometheus.Desc
	exchangeOffers *prometheus.Desc
	exchangeUp     *prometheus.Desc
	ohlcAge        *prometheus.Desc
	offersTotal    *prometheus.Desc
	lifecycleRows  *prometheus.Desc
	accountHealthy *prometheus.Desc
	accountAge     *prometheus.Desc
	fetchDuration  *prometheus.Desc
	fetchFailures  *prometheus.Desc
	cycleDuration  *prometheus.Desc
}

// NewCollector builds a collector over db. Register it on the service registry.
func NewCollector(db *dbutil.DB) *Collector {
	return &Collector{
		db: db,
		liveOffers: prometheus.NewDesc("dex_live_offers",
			"Number of currently live offers.", []string{"exchange", "direction"}, nil),
		liquidity: prometheus.NewDesc("dex_live_offer_btc_liquidity",
			"Sum of btc_max across live offers (available volume).", []string{"exchange", "direction"}, nil),
		netProfitable: prometheus.NewDesc("dex_net_profitable_offers",
			"Live offers whose latest net premium is positive.", []string{"direction"}, nil),
		bestNetPremium: prometheus.NewDesc("dex_best_net_premium_pct",
			"Best (max) net premium percent among live offers' latest lifecycle.", []string{"direction"}, nil),
		netPremiumStat: prometheus.NewDesc("dex_live_offer_net_premium_pct",
			"Distribution stat (avg/p50/p90) of latest net premium percent across live offers.", []string{"direction", "stat"}, nil),
		snapshotAge: prometheus.NewDesc("dex_snapshot_age_seconds",
			"Seconds since the most recent snapshot run.", nil, nil),
		snapshotOffers: prometheus.NewDesc("dex_snapshot_last_offers_count",
			"Total offers recorded by the most recent snapshot run.", nil, nil),
		exchangeOffers: prometheus.NewDesc("dex_exchange_last_offers",
			"Offers returned per exchange in the most recent snapshot.", []string{"exchange"}, nil),
		exchangeUp: prometheus.NewDesc("dex_exchange_up",
			"1 if the exchange returned offers in the most recent snapshot, else 0.", []string{"exchange"}, nil),
		ohlcAge: prometheus.NewDesc("dex_ohlc_age_seconds",
			"Seconds since the most recent OHLC candle (Kraken price freshness).", nil, nil),
		offersTotal: prometheus.NewDesc("dex_offers_total",
			"Total offers rows, split by live state.", []string{"is_live"}, nil),
		lifecycleRows: prometheus.NewDesc("dex_lifecycle_rows_total",
			"Estimated row count of order_lifecycle (planner estimate).", nil, nil),
		accountHealthy: prometheus.NewDesc("dex_account_healthy",
			"Account health (1 healthy, 0 unhealthy) by provider and category.", []string{"provider", "category"}, nil),
		accountAge: prometheus.NewDesc("dex_account_health_age_seconds",
			"Seconds since the account was last health-checked.", []string{"provider"}, nil),
		fetchDuration: prometheus.NewDesc("dex_exchange_fetch_duration_seconds",
			"Wall time the latest snapshot run spent fetching each exchange (both directions).", []string{"exchange"}, nil),
		fetchFailures: prometheus.NewDesc("dex_exchange_fetch_failures",
			"Failed direction-fetches per exchange in the latest snapshot run.", []string{"exchange"}, nil),
		cycleDuration: prometheus.NewDesc("dex_snapshot_cycle_duration_seconds",
			"Total wall time of the latest snapshot worker run.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.liveOffers
	ch <- c.liquidity
	ch <- c.netProfitable
	ch <- c.bestNetPremium
	ch <- c.netPremiumStat
	ch <- c.snapshotAge
	ch <- c.snapshotOffers
	ch <- c.exchangeOffers
	ch <- c.exchangeUp
	ch <- c.ohlcAge
	ch <- c.offersTotal
	ch <- c.lifecycleRows
	ch <- c.accountHealthy
	ch <- c.accountAge
	ch <- c.fetchDuration
	ch <- c.fetchFailures
	ch <- c.cycleDuration
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	data := c.fetch()
	if data == nil {
		return
	}

	for _, v := range data.liveOffers {
		ch <- prometheus.MustNewConstMetric(c.liveOffers, prometheus.GaugeValue, v.value, v.l1, v.l2)
	}
	for _, v := range data.liquidity {
		ch <- prometheus.MustNewConstMetric(c.liquidity, prometheus.GaugeValue, v.value, v.l1, v.l2)
	}
	for _, d := range data.dirStats {
		ch <- prometheus.MustNewConstMetric(c.netProfitable, prometheus.GaugeValue, d.profitable, d.direction)
		ch <- prometheus.MustNewConstMetric(c.bestNetPremium, prometheus.GaugeValue, d.best, d.direction)
		ch <- prometheus.MustNewConstMetric(c.netPremiumStat, prometheus.GaugeValue, d.avg, d.direction, "avg")
		ch <- prometheus.MustNewConstMetric(c.netPremiumStat, prometheus.GaugeValue, d.p50, d.direction, "p50")
		ch <- prometheus.MustNewConstMetric(c.netPremiumStat, prometheus.GaugeValue, d.p90, d.direction, "p90")
	}
	if data.snapshotAge.valid {
		ch <- prometheus.MustNewConstMetric(c.snapshotAge, prometheus.GaugeValue, data.snapshotAge.value)
	}
	if data.snapshotOffers.valid {
		ch <- prometheus.MustNewConstMetric(c.snapshotOffers, prometheus.GaugeValue, data.snapshotOffers.value)
	}
	for _, v := range data.exchangeOffers {
		ch <- prometheus.MustNewConstMetric(c.exchangeOffers, prometheus.GaugeValue, v.value, v.l1)
		up := 0.0
		if v.value > 0 {
			up = 1.0
		}
		ch <- prometheus.MustNewConstMetric(c.exchangeUp, prometheus.GaugeValue, up, v.l1)
	}
	if data.ohlcAge.valid {
		ch <- prometheus.MustNewConstMetric(c.ohlcAge, prometheus.GaugeValue, data.ohlcAge.value)
	}
	for _, v := range data.offersTotal {
		ch <- prometheus.MustNewConstMetric(c.offersTotal, prometheus.GaugeValue, v.value, v.l1)
	}
	if data.lifecycleRows.valid {
		ch <- prometheus.MustNewConstMetric(c.lifecycleRows, prometheus.GaugeValue, data.lifecycleRows.value)
	}
	for _, a := range data.accounts {
		ch <- prometheus.MustNewConstMetric(c.accountHealthy, prometheus.GaugeValue, a.healthy, a.provider, a.category)
		if a.age.valid {
			ch <- prometheus.MustNewConstMetric(c.accountAge, prometheus.GaugeValue, a.age.value, a.provider)
		}
	}
	for _, v := range data.fetchDurations {
		ch <- prometheus.MustNewConstMetric(c.fetchDuration, prometheus.GaugeValue, v.value, v.l1)
	}
	for _, v := range data.fetchFailures {
		ch <- prometheus.MustNewConstMetric(c.fetchFailures, prometheus.GaugeValue, v.value, v.l1)
	}
	if data.cycleDuration.valid {
		ch <- prometheus.MustNewConstMetric(c.cycleDuration, prometheus.GaugeValue, data.cycleDuration.value)
	}
}

// fetch returns cached data when fresh, otherwise re-queries the DB.
func (c *Collector) fetch() *snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && time.Since(c.cachedAt) < cacheTTL {
		return c.cached
	}
	data := c.query(context.Background())
	c.cached = data
	c.cachedAt = time.Now()
	return data
}

// --- data shapes ---

type nullFloat struct {
	value float64
	valid bool
}

type labeledVal struct {
	l1, l2 string
	value  float64
}

type dirStat struct {
	direction                       string
	profitable, best, avg, p50, p90 float64
}

type accountStat struct {
	provider, category string
	healthy            float64
	age                nullFloat
}

type snapshot struct {
	liveOffers     []labeledVal
	liquidity      []labeledVal
	dirStats       []dirStat
	snapshotAge    nullFloat
	snapshotOffers nullFloat
	exchangeOffers []labeledVal
	ohlcAge        nullFloat
	offersTotal    []labeledVal
	lifecycleRows  nullFloat
	accounts       []accountStat
	fetchDurations []labeledVal
	fetchFailures  []labeledVal
	cycleDuration  nullFloat
}

// query runs every aggregate. A failing section logs and is left empty so one
// bad query never blanks the whole collector.
func (c *Collector) query(ctx context.Context) *snapshot {
	s := &snapshot{}

	// Each scan gets its own deadline so one slow query can't starve the others.
	for _, scan := range []func(context.Context, *snapshot){
		c.scanOffers,
		c.scanDirStats,
		c.scanSnapshotLog,
		c.scanOHLC,
		c.scanLifecycleRows,
		c.scanAccounts,
	} {
		sctx, cancel := context.WithTimeout(ctx, queryTimeout)
		scan(sctx, s)
		cancel()
	}

	return s
}

func (c *Collector) scanOffers(ctx context.Context, s *snapshot) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT exchange, direction,
		       COUNT(*) FILTER (WHERE is_live) AS live,
		       COALESCE(SUM(btc_max) FILTER (WHERE is_live), 0) AS liquidity,
		       COUNT(*) AS total
		FROM offers
		GROUP BY exchange, direction`)
	if err != nil {
		logErr("offers", err)
		return
	}
	defer rows.Close()
	liveByState := map[bool]float64{}
	for rows.Next() {
		var exchange, direction string
		var live, liquidity, total float64
		if err := rows.Scan(&exchange, &direction, &live, &liquidity, &total); err != nil {
			logErr("offers scan", err)
			return
		}
		s.liveOffers = append(s.liveOffers, labeledVal{l1: exchange, l2: direction, value: live})
		s.liquidity = append(s.liquidity, labeledVal{l1: exchange, l2: direction, value: liquidity})
		liveByState[true] += live
		liveByState[false] += total - live
	}
	s.offersTotal = append(s.offersTotal,
		labeledVal{l1: "true", value: liveByState[true]},
		labeledVal{l1: "false", value: liveByState[false]},
	)
}

func (c *Collector) scanDirStats(ctx context.Context, s *snapshot) {
	// Start from live offers (~1.5k rows) and LATERAL-fetch the latest lifecycle
	// point per offer via idx_lifecycle_offer_time. DISTINCT ON over order_lifecycle
	// (~2.6M rows) made Postgres scan the whole table and blew the scrape timeout.
	rows, err := c.db.QueryContext(ctx, `
		SELECT o.direction,
		       (COUNT(*) FILTER (WHERE latest.net_premium_pct > 0))::float8 AS profitable,
		       COALESCE(MAX(latest.net_premium_pct), 0) AS best,
		       COALESCE(AVG(latest.net_premium_pct), 0) AS avg,
		       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY latest.net_premium_pct), 0) AS p50,
		       COALESCE(percentile_cont(0.9) WITHIN GROUP (ORDER BY latest.net_premium_pct), 0) AS p90
		FROM offers o
		CROSS JOIN LATERAL (
			SELECT ol.net_premium_pct
			FROM order_lifecycle ol
			WHERE ol.offer_db_id = o.id
			ORDER BY ol.snapshot_time DESC
			LIMIT 1
		) latest
		WHERE o.is_live
		GROUP BY o.direction`)
	if err != nil {
		logErr("dirstats", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var d dirStat
		if err := rows.Scan(&d.direction, &d.profitable, &d.best, &d.avg, &d.p50, &d.p90); err != nil {
			logErr("dirstats scan", err)
			return
		}
		s.dirStats = append(s.dirStats, d)
	}
}

func (c *Collector) scanSnapshotLog(ctx context.Context, s *snapshot) {
	// Freshness + last total from the most recent run.
	c.db.QueryRowContext(ctx, `
		SELECT EXTRACT(EPOCH FROM (now() - MAX(timestamp)))::float8, MAX(offers_count)::float8
		FROM snapshot_log`).Scan(scanNull(&s.snapshotAge), scanNull(&s.snapshotOffers))

	// Run duration of the most recent cycle (cycle_ms is NULL on rows written
	// by workers predating the column).
	c.db.QueryRowContext(ctx, `
		SELECT cycle_ms::float8 / 1000.0
		FROM snapshot_log WHERE cycle_ms IS NOT NULL
		ORDER BY timestamp DESC LIMIT 1`).Scan(scanNull(&s.cycleDuration))

	// Per-exchange counts + fetch timings from the latest run's JSONB payload.
	rows, err := c.db.QueryContext(ctx, `
		SELECT key, (value->>'count')::float8,
		       COALESCE((value->>'duration_ms')::float8, -1),
		       COALESCE((value->>'failed_fetches')::float8, -1)
		FROM snapshot_log, jsonb_each(exchanges)
		WHERE id = (SELECT id FROM snapshot_log ORDER BY timestamp DESC LIMIT 1)`)
	if err != nil {
		logErr("snapshot_log exchanges", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var exchange string
		var count, durationMS, failures float64
		if err := rows.Scan(&exchange, &count, &durationMS, &failures); err != nil {
			logErr("snapshot_log scan", err)
			return
		}
		s.exchangeOffers = append(s.exchangeOffers, labeledVal{l1: exchange, value: count})
		if durationMS >= 0 {
			s.fetchDurations = append(s.fetchDurations, labeledVal{l1: exchange, value: durationMS / 1000.0})
		}
		if failures >= 0 {
			s.fetchFailures = append(s.fetchFailures, labeledVal{l1: exchange, value: failures})
		}
	}
}

func (c *Collector) scanOHLC(ctx context.Context, s *snapshot) {
	c.db.QueryRowContext(ctx, `
		SELECT EXTRACT(EPOCH FROM (now() - MAX(time)))::float8 FROM ohlc`).Scan(scanNull(&s.ohlcAge))
}

func (c *Collector) scanLifecycleRows(ctx context.Context, s *snapshot) {
	// Planner estimate — avoids a full COUNT(*) on a large, fast-growing table.
	c.db.QueryRowContext(ctx, `
		SELECT reltuples::float8 FROM pg_class WHERE relname = 'order_lifecycle'`).Scan(scanNull(&s.lifecycleRows))
}

func (c *Collector) scanAccounts(ctx context.Context, s *snapshot) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT provider, COALESCE(category, 'unknown'),
		       (CASE WHEN is_healthy THEN 1 ELSE 0 END)::float8,
		       EXTRACT(EPOCH FROM (now() - last_health_check))::float8
		FROM accounts`)
	if err != nil {
		logErr("accounts", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a accountStat
		if err := rows.Scan(&a.provider, &a.category, &a.healthy, scanNull(&a.age)); err != nil {
			logErr("accounts scan", err)
			return
		}
		s.accounts = append(s.accounts, a)
	}
}

// scanNull adapts a nullFloat to database/sql's Scan via a tiny wrapper.
func scanNull(n *nullFloat) *nullFloatScanner {
	return &nullFloatScanner{n}
}

type nullFloatScanner struct{ n *nullFloat }

func (s *nullFloatScanner) Scan(src any) error {
	if src == nil {
		s.n.valid = false
		return nil
	}
	switch v := src.(type) {
	case float64:
		s.n.value, s.n.valid = v, true
	case int64:
		s.n.value, s.n.valid = float64(v), true
	case []byte:
		var f float64
		if _, err := fmt.Sscanf(string(v), "%g", &f); err != nil {
			s.n.valid = false
			return nil
		}
		s.n.value, s.n.valid = f, true
	default:
		s.n.valid = false
	}
	return nil
}

// logErr emits structured, level=ERROR logs so dbmetrics query failures surface in
// the Grafana errors dashboard (the old fmt.Fprintf to stderr was unstructured and
// invisible to the `{level="ERROR"}` Loki query).
func logErr(what string, err error) {
	slog.Error("dbmetrics query failed", "query", what, "err", err)
}
