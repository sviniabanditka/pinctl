package history

import (
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
)

func TestRetain_DeletesOldKeepsRecent(t *testing.T) {
	cfg := testCfg()
	cfg.Retention = config.Duration(time.Hour)
	s := openTestStore(t, cfg)

	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	insertRaw(t, s, "dev_1", now.Add(-2*time.Hour), 1)    // older than retention: gone
	insertRaw(t, s, "dev_1", now.Add(-30*time.Minute), 2) // within retention: kept

	if err := s.Retain(now); err != nil {
		t.Fatalf("Retain: %v", err)
	}

	if n := countRows(t, s, "dev_1"); n != 1 {
		t.Fatalf("row count after Retain = %d, want 1", n)
	}
	var v float64
	if err := s.db.QueryRow(`SELECT value FROM samples WHERE device_id = 'dev_1'`).Scan(&v); err != nil {
		t.Fatalf("reading remaining row: %v", err)
	}
	if v != 2 {
		t.Errorf("remaining row value = %v, want 2 (the recent one)", v)
	}
}

func TestRetain_ZeroRetentionKeepsEverything(t *testing.T) {
	cfg := testCfg()
	cfg.Retention = 0
	s := openTestStore(t, cfg)

	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	insertRaw(t, s, "dev_1", now.Add(-24*365*time.Hour), 1)

	if err := s.Retain(now); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if n := countRows(t, s, "dev_1"); n != 1 {
		t.Errorf("row count with retention disabled = %d, want 1 (nothing deleted)", n)
	}
}

func TestDeleteDevice_RemovesOnlyThatDevice(t *testing.T) {
	s := openTestStore(t, testCfg())
	now := time.Now().UTC()
	insertRaw(t, s, "dev_1", now, 1)
	insertRaw(t, s, "dev_1", now.Add(time.Second), 2)
	insertRaw(t, s, "dev_2", now, 3)

	if err := s.DeleteDevice("dev_1"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	if n := countRows(t, s, "dev_1"); n != 0 {
		t.Errorf("dev_1 rows after DeleteDevice = %d, want 0", n)
	}
	if n := countRows(t, s, "dev_2"); n != 1 {
		t.Errorf("dev_2 rows after deleting dev_1 = %d, want 1 (untouched)", n)
	}
}
