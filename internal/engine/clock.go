package engine

import "time"

// Ticker abstracts a periodic tick source (time.Ticker in production),
// letting tests inject a fake that fires on demand instead of real wall
// time, per docs/07-pipeline-engine.md "Determinism and testing hooks".
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// TickerFactory creates a Ticker that fires every d. NewRealTicker is the
// production default; tests supply their own factory returning a
// manually-driven fake.
type TickerFactory func(d time.Duration) Ticker

// realClock implements Clock over the real wall clock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// realTicker wraps time.Ticker to satisfy Ticker.
type realTicker struct {
	t *time.Ticker
}

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// NewRealTicker is the default TickerFactory, backed by time.Ticker.
func NewRealTicker(d time.Duration) Ticker {
	return realTicker{t: time.NewTicker(d)}
}
