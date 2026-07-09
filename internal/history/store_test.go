package history

import (
	"path/filepath"
	"testing"
)

func TestOpen_FreshCreatesSchemaAtCurrentVersion(t *testing.T) {
	s := openTestStore(t, testCfg())

	var version string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("reading schema_version: %v", err)
	}
	if version != "1" {
		t.Errorf("schema_version = %q, want %q", version, "1")
	}

	// samples table + index exist and are queryable.
	if _, err := s.db.Exec(`INSERT INTO samples (device_id, ts, value) VALUES ('dev_1', 1000, 1.5)`); err != nil {
		t.Fatalf("inserting into samples: %v", err)
	}
}

func TestOpen_ReopenPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	cfg := testCfg()

	s1, err := Open(path, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s1.db.Exec(`INSERT INTO samples (device_id, ts, value) VALUES ('dev_1', 1000, 1.5)`); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path, cfg)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer s2.Close()

	n := countRows(t, s2, "dev_1")
	if n != 1 {
		t.Errorf("row count after reopen = %d, want 1", n)
	}
}

func TestOpen_HigherSchemaVersionRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	cfg := testCfg()

	s1, err := Open(path, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s1.db.Exec(`UPDATE meta SET value = '999' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("bumping schema_version: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := Open(path, cfg); err == nil {
		t.Fatal("Open with a newer-than-supported schema_version should refuse, got nil error")
	}
}
