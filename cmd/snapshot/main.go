package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	bisqgrpc "dexunifiedapi/bisq_grpc/go"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	_ "modernc.org/sqlite"
)

var (
	snapshotDBPath  string
	bisqGRPCHost    string
	bisqAPIPassword string
	priceProvider   string
	torProxy        string
	dexMonitorURL   string
	robosatsOnions  = []string{
		"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion",
		"http://mmhaqzuirth5rx7gl24d4773lknltjhik57k7ahec5iefktezv4b3uid.onion",
		"http://4t4jxmivv6uqej6xzx2jx3fxh75gtt65v3szjoqmc4ugdlhipzdat6yd.onion",
		"http://otmoonrndnrddqdlhu6b36heunmbyw3cgvadqo2oqeau3656wfv7fwad.onion",
	}
)

func init() {
	snapshotDBPath = os.Getenv("DB_PATH")
	if snapshotDBPath == "" {
		snapshotDBPath = "/home/claudio_clawd_bot/orderbook/orderbook.db"
	}
	bisqGRPCHost = os.Getenv("BISQ_GRPC_HOST")
	if bisqGRPCHost == "" {
		bisqGRPCHost = "localhost:9998"
	}
	bisqAPIPassword = os.Getenv("BISQ_API_PASSWORD")
	priceProvider = os.Getenv("PRICE_PROVIDER")
	if priceProvider == "" {
		priceProvider = "bisq_tor"
	}
	torProxy = os.Getenv("TOR_PROXY_URL")
	if torProxy == "" {
		torProxy = "socks5h://127.0.0.1:9050"
	}
	dexMonitorURL = os.Getenv("DEXMONITOR_INTERNAL_URL")
	if dexMonitorURL == "" {
		dexMonitorURL = "http://localhost:5000"
	}
}

// bisqCreds injects the Bisq API password header on every gRPC call.
type bisqCreds struct{ password string }

func (c bisqCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"password": c.password}, nil
}
func (c bisqCreds) RequireTransportSecurity() bool { return false }

// --- Offer type ---

type offer struct {
	OfferID       string   `json:"offer_id,omitempty"`
	Price         float64  `json:"price"`
	PriceDiffPct  *float64 `json:"price_diff_percent,omitempty"`
	BTCMin        *float64 `json:"btc_min,omitempty"`
	BTCMax        *float64 `json:"btc_max,omitempty"`
	FiatMin       *int     `json:"fiat_min,omitempty"`
	FiatMax       *int     `json:"fiat_max,omitempty"`
	PaymentMethod string   `json:"payment_method,omitempty"`
	PriceType     *string  `json:"price_type,omitempty"`
}

type fetchResult struct {
	BTCPrice float64
	Offers   []offer
}

func priceTypeFixed() *string    { s := "fixed"; return &s }
func priceTypeFloating() *string { s := "floating"; return &s }

// --- HTTP clients ---

func torClient() *http.Client {
	proxyURL, _ := url.Parse(torProxy)
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:       http.ProxyURL(proxyURL),
			DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		},
	}
}

func clearnetClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

// --- Reference price providers ---

func priceBisqTor(fiat string) (int, error) {
	resp, err := torClient().Get("http://ro7nv73awqs3ga2qtqeqawrjpbxwarsazznszvr6whv7tes5ehffopid.onion/getAllMarketPrices")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data []struct {
			CurrencyCode string  `json:"currencyCode"`
			Price        float64 `json:"price"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}
	for _, c := range result.Data {
		if strings.EqualFold(c.CurrencyCode, fiat) {
			return int(c.Price), nil
		}
	}
	return 0, fmt.Errorf("currency %s not found", fiat)
}

func priceKraken(fiat string) (int, error) {
	pair := "XBT" + strings.ToUpper(fiat)
	resp, err := clearnetClient().Get("https://api.kraken.com/0/public/Ticker?pair=" + pair)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Result map[string]struct {
			C []string `json:"c"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}
	for _, ticker := range data.Result {
		if len(ticker.C) > 0 {
			v, _ := strconv.ParseFloat(ticker.C[0], 64)
			return int(v), nil
		}
	}
	return 0, fmt.Errorf("no ticker data for %s", pair)
}

func priceCoinGecko(fiat string) (int, error) {
	resp, err := clearnetClient().Get("https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=" + fiat)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}
	if btc, ok := data["bitcoin"]; ok {
		if price, ok := btc[fiat]; ok {
			return int(price), nil
		}
	}
	return 0, fmt.Errorf("no price for %s", fiat)
}

func fetchReferencePrice(fiat string) int {
	providers := map[string]func(string) (int, error){
		"bisq_tor":  priceBisqTor,
		"kraken":    priceKraken,
		"coingecko": priceCoinGecko,
	}
	fn, ok := providers[priceProvider]
	if !ok {
		fmt.Printf("[RefPrice] Unknown provider '%s', available: %v\n", priceProvider, []string{"bisq_tor", "kraken", "coingecko"})
		return 0
	}
	price, err := fn(fiat)
	if err != nil {
		fmt.Printf("[RefPrice] Error from %s: %v\n", priceProvider, err)
		return 0
	}
	fmt.Printf("[RefPrice] %s: %d %s\n", priceProvider, price, strings.ToUpper(fiat))
	return price
}

// --- Exchange fetchers ---

func fetchBisq(direction string, btcPrice int) *fetchResult {
	if btcPrice == 0 {
		fmt.Println("[Bisq] Could not get reference price")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(bisqGRPCHost,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bisqCreds{password: bisqAPIPassword}),
	)
	if err != nil {
		fmt.Printf("[Bisq] Connection error: %v\n", err)
		return nil
	}
	defer conn.Close()

	stub := bisqgrpc.NewOffersClient(conn)
	reply, err := stub.GetOffers(ctx, &bisqgrpc.GetOffersRequest{
		Direction:    strings.ToUpper(direction),
		CurrencyCode: "EUR",
	})
	if err != nil {
		fmt.Printf("[Bisq] Error getting %s offers: %v\n", direction, err)
		return nil
	}

	var offers []offer
	for _, o := range reply.Offers {
		price, _ := strconv.ParseFloat(o.Price, 64)
		btcMin := float64(o.MinAmount) / 1e8
		btcMax := float64(o.Amount) / 1e8
		fiatMin := int(parseFloatStr(o.MinVolume))
		fiatMax := int(parseFloatStr(o.Volume))
		pt := priceTypeFixed()
		if o.UseMarketBasedPrice {
			pt = priceTypeFloating()
		}
		offers = append(offers, offer{
			OfferID:       o.Id,
			Price:         price,
			BTCMin:        &btcMin,
			BTCMax:        &btcMax,
			FiatMin:       &fiatMin,
			FiatMax:       &fiatMax,
			PaymentMethod: o.PaymentMethodShortName,
			PriceType:     pt,
		})
	}

	return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
}

