package engine

import (
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

func mustNode(t *testing.T, nt NodeType, cfg map[string]any, deps NodeDeps) Node {
	t.Helper()
	n, err := nt.NewInstance(rawJSON(t, cfg), deps)
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	return n
}

// TestTimer_FiresOncePerPeriod verifies docs/08-node-catalog.md "timer":
// a one-tick pulse at a fixed period, phase anchored at Init, measured via
// the injectable Clock — not TickContext.Now.
func TestTimer_FiresOncePerPeriod(t *testing.T) {
	clk := newFakeClock()
	n := mustNode(t, timerType{}, map[string]any{"periodMs": 100}, NodeDeps{})
	mustNotErr(t, n.Init(clk))

	// tick 1, t=0: no time elapsed since Init, no pulse yet.
	out, err := n.Eval(TickContext{}, nil)
	mustNotErr(t, err)
	if out[0].Bool {
		t.Fatalf("tick at t=0: got pulse, want none")
	}

	// t=50ms: still before the first boundary.
	clk.Advance(50 * time.Millisecond)
	out, err = n.Eval(TickContext{}, nil)
	mustNotErr(t, err)
	if out[0].Bool {
		t.Fatalf("tick at t=50ms: got pulse, want none")
	}

	// t=100ms: boundary reached, exactly one pulse.
	clk.Advance(50 * time.Millisecond)
	out, err = n.Eval(TickContext{}, nil)
	mustNotErr(t, err)
	if !out[0].Bool {
		t.Fatalf("tick at t=100ms: want pulse")
	}

	// t=120ms: no new boundary yet.
	clk.Advance(20 * time.Millisecond)
	out, err = n.Eval(TickContext{}, nil)
	mustNotErr(t, err)
	if out[0].Bool {
		t.Fatalf("tick at t=120ms: got pulse, want none")
	}

	// t=200ms: second boundary.
	clk.Advance(80 * time.Millisecond)
	out, err = n.Eval(TickContext{}, nil)
	mustNotErr(t, err)
	if !out[0].Bool {
		t.Fatalf("tick at t=200ms: want pulse")
	}

	// A big jump that skips several boundaries in one step must still
	// only ever produce a single pulse for that one Eval call — no
	// catch-up burst — per docs/08-node-catalog.md "no fractional-tick
	// pulses ... up to one tick of jitter".
	clk.Advance(1000 * time.Millisecond) // 10 periods' worth
	out, err = n.Eval(TickContext{}, nil)
	mustNotErr(t, err)
	if !out[0].Bool {
		t.Fatalf("tick after big jump: want a pulse")
	}
	out, err = n.Eval(TickContext{}, nil)
	mustNotErr(t, err)
	if out[0].Bool {
		t.Fatalf("tick right after the big-jump pulse: want no second pulse")
	}
}

func TestTimer_ValidatePeriodBelowTickMs(t *testing.T) {
	p := model.Pipeline{ID: "p", TickMs: 20, Nodes: []model.Node{
		node(t, "n1", "timer", map[string]any{"periodMs": 10}),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected validation error for periodMs < tickMs")
	}
}

func TestTimer_ValidatePeriodAtOrAboveTickMs(t *testing.T) {
	p := model.Pipeline{ID: "p", TickMs: 20, Nodes: []model.Node{
		node(t, "n1", "timer", map[string]any{"periodMs": 20}),
	}}
	_, _, err := Validate(p, testDevices())
	mustNotErr(t, err)
}

// TestClock_Window covers docs/08-node-catalog.md "clock": same-day and
// overnight windows, inclusive start / exclusive end, and the
// day-of-week check applying to the window's start day for the
// post-midnight portion of an overnight window.
func TestClock_Window(t *testing.T) {
	tests := []struct {
		name  string
		start string
		end   string
		days  []string
		now   time.Time
		want  bool
	}{
		{
			name:  "same-day, inside window",
			start: "08:00", end: "18:00", days: []string{"mon"},
			now:  time.Date(2026, 7, 6, 12, 0, 0, 0, time.Local), // Monday
			want: true,
		},
		{
			name:  "same-day, at start boundary (inclusive)",
			start: "08:00", end: "18:00", days: []string{"mon"},
			now:  time.Date(2026, 7, 6, 8, 0, 0, 0, time.Local),
			want: true,
		},
		{
			name:  "same-day, at end boundary (exclusive)",
			start: "08:00", end: "18:00", days: []string{"mon"},
			now:  time.Date(2026, 7, 6, 18, 0, 0, 0, time.Local),
			want: false,
		},
		{
			name:  "same-day, wrong weekday",
			start: "08:00", end: "18:00", days: []string{"tue"},
			now:  time.Date(2026, 7, 6, 12, 0, 0, 0, time.Local), // Monday
			want: false,
		},
		{
			name:  "overnight, before-midnight portion on start day",
			start: "22:00", end: "06:00", days: []string{"mon"},
			now:  time.Date(2026, 7, 6, 23, 0, 0, 0, time.Local), // Monday 23:00
			want: true,
		},
		{
			name:  "overnight, after-midnight portion, day check applies to start day",
			start: "22:00", end: "06:00", days: []string{"mon"},
			now:  time.Date(2026, 7, 7, 3, 0, 0, 0, time.Local), // Tuesday 03:00, window started Monday
			want: true,
		},
		{
			name:  "overnight, after-midnight portion, start day not in days",
			start: "22:00", end: "06:00", days: []string{"tue"},
			now:  time.Date(2026, 7, 7, 3, 0, 0, 0, time.Local), // Tuesday 03:00 -> checks Monday, not in days
			want: false,
		},
		{
			name:  "overnight, outside window entirely",
			start: "22:00", end: "06:00", days: []string{"mon", "tue"},
			now:  time.Date(2026, 7, 7, 12, 0, 0, 0, time.Local), // Tuesday noon
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := mustNode(t, clockType{}, map[string]any{
				"start": tt.start, "end": tt.end, "days": tt.days,
			}, NodeDeps{})
			clk := &fixedClock{t: tt.now}
			mustNotErr(t, n.Init(clk))
			out, err := n.Eval(TickContext{}, nil)
			mustNotErr(t, err)
			if out[0].Bool != tt.want {
				t.Fatalf("clock(%s..%s, %v) at %v = %v, want %v", tt.start, tt.end, tt.days, tt.now, out[0].Bool, tt.want)
			}
		})
	}
}

func TestClock_EmptyDaysIsValidationError(t *testing.T) {
	_, err := clockType{}.Ports(rawJSON(t, map[string]any{"days": []string{}}), nil)
	if err == nil {
		t.Fatal("expected validation error for empty config.days")
	}
}

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }
