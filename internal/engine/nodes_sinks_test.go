package engine

import (
	"testing"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// TestLog_OnChangeOnly covers docs/08-node-catalog.md "log": logs the
// first value observed even with onChangeOnly, then only logs again on a
// value change.
func TestLog_OnChangeOnly(t *testing.T) {
	n := mustNode(t, logType{}, map[string]any{"label": "ldr-raw", "onChangeOnly": true},
		NodeDeps{NodeID: "n15"})
	mustNotErr(t, n.Init(newFakeClock()))
	ls := n.(logSink)

	// tick 1: first value ever, logged even though there's no "previous".
	_, err := n.Eval(TickContext{}, []Value{FloatValue(288)})
	mustNotErr(t, err)
	entry, ok := ls.LogEntry()
	if !ok {
		t.Fatal("tick 1: expected a log entry (first observed value)")
	}
	if entry.NodeID != "n15" || entry.Label != "ldr-raw" || entry.Value.Float != 288 {
		t.Fatalf("tick 1: entry = %+v, want nodeID=n15 label=ldr-raw value=288", entry)
	}

	// tick 2: same value -> onChangeOnly suppresses it.
	_, err = n.Eval(TickContext{}, []Value{FloatValue(288)})
	mustNotErr(t, err)
	if _, ok := ls.LogEntry(); ok {
		t.Fatal("tick 2: unchanged value should not log")
	}

	// tick 3: value changes -> logs again.
	_, err = n.Eval(TickContext{}, []Value{FloatValue(500)})
	mustNotErr(t, err)
	entry, ok = ls.LogEntry()
	if !ok || entry.Value.Float != 500 {
		t.Fatalf("tick 3: entry = %+v, ok=%v, want value=500", entry, ok)
	}
}

func TestLog_OnChangeOnlyFalseLogsEveryTick(t *testing.T) {
	n := mustNode(t, logType{}, map[string]any{"onChangeOnly": false}, NodeDeps{NodeID: "n1"})
	mustNotErr(t, n.Init(newFakeClock()))
	ls := n.(logSink)

	for i := 0; i < 3; i++ {
		_, err := n.Eval(TickContext{}, []Value{BoolValue(true)})
		mustNotErr(t, err)
		if _, ok := ls.LogEntry(); !ok {
			t.Fatalf("tick %d: expected a log entry every tick when onChangeOnly=false", i+1)
		}
	}
}

func TestLog_LabelDefaultsToNodeID(t *testing.T) {
	n := mustNode(t, logType{}, map[string]any{}, NodeDeps{NodeID: "n42"})
	mustNotErr(t, n.Init(newFakeClock()))
	ls := n.(logSink)
	_, err := n.Eval(TickContext{}, []Value{BoolValue(true)})
	mustNotErr(t, err)
	entry, ok := ls.LogEntry()
	if !ok || entry.Label != "n42" {
		t.Fatalf("entry = %+v, ok=%v, want label defaulting to node id n42", entry, ok)
	}
}

func TestLog_AcceptsEitherType(t *testing.T) {
	// The "in" port is declared AnyType — verify Ports()/validate() don't
	// reject either a float64 or a bool upstream feeding it
	// (docs/08-node-catalog.md: "accepts either type, logs whichever
	// arrives").
	pFloat := model.Pipeline{ID: "p1", Nodes: []model.Node{
		node(t, "c", "constant", map[string]any{"kind": "float", "value": 1}),
		node(t, "l", "log", map[string]any{}),
	}, Edges: []model.Edge{edge("c.out", "l.in")}}
	if _, _, err := Validate(pFloat, testDevices()); err != nil {
		t.Fatalf("float64 -> log: unexpected validation error: %v", err)
	}

	pBool := model.Pipeline{ID: "p2", Nodes: []model.Node{
		node(t, "c", "constant", map[string]any{"kind": "bool", "value": true}),
		node(t, "l", "log", map[string]any{}),
	}, Edges: []model.Edge{edge("c.out", "l.in")}}
	if _, _, err := Validate(pBool, testDevices()); err != nil {
		t.Fatalf("bool -> log: unexpected validation error: %v", err)
	}
}