func fetchHodlHodl(direction string, btcPrice int) *fetchResult {
	reqURL := fmt.Sprintf("https://hodlhodl.com/api/v1/offers?filters[side]=%s&filters[include_global]=true&filters[currency_code]=EUR&filters[only_working_now]=true&sort[by]=price", direction)
	resp, err := clearnetClient().Get(reqURL)
	if err != nil {
		fmt.Printf("[HodlHodl] Error: %v\n", err)
		return &fetchResult{}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Offers []struct {
			ID             string `json:"id"`
			Price          string `json:"price"`
			PriceSource    string `json:"price_source"`
			MinAmount      string `json:"min_amount"`
			MaxAmount      string `json:"max_amount"`
			PaymentMethods []struct {
				Name string `json:"name"`
			} `json:"payment_methods"`
			PaymentMethodInstructions []struct {
				PaymentMethodName string `json:"payment_method_name"`
			} `json:"payment_method_instructions"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Printf("[HodlHodl] Parse error: %v\n", err)
		return &fetchResult{}
	}

	var offers []offer
	for _, o := range data.Offers {
		price, err := strconv.ParseFloat(o.Price, 64)
		if err != nil || price == 0 {
			continue
		}
		fiatMin := int(parseFloatStr(o.MinAmount))
		fiatMax := int(parseFloatStr(o.MaxAmount))

		var method string
		if direction == "buy" && len(o.PaymentMethods) > 0 {
			method = o.PaymentMethods[0].Name
		} else if len(o.PaymentMethodInstructions) > 0 {
			method = o.PaymentMethodInstructions[0].PaymentMethodName
		}
		if strings.Contains(method, "SEPA") {
			method = "SEPA"
		} else if strings.Contains(method, "Any national bank") {
			method = "NATIONAL_BANK"
		}

		var pt *string
		switch o.PriceSource {
		case "fixed_value":
			pt = priceTypeFixed()
		case "exchange_rate":
			pt = priceTypeFloating()
		}

		btcMin := round8(float64(fiatMin) / price)
		btcMax := round8(float64(fiatMax) / price)
		offers = append(offers, offer{
			OfferID:       o.ID,
			Price:         price,
			BTCMin:        &btcMin,
			BTCMax:        &btcMax,
			FiatMin:       &fiatMin,
			FiatMax:       &fiatMax,
			PaymentMethod: method,
			PriceType:     pt,
		})
	}

	fmt.Printf("[HodlHodl] Found %d %s offers\n", len(offers), direction)
	return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
}

func fetchRobosats(direction string, btcPrice int) *fetchResult {
	offerType := 1 // sell
	if direction == "buy" {
		offerType = 0
	}

	client := torClient()
	for _, onion := range robosatsOnions {
		reqURL := fmt.Sprintf("%s/api/book/?currency=2&type=%d", onion, offerType)
		resp, err := client.Get(reqURL)
		if err != nil {
			fmt.Printf("[Robosats] %s failed: %v, trying next...\n", onion, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var rawOffers []json.RawMessage
		if err := json.Unmarshal(body, &rawOffers); err != nil {
			fmt.Printf("[Robosats] Parse error: %v, trying next...\n", err)
			continue
		}

		var offers []offer
		for _, raw := range rawOffers {
			var s string
			if json.Unmarshal(raw, &s) == nil && s == "not_found" {
				break
			}

			var line struct {
				ID            json.Number  `json:"id"`
				Price         json.Number  `json:"price"`
				Premium       json.Number  `json:"premium"`
				Amount        *json.Number `json:"amount"`
				MinAmount     json.Number  `json:"min_amount"`
				MaxAmount     json.Number  `json:"max_amount"`
				PaymentMethod string       `json:"payment_method"`
			}
			if err := json.Unmarshal(raw, &line); err != nil {
				continue
			}

			price, err := line.Price.Float64()
			if err != nil {
				continue
			}
			// RoboSats sometimes returns price=0 with only a premium percentage.
			// Derive absolute price from the reference BTC price in that case.
			pt := priceTypeFixed()
			if price == 0 {
				premium, _ := line.Premium.Float64()
				if premium == 0 || btcPrice == 0 {
					continue
				}
				price = float64(btcPrice) * (1 + premium/100)
				pt = priceTypeFloating()
			}

			var fiatMin, fiatMax int
			if line.Amount != nil {
				v, _ := line.Amount.Float64()
				fiatMin = int(v)
				fiatMax = int(v)
			} else {
				minV, _ := line.MinAmount.Float64()
				maxV, _ := line.MaxAmount.Float64()
				fiatMin = int(minV)
				fiatMax = int(maxV)
			}

			btcMin := round8(float64(fiatMin) / price)
			btcMax := round8(float64(fiatMax) / price)

			offers = append(offers, offer{
				OfferID:       line.ID.String(),
				Price:         price,
				BTCMin:        &btcMin,
				BTCMax:        &btcMax,
				FiatMin:       &fiatMin,
				FiatMax:       &fiatMax,
				PaymentMethod: line.PaymentMethod,
				PriceType:     pt,
			})
		}

		fmt.Printf("[Robosats] Found %d %s offers\n", len(offers), direction)
		return &fetchResult{BTCPrice: float64(btcPrice), Offers: offers}
	}

	fmt.Println("[Robosats] All onion addresses failed")
	return &fetchResult{}
}

// --- Database ---

func saveSnapshot(sdb *sql.DB, exchange, fiat, direction string, btcPrice float64, offers []offer) {
	if len(offers) == 0 {
		fmt.Printf("[%s] No offers to save\n", exchange)
		return
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[%s] DB error: %v\n", exchange, err)
		return
	}
	defer tx.Rollback()

	tx.Exec("INSERT INTO snapshots (exchange, fiat_currency, direction, btc_price) VALUES (?, ?, ?, ?)",
		exchange, fiat, direction, btcPrice)

	updated, inserted := 0, 0
	for _, o := range offers {
		var existingID int64
		var found bool

		if o.OfferID != "" {
			err := tx.QueryRow("SELECT id FROM offers WHERE offer_id = ?", o.OfferID).Scan(&existingID)
			found = err == nil
		}
		if !found {
			err := tx.QueryRow(`SELECT id FROM offers
				WHERE exchange = ? AND direction = ? AND price = ?
				  AND btc_min = ? AND btc_max = ? AND payment_method = ?`,
				exchange, direction, o.Price, o.BTCMin, o.BTCMax, o.PaymentMethod).Scan(&existingID)
			found = err == nil
		}

		if found {
			tx.Exec("UPDATE offers SET last_seen = CURRENT_TIMESTAMP, price = ?, price_type = ? WHERE id = ?",
				o.Price, o.PriceType, existingID)
			updated++
		} else {
			tx.Exec(`INSERT INTO offers
				(exchange, direction, price, btc_min, btc_max,
				 fiat_min, fiat_max, payment_method, offer_id, price_type)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				exchange, direction, o.Price, o.BTCMin, o.BTCMax,
				o.FiatMin, o.FiatMax, o.PaymentMethod, nilIfEmpty(o.OfferID), o.PriceType)
			inserted++
		}
	}

	tx.Commit()
	fmt.Printf("[%s] %d new, %d updated (%s)\n", exchange, inserted, updated, direction)
}

