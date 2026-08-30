package main

import (
	"fmt"
	"time"
)

const (
	defaultLifecycleRetentionDays   = 30
	lifecycleRetentionBatchSize     = 5000
	lifecycleRetentionBudgetDefault = 20 * time.Second
)

func lifecycleRetention() time.Duration {
	days := envIntOrDefault("LIFECYCLE_RETENTION_DAYS", defaultLifecycleRetentionDays)
	if days < 1 {
		return 0
	}
	return time.Duration(days) * 24 * time.Hour
}

func pruneOrderLifecycle(sdb closedOfferDB) {
	pruneOrderLifecycleUntil(sdb, time.Now().UTC(), lifecycleRetentionBudgetDefault)
}

func pruneOrderLifecycleUntil(sdb closedOfferDB, now time.Time, budget time.Duration) {
	retention := lifecycleRetention()
	if retention == 0 {
		return
	}
	cutoff := now.Add(-retention)
	deadline := time.Now().Add(budget)
	deleted := 0
	for time.Now().Before(deadline) {
		res, err := sdb.Exec(`
			DELETE FROM order_lifecycle
			WHERE id IN (
				SELECT id FROM order_lifecycle
				WHERE snapshot_time < ?
				ORDER BY id
				LIMIT ?
			)
		`, cutoff, lifecycleRetentionBatchSize)
		if err != nil {
			fmt.Printf("[LifecycleRetention] delete error: %v\n", err)
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			break
		}
		deleted += int(n)
		if n < int64(lifecycleRetentionBatchSize) {
			break
		}
	}
	if deleted > 0 {
		fmt.Printf("[LifecycleRetention] deleted=%d cutoff=%s\n", deleted, cutoff.UTC().Format(time.RFC3339))
	}
}
