package main

import (
	"context"
	"database/sql"
	"fmt"
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
		INSERT INTO mm_cex_provider_policies (provider, fees_last_attempted_at)
		VALUES ($1, $2) ON CONFLICT (provider)
		DO UPDATE SET fees_last_attempted_at = EXCLUDED.fees_last_attempted_at
	`, provider, now); err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	credentialSets, err := credentials.ResolveAll(provider)
	var snapshot cextrading.LiveFeeSnapshot
	if err == nil {
		for _, creds := range credentialSets {
			snapshot, err = cextrading.FetchLiveFeeSnapshot(provider, creds)
			if err == nil {
				break
			}
		}
	}
	if err == nil {
		var hedgeErrors []string
		for _, creds := range credentialSets {
			hedgeSnapshot, hedgeErr := cextrading.FetchLiveHedgeFeeSnapshot(provider, creds)
			if hedgeErr != nil {
				hedgeErrors = append(hedgeErrors, hedgeErr.Error())
				continue
			}
			snapshot.FuturesOpenTakerRate = hedgeSnapshot.FuturesOpenTakerRate
			snapshot.FuturesCloseTakerRate = hedgeSnapshot.FuturesCloseTakerRate
			snapshot.FuturesFundingRate = hedgeSnapshot.FuturesFundingRate
			snapshot.FundingIntervalSeconds = hedgeSnapshot.FundingIntervalSeconds
			break
		}
		if len(hedgeErrors) == len(credentialSets) {
			err = fmt.Errorf("hedge fee refresh: %s", strings.Join(hedgeErrors, "; "))
		}
	}
	hedgeWarning := ""
	if err != nil && snapshot.SpotTakerFeeRate != nil {
		hedgeWarning = err.Error()
		err = nil
	}
	if err != nil {
		_, recordErr := database.ExecContext(ctx, `
			UPDATE mm_cex_provider_policies SET fees_last_error = $2 WHERE provider = $1
		`, provider, err.Error())
		if recordErr != nil {
			return fmt.Errorf("fetch: %v; record failure: %w", err, recordErr)
		}
		return err
	}
	_, err = database.ExecContext(ctx, `
		UPDATE mm_cex_provider_policies SET
			spot_taker_fee_percent = COALESCE($2, spot_taker_fee_percent),
			withdrawal_fee_btc = COALESCE($3, withdrawal_fee_btc),
			futures_open_taker_fee_percent = COALESCE($4, futures_open_taker_fee_percent),
			futures_close_taker_fee_percent = COALESCE($5, futures_close_taker_fee_percent),
			futures_funding_fee_percent = COALESCE($6, futures_funding_fee_percent),
			futures_funding_interval_seconds = COALESCE($7, futures_funding_interval_seconds),
		fees_fetched_at = $8, fees_last_error = NULLIF($9, '')
		WHERE provider = $1
	`, provider, snapshot.SpotTakerFeeRate, snapshot.BTCWithdrawalFeeBTC,
		snapshot.FuturesOpenTakerRate, snapshot.FuturesCloseTakerRate,
		snapshot.FuturesFundingRate, snapshot.FundingIntervalSeconds, snapshot.FetchedAt, hedgeWarning)
	if err != nil {
		return fmt.Errorf("persist snapshot: %w", err)
	}
	return nil
}
