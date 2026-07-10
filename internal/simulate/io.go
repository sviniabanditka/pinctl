package simulate

import (
	"sync"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// simIO is the simulated engine.DeviceIO: ReadDevice returns whatever
// req.Inputs/req.Script most recently set for that device (the type's zero
// if never seeded), typed per the device's role; WriteDevice records into
// the current tick's write buffer instead of reaching any hardware. Per
// docs/18-simulation.md's DeviceIO contract for simulation, it never blocks
// and never errors — "a simulated board is always reachable".
type simIO struct {
	mu      sync.Mutex
	devices map[string]model.Device
	values  map[string]engine.Value // current per-device value, drives ReadDevice
	script  map[int][]ScriptEntry   // tick -> updates to apply before that tick
	writes  map[string]engine.Value // this-tick writes, drained by Run after each tick
}

func newSimIO(devices map[string]model.Device, inputs map[string]float64, script []ScriptEntry) *simIO {
	s := &simIO{
		devices: devices,
		values:  make(map[string]engine.Value, len(inputs)),
		script:  make(map[int][]ScriptEntry),
		writes:  make(map[string]engine.Value),
	}
	for devID, v := range inputs {
		s.values[devID] = valueForRole(devices[devID].Role, v)
	}
	for _, se := range script {
		s.script[se.Tick] = append(s.script[se.Tick], se)
	}
	return s
}

// valueForRole converts a raw float64 seed/script value into the
// engine.Value a device of role would actually produce: bool (nonzero ==
// true) for a readable digital device, float64 for everything else —
// docs/18-simulation.md's Request.Inputs doc: "bools: 0/1".
func valueForRole(role model.DeviceRole, v float64) engine.Value {
	if pt, ok := engine.DeviceReadPortType(role); ok && pt == engine.TypeBool {
		return engine.BoolValue(v != 0)
	}
	return engine.FloatValue(v)
}

// zeroForRole is a readable device's type's zero value, per
// docs/18-simulation.md: "A device the graph reads but `inputs` omits
// starts at its type's zero".
func zeroForRole(role model.DeviceRole) engine.Value {
	if pt, ok := engine.DeviceReadPortType(role); ok && pt == engine.TypeBool {
		return engine.BoolValue(false)
	}
	return engine.FloatValue(0)
}

// applyScript updates s.values with every ScriptEntry scheduled for tick,
// so the change is visible to ReadDevice calls made while evaluating that
// tick (Run calls this before firing the tick's ticker event).
func (s *simIO) applyScript(tick int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, se := range s.script[tick] {
		s.values[se.DeviceID] = valueForRole(s.devices[se.DeviceID].Role, se.Value)
	}
}

// ReadDevice implements engine.DeviceIO.
func (s *simIO) ReadDevice(d model.Device) (engine.Value, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.values[d.ID]; ok {
		return v, nil
	}
	v := zeroForRole(d.Role)
	s.values[d.ID] = v
	return v, nil
}

// WriteDevice implements engine.DeviceIO: it never fails and only records
// the value for the current tick's Frame.Writes (drained by drainWrites),
// never reaching internal/board.
func (s *simIO) WriteDevice(d model.Device, v engine.Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes[d.ID] = v
	return nil
}

// drainWrites returns (and clears) every write recorded since the last
// call, converted to the plain bool/float64 shape Frame.Writes uses. Always
// non-nil so a tick with no writes still serializes as {} rather than null.
func (s *simIO) drainWrites() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any, len(s.writes))
	for k, v := range s.writes {
		out[k] = valueToAny(v)
	}
	s.writes = make(map[string]engine.Value)
	return out
}
