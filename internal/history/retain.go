package history

import (
	"fmt"
	"time"
)

// incrementalVacuumPages bounds each Retain pass's incremental vacuum to a
// few hundred pages, per docs/16's "an incremental vacuum bounded to a few
// hundred pages so the pass never stalls" — freeing space from deleted
// rows gradually rather than in one large, latency-spiking pass.
const incrementalVacuumPages = 200

// Retain deletes every sample older than s.cfg.Retention (relative to
// now) and runs a bounded incremental vacuum to reclaim their space,
// per docs/16's "Retention". The caller runs it once at startup and then
// hourly for the life of the process; Retain itself is just one pass. A
// zero or negative Retention disables deletion (keep everything) but
// still runs the vacuum, matching "auto_vacuum=incremental" always being
// on regardless of the retention window.
func (s *Store) Retain(now time.Time) error {
	if s.cfg.Retention > 0 {
		cutoff := now.Add(-time.Duration(s.cfg.Retention)).UnixMilli()
		if _, err := s.db.Exec(`DELETE FROM samples WHERE ts < ?`, cutoff); err != nil {
			return fmt.Errorf("history: retention delete: %w", err)
		}
	}
	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, incrementalVacuumPages)); err != nil {
		return fmt.Errorf("history: incremental vacuum: %w", err)
	}
	return nil
}

// DeleteDevice removes every sample recorded for deviceID, per docs/16's
// "Deleting a device deletes its samples." Deleting an id with no samples
// is a no-op, not an error — callers (the API's device-delete handler)
// treat a DeleteDevice failure as best-effort and log it rather than
// failing the HTTP request, so this only needs to report a real database
// error, never "nothing to delete".
func (s *Store) DeleteDevice(deviceID string) error {
	if _, err := s.db.Exec(`DELETE FROM samples WHERE device_id = ?`, deviceID); err != nil {
		return fmt.Errorf("history: deleting device %s: %w", deviceID, err)
	}
	return nil
}
