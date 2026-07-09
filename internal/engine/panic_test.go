package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// panicNodeType stands in for any node (or DeviceIO call) carrying a latent
// bug: its Eval panics rather than returning an error.
type panicNodeType struct{}

func (panicNodeType) Name() string { return "test-panic" }

func (panicNodeType) Ports(json.RawMessage, map[string]model.Device) (PortSpec, error) {
	return PortSpec{Outputs: []Port{{Name: "out", Type: TypeBool}}}, nil
}

func (panicNodeType) NewInstance(json.RawMessage, NodeDeps) (Node, error) {
	return &panicNode{}, nil
}

type panicNode struct{}

func (*panicNode) Init(Clock) error { return nil }

func (*panicNode) Eval(TickContext, []Value) ([]Value, error) {
	panic("boom from a node")
}

func (*panicNode) Close() error { return nil }

// TestRunTickRecoversNodePanic covers NFR-REL-2 (docs/02-requirements.md):
// a panic inside a node must not take the runtime down along with every
// other pipeline and board. It becomes an ordinary tick error attributed to
// the offending node, and the usual consecutive-failure policy disables just
// that pipeline while the process lives on. Without the recover in runTick
// this test crashes the whole test binary.
func TestRunTickRecoversNodePanic(t *testing.T) {
	register(panicNodeType{})
	t.Cleanup(func() { delete(registry, "test-panic") })

	p := model.Pipeline{
		ID: "pl_panic", Name: "panicky", Enabled: true, TickMs: 10,
		Nodes: []model.Node{{ID: "n1", Type: "test-panic", Config: json.RawMessage(`{}`)}},
	}

	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	if err := mgr.Start(p, map[string]model.Device{}, newFakeIO()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tk := <-tickers

	// Every tick panics; the runner must survive each one and disable itself
	// on the third.
	for i := 0; i < maxConsecutiveFailures; i++ {
		tk.Fire(time.Unix(0, int64(i)))
	}

	st := waitForState(t, mgr, p.ID, StateDisabled)
	if st.LastErrorNode != "n1" {
		t.Errorf("LastErrorNode = %q, want %q", st.LastErrorNode, "n1")
	}
	if st.LastError == nil || !strings.Contains(st.LastError.Error(), "panicked") {
		t.Errorf("LastError = %v, want an error naming the panic", st.LastError)
	}
}
