package engine

import (
	"strings"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/model"
)

func testDevices() map[string]model.Device {
	return devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
		model.Device{ID: "sw", Role: model.RoleDigitalIn},
		model.Device{ID: "led", Role: model.RoleDigitalOut},
		model.Device{ID: "motor", Role: model.RolePWMOut},
	)
}

func TestValidate_UnknownNodeType(t *testing.T) {
	t.Parallel()
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "n1", "not-a-real-type", map[string]any{}),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	if len(errs) != 1 || errs[0].NodeID != "n1" || !strings.Contains(errs[0].Msg, "unknown node type") {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}

func TestValidate_DanglingEdge(t *testing.T) {
	t.Parallel()
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "n1", "device-read", map[string]any{"deviceId": "ldr"}),
		node(t, "n2", "device-write", map[string]any{"deviceId": "motor"}),
	}, Edges: []model.Edge{
		edge("n1.nope", "n2.in"),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "dangling edge") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a dangling edge error, got: %+v", errs)
	}
}

func TestValidate_TypeMismatch(t *testing.T) {
	t.Parallel()
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "n1", "device-read", map[string]any{"deviceId": "ldr"}),  // float64 out
		node(t, "n2", "device-write", map[string]any{"deviceId": "led"}), // bool in
	}, Edges: []model.Edge{
		edge("n1.out", "n2.in"),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "type mismatch") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a type mismatch error, got: %+v", errs)
	}
}

func TestValidate_MissingDevice(t *testing.T) {
	t.Parallel()
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "n1", "device-read", map[string]any{"deviceId": "does-not-exist"}),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	if len(errs) != 1 || errs[0].NodeID != "n1" || !strings.Contains(errs[0].Msg, "unknown device") {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}

func TestValidate_MissingRequiredInput(t *testing.T) {
	t.Parallel()
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "n1", "compare", map[string]any{"op": "lt", "threshold": 300}),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	if len(errs) != 1 || errs[0].NodeID != "n1" || !strings.Contains(errs[0].Msg, "required input") {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}

func TestValidate_Cycle(t *testing.T) {
	t.Parallel()
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "n1", "test-passthrough", map[string]any{}),
		node(t, "n2", "test-passthrough", map[string]any{}),
	}, Edges: []model.Edge{
		edge("n1.out", "n2.in"),
		edge("n2.out", "n1.in"),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	cycleNodes := map[string]bool{}
	for _, e := range errs {
		if strings.Contains(e.Msg, "cycle") {
			cycleNodes[e.NodeID] = true
		}
	}
	if !cycleNodes["n1"] || !cycleNodes["n2"] {
		t.Fatalf("expected both n1 and n2 flagged as part of a cycle, got: %+v", errs)
	}
}

func TestValidate_MultipleWritersWarning(t *testing.T) {
	t.Parallel()
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "c1", "constant", map[string]any{"kind": "bool", "value": true}),
		node(t, "c2", "constant", map[string]any{"kind": "bool", "value": false}),
		node(t, "w1", "device-write", map[string]any{"deviceId": "led"}),
		node(t, "w2", "device-write", map[string]any{"deviceId": "led"}),
	}, Edges: []model.Edge{
		edge("c1.out", "w1.in"),
		edge("c2.out", "w2.in"),
	}}
	_, warnings, err := Validate(p, testDevices())
	mustNotErr(t, err)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %+v", warnings)
	}
	if !strings.Contains(warnings[0].Msg, "led") || !strings.Contains(warnings[0].Msg, "w1") || !strings.Contains(warnings[0].Msg, "w2") {
		t.Fatalf("warning doesn't mention both writers: %+v", warnings[0])
	}
}

func TestValidate_AllErrorsReported(t *testing.T) {
	t.Parallel()
	// Two independent problems in one pipeline: both must be reported,
	// not just the first (docs/07-pipeline-engine.md "Validation" is
	// all-or-nothing but reports every failure found).
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "n1", "bogus-type", map[string]any{}),
		node(t, "n2", "device-read", map[string]any{"deviceId": "missing-device"}),
	}}
	_, _, err := Validate(p, testDevices())
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %+v", len(errs), errs)
	}
}

func TestValidate_TopoDeterminism_NoEdges(t *testing.T) {
	t.Parallel()
	// Nodes declared out of alphabetical order, no edges between them:
	// with every node at indegree 0, the tie-break (ascending node id)
	// must fully determine the order.
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "c3", "constant", map[string]any{"kind": "float", "value": 3}),
		node(t, "c1", "constant", map[string]any{"kind": "float", "value": 1}),
		node(t, "c2", "constant", map[string]any{"kind": "float", "value": 2}),
	}}
	order, _, err := Validate(p, testDevices())
	mustNotErr(t, err)
	want := []string{"c1", "c2", "c3"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestValidate_TopoDeterminism_Diamond(t *testing.T) {
	t.Parallel()
	// Diamond: src -> {b, a} -> sink. a and b are both ready at the same
	// step; ascending id ("a" before "b") must decide the order, and both
	// must appear before "sink" and after "src" regardless of declaration
	// order.
	p := model.Pipeline{ID: "p", Nodes: []model.Node{
		node(t, "sink", "test-passthrough", map[string]any{}),
		node(t, "b", "test-passthrough", map[string]any{}),
		node(t, "src", "constant", map[string]any{"kind": "float", "value": 1}),
		node(t, "a", "test-passthrough", map[string]any{}),
	}, Edges: []model.Edge{
		edge("src.out", "a.in"),
		edge("src.out", "b.in"),
		edge("a.out", "sink.in"),
	}}
	order, _, err := Validate(p, testDevices())
	mustNotErr(t, err)
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if pos["src"] != 0 {
		t.Fatalf("src should evaluate first, order = %v", order)
	}
	if pos["a"] >= pos["sink"] {
		t.Fatalf("a must evaluate before sink, order = %v", order)
	}
	// a and b are both ready right after src; ascending id breaks the tie.
	if pos["a"] != 1 || pos["b"] != 2 {
		t.Fatalf("expected a before b (id tie-break), order = %v", order)
	}
}
