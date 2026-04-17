package hedge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestFetchKrakenPerpInputs(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("supersecret"))
	t.Setenv("KRAKEN_FUTURES_API_KEY", "kraken-key")
	t.Setenv("KRAKEN_FUTURES_API_SECRET", secret)

	prevBase := krakenFuturesAPIBase
	prevClient := hedgeHTTPClient
	krakenFuturesAPIBase = "https://kraken.test"
	hedgeHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/derivatives/api/v3/feeschedules":
			return jsonResponse(t, http.StatusOK, map[string]any{
				"feeSchedules": []any{
					map[string]any{
						"uid":  "sched-1",
						"name": "KF USD Multi-Collateral Fees",
						"tiers": []any{
							map[string]any{"makerFee": 0.02, "takerFee": 0.05, "usdVolume": 0.0},
							map[string]any{"makerFee": 0.015, "takerFee": 0.04, "usdVolume": 100000.0},
						},
					},
				},
			}), nil
		case "/derivatives/api/v3/feeschedules/volumes":
			if req.Header.Get("APIKey") == "" || req.Header.Get("Authent") == "" {
				t.Fatalf("expected Kraken auth headers")
			}
			return jsonResponse(t, http.StatusOK, map[string]any{
				"result": "success",
				"volumesByFeeSchedule": map[string]any{
					"sched-1": 100000.0,
				},
			}), nil
		case "/derivatives/api/v3/historical-funding-rates":
			return jsonResponse(t, http.StatusOK, map[string]any{
				"rates": []any{
					map[string]any{"timestamp": "2026-04-13T00:00:00.000Z", "fundingRate": 1.0e-08, "relativeFundingRate": 0.0004},
					map[string]any{"timestamp": "2026-04-13T04:00:00.000Z", "fundingRate": 1.1e-08, "relativeFundingRate": 0.0005},
				},
			}), nil
		default:
			t.Fatalf("unexpected path: %s", req.URL.Path)
			return nil, nil
		}
	})}
	t.Cleanup(func() {
		krakenFuturesAPIBase = prevBase
		hedgeHTTPClient = prevClient
	})

	got, err := FetchKrakenPerpInputs("PI_XBTUSD")
	if err != nil {
		t.Fatalf("FetchKrakenPerpInputs returned error: %v", err)
	}

	assertFloatClose(t, got.OpenFeeRate, 0.0004)
	assertFloatClose(t, got.CloseFeeRate, 0.0004)
	assertFloatClose(t, got.FundingRate, 0.0005)
	if got.FundingInterval != 4*time.Hour {
		t.Fatalf("funding interval = %s, want 4h", got.FundingInterval)
	}
}

func TestFetchCoinbaseFuturesInputs(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("coinbase-secret"))
	t.Setenv("COINBASE_FUTURES_API_KEY", "cb-key")
	t.Setenv("COINBASE_FUTURES_API_SECRET", secret)
	t.Setenv("COINBASE_FUTURES_API_PASSPHRASE", "cb-pass")

	prevBase := coinbaseINTXAPIBase
	prevClient := hedgeHTTPClient
	coinbaseINTXAPIBase = "https://coinbase.test"
	hedgeHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/portfolios/fee-rates":
			if req.Header.Get("CB-ACCESS-KEY") == "" || req.Header.Get("CB-ACCESS-SIGN") == "" || req.Header.Get("CB-ACCESS-PASSPHRASE") == "" {
				t.Fatalf("expected Coinbase auth headers")
			}
			return jsonResponse(t, http.StatusOK, []any{
				map[string]any{"instrument_type": "SPOT", "maker_fee_rate": 0.001, "taker_fee_rate": 0.002},
				map[string]any{"instrument_type": "PERP", "maker_fee_rate": 0.0002, "taker_fee_rate": 0.0004},
			}), nil
		case "/api/v1/instruments/BTC-PERP":
			return jsonResponse(t, http.StatusOK, map[string]any{
				"symbol":           "BTC-PERP",
				"funding_interval": "28800000000000",
				"quote": map[string]any{
					"predicted_funding": "0.0003",
				},
			}), nil
		default:
			t.Fatalf("unexpected path: %s", req.URL.Path)
			return nil, nil
		}
	})}
	t.Cleanup(func() {
		coinbaseINTXAPIBase = prevBase
		hedgeHTTPClient = prevClient
	})

	got, err := FetchCoinbaseFuturesInputs("BTC-PERP")
	if err != nil {
		t.Fatalf("FetchCoinbaseFuturesInputs returned error: %v", err)
	}

	assertFloatClose(t, got.OpenFeeRate, 0.0004)
	assertFloatClose(t, got.CloseFeeRate, 0.0004)
	assertFloatClose(t, got.FundingRate, 0.0003)
	if got.FundingInterval != 8*time.Hour {
		t.Fatalf("funding interval = %s, want 8h", got.FundingInterval)
	}
}

func TestFetchCoinbaseFuturesInputsRequiresPassphrase(t *testing.T) {
	t.Setenv("COINBASE_FUTURES_API_KEY", "cb-key")
	t.Setenv("COINBASE_FUTURES_API_SECRET", base64.StdEncoding.EncodeToString([]byte("secret")))
	_ = os.Unsetenv("COINBASE_FUTURES_API_PASSPHRASE")
	_ = os.Unsetenv("COINBASE_API_PASSPHRASE")

	if _, err := FetchCoinbaseFuturesInputs("BTC-PERP"); err == nil {
		t.Fatal("expected missing-credentials error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(t *testing.T, status int, payload any) *http.Response {
	t.Helper()
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body.Bytes())),
	}
}

func assertFloatClose(t *testing.T, got, want float64) {
	t.Helper()
	const eps = 1e-12
	if got < want-eps || got > want+eps {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}