func writeSnapshotLog(sdb *sql.DB, exchangeData map[string]map[string]bool) {
	exchanges := make(map[string]any)
	total := 0
	for ex, ids := range exchangeData {
		idList := make([]string, 0, len(ids))
		for id := range ids {
			idList = append(idList, id)
		}
		exchanges[ex] = map[string]any{
			"count": len(ids),
			"ids":   idList,
		}
		total += len(ids)
	}
	jsonBytes, _ := json.Marshal(exchanges)
	sdb.Exec("INSERT INTO snapshot_log (exchanges, offers_count) VALUES (?, ?)", string(jsonBytes), total)
}

// --- Main ---

func main() {
	fmt.Println("=== Starting orderbook snapshot ===")

	refPrice := fetchReferencePrice("eur")
	if refPrice == 0 {
		fmt.Println("Could not get reference price, aborting")
		os.Exit(1)
	}
	fmt.Printf("Reference price: %d EUR\n", refPrice)

	sdb, err := sql.Open("sqlite", snapshotDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer sdb.Close()

	// Migrate: add columns to existing databases
	sdb.Exec("ALTER TABLE offers ADD COLUMN is_live BOOLEAN NOT NULL DEFAULT 0")
	sdb.Exec("ALTER TABLE offers ADD COLUMN duration INTEGER")
	sdb.Exec("ALTER TABLE offers ADD COLUMN historical_analysis_status TEXT")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_ref_price_10m REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_premium_10m_pct REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_trade_cost_pct_10m REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_trade_cost_btc_10m REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_net_premium_10m_pct REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_estimated_profit_btc_10m REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_estimated_profit_fiat_10m REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_btc_amount_10m REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN best_route_cex_10m TEXT")
	sdb.Exec("ALTER TABLE offers ADD COLUMN best_route_method_10m TEXT")
	sdb.Exec("ALTER TABLE offers ADD COLUMN price_type TEXT")
	sdb.Exec("ALTER TABLE offers ADD COLUMN cex_taker_fee_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN cex_withdraw_fee_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN network_fee_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN avg_hedge_fee_sum_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN hedge_open_fee_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN hedge_close_fee_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN hedge_funding_fee_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN hedge_fee_sum_btc REAL")
	sdb.Exec("ALTER TABLE offers ADD COLUMN hedge_venue TEXT")
	sdb.Exec("ALTER TABLE offers ADD COLUMN hedge_instrument TEXT")
	sdb.Exec("ALTER TABLE offers ADD COLUMN hedge_transfer_minutes INTEGER")
	sdb.Exec("ALTER TABLE offers ADD COLUMN dex_fee_btc REAL")
	sdb.Exec("ALTER TABLE order_lifecycle ADD COLUMN cex_taker_fee_btc REAL")
	sdb.Exec("ALTER TABLE order_lifecycle ADD COLUMN cex_withdraw_fee_btc REAL")
	sdb.Exec("ALTER TABLE order_lifecycle ADD COLUMN network_fee_btc REAL")
	sdb.Exec("ALTER TABLE order_lifecycle ADD COLUMN dex_fee_btc REAL")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN hedge_open_fee_btc")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN hedge_close_fee_btc")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN hedge_funding_fee_btc")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN hedge_fee_btc")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN hedge_venue")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN hedge_instrument")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN hedge_transfer_minutes")
	sdb.Exec("ALTER TABLE offers DROP COLUMN avg_cex_fee_btc")
	sdb.Exec("ALTER TABLE offers DROP COLUMN avg_network_fee_btc")
	sdb.Exec("ALTER TABLE order_lifecycle DROP COLUMN cex_fee_btc")
	sdb.Exec(`CREATE TABLE IF NOT EXISTS kraken_ohlc (
		time  DATETIME PRIMARY KEY,
		open  REAL NOT NULL,
		high  REAL NOT NULL,
		low   REAL NOT NULL,
		close REAL NOT NULL
	)`)

	// Fetch and store Kraken OHLC candles before settlement
	fetchAndStoreKrakenOHLC(sdb)

	// Record start time; used after the run to mark which offers were seen
	snapshotStart := time.Now().UTC().Format("2006-01-02 15:04:05")

	type exchFetcher struct {
		name string
		fn   func(string, int) *fetchResult
	}
	fetchers := []exchFetcher{
		{"bisq", fetchBisq},
		{"hodlhodl", fetchHodlHodl},
		{"robosats", fetchRobosats},
	}

	exchangeData := make(map[string]map[string]bool)

	for _, direction := range []string{"sell", "buy"} {
		fmt.Printf("\n--- Fetching %s offers ---\n", direction)
		for _, f := range fetchers {
			result := f.fn(direction, refPrice)
			if result != nil && len(result.Offers) > 0 {
				price := result.BTCPrice
				if price == 0 {
					price = float64(refPrice)
				}
				saveSnapshot(sdb, f.name, "EUR", direction, price, result.Offers)
				if exchangeData[f.name] == nil {
					exchangeData[f.name] = make(map[string]bool)
				}
				for _, o := range result.Offers {
					if o.OfferID != "" {
						exchangeData[f.name][o.OfferID] = true
					}
				}
			}
		}
	}

	// Update is_live and duration in one pass:
	// - live offers: is_live=1, duration=NULL
	// - closed offers: is_live=0, duration=seconds between first_seen and last_seen
	sdb.Exec(`UPDATE offers SET
		is_live = (last_seen >= ?),
		duration = CASE WHEN last_seen < ? THEN (strftime('%s', last_seen) - strftime('%s', first_seen)) ELSE NULL END`,
		snapshotStart, snapshotStart)

	// Explicitly close offers from any exchange that failed entirely this run
	for _, f := range fetchers {
		if _, ok := exchangeData[f.name]; !ok {
			res, _ := sdb.Exec(`UPDATE offers SET
				is_live = 0,
				duration = (strftime('%s', last_seen) - strftime('%s', first_seen))
				WHERE exchange = ?`, f.name)
			if n, _ := res.RowsAffected(); n > 0 {
				fmt.Printf("[%s] Exchange offline — marked %d offers as closed\n", f.name, n)
			}
		}
	}

	// Settle closed offers: compute avg_ref_price and price_diff_percent from historical OHLC
	settleClosedOffers(sdb)
	analyzeClosedOffers(sdb)

	// Push recently analyzed closed offers to dexMonitor (best-effort)
	pushClosedOffersToMonitor(sdb)

	// Push live offers directly to the server's in-memory hub.
	serverURL := os.Getenv("SERVER_INTERNAL_URL")
	if serverURL == "" {
		serverURL = "http://localhost:5010"
	}
	pushLiveOffersToServer(sdb, serverURL)

	writeSnapshotLog(sdb, exchangeData)
	total := 0
	names := make([]string, 0, len(exchangeData))
	for name, ids := range exchangeData {
		total += len(ids)
		names = append(names, name)
	}
	fmt.Printf("\n=== Snapshot complete — %d offers from %v ===\n", total, names)
}

