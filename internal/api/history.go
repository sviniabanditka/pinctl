// GET /api/v1/devices/{id}/history, per docs/16-history.md's "Query API".
package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/sviniabanditka/pinctl/internal/history"
)

// defaultHistoryLimit and maxHistoryLimit mirror docs/16's documented
// "limit | 2000 (max 10000)". internal/history.Store.Query enforces the
// same cap defensively too, but the API is what turns an over-cap request
// into 400 rather than silently clamping it.
const (
	defaultHistoryLimit = 2000
	maxHistoryLimit     = 10000
)

// historySampleOut is one entry of the response's "samples" array
// (docs/16's response shape). Min/Max are omitted for a raw (unbucketed)
// query, present for a bucketed one.
type historySampleOut struct {
	T   time.Time `json:"t"`
	V   float64   `json:"v"`
	Min *float64  `json:"min,omitempty"`
	Max *float64  `json:"max,omitempty"`
}

type historyResponse struct {
	DeviceID string             `json:"deviceId"`
	Role     string             `json:"role"`
	From     time.Time          `json:"from"`
	To       time.Time          `json:"to"`
	Bucket   string             `json:"bucket,omitempty"`
	Samples  []historySampleOut `json:"samples"`
}

// handleDeviceHistory serves GET /api/v1/devices/{id}/history. With
// history disabled on this instance (no HistoryStore wired in) it answers
// 503 history_disabled — an honest "this instance doesn't keep history"
// rather than an empty array that looks like a device nobody ever read
// (docs/16). Otherwise: 404 for an unknown device, 400 for an unparseable
// from/to/bucket, a from not before to, or a limit over maxHistoryLimit,
// 200 with raw or bucketed samples otherwise.
func (s *Server) handleDeviceHistory(w http.ResponseWriter, r *http.Request) {
	if s.history == nil {
		writeError(w, http.StatusServiceUnavailable, "history_disabled", "history is not enabled on this instance")
		return
	}

	id := r.PathValue("id")
	d, err := s.store.GetDevice(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	q := r.URL.Query()

	to := time.Now().UTC()
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeBadRequest(w, "to must be an RFC3339 timestamp")
			return
		}
		to = t.UTC()
	}

	from := to.Add(-time.Hour)
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeBadRequest(w, "from must be an RFC3339 timestamp")
			return
		}
		from = t.UTC()
	}
	if !from.Before(to) {
		writeBadRequest(w, "from must be before to")
		return
	}

	var bucket time.Duration
	if v := q.Get("bucket"); v != "" {
		b, err := time.ParseDuration(v)
		if err != nil || b <= 0 {
			writeBadRequest(w, "bucket must be a positive duration (e.g. 30s, 5m)")
			return
		}
		bucket = b
	}

	limit := defaultHistoryLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeBadRequest(w, "limit must be a positive integer")
			return
		}
		if n > maxHistoryLimit {
			writeBadRequest(w, fmt.Sprintf("limit must be <= %d", maxHistoryLimit))
			return
		}
		limit = n
	}

	samples, err := s.history.Query(id, history.Query{From: from, To: to, Bucket: bucket, Limit: limit})
	if err != nil {
		log.Printf("api: querying history for %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "querying history")
		return
	}

	out := historyResponse{
		DeviceID: d.ID,
		Role:     string(d.Role),
		From:     from,
		To:       to,
		Samples:  make([]historySampleOut, 0, len(samples)),
	}
	if bucket > 0 {
		out.Bucket = bucket.String()
	}
	for _, sm := range samples {
		out.Samples = append(out.Samples, historySampleOut{T: sm.T, V: sm.V, Min: sm.Min, Max: sm.Max})
	}
	writeJSON(w, http.StatusOK, out)
}
