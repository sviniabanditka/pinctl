package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ValidationError is one static validation failure, tied to a node where
// applicable (empty NodeID for pipeline-wide structural errors that don't
// point at a single node). docs/07-pipeline-engine.md "Validation".
type ValidationError struct {
	NodeID string
	Msg    string
}

func (e ValidationError) Error() string {
	if e.NodeID == "" {
		return e.Msg
	}
	return fmt.Sprintf("node %s: %s", e.NodeID, e.Msg)
}

// ValidationErrors aggregates every validation failure found in one pass —
// per docs/07-pipeline-engine.md, validation reports all failures, not just
// the first, and rejects the whole save all-or-nothing.
type ValidationErrors []ValidationError

func (v ValidationErrors) Error() string {
	msgs := make([]string, len(v))
	for i, e := range v {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}

// Warning is a non-fatal validation finding — currently only "multiple
// device-write nodes target the same device", per docs/07-pipeline-
// engine.md's "Policy: multiple writers to the same device".
type Warning struct {
	NodeID string // one of the offending nodes; see Msg for the full set
	Msg    string
}

// edgeRef points an input port at the output port that feeds it.
type edgeRef struct {
	FromNode string
	FromPort string
}

// compiled is the result of a full validation pass: everything Compile
// needs to instantiate Node implementations and build the executable
// graph, without yet touching a DeviceIO.
type compiled struct {
	order     []string // topological order, node IDs
	portSpecs map[string]PortSpec
	nodeTypes map[string]NodeType
	nodesByID map[string]model.Node
	// connected[nodeID][portName] reports whether that input port has an
	// incoming edge.
	connected map[string]map[string]bool
	// inputSrc[nodeID][portName] is the upstream output port feeding that
	// input, when connected.
	inputSrc map[string]map[string]edgeRef
	warnings []Warning
}

// Validate runs the static validation pass described in docs/07-pipeline-
// engine.md ("Validation"): unknown node type, dangling edge, type
// mismatch, missing/unbound device, missing required input, cycle. It
// reports every failure found (not just the first) plus non-fatal
// warnings, and is safe to call without a DeviceIO — it never instantiates
// a Node, only resolves port shapes and pipeline structure. This is the
// same pass internal/store runs at SavePipeline time, and the one
// Compile runs (via the unexported validate()) before instantiating nodes.
func Validate(p model.Pipeline, devices map[string]model.Device) (order []string, warnings []Warning, err error) {
	c, errs := validate(p, devices)
	if len(errs) > 0 {
		return nil, nil, ValidationErrors(errs)
	}
	return c.order, c.warnings, nil
}

func validate(p model.Pipeline, devices map[string]model.Device) (*compiled, []ValidationError) {
	var errs []ValidationError

	nodesByID := make(map[string]model.Node, len(p.Nodes))
	nodeTypes := make(map[string]NodeType, len(p.Nodes))
	portSpecs := make(map[string]PortSpec, len(p.Nodes))
	knownIDs := make(map[string]bool, len(p.Nodes)) // every node id seen, valid or not (for dangling-edge checks)

	for _, n := range p.Nodes {
		if knownIDs[n.ID] {
			errs = append(errs, ValidationError{NodeID: n.ID, Msg: "duplicate node id"})
			continue
		}
		knownIDs[n.ID] = true

		nt, ok := lookupNodeType(n.Type)
		if !ok {
			errs = append(errs, ValidationError{NodeID: n.ID, Msg: fmt.Sprintf("unknown node type %q", n.Type)})
			continue
		}
		ps, err := nt.Ports(n.Config, devices)
		if err != nil {
			errs = append(errs, ValidationError{NodeID: n.ID, Msg: err.Error()})
			continue
		}
		nodesByID[n.ID] = n
		nodeTypes[n.ID] = nt
		portSpecs[n.ID] = ps
	}

	// Index ports by name for existence/type lookups.
	outputType := make(map[string]map[string]PortType, len(portSpecs))
	inputPort := make(map[string]map[string]Port, len(portSpecs))
	for id, ps := range portSpecs {
		outs := make(map[string]PortType, len(ps.Outputs))
		for _, o := range ps.Outputs {
			outs[o.Name] = o.Type
		}
		outputType[id] = outs
		ins := make(map[string]Port, len(ps.Inputs))
		for _, in := range ps.Inputs {
			ins[in.Name] = in
		}
		inputPort[id] = ins
	}

	connected := make(map[string]map[string]bool, len(portSpecs))
	inputSrc := make(map[string]map[string]edgeRef, len(portSpecs))
	for id := range portSpecs {
		connected[id] = map[string]bool{}
		inputSrc[id] = map[string]edgeRef{}
	}

	// adjacency for topo sort, built only from edges that validated clean
	// between two known-good nodes.
	indegree := make(map[string]int, len(portSpecs))
	outEdges := make(map[string][]string, len(portSpecs)) // fromNode -> []toNode (may repeat)
	for id := range portSpecs {
		indegree[id] = 0
	}

	for _, e := range p.Edges {
		fromNode, fromPort, err := model.Endpoint(e.From)
		if err != nil {
			errs = append(errs, ValidationError{Msg: fmt.Sprintf("edge %q -> %q: bad from endpoint: %v", e.From, e.To, err)})
			continue
		}
		toNode, toPort, err := model.Endpoint(e.To)
		if err != nil {
			errs = append(errs, ValidationError{Msg: fmt.Sprintf("edge %q -> %q: bad to endpoint: %v", e.From, e.To, err)})
			continue
		}
		if !knownIDs[fromNode] {
			errs = append(errs, ValidationError{NodeID: fromNode, Msg: fmt.Sprintf("edge references unknown node %q", fromNode)})
			continue
		}
		if !knownIDs[toNode] {
			errs = append(errs, ValidationError{NodeID: toNode, Msg: fmt.Sprintf("edge references unknown node %q", toNode)})
			continue
		}
		fromOuts, fromOK := outputType[fromNode]
		toIns, toOK := inputPort[toNode]
		if !fromOK || !toOK {
			// One or both nodes failed their own Ports() resolution; that
			// error is already recorded, don't pile on a dangling-edge
			// error too.
			continue
		}
		fromType, ok := fromOuts[fromPort]
		if !ok {
			errs = append(errs, ValidationError{NodeID: fromNode, Msg: fmt.Sprintf("dangling edge: no output port %q", fromPort)})
			continue
		}
		toPortSpec, ok := toIns[toPort]
		if !ok {
			errs = append(errs, ValidationError{NodeID: toNode, Msg: fmt.Sprintf("dangling edge: no input port %q", toPort)})
			continue
		}
		if !toPortSpec.AnyType && fromType != toPortSpec.Type {
			errs = append(errs, ValidationError{
				NodeID: toNode,
				Msg:    fmt.Sprintf("type mismatch: %s.%s (%s) -> %s.%s (%s)", fromNode, fromPort, fromType, toNode, toPort, toPortSpec.Type),
			})
			continue
		}
		if connected[toNode][toPort] {
			errs = append(errs, ValidationError{NodeID: toNode, Msg: fmt.Sprintf("input port %q already has an incoming edge", toPort)})
			continue
		}
		connected[toNode][toPort] = true
		inputSrc[toNode][toPort] = edgeRef{FromNode: fromNode, FromPort: fromPort}
		indegree[toNode]++
		outEdges[fromNode] = append(outEdges[fromNode], toNode)
	}

	// Missing required input.
	for id, ps := range portSpecs {
		for _, in := range ps.Inputs {
			if in.Required && !connected[id][in.Name] {
				errs = append(errs, ValidationError{NodeID: id, Msg: fmt.Sprintf("required input %q has no incoming edge", in.Name)})
			}
		}
	}

	// timer.periodMs must be >= the pipeline's effective tickMs, per
	// docs/08-node-catalog.md ("Must be >= tickMs of the owning pipeline;
	// validation error otherwise"). Checked here, not in timerType.Ports,
	// since Ports has no access to the pipeline's tickMs.
	effectiveTickMs := p.TickMs
	if effectiveTickMs <= 0 {
		effectiveTickMs = defaultTickMs
	}
	for _, n := range nodesByID {
		if n.Type != "timer" {
			continue
		}
		var cfg timerConfig
		if err := decodeStrict(n.Config, &cfg); err != nil {
			continue // already reported via Ports()
		}
		periodMs := cfg.PeriodMs
		if periodMs == 0 {
			periodMs = defaultTimerPeriodMs
		}
		if periodMs < effectiveTickMs {
			errs = append(errs, ValidationError{NodeID: n.ID, Msg: fmt.Sprintf("config.periodMs (%d) must be >= pipeline tickMs (%d)", periodMs, effectiveTickMs)})
		}
	}

	// Kahn's algorithm, tie-break by ascending node id (lexicographic).
	order := make([]string, 0, len(portSpecs))
	deg := make(map[string]int, len(indegree))
	for id, d := range indegree {
		deg[id] = d
	}
	ready := make([]string, 0, len(portSpecs))
	for id, d := range deg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	for len(ready) > 0 {
		sort.Strings(ready)
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		nexts := outEdges[id]
		sort.Strings(nexts)
		for _, to := range nexts {
			deg[to]--
			if deg[to] == 0 {
				ready = append(ready, to)
			}
		}
	}
	if len(order) != len(portSpecs) {
		leftover := make([]string, 0, len(portSpecs)-len(order))
		inOrder := make(map[string]bool, len(order))
		for _, id := range order {
			inOrder[id] = true
		}
		for id := range portSpecs {
			if !inOrder[id] {
				leftover = append(leftover, id)
			}
		}
		sort.Strings(leftover)
		for _, id := range leftover {
			errs = append(errs, ValidationError{NodeID: id, Msg: "part of a cycle"})
		}
	}

	// Multiple writers to the same device: warning, not an error, per
	// docs/07-pipeline-engine.md "Policy: multiple writers to the same
	// device".
	writersByDevice := map[string][]string{}
	for id, n := range nodesByID {
		if n.Type != "device-write" {
			continue
		}
		var cfg deviceWriteConfig
		if err := decodeStrict(n.Config, &cfg); err != nil {
			continue // already reported via Ports()
		}
		writersByDevice[cfg.DeviceID] = append(writersByDevice[cfg.DeviceID], id)
	}
	var warnings []Warning
	deviceIDs := make([]string, 0, len(writersByDevice))
	for devID := range writersByDevice {
		deviceIDs = append(deviceIDs, devID)
	}
	sort.Strings(deviceIDs)
	for _, devID := range deviceIDs {
		writers := writersByDevice[devID]
		if len(writers) < 2 {
			continue
		}
		sort.Strings(writers)
		warnings = append(warnings, Warning{
			NodeID: writers[0],
			Msg:    fmt.Sprintf("multiple device-write nodes target device %q: %s (last-write-wins by evaluation order)", devID, strings.Join(writers, ", ")),
		})
	}

	c := &compiled{
		order:     order,
		portSpecs: portSpecs,
		nodeTypes: nodeTypes,
		nodesByID: nodesByID,
		connected: connected,
		inputSrc:  inputSrc,
		warnings:  warnings,
	}
	return c, errs
}