// fetchAndStoreKrakenOHLC fetches 1-min Kraken OHLC candles and upserts them into kraken_ohlc.
func fetchAndStoreKrakenOHLC(sdb *sql.DB) {
	resp, err := clearnetClient().Get("https://api.kraken.com/0/public/OHLC?pair=XBTEUR&interval=1")
	if err != nil {
		fmt.Printf("[OHLC] fetch error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Printf("[OHLC] parse error: %v\n", err)
		return
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[OHLC] db error: %v\n", err)
		return
	}
	defer tx.Rollback()

	n := 0
	for key, raw := range data.Result {
		if key == "last" {
			continue
		}
		var rows [][]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			continue
		}
		for _, c := range rows {
			if len(c) < 5 {
				continue
			}
			var ts float64
			json.Unmarshal(c[0], &ts)
			var open, high, low, cls string
			json.Unmarshal(c[1], &open)
			json.Unmarshal(c[2], &high)
			json.Unmarshal(c[3], &low)
			json.Unmarshal(c[4], &cls)
			t := time.Unix(int64(ts), 0).UTC().Format("2006-01-02 15:04:05")
			tx.Exec(`INSERT INTO kraken_ohlc (time, open, high, low, close) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(time) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close`,
				t,
				parseFloatStr(open), parseFloatStr(high), parseFloatStr(low), parseFloatStr(cls),
			)
			n++
		}
		break // only one non-"last" key
	}
	tx.Commit()
	fmt.Printf("[OHLC] upserted %d candles\n", n)
}

