package engine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ---- timer ----

// defaultTimerPeriodMs is timer's config.periodMs default, per
// docs/08-node-catalog.md.
const defaultTimerPeriodMs = 1000

type timerConfig struct {
	PeriodMs int `json:"periodMs"`
}

type timerType struct{}

func (timerType) Name() string { return "timer" }

func (timerType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	var cfg timerConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return PortSpec{}, fmt.Errorf("timer: config: %w", err)
	}
	if cfg.PeriodMs < 0 {
		return PortSpec{}, fmt.Errorf("timer: config.periodMs must be >= 0, got %d", cfg.PeriodMs)
	}
	return PortSpec{Outputs: []Port{{Name: "out", Type: TypeBool}}}, nil
}

func (timerType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	var cfg timerConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return nil, fmt.Errorf("timer: config: %w", err)
	}
	periodMs := cfg.PeriodMs
	if periodMs == 0 {
		periodMs = defaultTimerPeriodMs
	}
	return &timerNode{period: time.Duration(periodMs) * time.Millisecond}, nil
}

// timerNode emits a one-tick bool pulse at a fixed period, per
// docs/08-node-catalog.md "timer". The countdown's phase is anchored at
// Init (pipeline enable time, "not at process boot", and reset on every
// hot-reload since Init runs fresh on every Compile), and measured via the
// Clock handed to Init — never TickContext.Now, never time.Now directly —
// per docs/07-pipeline-engine.md "Determinism and testing hooks".
type timerNode struct {
	period   time.Duration
	clk      Clock
	nextFire time.Time
}

func (n *timerNode) Init(clk Clock) error {
	n.clk = clk
	n.nextFire = clk.Now().Add(n.period)
	return nil
}

func (n *timerNode) Eval(_ TickContext, _ []Value) ([]Value, error) {
	now := n.clk.Now()
	fired := false
	// Loop (rather than a single "if") so a period boundary that was
	// missed entirely (e.g. a huge single clock jump in a test) still only
	// ever produces one pulse for this tick and the phase doesn't drift —
	// docs/08-node-catalog.md: "no fractional-tick pulses... up to one
	// tick of jitter", never a burst of queued-up pulses.
	for !now.Before(n.nextFire) {
		fired = true
		n.nextFire = n.nextFire.Add(n.period)
	}
	return []Value{BoolValue(fired)}, nil
}

func (*timerNode) Close() error { return nil }

// ---- clock ----

type clockConfig struct {
	Start *string  `json:"start"`
	End   *string  `json:"end"`
	Days  []string `json:"days"`
}

type clockType struct{}

func (clockType) Name() string { return "clock" }

var allWeekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

func (clockType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	if _, err := parseClockConfig(raw); err != nil {
		return PortSpec{}, err
	}
	return PortSpec{Outputs: []Port{{Name: "out", Type: TypeBool}}}, nil
}

func (clockType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	cc, err := parseClockConfig(raw)
	if err != nil {
		return nil, err
	}
	return &clockNode{cc: cc}, nil
}

// parsedClockConfig is clockConfig decoded into minute-of-day ints and a
// weekday set, computed once at compile time rather than on every tick.
type parsedClockConfig struct {
	startMin int
	endMin   int
	days     map[time.Weekday]bool
}

var weekdayByName = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

func parseClockConfig(raw json.RawMessage) (parsedClockConfig, error) {
	var cfg clockConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return parsedClockConfig{}, fmt.Errorf("clock: config: %w", err)
	}
	start := "00:00"
	if cfg.Start != nil {
		start = *cfg.Start
	}
	end := "23:59"
	if cfg.End != nil {
		end = *cfg.End
	}
	startMin, err := parseHHMM(start)
	if err != nil {
		return parsedClockConfig{}, fmt.Errorf("clock: config.start: %w", err)
	}
	endMin, err := parseHHMM(end)
	if err != nil {
		return parsedClockConfig{}, fmt.Errorf("clock: config.end: %w", err)
	}
	dayNames := cfg.Days
	if cfg.Days == nil {
		dayNames = allWeekdays
	}
	if len(dayNames) == 0 {
		return parsedClockConfig{}, fmt.Errorf("clock: config.days must not be empty (a window that never fires)")
	}
	days := make(map[time.Weekday]bool, len(dayNames))
	for _, d := range dayNames {
		wd, ok := weekdayByName[d]
		if !ok {
			return parsedClockConfig{}, fmt.Errorf("clock: config.days: unknown day %q", d)
		}
		days[wd] = true
	}
	return parsedClockConfig{startMin: startMin, endMin: endMin, days: days}, nil
}

func parseHHMM(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("bad HH:MM %q: %w", s, err)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// clockNode emits true while the current time-of-day falls within its
// configured window, per docs/08-node-catalog.md "clock". Stateless across
// ticks by design ("no internal state carried between ticks, so a runtime
// clock change ... is picked up immediately") — re-evaluated fresh every
// tick from the Clock handed to Init.
type clockNode struct {
	cc  parsedClockConfig
	clk Clock
}

func (n *clockNode) Init(clk Clock) error {
	n.clk = clk
	return nil
}

func (n *clockNode) Eval(_ TickContext, _ []Value) ([]Value, error) {
	now := n.clk.Now().Local()
	nowMin := now.Hour()*60 + now.Minute()
	weekday := now.Weekday()

	var inWindow bool
	checkDay := weekday
	if n.cc.startMin <= n.cc.endMin {
		// Same-day window, [start, end).
		inWindow = nowMin >= n.cc.startMin && nowMin < n.cc.endMin
	} else {
		// Overnight window (end < start): wraps past midnight. The
		// day-of-week check applies to the start day, per
		// docs/08-node-catalog.md, so when we're in the post-midnight
		// portion (nowMin < endMin) the window's "start day" was
		// yesterday.
		switch {
		case nowMin >= n.cc.startMin:
			inWindow = true
		case nowMin < n.cc.endMin:
			inWindow = true
			checkDay = weekday - 1
			if checkDay < time.Sunday {
				checkDay = time.Saturday
			}
		default:
			inWindow = false
		}
	}

	out := inWindow && n.cc.days[checkDay]
	return []Value{BoolValue(out)}, nil
}

func (*clockNode) Close() error { return nil }
