package main

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// appMetrics holds the unified-api's in-process instruments (live hub, websocket
// fan-out, snapshot ingest, account-health timing). It is nil under tests that
// never call newServerMetrics; every method is nil-safe so those paths are
// unaffected.
var appMetrics *serverMetrics

type serverMetrics struct {
	wsClients        prometheus.Gauge
	wsBroadcasts     prometheus.Counter
	liveOffersInHub  prometheus.Gauge
	liveOffersPushed prometheus.Counter
	healthCheckDur   *prometheus.HistogramVec
	healthChecks     *prometheus.CounterVec
	routeResolutions *prometheus.CounterVec
}

// newServerMetrics registers the unified-api instruments on reg.
func newServerMetrics(reg prometheus.Registerer) *serverMetrics {
	m := &serverMetrics{
		wsClients: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "dex_ws_clients",
			Help: "Active /ws/live_offers websocket subscribers.",
		}),
		wsBroadcasts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dex_ws_broadcasts_total",
			Help: "Live-offer payloads broadcast to websocket subscribers.",
		}),
		liveOffersInHub: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "dex_live_offers_in_hub",
			Help: "Live offers currently held in the in-memory hub.",
		}),
		liveOffersPushed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dex_live_offers_pushed_total",
			Help: "Snapshot-worker pushes received on /internal/live_offers.",
		}),
		healthCheckDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dex_account_health_check_duration_seconds",
			Help:    "Duration of an account health check, by provider.",
			Buckets: prometheus.DefBuckets,
		}, []string{"provider"}),
		healthChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dex_account_health_checks_total",
			Help: "Account health checks run, by provider and result (ok|fail).",
		}, []string{"provider", "result"}),
		routeResolutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dex_snapshot_route_resolutions_total",
			Help: "Offers resolved, blocked, or out of scope while building route books.",
		}, []string{"status", "reason"}),
	}
	reg.MustRegister(m.wsClients, m.wsBroadcasts, m.liveOffersInHub, m.liveOffersPushed,
		m.healthCheckDur, m.healthChecks, m.routeResolutions)
	return m
}

func (m *serverMetrics) observeRouteResolution(status, reason string) {
	if m == nil {
		return
	}
	if reason == "" {
		reason = "none"
	}
	m.routeResolutions.WithLabelValues(status, reason).Inc()
}

func (m *serverMetrics) incWSClient() {
	if m == nil {
		return
	}
	m.wsClients.Inc()
}

func (m *serverMetrics) decWSClient() {
	if m == nil {
		return
	}
	m.wsClients.Dec()
}

func (m *serverMetrics) observeBroadcast() {
	if m == nil {
		return
	}
	m.wsBroadcasts.Inc()
}

func (m *serverMetrics) setOffersInHub(n int) {
	if m == nil {
		return
	}
	m.liveOffersInHub.Set(float64(n))
}

func (m *serverMetrics) observePush() {
	if m == nil {
		return
	}
	m.liveOffersPushed.Inc()
}

// instrumentCheck wraps an account-health check func so each run records its
// duration and ok/fail outcome. Returns the original func unchanged when
// metrics are not wired (tests).
func (m *serverMetrics) instrumentCheck(provider string, check func(context.Context) error) func(context.Context) error {
	if m == nil {
		return check
	}
	return func(ctx context.Context) error {
		start := time.Now()
		err := check(ctx)
		m.healthCheckDur.WithLabelValues(provider).Observe(time.Since(start).Seconds())
		result := "ok"
		if err != nil {
			result = "fail"
		}
		m.healthChecks.WithLabelValues(provider, result).Inc()
		return err
	}
}
