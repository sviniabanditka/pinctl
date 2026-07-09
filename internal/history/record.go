package history

import (
	"context"
	"log"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/sampling"
)

// maxBufferRows is docs/16's "flush ... sooner if the buffer exceeds 512
// rows".
const maxBufferRows = 512

// maxFlushBackoff caps how long Record waits between flush attempts after
// consecutive failures, per docs/16's "consecutive failures back off"; it
// must stay bounded so a transient failure (a full card that then frees
// up) is noticed again within a reasonable time rather than never.
const maxFlushBackoff = 5 * time.Minute

// flushFailureLogWindow is how often a still-failing flush gets logged
// again, per docs/16's "Consecutive failures back off and stop retrying
// rather than filling the journal" — the first failure always logs, every
// one after that is rate-limited so a persistently full disk doesn't spam
// the log forever.
const flushFailureLogWindow = time.Minute

// sampleRow is one buffered sample awaiting flush.
type sampleRow struct {
	deviceID string
	ts       int64 // unix milliseconds, UTC
	value    float64
}

// Record subscribes to bus and records a sample for every Update that
// passes the sampling.Policy built from s's config, per device, batching
// them into one transaction every s.cfg.FlushInterval (or sooner, once
// the buffer exceeds maxBufferRows). It returns once ctx is done, after a
// final flush of whatever is buffered. Call it from its own goroutine;
// devices is consulted (best-effort) to skip an Update for a device that
// has already been deleted from the store, so a stray in-flight Update
// racing DeleteDevice can't resurrect samples DeleteDevice just removed.
//
// A failed flush is logged and its batch dropped — telemetry is not worth
// blocking, or slowly growing unbounded memory for, the runtime (docs/16's
// "A flush fails (disk full, corruption)").
func (s *Store) Record(ctx context.Context, bus Bus, devices DeviceLister) {
	ch, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	policy := sampling.Policy{
		Deadband:    s.cfg.Deadband,
		MinInterval: time.Duration(s.cfg.MinInterval),
		MaxInterval: time.Duration(s.cfg.MaxInterval),
	}

	states := make(map[string]sampling.State)
	var buf []sampleRow

	var (
		consecutiveFails int
		backoffUntil     time.Time
		lastFailLog      time.Time
	)

	flush := func() {
		if len(buf) == 0 {
			return
		}
		rows := buf
		buf = nil // dropped either way: on success it's written, on failure docs/16 says drop it

		now := s.clock.Now()
		if consecutiveFails > 0 && now.Before(backoffUntil) {
			return // still backing off; the batch is simply lost, same as a hard flush failure
		}

		if err := s.insertBatch(rows); err != nil {
			consecutiveFails++
			backoff := time.Duration(s.cfg.FlushInterval) << uint(min(consecutiveFails, 16))
			if backoff <= 0 || backoff > maxFlushBackoff {
				backoff = maxFlushBackoff
			}
			backoffUntil = now.Add(backoff)
			if consecutiveFails == 1 || now.Sub(lastFailLog) >= flushFailureLogWindow {
				log.Printf("history: flushing %d sample(s): %v (backing off %s)", len(rows), err, backoff)
				lastFailLog = now
			}
			return
		}
		consecutiveFails = 0
	}

	ticker := time.NewTicker(time.Duration(s.cfg.FlushInterval))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case u, ok := <-ch:
			if !ok {
				ch = nil
				continue
			}
			if _, err := devices.GetDevice(u.Device.ID); err != nil {
				continue // deleted since the update was queued
			}
			prev := states[u.Device.ID]
			if !policy.Decide(prev, u.Device.Role, u.Value, u.At) {
				continue
			}
			states[u.Device.ID] = sampling.State{Value: u.Value, At: u.At, Valid: true}
			buf = append(buf, sampleRow{
				deviceID: u.Device.ID,
				ts:       u.At.UnixMilli(),
				value:    toStoredValue(u.Value),
			})
			if len(buf) >= maxBufferRows {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// toStoredValue converts an engine.Value to the float64 samples stores
// it as: a bool becomes 0.0/1.0, per docs/16's "a bool is stored as
// 0.0/1.0 rather than in its own column".
func toStoredValue(v engine.Value) float64 {
	if v.Type == engine.TypeBool {
		if v.Bool {
			return 1
		}
		return 0
	}
	return v.Float
}

// insertBatch writes rows in one transaction, per docs/16's "Writes are
// batched ... flush inside one transaction".
func (s *Store) insertBatch(rows []sampleRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	stmt, err := tx.Prepare(`INSERT INTO samples (device_id, ts, value) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range rows {
		if _, err := stmt.Exec(r.deviceID, r.ts, r.value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
