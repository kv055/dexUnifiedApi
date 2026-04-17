package hedge

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	krakenFuturesAPIBase = configuredKrakenFuturesBaseURL()
	coinbaseINTXAPIBase  = configuredCoinbaseINTXBaseURL()
	hedgeHTTPClient      = configuredHTTPClient()
)

// LivePerpInputs contains the live derivatives inputs needed by the hedge optimizer.
// Open/close fees currently use taker rates because the intended hedge flow enters
// and exits immediately and should assume immediate execution.
type LivePerpInputs struct {
	Venue           string
	Instrument      string
	OpenFeeRate     float64
	CloseFeeRate    float64
	FundingRate     float64
	FundingInterval time.Duration
	FetchedAt       time.Time
}

// FetchKrakenPerpInputs fetches live taker fee data from Kraken Futures fee schedules
// and the latest funding rate for the supplied perpetual symbol, such as PI_XBTUSD.
func FetchKrakenPerpInputs(symbol string) (LivePerpInputs, error) {
	if strings.TrimSpace(symbol) == "" {
		return LivePerpInputs{}, fmt.Errorf("symbol is required")
	}

	apiKey := firstEnv("KRAKEN_FUTURES_API_KEY", "KRAKEN_API_KEY")
	apiSecret := firstEnv("KRAKEN_FUTURES_API_SECRET", "KRAKEN_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		return LivePerpInputs{}, fmt.Errorf("kraken futures API credentials not configured")
	}

	feeSchedules, err := fetchKrakenFeeSchedules()
	if err != nil {
		return LivePerpInputs{}, err
	}
	volumesBySchedule, err := fetchKrakenFeeScheduleVolumes(apiKey, apiSecret)
	if err != nil {
		return LivePerpInputs{}, err
	}
	takerRate, err := resolveKrakenCurrentTakerRate(feeSchedules, volumesBySchedule)
	if err != nil {
		return LivePerpInputs{}, err
	}

	fundingRate, err := fetchKrakenLatestFundingRate(symbol)
	if err != nil {
		return LivePerpInputs{}, err
	}

	return LivePerpInputs{
		Venue:           "kraken_futures",
		Instrument:      symbol,
		OpenFeeRate:     takerRate,
		CloseFeeRate:    takerRate,
		FundingRate:     fundingRate,
		FundingInterval: venueFundingInterval(VenueKrakenFutures),
		FetchedAt:       time.Now().UTC(),
	}, nil
}

// FetchCoinbaseFuturesInputs fetches live taker fee data from Coinbase INTX portfolio
// fee rates and the current predicted funding for the supplied perpetual instrument,
// such as BTC-PERP.
func FetchCoinbaseFuturesInputs(instrument string) (LivePerpInputs, error) {
	if strings.TrimSpace(instrument) == "" {
		return LivePerpInputs{}, fmt.Errorf("instrument is required")
	}

	apiKey := firstEnv("COINBASE_FUTURES_API_KEY", "COINBASE_API_KEY")
	apiSecret := firstEnv("COINBASE_FUTURES_API_SECRET", "COINBASE_API_SECRET")
	passphrase := firstEnv("COINBASE_FUTURES_API_PASSPHRASE", "COINBASE_API_PASSPHRASE")
	if apiKey == "" || apiSecret == "" || passphrase == "" {
		return LivePerpInputs{}, fmt.Errorf("coinbase futures API credentials not configured")
	}

	takerRate, err := fetchCoinbasePerpTakerRate(apiKey, apiSecret, passphrase)
	if err != nil {
		return LivePerpInputs{}, err
	}
	fundingRate, fundingInterval, err := fetchCoinbaseInstrumentFunding(instrument)
	if err != nil {
		return LivePerpInputs{}, err
	}

	return LivePerpInputs{
		Venue:           "coinbase_futures",
		Instrument:      instrument,
		OpenFeeRate:     takerRate,
		CloseFeeRate:    takerRate,
		FundingRate:     fundingRate,
		FundingInterval: fundingInterval,
		FetchedAt:       time.Now().UTC(),
	}, nil
}

// FetchDefaultVenueInputs fetches live derivatives inputs for the venue's hardcoded
// standard BTC perpetual instrument.
func FetchDefaultVenueInputs(venue Venue) (LivePerpInputs, error) {
	profile, err := DefaultVenueProfile(venue)
	if err != nil {
		return LivePerpInputs{}, err
	}

	switch venue {
	case VenueKrakenFutures:
		return FetchKrakenPerpInputs(profile.Instrument)
	case VenueCoinbaseFutures:
		return FetchCoinbaseFuturesInputs(profile.Instrument)
	default:
		return LivePerpInputs{}, fmt.Errorf("unsupported hedge venue %q", venue)
	}
}

