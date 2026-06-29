package dbutil

import (
	"strings"
	"testing"
)

func TestLNDChannelBackupsMigrationDefinesTable(t *testing.T) {
	sqlBytes, err := schemaFiles.ReadFile("schema/14_lnd_channel_backups.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	sql := string(sqlBytes)
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS lnd_channel_backups") {
		t.Fatal("schema must create lnd_channel_backups")
	}
	if !strings.Contains(sql, "multi_chan_backup BYTEA") {
		t.Fatal("schema must store encrypted backup blob in multi_chan_backup")
	}
}
