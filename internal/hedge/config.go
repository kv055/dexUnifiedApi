package hedge

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

//go:embed config.json
var embeddedConfigJSON []byte

type hedgeFileConfig struct {
	API struct {
		KrakenFuturesBaseURL string `json:"kraken_futures_base_url"`
		CoinbaseINTXBaseURL  string `json:"coinbase_intx_base_url"`
		HTTPTimeoutSeconds   int    `json:"http_timeout_seconds"`
	} `json:"api"`
	OptimizerDefaults struct {
		MaxSupportedLeverage float64 `json:"max_supported_leverage"`
		StressMovePct        float64 `json:"stress_move_pct"`
		TargetHedgeRatio     float64 `json:"target_hedge_ratio"`
		AllowPartialHedge    bool    `json:"allow_partial_hedge"`
		HedgeRatioStep       float64 `json:"hedge_ratio_step"`
		SearchStepEUR        float64 `json:"search_step_eur"`
		LeverageStep         float64 `json:"leverage_step"`
		SafetyBufferEUR      float64 `json:"safety_buffer_eur"`
	} `json:"optimizer_defaults"`
	Venues map[string]struct {
		Instrument            string  `json:"instrument"`
		MaxLeverage           float64 `json:"max_leverage"`
		MaintenanceMarginRate float64 `json:"maintenance_margin_rate"`
		FundingIntervalHours  float64 `json:"funding_interval_hours"`
		FallbackOpenFeeRate   float64 `json:"fallback_open_fee_rate"`
		FallbackCloseFeeRate  float64 `json:"fallback_close_fee_rate"`
		FallbackFundingRate   float64 `json:"fallback_funding_rate"`
	} `json:"venues"`
}

var (
	configOnce      sync.Once
	cachedConfig    hedgeFileConfig
	cachedConfigErr error
)

func loadHedgeConfig() (hedgeFileConfig, error) {
	configOnce.Do(func() {
		data := embeddedConfigJSON
		if override := os.Getenv("DEXMONITOR_HEDGE_CONFIG"); override != "" {
			b, err := os.ReadFile(override)
			if err != nil {
				cachedConfigErr = fmt.Errorf("read hedge config %s: %w", override, err)
				return
			}
			data = b
		}
		if err := json.Unmarshal(data, &cachedConfig); err != nil {
			cachedConfigErr = fmt.Errorf("parse hedge config: %w", err)
			return
		}
	})
	return cachedConfig, cachedConfigErr
}

func configuredMaxSupportedLeverage() float64 {
	cfg, err := loadHedgeConfig()
	if err != nil || cfg.OptimizerDefaults.MaxSupportedLeverage <= 0 {
		return 10
	}
	return cfg.OptimizerDefaults.MaxSupportedLeverage
}

func configuredKrakenFuturesBaseURL() string {
	cfg, err := loadHedgeConfig()
	if err != nil || cfg.API.KrakenFuturesBaseURL == "" {
		return "https://futures.kraken.com"
	}
	return cfg.API.KrakenFuturesBaseURL
}

func configuredCoinbaseINTXBaseURL() string {
	cfg, err := loadHedgeConfig()
	if err != nil || cfg.API.CoinbaseINTXBaseURL == "" {
		return "https://api.international.coinbase.com"
	}
	return cfg.API.CoinbaseINTXBaseURL
}

func configuredHTTPClient() *http.Client {
	cfg, err := loadHedgeConfig()
	timeout := 10 * time.Second
	if err == nil && cfg.API.HTTPTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.API.HTTPTimeoutSeconds) * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func applyDefaultOptimizerConfig(cfg Config) Config {
	fileCfg, err := loadHedgeConfig()
	if err != nil {
		return cfg
	}
	d := fileCfg.OptimizerDefaults
	if cfg.StressMovePct == 0 {
		cfg.StressMovePct = d.StressMovePct
	}
	if cfg.TargetHedgeRatio == 0 {
		cfg.TargetHedgeRatio = d.TargetHedgeRatio
	}
	if !cfg.AllowPartialHedge {
		cfg.AllowPartialHedge = d.AllowPartialHedge
	}
	if cfg.HedgeRatioStep == 0 {
		cfg.HedgeRatioStep = d.HedgeRatioStep
	}
	if cfg.SearchStepEUR == 0 {
		cfg.SearchStepEUR = d.SearchStepEUR
	}
	if cfg.LeverageStep == 0 {
		cfg.LeverageStep = d.LeverageStep
	}
	if cfg.SafetyBufferEUR == 0 {
		cfg.SafetyBufferEUR = d.SafetyBufferEUR
	}
	return cfg
}