func FallbackVenueInputs(venue Venue) (LivePerpInputs, error) {
	profile, err := DefaultVenueProfile(venue)
	if err != nil {
		return LivePerpInputs{}, err
	}
	return LivePerpInputs{
		Venue:           string(profile.Venue),
		Instrument:      profile.Instrument,
		OpenFeeRate:     profile.FallbackOpenFeeRate,
		CloseFeeRate:    profile.FallbackCloseFeeRate,
		FundingRate:     profile.FallbackFundingRate,
		FundingInterval: venueFundingInterval(venue),
		FetchedAt:       time.Now().UTC(),
	}, nil
}

type krakenFeeSchedulesResponse struct {
	FeeSchedules []struct {
		UID   string `json:"uid"`
		Name  string `json:"name"`
		Tiers []struct {
			MakerFee  float64 `json:"makerFee"`
			TakerFee  float64 `json:"takerFee"`
			USDVolume float64 `json:"usdVolume"`
		} `json:"tiers"`
	} `json:"feeSchedules"`
}

type krakenFeeScheduleVolumesResponse struct {
	Result               string             `json:"result"`
	VolumesByFeeSchedule map[string]float64 `json:"volumesByFeeSchedule"`
	Error                string             `json:"error"`
}

func fetchKrakenFeeSchedules() (krakenFeeSchedulesResponse, error) {
	var out krakenFeeSchedulesResponse
	req, err := http.NewRequest(http.MethodGet, krakenFuturesAPIBase+"/derivatives/api/v3/feeschedules", nil)
	if err != nil {
		return out, err
	}
	if err := doJSON(req, &out); err != nil {
		return out, fmt.Errorf("kraken futures fee schedules: %w", err)
	}
	if len(out.FeeSchedules) == 0 {
		return out, fmt.Errorf("kraken futures fee schedules: empty response")
	}
	return out, nil
}

func fetchKrakenFeeScheduleVolumes(apiKey, apiSecret string) (map[string]float64, error) {
	path := "/derivatives/api/v3/feeschedules/volumes"
	req, err := http.NewRequest(http.MethodGet, krakenFuturesAPIBase+path, nil)
	if err != nil {
		return nil, err
	}
	signKrakenFuturesRequest(req, apiKey, apiSecret, path, "")

	var out krakenFeeScheduleVolumesResponse
	if err := doJSON(req, &out); err != nil {
		return nil, fmt.Errorf("kraken futures fee schedule volumes: %w", err)
	}
	if out.Result != "" && !strings.EqualFold(out.Result, "success") {
		return nil, fmt.Errorf("kraken futures fee schedule volumes result=%q error=%q", out.Result, out.Error)
	}
	if len(out.VolumesByFeeSchedule) == 0 {
		return nil, fmt.Errorf("kraken futures fee schedule volumes: empty response")
	}
	return out.VolumesByFeeSchedule, nil
}

func resolveKrakenCurrentTakerRate(schedules krakenFeeSchedulesResponse, volumesBySchedule map[string]float64) (float64, error) {
	bestVolume := -1.0
	bestRate := 0.0
	found := false

	for _, schedule := range schedules.FeeSchedules {
		volume, ok := volumesBySchedule[schedule.UID]
		if !ok || len(schedule.Tiers) == 0 {
			continue
		}
		rate, ok := krakenTierRateForVolume(schedule.Tiers, volume)
		if !ok {
			continue
		}
		if !found || volume > bestVolume {
			bestVolume = volume
			bestRate = rate
			found = true
		}
	}

	if !found {
		// Fall back to the first schedule's lowest-volume tier if the private volume lookup
		// returned no matching schedule. This is still live schedule data, just not user-tiered.
		for _, schedule := range schedules.FeeSchedules {
			if len(schedule.Tiers) == 0 {
				continue
			}
			rate, ok := krakenTierRateForVolume(schedule.Tiers, 0)
			if ok {
				return rate / 100, nil
			}
		}
		return 0, fmt.Errorf("kraken futures taker fee: no matching fee schedule")
	}

	// Kraken fee tiers are documented in percentage points (e.g. 0.05 = 0.05%),
	// while the optimizer expects a decimal rate (e.g. 0.0005).
	return bestRate / 100, nil
}

func krakenTierRateForVolume(tiers []struct {
	MakerFee  float64 `json:"makerFee"`
	TakerFee  float64 `json:"takerFee"`
	USDVolume float64 `json:"usdVolume"`
}, volume float64) (float64, bool) {
	if len(tiers) == 0 {
		return 0, false
	}
	bestThreshold := -1.0
	bestRate := 0.0
	for _, tier := range tiers {
		if tier.USDVolume <= volume && tier.USDVolume >= bestThreshold {
			bestThreshold = tier.USDVolume
			bestRate = tier.TakerFee
		}
	}
	if bestThreshold < 0 {
		return tiers[0].TakerFee, true
	}
	return bestRate, true
}

