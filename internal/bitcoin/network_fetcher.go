package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"time"
)

// NetworkDataFetcher handles all Bitcoin network data API calls via mempool.space.
type NetworkDataFetcher struct {
	client     *http.Client
	baseURL    string
	userAgent  string
	lastUpdate time.Time
}

// NewNetworkDataFetcher creates a new fetcher with a plain HTTP client.
func NewNetworkDataFetcher() *NetworkDataFetcher {
	return NewNetworkDataFetcherWithClient(&http.Client{Timeout: 10 * time.Second})
}

// NewNetworkDataFetcherWithClient creates a fetcher using the provided HTTP client.
// Pass a Tor-proxied client when running inside a container that routes through Tor.
func NewNetworkDataFetcherWithClient(client *http.Client) *NetworkDataFetcher {
	return &NetworkDataFetcher{
		client:    client,
		baseURL:   "https://mempool.space/api/v1",
		userAgent: "dexMonitor/1.0 (Bitcoin Fee Predictor)",
	}
}

// FetchRecommendedFees fetches current recommended fee rates from mempool.space.
func (ndf *NetworkDataFetcher) FetchRecommendedFees() (*FeeEstimate, error) {
	resp, err := ndf.makeRequest(ndf.baseURL + "/fees/recommended")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recommended fees: %w", err)
	}
	defer resp.Body.Close()

	var feeResp BitcoinFeeResponse
	if err := json.NewDecoder(resp.Body).Decode(&feeResp); err != nil {
		return nil, fmt.Errorf("failed to decode fee response: %w", err)
	}

	estimate := &FeeEstimate{
		EconomicFee: feeResp.EconomyFee,
		StandardFee: feeResp.HalfHourFee,
		FastFee:     feeResp.FastestFee,
		ExpressFee:  feeResp.FastestFee + 5, // small buffer above fastest
		MinimumFee:  feeResp.MinimumFee,
		LastUpdated: time.Now(),
		Confidence:  0.95,
		Source:      "mempool.space",
	}

	ndf.lastUpdate = time.Now()
	return estimate, nil
}

// FetchMempoolData fetches current mempool statistics.
// Note: the mempool endpoint lives under /api, not /api/v1.
func (ndf *NetworkDataFetcher) FetchMempoolData() (*MempoolData, error) {
	resp, err := ndf.makeRequest("https://mempool.space/api/mempool")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch mempool data: %w", err)
	}
	defer resp.Body.Close()

	var mempoolResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&mempoolResp); err != nil {
		return nil, fmt.Errorf("failed to decode mempool response: %w", err)
	}

	data := &MempoolData{
		BlockTarget:        make(map[string]int64),
		PercentileFeeRates: make(map[string]int64),
		FetchedAt:          time.Now(),
	}

	if count, ok := mempoolResp["count"].(float64); ok {
		data.TotalUnconfirmedTxs = int64(count)
	}
	if memUsed, ok := mempoolResp["memUsed"].(float64); ok {
		data.TotalMemoryUsed = int64(memUsed)
	}
	if vSizes, ok := mempoolResp["vSizes"].([]interface{}); ok && len(vSizes) > 0 {
		if vSize, ok := vSizes[0].(map[string]interface{}); ok {
			if avg, ok := vSize["avgFee"].(float64); ok {
				data.AverageFeeRate = int64(avg)
			}
		}
	}

	blocks, err := ndf.FetchMempoolBlocks()
	if err != nil {
		return nil, err
	}
	data.ProjectedBlocks = blocks
	if len(blocks) > 0 {
		data.MinFeeRate = math.MaxInt64
		for i, block := range blocks {
			if block.MedianFee > 0 {
				data.MedianFeeRate = block.MedianFee
			}
			if len(block.FeeRange) == 0 {
				continue
			}
			minFee := block.FeeRange[0]
			maxFee := block.FeeRange[len(block.FeeRange)-1]
			if minFee > maxFee {
				minFee, maxFee = maxFee, minFee
			}
			if minFee > 0 && minFee < data.MinFeeRate {
				data.MinFeeRate = minFee
			}
			if maxFee > data.MaxFeeRate {
				data.MaxFeeRate = maxFee
			}
			data.BlockTarget[fmt.Sprintf("%d", i+1)] = minFee
		}
		if data.MinFeeRate == math.MaxInt64 {
			data.MinFeeRate = 0
		}
		if data.MedianFeeRate == 0 {
			data.MedianFeeRate = blocks[0].MedianFee
		}
	}

	return data, nil
}

// FetchBlockHeight fetches the current Bitcoin tip block height.
func (ndf *NetworkDataFetcher) FetchBlockHeight() (int32, error) {
	resp, err := ndf.makeRequest(ndf.baseURL + "/blocks/tip/height")
	if err != nil {
		return 0, fmt.Errorf("failed to fetch block height: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %w", err)
	}

	var height int32
	if err := json.Unmarshal(body, &height); err != nil {
		return 0, fmt.Errorf("failed to parse block height: %w", err)
	}

	return height, nil
}

