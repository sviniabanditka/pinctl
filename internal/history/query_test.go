package history

import (
	"testing"
	"time"
)

// insertRaw inserts one raw sample directly, bypassing Record, for tests
// that want exact control over timestamps.
func insertRaw(t *testing.T, s *Store, deviceID string, at time.Time, value float64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO samples (device_id, ts, value) VALUES (?, ?, ?)`,
		deviceID, at.UnixMilli(), value); err != nil {
		t.Fatalf("inserting sample: %v", err)
	}
}

func TestQuery_RawAscendingOrder(t *testing.T) {
	s := openTestStore(t, testCfg())
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	insertRaw(t, s, "dev_1", base, 1)
	insertRaw(t, s, "dev_1", base.Add(time.Second), 2)
	insertRaw(t, s, "dev_1", base.Add(2*time.Second), 3)

	samples, err := s.Query("dev_1", Query{From: base.Add(-time.Minute), To: base.Add(time.Minute)})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("len(samples) = %d, want 3", len(samples))
	}
	for i, want := range []float64{1, 2, 3} {
		if samples[i].V != want {
			t.Errorf("samples[%d].V = %v, want %v", i, samples[i].V, want)
		}
		if samples[i].Min != nil || samples[i].Max != nil {
			t.Errorf("samples[%d] has Min/Max set for a raw (unbucketed) query", i)
		}
	}
	if !samples[0].T.Before(samples[1].T) || !samples[1].T.Before(samples[2].T) {
		t.Error("expected ascending time order")
	}
}

func TestQuery_RawFromToWindow(t *testing.T) {
	s := openTestStore(t, testCfg())
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	insertRaw(t, s, "dev_1", base.Add(-time.Hour), 1) // outside window
	insertRaw(t, s, "dev_1", base, 2)                 // inside (from is inclusive)
	insertRaw(t, s, "dev_1", base.Add(time.Hour), 3)  // outside window (to is exclusive)

	samples, err := s.Query("dev_1", Query{From: base, To: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 1 || samples[0].V != 2 {
		t.Fatalf("samples = %+v, want exactly the one in-window sample", samples)
	}
}

func TestQuery_BucketedAvgMinMax(t *testing.T) {
	s := openTestStore(t, testCfg())
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	// Window 1 (12:00:00-12:00:30): 100, 200 -> avg 150, min 100, max 200.
	insertRaw(t, s, "dev_1", base, 100)
	insertRaw(t, s, "dev_1", base.Add(10*time.Second), 200)
	// Window 2 (12:00:30-12:01:00): a single 50.
	insertRaw(t, s, "dev_1", base.Add(35*time.Second), 50)

	samples, err := s.Query("dev_1", Query{From: base, To: base.Add(time.Minute), Bucket: 30 * time.Second})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2 (one per non-empty window); got %+v", len(samples), samples)
	}

	first := samples[0]
	if first.V != 150 {
		t.Errorf("window 1 avg = %v, want 150", first.V)
	}
	if first.Min == nil || *first.Min != 100 {
		t.Errorf("window 1 min = %v, want 100", first.Min)
	}
	if first.Max == nil || *first.Max != 200 {
		t.Errorf("window 1 max = %v, want 200", first.Max)
	}

	second := samples[1]
	if second.V != 50 || second.Min == nil || *second.Min != 50 || second.Max == nil || *second.Max != 50 {
		t.Errorf("window 2 = %+v, want avg=min=max=50", second)
	}
}

func TestQuery_EmptyWindowsOmitted(t *testing.T) {
	s := openTestStore(t, testCfg())
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	insertRaw(t, s, "dev_1", base, 1)
	insertRaw(t, s, "dev_1", base.Add(2*time.Minute), 2) // a gap of empty buckets in between

	samples, err := s.Query("dev_1", Query{From: base, To: base.Add(3 * time.Minute), Bucket: 30 * time.Second})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2 (empty windows omitted, not zero-filled); got %+v", len(samples), samples)
	}
}

func TestQuery_LimitEnforcedServerSide(t *testing.T) {
	s := openTestStore(t, testCfg())
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		insertRaw(t, s, "dev_1", base.Add(time.Duration(i)*time.Second), float64(i))
	}

	// A limit far beyond maxQueryLimit is clamped, not honored verbatim.
	samples, err := s.Query("dev_1", Query{From: base, To: base.Add(time.Hour), Limit: 1_000_000})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 20 {
		t.Fatalf("len(samples) = %d, want all 20 (well under the cap)", len(samples))
	}

	samples, err = s.Query("dev_1", Query{From: base, To: base.Add(time.Hour), Limit: 5})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 5 {
		t.Fatalf("len(samples) = %d, want 5", len(samples))
	}
	// Newest-truncated: keeping the most recent 5 of 20 (i=15..19), ascending.
	if samples[0].V != 15 || samples[4].V != 19 {
		t.Errorf("limited samples = %+v, want the newest 5 (15..19) ascending", samples)
	}
}

func TestQuery_DeviceWithNoSamples(t *testing.T) {
	s := openTestStore(t, testCfg())
	samples, err := s.Query("dev_nothing", Query{From: time.Now().Add(-time.Hour), To: time.Now()})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("len(samples) = %d, want 0", len(samples))
	}
}
