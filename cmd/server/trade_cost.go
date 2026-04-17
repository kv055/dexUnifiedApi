package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"

	"dexunifiedapi/internal/bitcoin"
	"dexunifiedapi/internal/hedge"
)

// withdrawalFallbacks are the static BTC withdrawal fees used when the live exchange
// API is unavailable (no credentials, network error). Values are method-aware so
// route optimization can compare onchain and Lightning routes separately.
var withdrawalFallbacks = map[string]map[WithdrawalMethod]float64{
	"kraken": {
		MethodOnchain:   0.00015,
		MethodLightning: 0.0,
	},
	"binance": {
		MethodOnchain:   0.0000046,
		MethodLightning: 0.0,
	},
	"coinbase": {
		MethodOnchain:   0.0,
		MethodLightning: 0.0,
	},
	"bitstamp": {
		MethodOnchain: 0.0003,
	},
}

// takerFeeFallbacks are the static taker fees (as fraction) used when the live
// exchange API is unavailable or unsupported (e.g. Coinbase).
var takerFeeFallbacks = map[string]float64{
	"kraken":   0.0040,
	"binance":  0.0010,
	"coinbase": 0.0060,
	"bitstamp": 0.0050,
}

const (
	// feeTTL is the single refresh interval for all fee components:
	// CEX taker fee, CEX withdrawal fee, and BTC network fee.
	feeTTL = 30 * time.Minute

	// defaultBTCTradeSize is the fallback BTC amount used when an offer has no size set.
	defaultBTCTradeSize = 0.01
)

// loadCredentials reads {EXCHANGE}_API_KEY and {EXCHANGE}_API_SECRET from env.
func loadCredentials(exchange string) (key, secret string) {
	prefix := strings.ToUpper(exchange)
	return os.Getenv(prefix + "_API_KEY"), os.Getenv(prefix + "_API_SECRET")
}

// httpClient is shared across all fetchers.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// ---------------------------------------------------------------------------
// cachedFee — generic lazy-refresh cache for a single float64 fee value
// ---------------------------------------------------------------------------

type cachedFee struct {
	mu        sync.RWMutex
	value     float64
	fetchedAt time.Time
	ttl       time.Duration
}

func newCachedFee(ttl time.Duration) *cachedFee { return &cachedFee{ttl: ttl} }

// get returns the cached value if fresh, else calls refresh and caches the result.
// On refresh failure it keeps the last known value (or fallback if never fetched).
func (c *cachedFee) get(fallback float64, refresh func() (float64, error), label string) float64 {
	c.mu.RLock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl {
		v := c.value
		c.mu.RUnlock()
		return v
	}
	c.mu.RUnlock()

	v, err := refresh()
	c.mu.Lock()
	if err == nil {
		c.value = v
		c.fetchedAt = time.Now()
		fmt.Printf("[TradeCost] %s updated: %.8f (live)\n", label, v)
	} else {
		if c.fetchedAt.IsZero() {
			c.value = fallback
			fmt.Printf("[TradeCost] %s fetch failed (%v), using fallback %.8f\n", label, err, fallback)
		} else {
			fmt.Printf("[TradeCost] %s refresh failed (%v), keeping cached %.8f\n", label, err, c.value)
		}
		// Stamp fetchedAt even on failure so we don't retry every call for the TTL duration.
		c.fetchedAt = time.Now()
	}
	result := c.value
	c.mu.Unlock()
	return result
}

// ---------------------------------------------------------------------------
// Withdrawal fee fetcher
// ---------------------------------------------------------------------------

type withdrawalFeeFetcher struct {
	exchange  string
	apiKey    string
	apiSecret string
	caches    map[WithdrawalMethod]*cachedFee
}

func newWithdrawalFeeFetcher(exchange string) *withdrawalFeeFetcher {
	key, secret := loadCredentials(exchange)
	return &withdrawalFeeFetcher{
		exchange:  exchange,
		apiKey:    key,
		apiSecret: secret,
		caches: map[WithdrawalMethod]*cachedFee{
			MethodOnchain:   newCachedFee(feeTTL),
			MethodLightning: newCachedFee(feeTTL),
		},
	}
}

func (f *withdrawalFeeFetcher) get(method WithdrawalMethod) float64 {
	fallbacks := withdrawalFallbacks[f.exchange]
	fallback := fallbacks[method]
	cache := f.caches[method]
	if cache == nil {
		return fallback
	}

	label := fmt.Sprintf("%s %s withdrawal fee BTC", f.exchange, method)
	return cache.get(fallback, func() (float64, error) {
		return f.fetchLive(method)
	}, label)
}

func (f *withdrawalFeeFetcher) fetchLive(method WithdrawalMethod) (float64, error) {
	if f.apiKey == "" || f.apiSecret == "" {
		return 0, fmt.Errorf("no API credentials for %s", f.exchange)
	}
	switch f.exchange {
	case "kraken":
		return f.fetchKraken(method)
	case "binance":
		return f.fetchBinance(method)
	case "coinbase":
		if method == MethodOnchain || method == MethodLightning {
			return 0, nil
		}
		return 0, fmt.Errorf("coinbase does not support %s withdrawals", method)
	case "bitstamp":
		return f.fetchBitstamp(method)
	default:
		return 0, fmt.Errorf("unsupported exchange: %s", f.exchange)
	}
}