// FetchFeePercentiles fetches fee rate percentiles from the first two mempool block projections.
func (ndf *NetworkDataFetcher) FetchFeePercentiles() (map[string]int64, error) {
	blocks, err := ndf.FetchMempoolBlocks()
	if err != nil {
		return nil, err
	}

	percentiles := make(map[string]int64)

	if len(blocks) > 0 {
		if feeRange := blocks[0].FeeRange; len(feeRange) >= 2 {
			percentiles["min"] = feeRange[0]
			percentiles["max"] = feeRange[len(feeRange)-1]
		}
	}

	if len(blocks) > 1 {
		if feeRange := blocks[1].FeeRange; len(feeRange) > 0 {
			percentiles["median"] = feeRange[len(feeRange)/2]
		}
	}

	return percentiles, nil
}

// FetchMempoolBlocks fetches the projected fee ranges for upcoming blocks.
func (ndf *NetworkDataFetcher) FetchMempoolBlocks() ([]MempoolBlockProjection, error) {
	resp, err := ndf.makeRequest(ndf.baseURL + "/fees/mempool-blocks")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch mempool blocks: %w", err)
	}
	defer resp.Body.Close()

	var rawBlocks []struct {
		BlockVSize float64   `json:"blockVSize"`
		NTx        float64   `json:"nTx"`
		MedianFee  float64   `json:"medianFee"`
		FeeRange   []float64 `json:"feeRange"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawBlocks); err != nil {
		return nil, fmt.Errorf("failed to decode mempool blocks response: %w", err)
	}

	blocks := make([]MempoolBlockProjection, 0, len(rawBlocks))
	for _, raw := range rawBlocks {
		feeRange := make([]int64, 0, len(raw.FeeRange))
		for _, fee := range raw.FeeRange {
			feeRange = append(feeRange, int64(math.Round(fee)))
		}
		slices.Sort(feeRange)
		blocks = append(blocks, MempoolBlockProjection{
			BlockVSize:    int64(math.Round(raw.BlockVSize)),
			NTransactions: int64(math.Round(raw.NTx)),
			MedianFee:     int64(math.Round(raw.MedianFee)),
			FeeRange:      feeRange,
		})
	}

	return blocks, nil
}

// FetchNetworkStats fetches and combines fee rates, mempool size, and block height
// into a single NetworkStats snapshot. Congestion level is derived from fee ratios.
func (ndf *NetworkDataFetcher) FetchNetworkStats() (*NetworkStats, error) {
	fees, err := ndf.FetchRecommendedFees()
	if err != nil {
		return nil, err
	}

	mempool, err := ndf.FetchMempoolData()
	if err != nil {
		return nil, err
	}

	height, err := ndf.FetchBlockHeight()
	if err != nil {
		return nil, err
	}

	congestion := "low"
	if fees.StandardFee > 10 && fees.FastFee > 20 {
		congestion = "high"
	} else if fees.StandardFee > 5 || fees.FastFee > 10 {
		congestion = "medium"
	}

	return &NetworkStats{
		MempoolSize:        mempool.TotalMemoryUsed,
		TransactionCount:   mempool.TotalUnconfirmedTxs,
		AverageBlockTime:   10 * time.Minute,
		CongestionLevel:    congestion,
		LastBlockHeight:    height,
		LastBlockTime:      time.Now(),
		TotalFeesInMempool: 0,
	}, nil
}

// LightningFeeStats holds network-level fee statistics for Lightning routing estimates.
type LightningFeeStats struct {
	MedFeeRatePPM  int64 // median routing fee rate across the network (parts per million)
	AvgBaseFeeMsat int64 // average base fee per hop in millisatoshis
}

// FetchLightningFeeStats fetches current Lightning network fee statistics from mempool.space.
func (ndf *NetworkDataFetcher) FetchLightningFeeStats() (*LightningFeeStats, error) {
	resp, err := ndf.makeRequest(ndf.baseURL + "/lightning/statistics/latest")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch lightning stats: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Latest struct {
			MedFeeRate      int64 `json:"med_fee_rate"`
			AvgBaseFeeMtoks int64 `json:"avg_base_fee_mtokens"`
		} `json:"latest"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode lightning stats: %w", err)
	}

	return &LightningFeeStats{
		MedFeeRatePPM:  result.Latest.MedFeeRate,
		AvgBaseFeeMsat: result.Latest.AvgBaseFeeMtoks,
	}, nil
}

// FetchAddressBalance returns the confirmed BTC balance of a Bitcoin address
// by querying the mempool.space public API. The returned value is in BTC (not satoshis).
func (ndf *NetworkDataFetcher) FetchAddressBalance(ctx context.Context, address string) (float64, error) {
	url := "https://mempool.space/api/address/" + address

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("bitcoin: failed to create address request: %w", err)
	}
	req.Header.Set("User-Agent", ndf.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := ndf.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bitcoin: address balance request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bitcoin: mempool.space returned HTTP %d for address %s", resp.StatusCode, address)
	}

	var ar struct {
		ChainStats struct {
			FundedSum int64 `json:"funded_txo_sum"`
			SpentSum  int64 `json:"spent_txo_sum"`
		} `json:"chain_stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return 0, fmt.Errorf("bitcoin: failed to decode address response: %w", err)
	}

	confirmedSatoshis := ar.ChainStats.FundedSum - ar.ChainStats.SpentSum
	return float64(confirmedSatoshis) / 1e8, nil
}

// GetLastUpdate returns the timestamp of the most recent successful fetch.
func (ndf *NetworkDataFetcher) GetLastUpdate() time.Time {
	return ndf.lastUpdate
}

// makeRequest performs an HTTP GET with appropriate headers and validates the status code.
func (ndf *NetworkDataFetcher) makeRequest(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", ndf.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := ndf.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	return resp, nil
}