type krakenHistoricalFundingResponse struct {
	Rates []struct {
		Timestamp           string  `json:"timestamp"`
		FundingRate         float64 `json:"fundingRate"`
		RelativeFundingRate float64 `json:"relativeFundingRate"`
	} `json:"rates"`
}

func fetchKrakenLatestFundingRate(symbol string) (float64, error) {
	u := krakenFuturesAPIBase + "/derivatives/api/v3/historical-funding-rates?symbol=" + url.QueryEscape(symbol)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}

	var out krakenHistoricalFundingResponse
	if err := doJSON(req, &out); err != nil {
		return 0, fmt.Errorf("kraken futures funding: %w", err)
	}
	if len(out.Rates) == 0 {
		return 0, fmt.Errorf("kraken futures funding: empty response")
	}
	latest := out.Rates[len(out.Rates)-1]
	return latest.RelativeFundingRate, nil
}

type coinbasePortfolioFeeRatesResponse []struct {
	InstrumentType string  `json:"instrument_type"`
	MakerFeeRate   float64 `json:"maker_fee_rate"`
	TakerFeeRate   float64 `json:"taker_fee_rate"`
}

func fetchCoinbasePerpTakerRate(apiKey, apiSecret, passphrase string) (float64, error) {
	path := "/api/v1/portfolios/fee-rates"
	req, err := http.NewRequest(http.MethodGet, coinbaseINTXAPIBase+path, nil)
	if err != nil {
		return 0, err
	}
	signCoinbaseINTXRequest(req, apiKey, apiSecret, passphrase, path, "")

	var out coinbasePortfolioFeeRatesResponse
	if err := doJSON(req, &out); err != nil {
		return 0, fmt.Errorf("coinbase futures fee rates: %w", err)
	}
	for _, row := range out {
		if strings.EqualFold(row.InstrumentType, "PERP") {
			return row.TakerFeeRate, nil
		}
	}
	return 0, fmt.Errorf("coinbase futures fee rates: no PERP tier found")
}

type coinbaseInstrumentResponse struct {
	Symbol          string      `json:"symbol"`
	FundingInterval json.Number `json:"funding_interval"`
	Quote           struct {
		PredictedFunding json.Number `json:"predicted_funding"`
	} `json:"quote"`
}

func fetchCoinbaseInstrumentFunding(instrument string) (float64, time.Duration, error) {
	req, err := http.NewRequest(http.MethodGet, coinbaseINTXAPIBase+"/api/v1/instruments/"+url.PathEscape(instrument), nil)
	if err != nil {
		return 0, 0, err
	}

	var out coinbaseInstrumentResponse
	if err := doJSON(req, &out); err != nil {
		return 0, 0, fmt.Errorf("coinbase futures instrument details: %w", err)
	}

	fundingRate, err := out.Quote.PredictedFunding.Float64()
	if err != nil {
		return 0, 0, fmt.Errorf("coinbase futures predicted funding: %w", err)
	}

	intervalNS, err := out.FundingInterval.Int64()
	if err != nil {
		intervalF, ferr := out.FundingInterval.Float64()
		if ferr != nil {
			return 0, 0, fmt.Errorf("coinbase futures funding interval: %w", err)
		}
		intervalNS = int64(intervalF)
	}

	return fundingRate, time.Duration(intervalNS), nil
}

func signKrakenFuturesRequest(req *http.Request, apiKey, apiSecret, path, postData string) {
	nonce := strconv.FormatInt(time.Now().UnixMilli(), 10)
	hash := sha256.Sum256([]byte(postData + nonce + path))
	secretBytes, _ := base64.StdEncoding.DecodeString(apiSecret)
	mac := hmac.New(sha512.New, secretBytes)
	mac.Write(hash[:])

	req.Header.Set("APIKey", apiKey)
	req.Header.Set("Nonce", nonce)
	req.Header.Set("Authent", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

func signCoinbaseINTXRequest(req *http.Request, apiKey, apiSecret, passphrase, path, body string) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	secretBytes, _ := base64.StdEncoding.DecodeString(apiSecret)
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(timestamp + req.Method + path + body))

	req.Header.Set("CB-ACCESS-KEY", apiKey)
	req.Header.Set("CB-ACCESS-PASSPHRASE", passphrase)
	req.Header.Set("CB-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("CB-ACCESS-SIGN", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

func doJSON(req *http.Request, target any) error {
	resp, err := hedgeHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
