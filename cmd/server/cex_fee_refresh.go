package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"commons/exchangecredentials"
	"commons/tradecost/cextrading"
)

const defaultCEXFeeRefreshInterval = 15 * time.Minute

func runPeriodicCEXFeeRefresh(ctx context.Context, interval time.Duration, refresh func(context.Context) error, reportError func(error)) {
	if interval <= 0 {
		interval = defaultCEXFeeRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := refresh(ctx); err != nil && reportError != nil {
				reportError(err)
			}
		}
	}
}

func refreshPersistedCEXFees(ctx context.Context, database *sql.DB, credentials *exchangecredentials.Store) error {
	var failures []string
	for _, provider := range cextrading.SupportedCEXes() {
		if err := refreshProviderFees(ctx, database, credentials, provider); err != nil {
			failures = append(failures, provider+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("CEX fee refresh failures: %s", strings.Join(failures, "; "))
	}
	return nil
}

func refreshProviderFees(ctx context.Context, database *sql.DB, credentials *exchangecredentials.Store, provider string) error {
	now := time.Now().UTC()
	if _, err := database.ExecContext(ctx, `
		INSERT INTO mm_cex_provider_policies (
			provider, fees_last_attempted_at, spot_fees_last_attempted_at,
			withdrawal_fees_last_attempted_at, hedge_fees_last_attempted_at)
		VALUES ($1, $2, $2, $2, $2) ON CONFLICT (provider) DO UPDATE SET
			fees_last_attempted_at = EXCLUDED.fees_last_attempted_at,
			spot_fees_last_attempted_at = EXCLUDED.spot_fees_last_attempted_at,
			withdrawal_fees_last_attempted_at = EXCLUDED.withdrawal_fees_last_attempted_at,
			hedge_fees_last_attempted_at = EXCLUDED.hedge_fees_last_attempted_at
	`, provider, now); err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	credentialSets, err := credentials.ResolveAll(provider)
	if err != nil {
		credentialSets = nil
	}
	componentErrors := make([]string, 0, 3)

	spotRate, spotErr := fetchFirstCredential(credentialSets, provider == "coinbase", func(creds exchangecredentials.Credentials) (float64, error) {
		return cextrading.FetchLiveSpotTakerRate(provider, creds)
	})
	if spotErr == nil {
		_, spotErr = database.ExecContext(ctx, `UPDATE mm_cex_provider_policies SET
			spot_taker_fee_percent=$2, spot_fees_fetched_at=$3, spot_fees_last_error=NULL
			WHERE provider=$1`, provider, spotRate, now)
	}
	if spotErr != nil {
		componentErrors = append(componentErrors, "spot: "+spotErr.Error())
		_, _ = database.ExecContext(ctx, `UPDATE mm_cex_provider_policies SET spot_fees_last_error=$2 WHERE provider=$1`, provider, spotErr.Error())
	}
	appMetrics.observeCEXFeeRefresh(provider, "spot", func() *float64 {
		if spotErr == nil {
			return &spotRate
		}
		return nil
	}(), "exchange_reported", spotErr)

	withdrawalFee, withdrawalSource, observedFee, observedAt, withdrawalWarning, withdrawalErr := refreshWithdrawalComponent(ctx, database, provider, credentialSets, now)
	if withdrawalErr == nil {
		_, withdrawalErr = database.ExecContext(ctx, `UPDATE mm_cex_provider_policies SET
			withdrawal_fee_btc=$2, withdrawal_fees_fetched_at=$3, withdrawal_fees_last_error=NULLIF($7, ''),
			withdrawal_fee_source=$4, withdrawal_fee_observed_btc=$5, withdrawal_fee_observed_at=$6
			WHERE provider=$1`, provider, withdrawalFee, now, withdrawalSource, observedFee, observedAt, withdrawalWarning)
	}
	if withdrawalErr != nil {
		componentErrors = append(componentErrors, "withdrawal: "+withdrawalErr.Error())
		_, _ = database.ExecContext(ctx, `UPDATE mm_cex_provider_policies SET withdrawal_fees_last_error=$2 WHERE provider=$1`, provider, withdrawalErr.Error())
	} else if withdrawalWarning != "" {
		componentErrors = append(componentErrors, "withdrawal: "+withdrawalWarning)
	}
	withdrawalMetricErr := withdrawalErr
	if withdrawalMetricErr == nil && withdrawalWarning != "" {
		withdrawalMetricErr = fmt.Errorf("%s", withdrawalWarning)
	}
	appMetrics.observeCEXFeeRefresh(provider, "withdrawal", func() *float64 {
		if withdrawalErr == nil {
			return &withdrawalFee
		}
		return nil
	}(), withdrawalSource, withdrawalMetricErr)

	if provider != "bitstamp" {
		hedgeSnapshot, hedgeErr := fetchFirstHedgeCredential(credentialSets, provider)
		if hedgeErr == nil {
			_, hedgeErr = database.ExecContext(ctx, `UPDATE mm_cex_provider_policies SET
				futures_open_taker_fee_percent=$2, futures_close_taker_fee_percent=$3,
				futures_funding_fee_percent=$4, futures_funding_interval_seconds=$5,
				hedge_fees_fetched_at=$6, hedge_fees_last_error=NULL WHERE provider=$1`,
				provider, hedgeSnapshot.FuturesOpenTakerRate, hedgeSnapshot.FuturesCloseTakerRate,
				hedgeSnapshot.FuturesFundingRate, hedgeSnapshot.FundingIntervalSeconds, now)
		}
		if hedgeErr != nil {
			componentErrors = append(componentErrors, "hedge: "+hedgeErr.Error())
			_, _ = database.ExecContext(ctx, `UPDATE mm_cex_provider_policies SET hedge_fees_last_error=$2 WHERE provider=$1`, provider, hedgeErr.Error())
		}
		var hedgeValue *float64
		if hedgeErr == nil && hedgeSnapshot.FuturesOpenTakerRate != nil {
			hedgeValue = hedgeSnapshot.FuturesOpenTakerRate
		}
		appMetrics.observeCEXFeeRefresh(provider, "hedge", hedgeValue, "exchange_reported", hedgeErr)
	}

	aggregateError := strings.Join(componentErrors, "; ")
	_, persistErr := database.ExecContext(ctx, `UPDATE mm_cex_provider_policies SET
		fees_fetched_at=GREATEST(spot_fees_fetched_at, withdrawal_fees_fetched_at, hedge_fees_fetched_at),
		fees_last_error=NULLIF($2, '') WHERE provider=$1`, provider, aggregateError)
	if persistErr != nil {
		return fmt.Errorf("persist aggregate status: %w", persistErr)
	}
	if aggregateError != "" {
		return fmt.Errorf("%s", aggregateError)
	}
	return nil
}

func fetchFirstCredential[T any](sets []exchangecredentials.Credentials, requireCDP bool, fetch func(exchangecredentials.Credentials) (T, error)) (T, error) {
	var zero T
	var failures []string
	for _, creds := range sets {
		if requireCDP && strings.TrimSpace(creds.Passphrase) != "" {
			continue
		}
		value, err := fetch(creds)
		if err == nil {
			return value, nil
		}
		failures = append(failures, err.Error())
	}
	if len(failures) == 0 {
		return zero, fmt.Errorf("no compatible credentials configured")
	}
	return zero, fmt.Errorf("%s", strings.Join(failures, "; "))
}

func fetchFirstHedgeCredential(sets []exchangecredentials.Credentials, provider string) (cextrading.LiveFeeSnapshot, error) {
	return fetchFirstCredential(sets, false, func(creds exchangecredentials.Credentials) (cextrading.LiveFeeSnapshot, error) {
		if provider == "coinbase" && strings.TrimSpace(creds.Passphrase) == "" {
			return cextrading.LiveFeeSnapshot{}, fmt.Errorf("coinbase INTX credentials not configured on this key")
		}
		return cextrading.FetchLiveHedgeFeeSnapshot(provider, creds)
	})
}

func refreshWithdrawalComponent(ctx context.Context, database *sql.DB, provider string, sets []exchangecredentials.Credentials, now time.Time) (float64, string, sql.NullFloat64, sql.NullTime, string, error) {
	if provider != "coinbase" {
		fee, err := fetchFirstCredential(sets, false, func(creds exchangecredentials.Credentials) (float64, error) {
			return cextrading.FetchLiveWithdrawalFee(provider, creds)
		})
		return fee, "exchange_reported", sql.NullFloat64{}, sql.NullTime{}, "", err
	}
	observations, err := fetchFirstCredential(sets, true, func(creds exchangecredentials.Credentials) ([]cextrading.CoinbaseFeeObservation, error) {
		return cextrading.FetchCoinbaseSendFeeObservations(creds, 30*24*time.Hour)
	})
	warning := ""
	if err != nil {
		warning = "Coinbase send-fee history unavailable; using conservative floor/mempool reserve: " + err.Error()
	}
	for _, observation := range observations {
		metadata, marshalErr := json.Marshal(map[string]string{"tx_hash": observation.TxHash})
		if marshalErr != nil {
			return 0, "", sql.NullFloat64{}, sql.NullTime{}, warning, fmt.Errorf("encode Coinbase fee observation: %w", marshalErr)
		}
		if _, err := database.ExecContext(ctx, `INSERT INTO cex_fee_observations
			(provider, component, external_id, value, observed_at, metadata)
			VALUES ('coinbase','withdrawal',$1,$2,$3,$4::jsonb)
			ON CONFLICT (provider, component, external_id) DO UPDATE SET value=EXCLUDED.value,
			observed_at=EXCLUDED.observed_at, metadata=EXCLUDED.metadata`,
			observation.TransactionID, observation.AmountBTC, observation.ObservedAt, string(metadata)); err != nil {
			return 0, "", sql.NullFloat64{}, sql.NullTime{}, warning, fmt.Errorf("persist Coinbase fee observation: %w", err)
		}
	}
	var observed sql.NullFloat64
	var observedAt sql.NullTime
	if err := database.QueryRowContext(ctx, `SELECT MAX(value), MAX(observed_at) FROM cex_fee_observations
		WHERE provider='coinbase' AND component='withdrawal' AND observed_at >= $1`, now.Add(-30*24*time.Hour)).Scan(&observed, &observedAt); err != nil {
		return 0, "", sql.NullFloat64{}, sql.NullTime{}, warning, err
	}
	floorSat := int64(10_000)
	if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(os.Getenv("COINBASE_WITHDRAW_RESERVE_FLOOR_SAT")), 10, 64); parseErr == nil && parsed > 0 {
		floorSat = parsed
	}
	reserve := float64(floorSat) / 1e8
	if observed.Valid && observed.Float64 > reserve {
		reserve = observed.Float64
	}
	return reserve, "conservative_combined", observed, observedAt, warning, nil
}
