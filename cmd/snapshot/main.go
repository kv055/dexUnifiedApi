package main

import (
	"context"
	"dexunifiedapi/internal/config"
	"dexunifiedapi/db"
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
)

var (
	adminDatabaseURL string
	databaseURL      string
	bisqGRPCHost     string
	bisqAPIPassword  string
	priceProvider    string
	torProxy         string
	robosatsOnions   = []string{
		"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion",
		"http://mmhaqzuirth5rx7gl24d4773lknltjhik57k7ahec5iefktezv4b3uid.onion",
		"http://4t4jxmivv6uqej6xzx2jx3fxh75gtt65v3szjoqmc4ugdlhipzdat6yd.onion",
		"http://otmoonrndnrddqdlhu6b36heunmbyw3cgvadqo2oqeau3656wfv7fwad.onion",
	}
)

func init() {
	if runningUnderGoTest() {
		_ = config.LoadRequiredDotEnv(".env")
		return
	}
	if err := config.LoadRequiredDotEnv(".env"); err != nil {
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
}

func runningUnderGoTest() bool {
	return strings.HasSuffix(os.Args[0], ".test")
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
	FiatCurrency  string   `json:"fiat_currency,omitempty"`
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

type livePayload struct {
	ID            int64    `json:"id"`
	Exchange      string   `json:"exchange"`
	FiatCurrency  string   `json:"fiat_currency"`
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
			FiatCurrency:  "EUR",
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
			FiatCurrency:  "EUR",
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
				FiatCurrency:  "EUR",
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

func saveSnapshot(sdb *dbutil.DB, exchange, fiat, direction string, btcPrice float64, offers []offer) []livePayload {
	if len(offers) == 0 {
		fmt.Printf("[%s] No offers to save\n", exchange)
		return nil
	}

	tx, err := sdb.Begin()
	if err != nil {
		fmt.Printf("[%s] DB error: %v\n", exchange, err)
		return nil
	}
	defer tx.Rollback()

	tx.Exec("INSERT INTO snapshots (exchange, fiat_currency, direction, btc_price) VALUES (?, ?, ?, ?)",
		exchange, fiat, direction, btcPrice)

	liveOffers := make([]livePayload, 0, len(offers))
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
				WHERE exchange = ? AND fiat_currency = ? AND direction = ?
				  AND btc_min = ? AND btc_max = ? AND payment_method = ?`,
				exchange, fiat, direction, o.BTCMin, o.BTCMax, o.PaymentMethod).Scan(&existingID)
			found = err == nil
		}

		live := livePayload{
			Exchange:     exchange,
			FiatCurrency: fiat,
			Direction:    direction,
			Price:        o.Price,
			BTCMin:       o.BTCMin,
			BTCMax:       o.BTCMax,
			PriceType:    o.PriceType,
		}
		if o.FiatMin != nil {
			v := int64(*o.FiatMin)
			live.FiatMin = &v
		}
		if o.FiatMax != nil {
			v := int64(*o.FiatMax)
			live.FiatMax = &v
		}
		if o.PaymentMethod != "" {
			method := o.PaymentMethod
			live.PaymentMethod = &method
		}
		if o.OfferID != "" {
			offerID := o.OfferID
			live.OfferID = &offerID
		}

		if found {
			_ = tx.QueryRow(`UPDATE offers SET
				last_seen = CURRENT_TIMESTAMP,
				price_type = ?,
				price = ?,
				fiat_min = ?,
				fiat_max = ?,
				payment_method = ?,
				offer_id = COALESCE(offer_id, ?)
				WHERE id = ?
				RETURNING id,
					to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
					to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`,
				o.PriceType, o.Price, o.FiatMin, o.FiatMax, nilIfEmpty(o.PaymentMethod), nilIfEmpty(o.OfferID), existingID,
			).Scan(&live.ID, &live.FirstSeen, &live.LastSeen)
			updated++
		} else {
			_ = tx.QueryRow(`INSERT INTO offers
				(exchange, fiat_currency, direction, btc_min, btc_max,
				 fiat_min, fiat_max, payment_method, offer_id, price_type, price)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				RETURNING id,
					to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
					to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`,
				exchange, fiat, direction, o.BTCMin, o.BTCMax,
				o.FiatMin, o.FiatMax, nilIfEmpty(o.PaymentMethod), nilIfEmpty(o.OfferID), o.PriceType, o.Price,
			).Scan(&live.ID, &live.FirstSeen, &live.LastSeen)
			inserted++
		}
		liveOffers = append(liveOffers, live)
	}

	tx.Commit()
	fmt.Printf("[%s] %d new, %d updated (%s)\n", exchange, inserted, updated, direction)
	return liveOffers
}

func writeSnapshotLog(sdb *dbutil.DB, exchangeData map[string]map[string]bool) {
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
	sdb.Exec("INSERT INTO snapshot_log (exchanges, offers_count) VALUES (?::jsonb, ?)", string(jsonBytes), total)
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

	sdb, err := dbutil.Open(databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer sdb.Close()

	if err := sdb.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ping database: %v\n", err)
		os.Exit(1)
	}
	if err := dbutil.EnsureUnifiedSchema(sdb); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize schema: %v\n", err)
		os.Exit(1)
	}

	// Fetch and store Kraken OHLC candles before settlement
	fetchAndStoreKrakenOHLC(sdb)

	// Record start time; used after the run to mark which offers were seen
	snapshotStart := time.Now().UTC()

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
	liveOffers := make([]livePayload, 0)

	for _, direction := range []string{"sell", "buy"} {
		fmt.Printf("\n--- Fetching %s offers ---\n", direction)
		for _, f := range fetchers {
			result := f.fn(direction, refPrice)
			if result != nil && len(result.Offers) > 0 {
				price := result.BTCPrice
				if price == 0 {
					price = float64(refPrice)
				}
				liveOffers = append(liveOffers, saveSnapshot(sdb, f.name, "EUR", direction, price, result.Offers)...)
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
	// - live offers: is_live=true, duration=NULL
	// - closed offers: is_live=false, duration=seconds between first_seen and last_seen
	sdb.Exec(`UPDATE offers SET
		is_live = (last_seen >= ?),
		duration = CASE WHEN last_seen < ? THEN EXTRACT(EPOCH FROM (last_seen - first_seen))::BIGINT ELSE NULL END`,
		snapshotStart, snapshotStart)

	// Explicitly close offers from any exchange that failed entirely this run
	for _, f := range fetchers {
		if _, ok := exchangeData[f.name]; !ok {
			res, _ := sdb.Exec(`UPDATE offers SET
				is_live = FALSE,
				duration = EXTRACT(EPOCH FROM (last_seen - first_seen))::BIGINT
				WHERE exchange = ?`, f.name)
			if n, _ := res.RowsAffected(); n > 0 {
				fmt.Printf("[%s] Exchange offline — marked %d offers as closed\n", f.name, n)
			}
		}
	}

	// Push live offers directly to the server's in-memory hub.
	serverURL := os.Getenv("SERVER_INTERNAL_URL")
	if serverURL == "" {
		serverURL = "http://localhost:5010"
	}
	pushLiveOffersToServer(liveOffers, serverURL)

	// Settle closed offers: compute avg_ref_price from historical OHLC.
	// Keep this after the live push so the server can persist the latest
	// lifecycle snapshot before we decide whether a closed offer has history.
	settleClosedOffers(sdb)
	analyzeClosedOffers(sdb)

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
func fetchAndStoreKrakenOHLC(sdb *dbutil.DB) {
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

// settleClosedOffers computes avg_ref_price for offers that just closed and do
// not have a settled historical reference price yet.
func settleClosedOffers(sdb *dbutil.DB) {
	rows, err := sdb.Query(`
		SELECT id,
		       to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
		       to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM offers
		WHERE is_live = FALSE AND avg_ref_price IS NULL
	`)
	if err != nil {
		fmt.Printf("[Settle] query error: %v\n", err)
		return
	}
	defer rows.Close()

	type toSettle struct {
		id        int64
		firstSeen string
		lastSeen  string
	}
	var batch []toSettle
	for rows.Next() {
		var o toSettle
		if err := rows.Scan(&o.id, &o.firstSeen, &o.lastSeen); err != nil {
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
				ORDER BY ABS(EXTRACT(EPOCH FROM (time - ?::timestamptz)))
				LIMIT 1
			`, o.lastSeen).Scan(&avgClose)
		}

		if avgClose == 0 {
			continue
		}

		sdb.Exec(`UPDATE offers SET avg_ref_price = ? WHERE id = ?`,
			avgClose, o.id)
		settled++
	}
	if settled > 0 {
		fmt.Printf("[Settle] settled %d closed offers with historical avg ref price\n", settled)
	}
}

// pushLiveOffersToServer posts the freshly fetched live offers to the server's
// /internal/live_offers endpoint so the hub can serve them from memory without
// any DB reads.
func pushLiveOffersToServer(offers []livePayload, serverURL string) {
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
func fetchAndStoreKrakenOHLCSince(sdb *dbutil.DB, sinceUnix int64) {
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
