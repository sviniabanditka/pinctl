// Package history is pinctl's value history (docs/16-history.md): a
// devicebus subscriber that batches every device value change into a
// SQLite database, and the query surface over it. History observes; it
// never participates — a failing or full database must never stall a
// pipeline tick, a board write, or the HTTP server (docs/16's
// "Principles"). Devices and pipelines stay JSON files under the data
// dir; history is the only thing that lives in a database, and deleting
// history.db loses only the past, never the running configuration.
//
// Storage is modernc.org/sqlite, a pure-Go driver, so CGO_ENABLED=0
// cross-compilation to linux/arm64 and linux/arm keeps working — the
// constraint that disqualifies mattn/go-sqlite3 (docs/16's "Storage").
package history

import (
	"time"

	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// Bus is what Record needs from internal/devicebus. *devicebus.Bus
// satisfies it.
type Bus interface {
	Subscribe() (<-chan devicebus.Update, func())
}

// DeviceLister is what Record needs from internal/store to confirm a
// device that produced a bus Update still exists — a defensive check
// against a stray in-flight Update racing a concurrent device deletion,
// so a deleted device's samples don't reappear after DeleteDevice has
// already cleared them. *store.Store satisfies it.
type DeviceLister interface {
	GetDevice(id string) (model.Device, error)
}

// Clock abstracts time.Now so the recorder's batching/backoff is
// deterministically testable, same pattern as internal/mqtt.Clock.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Query is GET /api/v1/devices/{id}/history's parameters (docs/16's
// "Query API"). From/To are the closed-open window [From, To); a zero
// Bucket means raw samples, otherwise samples are aggregated into fixed
// Bucket-wide windows. Limit <= 0 or > the server-side cap is clamped by
// Store.Query rather than erroring — the API layer is the one that
// rejects an over-cap Limit with 400, per docs/16.
type Query struct {
	From   time.Time
	To     time.Time
	Bucket time.Duration
	Limit  int
}

// Sample is one point in a Query result: a raw sample (Min/Max nil) or
// one bucketed window's avg/min/max (docs/16's response shape).
type Sample struct {
	T   time.Time
	V   float64
	Min *float64
	Max *float64
}