// settleClosedOffers computes avg_ref_price and price_diff_percent for offers that
// just closed and do not have a settled historical reference price yet.
// over the offer's lifetime.
func settleClosedOffers(sdb *sql.DB) {
	rows, err := sdb.Query(`
		SELECT id, direction, price, first_seen, last_seen
		FROM offers
		WHERE is_live = 0 AND avg_ref_price IS NULL
	`)
	if err != nil {
		fmt.Printf("[Settle] query error: %v\n", err)
		return
	}
	defer rows.Close()

	type toSettle struct {
		id        int64
		direction string
		price     float64
		firstSeen string
		lastSeen  string
	}
	var batch []toSettle
	for rows.Next() {
		var o toSettle
		if err := rows.Scan(&o.id, &o.direction, &o.price, &o.firstSeen, &o.lastSeen); err != nil {
			continue
		}
		batch = append(batch, o)
	}
	rows.Close()

	settled := 0
	for _, o := range batch {
		var avgClose float64
		sdb.QueryRow(`
			SELECT AVG(close) FROM kraken_ohlc
			WHERE time >= ? AND time <= ?
		`, o.firstSeen, o.lastSeen).Scan(&avgClose)

		// No candles in range — offer's lifetime may predate our cached OHLC window.
		// Fetch historical data from Kraken using the offer's first_seen as the
		// since parameter, upsert into kraken_ohlc, then retry.
		if avgClose == 0 {
			if firstSeenUnix := parseDBTimeUnix(o.firstSeen); firstSeenUnix > 0 {
				fetchAndStoreKrakenOHLCSince(sdb, firstSeenUnix)
				sdb.QueryRow(`
					SELECT AVG(close) FROM kraken_ohlc
					WHERE time >= ? AND time <= ?
				`, o.firstSeen, o.lastSeen).Scan(&avgClose)
			}
		}

		// Last resort: nearest single candle to last_seen
		if avgClose == 0 {
			sdb.QueryRow(`
				SELECT close FROM kraken_ohlc
				ORDER BY ABS(strftime('%s', time) - strftime('%s', ?))
				LIMIT 1
			`, o.lastSeen).Scan(&avgClose)
		}

		if avgClose == 0 {
			continue
		}

		pct := math.Round((o.price/avgClose-1)*10000) / 100
		if o.direction == "sell" {
			pct = -pct
		}
		sdb.Exec(`UPDATE offers SET avg_ref_price = ?, price_diff_percent = ? WHERE id = ?`,
			avgClose, pct, o.id)
		settled++
	}
	if settled > 0 {
		fmt.Printf("[Settle] settled %d closed offers with historical avg ref price\n", settled)
	}
}

