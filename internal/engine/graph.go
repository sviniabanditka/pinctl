package engine

import (
	"fmt"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// defaultTickMs is used when pipeline.TickMs is zero/unset, per
// docs/07-pipeline-engine.md "Default tick rate: 20 ms."
const defaultTickMs = 20

// nodeInstance is one compiled node: its live Node implementation plus the
// static wiring the runner needs to assemble its inputs each tick.
type nodeInstance struct {
	id      string
	impl    Node
	inputs  []Port // ordered per PortSpec.Inputs
	outputs []Port // ordered per PortSpec.Outputs
	// srcNode/srcPortIdx[i] describe where inputs[i] comes from, or -1/""
	// if unconnected (the node itself falls back to its config default).
	srcNode    []string
	srcPortIdx []int
}

// Graph is a pipeline compiled and ready to run: node instances plus the
// precomputed topological evaluation order (docs/07-pipeline-engine.md
// "Graph model", "Topological sort").
type Graph struct {
	PipelineID string
	TickMs     int
	Order      []string // topological order, node IDs
	Nodes      map[string]*nodeInstance
	Warnings   []Warning
}

// Compile validates p (see Validate) and, if it passes, instantiates every
// node (NewInstance + Init) against io, producing an executable Graph. On
// validation failure, err is a ValidationErrors and Graph is nil — no
// partial graph is ever returned, per docs/07-pipeline-engine.md's
// all-or-nothing validation policy.
//
// clk is threaded into every node's Init (docs/07-pipeline-engine.md
// "Determinism and testing hooks": "[timing-dependent code] reads time
// exclusively through a small Clock interface ... passed in at Node.Init");
// production callers pass a real-clock implementation, tests pass a fake
// they can advance under full control. Nodes that need wall-clock time at
// Eval time (timer, clock, debounce) store the Clock handed to Init and
// call it themselves, rather than trusting TickContext.Now (which reflects
// the scheduler's ticker-fired timestamp, a different, only loosely
// related time source — see docs/07-pipeline-engine.md "One tick, in
// full").
func Compile(p model.Pipeline, devices map[string]model.Device, io DeviceIO, clk Clock) (*Graph, []Warning, error) {
	c, errs := validate(p, devices)
	if len(errs) > 0 {
		return nil, nil, ValidationErrors(errs)
	}

	tickMs := p.TickMs
	if tickMs <= 0 {
		tickMs = defaultTickMs
	}

	nodes := make(map[string]*nodeInstance, len(c.order))
	for id, ps := range c.portSpecs {
		nt := c.nodeTypes[id]
		n := c.nodesByID[id]

		connectedInputs := make(map[string]bool, len(ps.Inputs))
		for _, in := range ps.Inputs {
			connectedInputs[in.Name] = c.connected[id][in.Name]
		}

		deps := NodeDeps{Devices: devices, IO: io, ConnectedInputs: connectedInputs, TickMs: tickMs, NodeID: id}
		impl, err := nt.NewInstance(n.Config, deps)
		if err != nil {
			return nil, nil, ValidationErrors{{NodeID: id, Msg: fmt.Sprintf("instantiate: %v", err)}}
		}

		srcNode := make([]string, len(ps.Inputs))
		srcPortIdx := make([]int, len(ps.Inputs))
		for i, in := range ps.Inputs {
			ref, ok := c.inputSrc[id][in.Name]
			if !ok {
				srcNode[i] = ""
				srcPortIdx[i] = -1
				continue
			}
			srcNode[i] = ref.FromNode
			srcPortIdx[i] = outputIndex(c.portSpecs[ref.FromNode], ref.FromPort)
		}

		nodes[id] = &nodeInstance{
			id:         id,
			impl:       impl,
			inputs:     ps.Inputs,
			outputs:    ps.Outputs,
			srcNode:    srcNode,
			srcPortIdx: srcPortIdx,
		}
	}

	g := &Graph{
		PipelineID: p.ID,
		TickMs:     tickMs,
		Order:      c.order,
		Nodes:      nodes,
		Warnings:   c.warnings,
	}

	if clk == nil {
		clk = realClock{}
	}
	for _, id := range g.Order {
		if err := nodes[id].impl.Init(clk); err != nil {
			return nil, nil, ValidationErrors{{NodeID: id, Msg: fmt.Sprintf("init: %v", err)}}
		}
	}

	return g, c.warnings, nil
}

func outputIndex(ps PortSpec, name string) int {
	for i, o := range ps.Outputs {
		if o.Name == name {
			return i
		}
	}
	return -1
}
