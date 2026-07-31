package dbutil

import (
	"strings"
	"testing"
)

func TestDropLNDMasterKeyHashesMigration(t *testing.T) {
	sqlBytes, err := migrationFiles.ReadFile("migrations/20260724_drop_lnd_master_key_hashes.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if !strings.Contains(string(sqlBytes), "DROP TABLE IF EXISTS lnd_master_key_hashes") {
		t.Fatal("migration must drop lnd_master_key_hashes")
	}
}