// pushLiveOffersToServer queries all is_live=1 offers from the DB and POSTs
// them to the server's /internal/live_offers endpoint so the hub can serve
// them directly from memory without any DB reads.
func pushLiveOffersToServer(sdb *sql.DB, serverURL string) {
	type livePayload struct {
		ID            int64    `json:"id"`
		Exchange      string   `json:"exchange"`
		Direction     string   `json:"direction"`
		Price         float64  `json:"price"`
		BTCMin        *float64 `json:"btc_min"`
		BTCMax        *float64 `json:"btc_max"`
		FiatMin       *int64   `json:"fiat_min"`
		FiatMax       *int64   `json:"fiat_max"`
		PaymentMethod *string  `json:"payment_method"`
		OfferID       *string  `json:"offer_id"`
		FirstSeen     string   `json:"first_seen"`
		LastSeen      string   `json:"last_seen"`
		PriceType     *string  `json:"price_type"`
	}

	rows, err := sdb.Query(`
		SELECT id, exchange, direction, price, btc_min, btc_max,
		       fiat_min, fiat_max, payment_method, offer_id, first_seen, last_seen, price_type
		FROM offers WHERE is_live = 1
	`)
	if err != nil {
		fmt.Printf("[LivePush] query error: %v\n", err)
		return
	}
	defer rows.Close()

	var offers []livePayload
	for rows.Next() {
		var o livePayload
		var btcMin, btcMax sql.NullFloat64
		var fiatMin, fiatMax sql.NullInt64
		var paymentMethod, offerID, priceType sql.NullString
		if err := rows.Scan(
			&o.ID, &o.Exchange, &o.Direction, &o.Price,
			&btcMin, &btcMax, &fiatMin, &fiatMax,
			&paymentMethod, &offerID, &o.FirstSeen, &o.LastSeen, &priceType,
		); err != nil {
			fmt.Printf("[LivePush] scan error: %v\n", err)
			continue
		}
		if btcMin.Valid {
			o.BTCMin = &btcMin.Float64
		}
		if btcMax.Valid {
			o.BTCMax = &btcMax.Float64
		}
		if fiatMin.Valid {
			o.FiatMin = &fiatMin.Int64
		}
		if fiatMax.Valid {
			o.FiatMax = &fiatMax.Int64
		}
		if paymentMethod.Valid {
			o.PaymentMethod = &paymentMethod.String
		}
		if offerID.Valid {
			o.OfferID = &offerID.String
		}
		if priceType.Valid {
			o.PriceType = &priceType.String
		}
		offers = append(offers, o)
	}
	rows.Close()

	body, err := json.Marshal(offers)
	if err != nil {
		fmt.Printf("[LivePush] marshal error: %v\n", err)
		return
	}

	resp, err := http.Post(serverURL+"/internal/live_offers", "application/json", strings.NewReader(string(body)))
	if err != nil {
		fmt.Printf("[LivePush] POST failed (non-fatal): %v\n", err)
		return
	}
	resp.Body.Close()
	fmt.Printf("[LivePush] pushed %d live offers to server\n", len(offers))
}

