package dbutil

import (
	"strings"
	"testing"
)

func TestLNDChannelBackupsMigrationDefinesTable(t *testing.T) {
	sqlBytes, err := schemaFiles.ReadFile("schema/019_lnd_channel_backups.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(sqlBytes)
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS lnd_channel_backups") {
		t.Fatal("migration 019 must create lnd_channel_backups")
	}
	if !strings.Contains(sql, "multi_chan_backup BYTEA") {
		t.Fatal("migration 019 must store encrypted backup blob in multi_chan_backup")
	}
}