func (f *withdrawalFeeFetcher) fetchKraken(method WithdrawalMethod) (float64, error) {
	const urlPath = "/0/private/WithdrawMethods"
	nonce := strconv.FormatInt(time.Now().UnixMilli(), 10)
	form := url.Values{"nonce": {nonce}, "asset": {"XBT"}}
	encoded := form.Encode()

	sha256Sum := sha256.Sum256([]byte(nonce + encoded))
	hmacMsg := append([]byte(urlPath), sha256Sum[:]...)
	secret, err := base64.StdEncoding.DecodeString(f.apiSecret)
	if err != nil {
		return 0, fmt.Errorf("kraken: invalid api secret: %w", err)
	}
	mac := hmac.New(sha512.New, secret)
	mac.Write(hmacMsg)

	req, _ := http.NewRequest(http.MethodPost, "https://api.kraken.com"+urlPath, bytes.NewBufferString(encoded))
	req.Header.Set("API-Key", f.apiKey)
	req.Header.Set("API-Sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("kraken: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Error  []string `json:"error"`
		Result []struct {
			Asset   string `json:"asset"`
			Network string `json:"network"`
			Fee     string `json:"fee"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("kraken: decode: %w", err)
	}
	if len(result.Error) > 0 {
		return 0, fmt.Errorf("kraken API: %v", result.Error)
	}
	for _, m := range result.Result {
		if m.Asset != "XXBT" {
			continue
		}
		network := strings.ToLower(m.Network)
		switch method {
		case MethodLightning:
			if strings.Contains(network, "lightning") {
				return strconv.ParseFloat(m.Fee, 64)
			}
		case MethodOnchain:
			if network == "bitcoin" || network == "btc" || strings.Contains(network, "segwit") {
				return strconv.ParseFloat(m.Fee, 64)
			}
		}
	}
	return 0, fmt.Errorf("kraken: %s withdrawal method not found", method)
}

func (f *withdrawalFeeFetcher) fetchBinance(method WithdrawalMethod) (float64, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	qs := "timestamp=" + timestamp
	mac := hmac.New(sha256.New, []byte(f.apiSecret))
	mac.Write([]byte(qs))

	req, _ := http.NewRequest(http.MethodGet,
		"https://api.binance.com/sapi/v1/capital/config/getall?"+qs+"&signature="+hex.EncodeToString(mac.Sum(nil)), nil)
	req.Header.Set("X-MBX-APIKEY", f.apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("binance: %w", err)
	}
	defer resp.Body.Close()

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, fmt.Errorf("binance: decode: %w", err)
	}
	var errObj struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(raw, &errObj) == nil && errObj.Code != 0 {
		return 0, fmt.Errorf("binance API %d: %s", errObj.Code, errObj.Msg)
	}
	var coins []struct {
		Coin        string `json:"coin"`
		NetworkList []struct {
			Network     string `json:"network"`
			IsDefault   bool   `json:"isDefault"`
			WithdrawFee string `json:"withdrawFee"`
		} `json:"networkList"`
	}
	if err := json.Unmarshal(raw, &coins); err != nil {
		return 0, fmt.Errorf("binance: decode coins: %w", err)
	}
	for _, coin := range coins {
		if coin.Coin != "BTC" {
			continue
		}
		for _, net := range coin.NetworkList {
			network := strings.ToLower(net.Network)
			switch method {
			case MethodLightning:
				if strings.Contains(network, "lightning") || network == "ln" {
					return strconv.ParseFloat(net.WithdrawFee, 64)
				}
			case MethodOnchain:
				if network == "btc" || network == "bitcoin" || net.IsDefault {
					return strconv.ParseFloat(net.WithdrawFee, 64)
				}
			}
		}
	}
	return 0, fmt.Errorf("binance: %s withdrawal fee not found", method)
}

func (f *withdrawalFeeFetcher) fetchBitstamp(method WithdrawalMethod) (float64, error) {
	if method != MethodOnchain {
		return 0, fmt.Errorf("bitstamp does not support %s withdrawals", method)
	}

	const (
		urlHost = "www.bitstamp.net"
		urlPath = "/api/v2/fees/withdrawal/"
	)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.New().String()
	xAuth := "BITSTAMP " + f.apiKey
	message := xAuth + "POST" + urlHost + urlPath + "" + nonce + timestamp + "v2" + ""
	mac := hmac.New(sha256.New, []byte(f.apiSecret))
	mac.Write([]byte(message))

	req, _ := http.NewRequest(http.MethodPost, "https://"+urlHost+urlPath, nil)
	req.Header.Set("X-Auth", xAuth)
	req.Header.Set("X-Auth-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Auth-Nonce", nonce)
	req.Header.Set("X-Auth-Timestamp", timestamp)
	req.Header.Set("X-Auth-Version", "v2")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bitstamp: %w", err)
	}
	defer resp.Body.Close()

	var entries []struct {
		Currency string `json:"currency"`
		Fee      string `json:"fee"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return 0, fmt.Errorf("bitstamp: decode: %w", err)
	}
	for _, e := range entries {
		if strings.EqualFold(e.Currency, "BTC") {
			return strconv.ParseFloat(e.Fee, 64)
		}
	}
	return 0, fmt.Errorf("bitstamp: BTC withdrawal fee not found")
}

// ---------------------------------------------------------------------------
// Taker fee fetcher
// ---------------------------------------------------------------------------
//
// Supported: Kraken (TradeVolume), Binance (tradeFee), Bitstamp (fees/trading).
// Coinbase: no clean endpoint across API versions — static fallback only.

type takerFeeFetcher struct {
	exchange  string
	apiKey    string
	apiSecret string
	cache     *cachedFee
}

func newTakerFeeFetcher(exchange string) *takerFeeFetcher {
	key, secret := loadCredentials(exchange)
	return &takerFeeFetcher{
		exchange:  exchange,
		apiKey:    key,
		apiSecret: secret,
		cache:     newCachedFee(feeTTL),
	}
}

func (f *takerFeeFetcher) get() float64 {
	label := f.exchange + " taker fee fraction"
	fallback := takerFeeFallbacks[f.exchange]
	return f.cache.get(fallback, f.fetchLive, label)
}

func (f *takerFeeFetcher) fetchLive() (float64, error) {
	if f.apiKey == "" || f.apiSecret == "" {
		return 0, fmt.Errorf("no API credentials for %s", f.exchange)
	}
	switch f.exchange {
	case "kraken":
		return f.fetchKraken()
	case "binance":
		return f.fetchBinance()
	case "coinbase":
		// Coinbase has no stable single endpoint for user taker fee across API versions.
		return 0, fmt.Errorf("coinbase taker fee not fetchable, using static fallback")
	case "bitstamp":
		return f.fetchBitstamp()
	default:
		return 0, fmt.Errorf("unsupported exchange: %s", f.exchange)
	}
}

// fetchKraken fetches the user's current taker fee via POST /0/private/TradeVolume.
// The API returns the fee as a percent string (e.g. "0.26" = 0.26%), which is
// divided by 100 to produce a fraction.
func (f *takerFeeFetcher) fetchKraken() (float64, error) {
	const urlPath = "/0/private/TradeVolume"
	nonce := strconv.FormatInt(time.Now().UnixMilli(), 10)
	// Request fee schedule for the BTC/EUR pair.
	form := url.Values{"nonce": {nonce}, "pair": {"XBTEUR"}}
	encoded := form.Encode()

	sha256Sum := sha256.Sum256([]byte(nonce + encoded))
	hmacMsg := append([]byte(urlPath), sha256Sum[:]...)
	secret, err := base64.StdEncoding.DecodeString(f.apiSecret)
	if err != nil {
		return 0, fmt.Errorf("kraken: invalid api secret: %w", err)
	}
	mac := hmac.New(sha512.New, secret)
	mac.Write(hmacMsg)

	req, _ := http.NewRequest(http.MethodPost, "https://api.kraken.com"+urlPath, bytes.NewBufferString(encoded))
	req.Header.Set("API-Key", f.apiKey)
	req.Header.Set("API-Sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("kraken: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Error  []string `json:"error"`
		Result struct {
			Fees map[string]struct {
				Fee string `json:"fee"` // percent string, e.g. "0.26"
			} `json:"fees"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("kraken: decode: %w", err)
	}
	if len(result.Error) > 0 {
		return 0, fmt.Errorf("kraken API: %v", result.Error)
	}
	// Kraken uses its internal pair name "XXBTZEUR" in the response.
	for _, key := range []string{"XXBTZEUR", "XBTEUR"} {
		if entry, ok := result.Result.Fees[key]; ok {
			pct, err := strconv.ParseFloat(entry.Fee, 64)
			if err != nil {
				return 0, fmt.Errorf("kraken: parse fee %q: %w", entry.Fee, err)
			}
			return pct / 100, nil // convert percent to fraction
		}
	}
	return 0, fmt.Errorf("kraken: XBTEUR taker fee not found in TradeVolume response")
}

// fetchBinance fetches the user's taker fee via GET /sapi/v1/asset/tradeFee.
// The API returns the fee already as a fraction (e.g. "0.001" = 0.1%).
// Queries BTCUSDT as representative — Binance taker fees are account-wide (VIP tier).
func (f *takerFeeFetcher) fetchBinance() (float64, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	qs := "symbol=BTCUSDT&timestamp=" + timestamp
	mac := hmac.New(sha256.New, []byte(f.apiSecret))
	mac.Write([]byte(qs))

	req, _ := http.NewRequest(http.MethodGet,
		"https://api.binance.com/sapi/v1/asset/tradeFee?"+qs+"&signature="+hex.EncodeToString(mac.Sum(nil)), nil)
	req.Header.Set("X-MBX-APIKEY", f.apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("binance: %w", err)
	}
	defer resp.Body.Close()

	var entries []struct {
		Symbol          string `json:"symbol"`
		MakerCommission string `json:"makerCommission"`
		TakerCommission string `json:"takerCommission"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return 0, fmt.Errorf("binance: decode: %w", err)
	}
	for _, e := range entries {
		if e.Symbol == "BTCUSDT" {
			return strconv.ParseFloat(e.TakerCommission, 64) // already a fraction
		}
	}
	return 0, fmt.Errorf("binance: BTCUSDT taker fee not found")
}

// fetchBitstamp fetches the user's taker fee via POST /api/v2/fees/trading/.
// The API returns the fee as a percent string (e.g. "0.50" = 0.50%), which is
// divided by 100 to produce a fraction.
func (f *takerFeeFetcher) fetchBitstamp() (float64, error) {
	const (
		urlHost = "www.bitstamp.net"
		urlPath = "/api/v2/fees/trading/"
	)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.New().String()
	xAuth := "BITSTAMP " + f.apiKey
	message := xAuth + "POST" + urlHost + urlPath + "" + nonce + timestamp + "v2" + ""
	mac := hmac.New(sha256.New, []byte(f.apiSecret))
	mac.Write([]byte(message))

	req, _ := http.NewRequest(http.MethodPost, "https://"+urlHost+urlPath, nil)
	req.Header.Set("X-Auth", xAuth)
	req.Header.Set("X-Auth-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Auth-Nonce", nonce)
	req.Header.Set("X-Auth-Timestamp", timestamp)
	req.Header.Set("X-Auth-Version", "v2")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bitstamp: %w", err)
	}
	defer resp.Body.Close()

	var entries []struct {
		Market string `json:"market"`
		Fees   struct {
			Taker string `json:"taker"` // percent string, e.g. "0.50"
		} `json:"fees"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return 0, fmt.Errorf("bitstamp: decode: %w", err)
	}
	for _, e := range entries {
		if e.Market == "BTC/EUR" {
			pct, err := strconv.ParseFloat(e.Fees.Taker, 64)
			if err != nil {
				return 0, fmt.Errorf("bitstamp: parse taker fee %q: %w", e.Fees.Taker, err)
			}
			return pct / 100, nil // convert percent to fraction
		}
	}
	return 0, fmt.Errorf("bitstamp: BTC/EUR taker fee not found")
}

// ---------------------------------------------------------------------------
// DEX taker fees
// ---------------------------------------------------------------------------

// dexTakerFees are the configured taker fees for each DEX platform (as fraction).
// They act as fallbacks for live sources and as the canonical values for static-fee DEXes.
// Bisq:     1.15% paid in BTC (taker side). Source: bisq.wiki/Trading_fees
// RoboSats: 0.175% — fetched live from /api/info/; this is the fallback.
// HodlHodl: 0.5% — both sides pay; no API endpoint. Source: hodlhodl.com/pages/faq
var dexTakerFees = map[string]float64{
	"bisq":     0.0115,
	"robosats": 0.00175,
	"hodlhodl": 0.005,
}

// robosatsOnions are the coordinator onion addresses, tried in order.
// Matches the list in cmd/snapshot/main.go.
var robosatsOnions = []string{
	"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion",
	"http://mmhaqzuirth5rx7gl24d4773lknltjhik57k7ahec5iefktezv4b3uid.onion",
	"http://4t4jxmivv6uqej6xzx2jx3fxh75gtt65v3szjoqmc4ugdlhipzdat6yd.onion",
	"http://otmoonrndnrddqdlhu6b36heunmbyw3cgvadqo2oqeau3656wfv7fwad.onion",
}

// newTorHTTPClient returns an HTTP client routed through the local Tor SOCKS5 proxy.
func newTorHTTPClient() (*http.Client, error) {
	torProxyAddr := os.Getenv("TOR_PROXY_ADDR")
	if torProxyAddr == "" {
		torProxyAddr = "127.0.0.1:9050"
	}
	dialer, err := proxy.SOCKS5("tcp", torProxyAddr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("tor proxy: %w", err)
	}
	return &http.Client{
		Transport: &http.Transport{Dial: dialer.Dial},
		Timeout:   15 * time.Second,
	}, nil
}

// fetchRobosatsTakerFee fetches the taker_fee from GET /api/info/ on each
// coordinator in turn, returning the first successful result.
func fetchRobosatsTakerFee() (float64, error) {
	torHTTP, err := newTorHTTPClient()
	if err != nil {
		return 0, err
	}
	var lastErr error
	for _, onion := range robosatsOnions {
		resp, err := torHTTP.Get(onion + "/api/info/")
		if err != nil {
			lastErr = err
			continue
		}
		var info struct {
			TakerFee float64 `json:"taker_fee"`
		}
		err = json.NewDecoder(resp.Body).Decode(&info)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if info.TakerFee <= 0 {
			lastErr = fmt.Errorf("coordinator returned taker_fee=0")
			continue
		}
		return info.TakerFee, nil
	}
	return 0, fmt.Errorf("all coordinators failed, last: %w", lastErr)
}

// ---------------------------------------------------------------------------
// TradeCostEstimator
// ---------------------------------------------------------------------------

// TradeCostEstimator estimates the total cost of one CEX→DEX arbitrage cycle:
//  1. CEX taker fee — fetched live from exchange API where available, static fallback
//  2. CEX BTC withdrawal fee — fetched live from exchange API, static fallback
//  3. Bitcoin on-chain network fee — congestion-adaptive, dynamic vsize, cached 5 min
//  4. DEX platform fee — static per-exchange taker rate
//
// All components are expressed as a percentage of the trade value so the result
// can be directly compared to / subtracted from the DEX offer premium.
type TradeCostEstimator struct {
	exchange           string
	taker              *takerFeeFetcher
	withdrawal         *withdrawalFeeFetcher
	dexFees            map[string]*cachedFee
	btcPredictor       *bitcoin.BitcoinFeePredictor
	cexProfile         CexWithdrawalProfile
	networkFeeOverride func(WithdrawalMethod, float64) networkFeeDetails
	hedgeCostOverride  func(btcAmount float64, refPriceEUR float64, transferMinutes float64) hedgeCostDetails
}

func NewTradeCostEstimator(exchange string) *TradeCostEstimator {
	torClient, err := newTorHTTPClient()
	if err != nil {
		fmt.Printf("[TradeCost] Tor client unavailable, bitcoin fee fetcher will use direct HTTP: %v\n", err)
		torClient = nil
	}
	return &TradeCostEstimator{
		exchange:   exchange,
		taker:      newTakerFeeFetcher(exchange),
		withdrawal: newWithdrawalFeeFetcher(exchange),
		dexFees: map[string]*cachedFee{
			"bisq":     newCachedFee(feeTTL),
			"robosats": newCachedFee(feeTTL),
			"hodlhodl": newCachedFee(feeTTL),
		},
		btcPredictor: bitcoin.NewBitcoinFeePredictorWithClient(torClient),
		cexProfile:   cexProfileFor(exchange),
	}
}

func (e *TradeCostEstimator) getDexFeeFrac(dexExchange string) float64 {
	fallback := dexTakerFees[dexExchange]
	cache := e.dexFees[dexExchange]
	if cache == nil {
		return fallback
	}

	refresh := func() (float64, error) {
		return fallback, nil
	}
	label := dexExchange + " DEX taker fee"

	if dexExchange == "robosats" {
		refresh = fetchRobosatsTakerFee
	}

	return cache.get(fallback, refresh, label)
}

type TradeRouteOptimizer struct {
	estimators map[string]*TradeCostEstimator
}

func NewTradeRouteOptimizer(estimators map[string]*TradeCostEstimator) *TradeRouteOptimizer {
	return &TradeRouteOptimizer{estimators: estimators}
}

// BTCNetworkFeeDetail carries the full onchain fee calculation context for display.
// Only populated for onchain transfers; nil for Lightning.
type BTCNetworkFeeDetail struct {
	TxVBytes                        int64   `json:"tx_vbytes"` // calculated virtual size (per tx)
	TxType                          string  `json:"tx_type"`   // "P2WPKH", "P2TR", etc.
	TxInputs                        int     `json:"tx_inputs"`
	TxOutputs                       int     `json:"tx_outputs"`
	NumTxs                          int     `json:"num_txs"`         // number of transactions (2 for bid side, 1 for ask side)
	SelectedSatVB                   int64   `json:"selected_sat_vb"` // fee rate for the chosen tier
	RequiredConfirmations           int     `json:"required_confirmations"`
	SelectedInclusionMinutes        int     `json:"selected_inclusion_minutes"`
	SelectedFullConfirmationMinutes int     `json:"selected_full_confirmation_minutes"`
	EconomicSatVB                   int64   `json:"economic_sat_vb"` // all-tier rates for comparison
	EconomicInclusionMinutes        int     `json:"economic_inclusion_minutes"`
	EconomicFullConfirmationMinutes int     `json:"economic_full_confirmation_minutes"`
	StandardSatVB                   int64   `json:"standard_sat_vb"`
	StandardInclusionMinutes        int     `json:"standard_inclusion_minutes"`
	StandardFullConfirmationMinutes int     `json:"standard_full_confirmation_minutes"`
	FastSatVB                       int64   `json:"fast_sat_vb"`
	FastInclusionMinutes            int     `json:"fast_inclusion_minutes"`
	FastFullConfirmationMinutes     int     `json:"fast_full_confirmation_minutes"`
	ExpressSatVB                    int64   `json:"express_sat_vb"`
	ExpressInclusionMinutes         int     `json:"express_inclusion_minutes"`
	ExpressFullConfirmationMinutes  int     `json:"express_full_confirmation_minutes"`
	CongestionLevel                 string  `json:"congestion_level"`
	MempoolSizeMB                   float64 `json:"mempool_size_mb"`
	UnconfirmedTxCount              int64   `json:"unconfirmed_tx_count"`
	DataAgeMinutes                  int     `json:"data_age_minutes"`
}

// networkFeeDetails bundles the fee amount with metadata about how it was calculated.
type networkFeeDetails struct {
	feeBTC     float64
	method     WithdrawalMethod
	tier       string  // "economic", "standard", "fast", "express", "lightning"
	confidence float64 // 0–1.0
	detail     *BTCNetworkFeeDetail
}

type hedgeCostDetails struct {
	feeBTC          float64
	feePct          float64
	openFeeBTC      float64
	openFeePct      float64
	closeFeeBTC     float64
	closeFeePct     float64
	fundingFeeBTC   float64
	fundingFeePct   float64
	venue           string
	instrument      string
	transferMinutes float64
	position        *HedgePositionDetail
}

func resolveRequiredConfirmations(direction string, requested int) int {
	if requested >= 1 && requested <= 6 {
		return requested
	}
	if direction == "sell" {
		return 4
	}
	return 2
}

// CostPct returns the estimated total arbitrage cost as a percentage of btcAmount.
// dexExchange is the name of the DEX the offer is on (e.g. "bisq", "robosats", "hodlhodl").
// A DEX offer premium must exceed this value to be profitable.
func (e *TradeCostEstimator) CostPct(btcAmount float64, dexExchange string) float64 {
	return e.CostPctWithConfirmations(btcAmount, dexExchange, 1)
}

func (e *TradeCostEstimator) CostPctWithConfirmations(btcAmount float64, dexExchange string, requiredConfirmations int) float64 {
	if btcAmount <= 0 {
		return 0
	}
	method := resolveWithdrawalMethod(e.cexProfile, dexExchange)
	return e.costPctForMethod(btcAmount, dexExchange, method, requiredConfirmations)
}

func (e *TradeCostEstimator) costPctForMethod(btcAmount float64, dexExchange string, method WithdrawalMethod, requiredConfirmations int) float64 {
	if btcAmount <= 0 {
		return 0
	}
	takerFrac := e.taker.get()
	withdrawalFee := e.withdrawal.get(method)
	netFee := e.getNetworkFeeForMethod(method, btcAmount, requiredConfirmations)
	dexFeeFrac := e.getDexFeeFrac(dexExchange)

	tradingFee := btcAmount * takerFrac
	dexFee := btcAmount * dexFeeFrac
	return (tradingFee + withdrawalFee + netFee.feeBTC + dexFee) / btcAmount * 100
}

// getNetworkFee returns the network fee for a CEX→DEX transfer, taking into account
// the settlement method supported by the selected CEX and DEX combination.
//
//   - Lightning: both CEX and DEX support LN → returns a small constant routing estimate.
//   - Onchain: uses the CEX's address format and typical tx structure for an accurate
//     congestion-adaptive prediction via the bitcoin fee predictor.
func (e *TradeCostEstimator) getNetworkFee(dexExchange string, btcAmount float64) networkFeeDetails {
	method := resolveWithdrawalMethod(e.cexProfile, dexExchange)
	return e.getNetworkFeeForMethod(method, btcAmount, 1)
}

func (e *TradeCostEstimator) getNetworkFeeForMethod(method WithdrawalMethod, btcAmount float64, requiredConfirmations int) networkFeeDetails {
	return e.getNetworkFeeForMethodWithTier(method, btcAmount, requiredConfirmations, "")
}

func (e *TradeCostEstimator) getNetworkFeeForMethodWithTier(method WithdrawalMethod, btcAmount float64, requiredConfirmations int, feeTier string) networkFeeDetails {
	if e.networkFeeOverride != nil {
		return e.networkFeeOverride(method, btcAmount)
	}

	if method == MethodLightning {
		if e.btcPredictor.IsDataStale(feeTTL) {
			if err := e.btcPredictor.FetchLatestData(); err != nil {
				fmt.Printf("[TradeCost] lightning fee fetch failed: %v\n", err)
			}
		}
		return networkFeeDetails{
			feeBTC:     e.btcPredictor.EstimateLightningRoutingFee(btcAmount),
			method:     MethodLightning,
			tier:       "lightning",
			confidence: 0.5,
		}
	}

	// Onchain: refresh predictor cache if stale.
	if e.btcPredictor.IsDataStale(feeTTL) {
		if err := e.btcPredictor.FetchLatestData(); err != nil {
			fmt.Printf("[TradeCost] bitcoin fee fetch failed: %v\n", err)
		}
	}

	ctx, err := e.btcPredictor.GetDetailedRecommendationForTier(
		e.cexProfile.TypicalInputs,
		e.cexProfile.TypicalOutputs,
		e.cexProfile.OnchainTxType,
		requiredConfirmations,
		feeTier,
	)
	if err != nil || ctx.Recommendation == nil {
		return networkFeeDetails{method: MethodOnchain}
	}

	var detail *BTCNetworkFeeDetail
	if ctx.Estimate != nil {
		detail = &BTCNetworkFeeDetail{
			TxVBytes:                        ctx.Estimate.TransactionSize,
			TxType:                          ctx.TxType,
			TxInputs:                        ctx.TxInputs,
			TxOutputs:                       ctx.TxOutputs,
			SelectedSatVB:                   ctx.Recommendation.FeeRateSatVB,
			RequiredConfirmations:           requiredConfirmations,
			SelectedInclusionMinutes:        int(ctx.Recommendation.EstimatedInclusionTime / time.Minute),
			SelectedFullConfirmationMinutes: int(ctx.Recommendation.EstimatedFullConfirmationTime / time.Minute),
			CongestionLevel:                 ctx.Estimate.NetworkCongestion,
			DataAgeMinutes:                  ctx.DataAgeMinutes,
		}
		if ctx.FeeRates != nil {
			detail.EconomicSatVB = ctx.FeeRates.EconomicFee
			detail.EconomicInclusionMinutes = int(ctx.Estimate.EconomicInclusionTime / time.Minute)
			detail.EconomicFullConfirmationMinutes = int(ctx.Estimate.EconomicTime / time.Minute)
			detail.StandardSatVB = ctx.FeeRates.StandardFee
			detail.StandardInclusionMinutes = int(ctx.Estimate.StandardInclusionTime / time.Minute)
			detail.StandardFullConfirmationMinutes = int(ctx.Estimate.StandardTime / time.Minute)
			detail.FastSatVB = ctx.FeeRates.FastFee
			detail.FastInclusionMinutes = int(ctx.Estimate.FastInclusionTime / time.Minute)
			detail.FastFullConfirmationMinutes = int(ctx.Estimate.FastTime / time.Minute)
			detail.ExpressSatVB = ctx.FeeRates.ExpressFee
			detail.ExpressInclusionMinutes = int(ctx.Estimate.ExpressInclusionTime / time.Minute)
			detail.ExpressFullConfirmationMinutes = int(ctx.Estimate.ExpressTime / time.Minute)
		}
		if ctx.NetworkStats != nil {
			detail.MempoolSizeMB = math.Round(float64(ctx.NetworkStats.MempoolSize)/(1024*1024)*100) / 100
			detail.UnconfirmedTxCount = ctx.NetworkStats.TransactionCount
		}
	}

	confidence := 0.0
	if ctx.Estimate != nil {
		confidence = ctx.Estimate.EstimatedConfidence
	}
	return networkFeeDetails{
		feeBTC:     ctx.Recommendation.TotalFeeBTC,
		method:     MethodOnchain,
		tier:       ctx.Recommendation.Tier,
		confidence: confidence,
		detail:     detail,
	}
}

// ---------------------------------------------------------------------------
// CostBreakdown — per-component cost details
// ---------------------------------------------------------------------------

// CostBreakdown holds each component of the arbitrage cost, all expressed both
// as absolute BTC amounts and as percentages of the trade value.
type CostBreakdown struct {
	BTCAmount            float64              `json:"btc_amount"`             // trade size used for calculation
	RouteCEX             string               `json:"route_cex"`              // chosen CEX for the cheapest route
	RouteDEX             string               `json:"route_dex"`              // DEX of the evaluated offer
	RouteMethod          string               `json:"route_method"`           // chosen settlement method for the cheapest route
	TradeDirection       string               `json:"trade_direction"`        // "bid" (buy CEX→sell DEX) or "ask" (buy DEX→sell CEX)
	NumNetworkTxs        int                  `json:"num_network_txs"`        // 2 for bid side (pre-approved addr hop), 1 for ask side
	EstimatedProfitPct   float64              `json:"estimated_profit_pct"`   // net premium after costs as % of trade
	EstimatedProfitBTC   float64              `json:"estimated_profit_btc"`   // estimated profit in BTC for the chosen trade size
	CexTakerFeePct       float64              `json:"cex_taker_fee_pct"`      // CEX taker fee as % of trade
	CexTakerFeeBTC       float64              `json:"cex_taker_fee_btc"`      // CEX taker fee in BTC
	WithdrawalFeePct     float64              `json:"withdrawal_fee_pct"`     // CEX BTC withdrawal fee as % of trade (0 for ask side)
	WithdrawalFeeBTC     float64              `json:"withdrawal_fee_btc"`     // CEX BTC withdrawal fee in BTC (0 for ask side)
	NetworkFeePct        float64              `json:"network_fee_pct"`        // settlement network fee as % of trade
	NetworkFeeBTC        float64              `json:"network_fee_btc"`        // settlement network fee in BTC
	NetworkFeeMethod     string               `json:"network_fee_method"`     // "onchain" or "lightning"
	NetworkFeeTier       string               `json:"network_fee_tier"`       // "economic", "standard", "fast", "express", "lightning"
	NetworkFeeConfidence float64              `json:"network_fee_confidence"` // 0–1.0 estimate reliability
	NetworkFeeDetail     *BTCNetworkFeeDetail `json:"network_fee_detail"`     // full onchain calc context; nil for Lightning
	Hedge                HedgeBreakdown       `json:"hedge"`                  // perp hedge detail grouped under its own nested object
	DexFeePct            float64              `json:"dex_fee_pct"`            // DEX platform fee as % of trade
	DexFeeBTC            float64              `json:"dex_fee_btc"`            // DEX platform fee in BTC
	TotalCostPct         float64              `json:"total_cost_pct"`         // sum of all components as % of trade
	TotalCostBTC         float64              `json:"total_cost_btc"`         // sum of all components in BTC
}

type HedgeBreakdown struct {
	OpenFeePct      float64              `json:"open_fee_pct"`      // perp opening fee as % of trade
	OpenFeeBTC      float64              `json:"open_fee_btc"`      // perp opening fee in BTC
	CloseFeePct     float64              `json:"close_fee_pct"`     // perp closing fee as % of trade
	CloseFeeBTC     float64              `json:"close_fee_btc"`     // perp closing fee in BTC
	FundingFeePct   float64              `json:"funding_fee_pct"`   // perp funding cost over transfer window as % of trade
	FundingFeeBTC   float64              `json:"funding_fee_btc"`   // perp funding cost over transfer window in BTC
	FeeSumPct       float64              `json:"fee_sum_pct"`       // perp open+close+funding as % of trade
	FeeSumBTC       float64              `json:"fee_sum_btc"`       // perp open+close+funding in BTC
	Venue           string               `json:"venue"`             // chosen hedge venue
	Instrument      string               `json:"instrument"`        // chosen perp instrument
	TransferMinutes int                  `json:"transfer_minutes"`  // transfer-time input used for hedge costing
	Position        *HedgePositionDetail `json:"position"`          // position sizing simulation; nil when unavailable (e.g. ask side or missing ref price)
}

// HedgePositionDetail is the per-offer simulation of how the short perp hedge
// would be executed: hedge notional, leverage, collateral, and liquidation
// headroom. All EUR amounts are rounded to 2 decimals; rates are unrounded.
type HedgePositionDetail struct {
	HedgeRatio                   float64 `json:"hedge_ratio"`                       // fraction of spot transfer that is hedged (1.0 = full)
	LeverageUsed                 float64 `json:"leverage_used"`                     // leverage assumed for sizing (venue max)
	HedgeNotionalEUR             float64 `json:"hedge_notional_eur"`                // short perp notional in EUR
	InitialMarginEUR             float64 `json:"initial_margin_eur"`                // required initial margin = notional / leverage
	RecommendedCollateralEUR     float64 `json:"recommended_collateral_eur"`        // collateral to post so the position survives the stress move
	StressMovePct                float64 `json:"stress_move_pct"`                   // adverse BTC move assumed in the stress scenario (e.g. 0.10)
	StressLossEUR                float64 `json:"stress_loss_eur"`                   // unrealized loss on the short under the stress move
	MaintenanceRequirementEUR    float64 `json:"maintenance_requirement_eur"`       // venue maintenance margin on the notional
	FreeCollateralAfterStressEUR float64 `json:"free_collateral_after_stress_eur"`  // equity left above maintenance after the stress move
	LiquidationMovePct           float64 `json:"liquidation_move_pct"`              // adverse BTC move that would liquidate the position at the chosen leverage
	BTCPriceEUR                  float64 `json:"btc_price_eur"`                     // CEX ref price used to size the notional
}

func round8(v float64) float64 {
	return math.Round(v*1e8) / 1e8
}

func round4(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func round2(v float64) float64 {
	return math.Round(v*1e2) / 1e2
}

// Breakdown returns a full per-component cost breakdown for a given trade size, DEX, and offer direction.
// direction should be the DEX offer direction: "buy" (bid side) or "sell" (ask side).
func (e *TradeCostEstimator) Breakdown(btcAmount float64, dexExchange string, direction string) CostBreakdown {
	return e.BreakdownWithConfirmations(btcAmount, dexExchange, direction, 0)
}

func (e *TradeCostEstimator) BreakdownWithConfirmations(btcAmount float64, dexExchange string, direction string, requiredConfirmations int) CostBreakdown {
	if btcAmount <= 0 {
		return CostBreakdown{}
	}
	requiredConfirmations = resolveRequiredConfirmations(direction, requiredConfirmations)
	method := resolveWithdrawalMethod(e.cexProfile, dexExchange)
	return e.breakdownForMethod(btcAmount, 0, dexExchange, method, direction, requiredConfirmations, false)
}

func (e *TradeCostEstimator) breakdownForMethod(btcAmount float64, refPriceEUR float64, dexExchange string, method WithdrawalMethod, direction string, requiredConfirmations int, coupledHedge bool) CostBreakdown {
	return e.breakdownForMethodWithTier(btcAmount, refPriceEUR, dexExchange, method, direction, requiredConfirmations, "", coupledHedge)
}

func (e *TradeCostEstimator) breakdownForMethodWithTier(btcAmount float64, refPriceEUR float64, dexExchange string, method WithdrawalMethod, direction string, requiredConfirmations int, feeTier string, coupledHedge bool) CostBreakdown {
	if btcAmount <= 0 {
		return CostBreakdown{}
	}
	takerFrac := e.taker.get()
	dexFeeFrac := e.getDexFeeFrac(dexExchange)
	netFee := e.getNetworkFeeForMethodWithTier(method, btcAmount, requiredConfirmations, feeTier)

	// Ask side: buyer purchases BTC from DEX seller then deposits to CEX — no CEX withdrawal fee,
	// and only 1 network transaction (DEX wallet → CEX deposit address).
	// Bid side (default): buyer purchases BTC on CEX then withdraws — withdrawal fee applies,
	// and 2 network transactions are needed because withdrawals must go via a pre-approved address.
	isBid := direction != "sell"
	tradeDir := "bid"
	withdrawalFee := 0.0
	numNetworkTxs := 1
	if isBid {
		withdrawalFee = e.withdrawal.get(method)
		numNetworkTxs = 2
	} else {
		tradeDir = "ask"
	}

	if netFee.detail != nil {
		netFee.detail.NumTxs = numNetworkTxs
	}

	takerBTC := btcAmount * takerFrac
	dexBTC := btcAmount * dexFeeFrac
	networkFeeBTC := netFee.feeBTC * float64(numNetworkTxs)
	hedgeCost := hedgeCostDetails{}
	if isBid {
		transferMinutes := 0.0
		if netFee.detail != nil && netFee.detail.SelectedFullConfirmationMinutes > 0 {
			transferMinutes = float64(netFee.detail.SelectedFullConfirmationMinutes * numNetworkTxs)
		}
		hedgeCost = e.estimateBestHedgeCost(btcAmount, refPriceEUR, transferMinutes, coupledHedge)
	}

	totalBTC := takerBTC + withdrawalFee + networkFeeBTC + hedgeCost.feeBTC + dexBTC
	totalPct := totalBTC / btcAmount * 100

	pct := func(btc float64) float64 { return round4(btc / btcAmount * 100) }

	return CostBreakdown{
		BTCAmount:            round8(btcAmount),
		RouteCEX:             e.exchange,
		RouteDEX:             dexExchange,
		RouteMethod:          string(method),
		TradeDirection:       tradeDir,
		NumNetworkTxs:        numNetworkTxs,
		CexTakerFeePct:       pct(takerBTC),
		CexTakerFeeBTC:       round8(takerBTC),
		WithdrawalFeePct:     pct(withdrawalFee),
		WithdrawalFeeBTC:     round8(withdrawalFee),
		NetworkFeePct:        pct(networkFeeBTC),
		NetworkFeeBTC:        round8(networkFeeBTC),
		NetworkFeeMethod:     string(netFee.method),
		NetworkFeeTier:       netFee.tier,
		NetworkFeeConfidence: round4(netFee.confidence),
		NetworkFeeDetail:     netFee.detail,
		Hedge: HedgeBreakdown{
			OpenFeePct:      pct(hedgeCost.openFeeBTC),
			OpenFeeBTC:      round8(hedgeCost.openFeeBTC),
			CloseFeePct:     pct(hedgeCost.closeFeeBTC),
			CloseFeeBTC:     round8(hedgeCost.closeFeeBTC),
			FundingFeePct:   pct(hedgeCost.fundingFeeBTC),
			FundingFeeBTC:   round8(hedgeCost.fundingFeeBTC),
			FeeSumPct:       pct(hedgeCost.feeBTC),
			FeeSumBTC:       round8(hedgeCost.feeBTC),
			Venue:           hedgeCost.venue,
			Instrument:      hedgeCost.instrument,
			TransferMinutes: int(math.Round(hedgeCost.transferMinutes)),
			Position:        hedgeCost.position,
		},
		DexFeePct:            pct(dexBTC),
		DexFeeBTC:            round8(dexBTC),
		TotalCostPct:         round4(totalPct),
		TotalCostBTC:         round8(totalBTC),
	}
}

// cexToHedgeVenue maps a spot CEX name to its paired perp hedge venue.
// Used in coupled mode to ensure the same exchange is used for both legs.
var cexToHedgeVenue = map[string]hedge.Venue{
	"kraken":   hedge.VenueKrakenFutures,
	"coinbase": hedge.VenueCoinbaseFutures,
}

func (e *TradeCostEstimator) estimateBestHedgeCost(btcAmount float64, refPriceEUR float64, transferMinutes float64, coupledHedge bool) hedgeCostDetails {
	if e.hedgeCostOverride != nil {
		return e.hedgeCostOverride(btcAmount, refPriceEUR, transferMinutes)
	}
	if btcAmount <= 0 {
		return hedgeCostDetails{}
	}

	allCandidates := []hedge.Venue{hedge.VenueKrakenFutures, hedge.VenueCoinbaseFutures}
	candidates := allCandidates
	if coupledHedge {
		if paired, ok := cexToHedgeVenue[e.exchange]; ok {
			candidates = []hedge.Venue{paired}
		}
	}
	best := hedgeCostDetails{}
	found := false

	for _, venue := range candidates {
		profile, err := hedge.DefaultVenueProfile(venue)
		if err != nil {
			continue
		}

		inputs, err := hedge.FetchDefaultVenueInputs(venue)
		if err != nil {
			inputs, err = hedge.FallbackVenueInputs(venue)
			if err != nil {
				continue
			}
		}

		cfg := hedge.Config{TransferMinutes: transferMinutes}
		cfg = hedge.ApplyVenueProfile(cfg, profile)
		cfg = hedge.ApplyLivePerpInputs(cfg, inputs)
		openFeeBTC := btcAmount * cfg.PerpOpenFeeRate
		closeFeeBTC := btcAmount * cfg.PerpCloseFeeRate
		fundingFeeBTC := btcAmount * cfg.FundingCostRate
		feeBTC := openFeeBTC + closeFeeBTC + fundingFeeBTC
		rate := cfg.PerpOpenFeeRate + cfg.PerpCloseFeeRate + cfg.FundingCostRate

		var position *HedgePositionDetail
		if refPriceEUR > 0 {
			sim := hedge.SimulatePosition(cfg, btcAmount, refPriceEUR)
			position = &HedgePositionDetail{
				HedgeRatio:                   round4(sim.HedgeRatio),
				LeverageUsed:                 round4(sim.LeverageUsed),
				HedgeNotionalEUR:             round2(sim.HedgeNotionalEUR),
				InitialMarginEUR:             round2(sim.InitialMarginEUR),
				RecommendedCollateralEUR:     round2(sim.RecommendedCollateralEUR),
				StressMovePct:                round4(sim.StressMovePct),
				StressLossEUR:                round2(sim.StressLossEUR),
				MaintenanceRequirementEUR:    round2(sim.MaintenanceRequirementEUR),
				FreeCollateralAfterStressEUR: round2(sim.FreeCollateralAfterStressEUR),
				LiquidationMovePct:           round4(sim.LiquidationMovePct),
				BTCPriceEUR:                  round2(sim.BTCPriceEUR),
			}
		}

		details := hedgeCostDetails{
			feeBTC:          feeBTC,
			feePct:          rate * 100,
			openFeeBTC:      openFeeBTC,
			openFeePct:      cfg.PerpOpenFeeRate * 100,
			closeFeeBTC:     closeFeeBTC,
			closeFeePct:     cfg.PerpCloseFeeRate * 100,
			fundingFeeBTC:   fundingFeeBTC,
			fundingFeePct:   cfg.FundingCostRate * 100,
			venue:           inputs.Venue,
			instrument:      inputs.Instrument,
			transferMinutes: transferMinutes,
			position:        position,
		}
		if !found || details.feeBTC < best.feeBTC {
			best = details
			found = true
		}
	}

	if !found {
		return hedgeCostDetails{}
	}
	return best
}

func (o *TradeRouteOptimizer) BestBreakdown(btcAmount float64, dexExchange string, direction string) CostBreakdown {
	return o.BestBreakdownWithConfirmations(btcAmount, dexExchange, direction, 0)
}

func (o *TradeRouteOptimizer) BestBreakdownWithConfirmations(btcAmount float64, dexExchange string, direction string, requiredConfirmations int) CostBreakdown {
	return o.BestBreakdownFull(btcAmount, 0, dexExchange, direction, requiredConfirmations, nil, "", false)
}

// BestBreakdownFull finds the lowest-cost route, optionally excluding certain CEXes
// and/or forcing a specific BTC network fee tier.
// refPriceEUR is the CEX reference price used to size the hedge position
// simulation; pass 0 to skip the simulation.
// coupledHedge forces the hedge venue to match the spot CEX where a pairing exists.
func (o *TradeRouteOptimizer) BestBreakdownFull(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedCEXes []string, feeTier string, coupledHedge bool) CostBreakdown {
	if o == nil || btcAmount <= 0 {
		return CostBreakdown{}
	}

	excluded := make(map[string]struct{}, len(excludedCEXes))
	for _, c := range excludedCEXes {
		excluded[c] = struct{}{}
	}

	best := CostBreakdown{}
	found := false
	for _, cex := range supportedCEXes {
		if _, skip := excluded[cex]; skip {
			continue
		}
		est := o.estimators[cex]
		if est == nil {
			continue
		}
		for _, method := range supportedWithdrawalMethods(est.cexProfile, dexExchange) {
			bd := est.breakdownForMethodWithTier(btcAmount, refPriceEUR, dexExchange, method, direction, requiredConfirmations, feeTier, coupledHedge)
			if !found || bd.TotalCostPct < best.TotalCostPct {
				best = bd
				found = true
			}
		}
	}

	if !found {
		return CostBreakdown{}
	}
	return best
}