// fetchAndStoreKrakenOHLCSince fetches 1-min candles starting from sinceUnix
// and upserts them into kraken_ohlc. Used to backfill history for offers whose
// lifetime predates the standard OHLC cache window (~7.5 days).
func fetchAndStoreKrakenOHLCSince(sdb *sql.DB, sinceUnix int64) {
	url := fmt.Sprintf("https://api.kraken.com/0/public/OHLC?pair=XBTEUR&interval=1&since=%d", sinceUnix)
	resp, err := clearnetClient().Get(url)
	if err != nil {
		fmt.Printf("[OHLCSince] fetch error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Printf("[OHLCSince] parse error: %v\n", err)
		return
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[OHLCSince] db error: %v\n", err)
		return
	}
	defer tx.Rollback()

	n := 0
	for key, raw := range data.Result {
		if key == "last" {
			continue
		}
		var rows [][]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			continue
		}
		for _, c := range rows {
			if len(c) < 5 {
				continue
			}
			var ts float64
			json.Unmarshal(c[0], &ts)
			var open, high, low, cls string
			json.Unmarshal(c[1], &open)
			json.Unmarshal(c[2], &high)
			json.Unmarshal(c[3], &low)
			json.Unmarshal(c[4], &cls)
			t := time.Unix(int64(ts), 0).UTC().Format("2006-01-02 15:04:05")
			tx.Exec(`INSERT INTO kraken_ohlc (time, open, high, low, close) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(time) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close`,
				t,
				parseFloatStr(open), parseFloatStr(high), parseFloatStr(low), parseFloatStr(cls),
			)
			n++
		}
		break
	}
	tx.Commit()
	fmt.Printf("[OHLCSince] upserted %d historical candles\n", n)
}

// parseDBTimeUnix parses a SQLite timestamp string and returns its Unix timestamp.
// Returns 0 if parsing fails.
func parseDBTimeUnix(s string) int64 {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02T15:04:05Z"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC().Unix()
		}
	}
	return 0
}

// --- Helpers ---

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round8(v float64) float64 {
	return math.Round(v*1e8) / 1e8
}

func parseFloatStr(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// pushClosedOffersToMonitor queries recently analyzed closed offers and POSTs
// their summaries to dexMonitor's /internal/closed_offers endpoint (best-effort).
func pushClosedOffersToMonitor(sdb *sql.DB) {
	rows, err := sdb.Query(`
		SELECT id, exchange, direction, price, price_diff_percent,
		       btc_min, btc_max, fiat_min, fiat_max,
		       payment_method, offer_id, first_seen, last_seen,
		       is_live, duration, historical_analysis_status,
		       avg_ref_price, avg_premium_pct, avg_trade_cost_pct, avg_trade_cost_btc,
		       cex_taker_fee_btc, cex_withdraw_fee_btc, network_fee_btc, dex_fee_btc,
		       avg_net_premium_pct, avg_estimated_profit_btc, avg_estimated_profit_fiat,
		       avg_btc_amount, price_type, best_route_cex, best_route_method,
		       hedge_open_fee_btc, hedge_close_fee_btc, hedge_funding_fee_btc,
		       hedge_fee_sum_btc, hedge_venue, hedge_instrument, hedge_transfer_minutes
		FROM offers
		WHERE is_live = 0
		  AND historical_analysis_status IN ('analyzed', 'non_premium')
		  AND last_seen >= datetime('now', '-10 minutes')
	`)
	if err != nil {
		fmt.Printf("[MonitorPush] closed offers query error: %v\n", err)
		return
	}
	defer rows.Close()

	var offers []map[string]any
	cols, _ := rows.Columns()
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = values[i]
		}
		offers = append(offers, row)
	}

	if len(offers) == 0 {
		return
	}

	body, err := json.Marshal(offers)
	if err != nil {
		fmt.Printf("[MonitorPush] closed offers marshal error: %v\n", err)
		return
	}
	targetURL := strings.TrimRight(dexMonitorURL, "/") + "/internal/closed_offers"
	resp, err := http.Post(targetURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		fmt.Printf("[MonitorPush] closed offers push error (non-fatal): %v\n", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		fmt.Printf("[MonitorPush] closed offers push returned %d\n", resp.StatusCode)
	} else {
		fmt.Printf("[MonitorPush] pushed %d closed offers to dexMonitor\n", len(offers))
	}
}
