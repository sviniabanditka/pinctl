package history

import (
	"fmt"
	"time"
)

// defaultQueryLimit and maxQueryLimit are docs/16's Query API defaults:
// "limit | 2000 (max 10000) | Row cap". The API layer validates and
// rejects an over-cap limit with 400 before ever calling Query; Query
// itself clamps defensively too, so a limit is enforced no matter how
// Query is called.
const (
	defaultQueryLimit = 2000
	maxQueryLimit     = 10000
)

// Query returns deviceID's samples in [q.From, q.To), ascending by time.
//
// With q.Bucket zero, raw samples are returned: if more than q.Limit rows
// fall in the window, the *newest* q.Limit are kept (docs/16: "raw samples
// are returned, newest-truncated at limit") — never the oldest, so a
// capped query still shows what's happening now.
//
// With q.Bucket set, each fixed q.Bucket-wide window yields one Sample
// whose V is the window's average and Min/Max the window's extremes, so a
// chart can draw a band instead of averaging away a spike (docs/16: "plus
// min/max so a chart can draw a band rather than lie about a spike it
// averaged away"). An empty window is omitted, not zero-filled. The
// bucket grouping — and the same newest-first truncation before ascending
// output — is computed in SQL via ts / bucketMs.
func (s *Store) Query(deviceID string, q Query) ([]Sample, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	if limit > maxQueryLimit {
		limit = maxQueryLimit
	}

	fromMs := q.From.UnixMilli()
	toMs := q.To.UnixMilli()

	if q.Bucket <= 0 {
		return s.queryRaw(deviceID, fromMs, toMs, limit)
	}
	return s.queryBucketed(deviceID, fromMs, toMs, q.Bucket.Milliseconds(), limit)
}

func (s *Store) queryRaw(deviceID string, fromMs, toMs int64, limit int) ([]Sample, error) {
	rows, err := s.db.Query(`
		SELECT ts, value FROM samples
		WHERE device_id = ? AND ts >= ? AND ts < ?
		ORDER BY ts DESC
		LIMIT ?`, deviceID, fromMs, toMs, limit)
	if err != nil {
		return nil, fmt.Errorf("history: querying %s: %w", deviceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Sample
	for rows.Next() {
		var ts int64
		var v float64
		if err := rows.Scan(&ts, &v); err != nil {
			return nil, fmt.Errorf("history: scanning %s: %w", deviceID, err)
		}
		out = append(out, Sample{T: time.UnixMilli(ts).UTC(), V: v})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: querying %s: %w", deviceID, err)
	}
	reverse(out) // DESC-then-reverse keeps the newest rows under limit, ascending on the way out
	return out, nil
}

func (s *Store) queryBucketed(deviceID string, fromMs, toMs, bucketMs int64, limit int) ([]Sample, error) {
	if bucketMs <= 0 {
		return nil, fmt.Errorf("history: bucket must be positive")
	}

	rows, err := s.db.Query(`
		SELECT (ts / ?) AS bucket, AVG(value), MIN(value), MAX(value)
		FROM samples
		WHERE device_id = ? AND ts >= ? AND ts < ?
		GROUP BY bucket
		ORDER BY bucket DESC
		LIMIT ?`, bucketMs, deviceID, fromMs, toMs, limit)
	if err != nil {
		return nil, fmt.Errorf("history: querying %s: %w", deviceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Sample
	for rows.Next() {
		var bucket int64
		var avg, lo, hi float64
		if err := rows.Scan(&bucket, &avg, &lo, &hi); err != nil {
			return nil, fmt.Errorf("history: scanning %s: %w", deviceID, err)
		}
		out = append(out, Sample{
			T:   time.UnixMilli(bucket * bucketMs).UTC(),
			V:   avg,
			Min: &lo,
			Max: &hi,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: querying %s: %w", deviceID, err)
	}
	reverse(out)
	return out, nil
}

func reverse(s []Sample) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
