package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// decodeStrict unmarshals raw into dst, rejecting unknown fields, per
// docs/07-pipeline-engine.md "Config unmarshaling" (mirrors the config-file
// strictness in docs/06-runtime.md).
func decodeStrict(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// deviceRoleForRead maps a device role to the port type device-read
// exposes, per docs/08-node-catalog.md.
func deviceRoleForRead(role model.DeviceRole) (PortType, bool) {
	switch role {
	case model.RoleDigitalIn:
		return TypeBool, true
	case model.RoleAnalogIn:
		return TypeFloat64, true
	default:
		return 0, false
	}
}

// deviceRoleForWrite maps a device role to the port type device-write
// accepts, per docs/08-node-catalog.md.
func deviceRoleForWrite(role model.DeviceRole) (PortType, bool) {
	switch role {
	case model.RoleDigitalOut:
		return TypeBool, true
	case model.RolePWMOut, model.RoleServo:
		return TypeFloat64, true
	default:
		return 0, false
	}
}

// ---- device-read ----

type deviceReadConfig struct {
	DeviceID string `json:"deviceId"`
}

type deviceReadType struct{}

func (deviceReadType) Name() string { return "device-read" }

func (deviceReadType) Ports(raw json.RawMessage, devices map[string]model.Device) (PortSpec, error) {
	var cfg deviceReadConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return PortSpec{}, fmt.Errorf("device-read: config: %w", err)
	}
	if cfg.DeviceID == "" {
		return PortSpec{}, fmt.Errorf("device-read: config.deviceId is required")
	}
	dev, ok := devices[cfg.DeviceID]
	if !ok {
		return PortSpec{}, fmt.Errorf("device-read: unknown device %q", cfg.DeviceID)
	}
	pt, ok := deviceRoleForRead(dev.Role)
	if !ok {
		return PortSpec{}, fmt.Errorf("device-read: device %q has role %q, want digital-in or analog-in", cfg.DeviceID, dev.Role)
	}
	return PortSpec{Outputs: []Port{{Name: "out", Type: pt}}}, nil
}

func (deviceReadType) NewInstance(raw json.RawMessage, deps NodeDeps) (Node, error) {
	var cfg deviceReadConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return nil, err
	}
	dev, ok := deps.Devices[cfg.DeviceID]
	if !ok {
		return nil, fmt.Errorf("device-read: unknown device %q", cfg.DeviceID)
	}
	pt, ok := deviceRoleForRead(dev.Role)
	if !ok {
		return nil, fmt.Errorf("device-read: device %q has unreadable role %q", cfg.DeviceID, dev.Role)
	}
	return &deviceReadNode{device: dev, outType: pt, io: deps.IO}, nil
}

type deviceReadNode struct {
	device  model.Device
	outType PortType
	io      DeviceIO
}

func (*deviceReadNode) Init(Clock) error { return nil }

func (n *deviceReadNode) Eval(_ TickContext, _ []Value) ([]Value, error) {
	v, err := n.io.ReadDevice(n.device)
	if err != nil {
		return nil, fmt.Errorf("device-read %s: %w", n.device.ID, err)
	}
	if v.Type != n.outType {
		return nil, fmt.Errorf("device-read %s: DeviceIO returned %s, want %s", n.device.ID, v.Type, n.outType)
	}
	return []Value{v}, nil
}

func (*deviceReadNode) Close() error { return nil }

// ---- constant ----

type constantConfig struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

type constantType struct{}

func (constantType) Name() string { return "constant" }

func (constantType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	_, pt, err := decodeConstant(raw)
	if err != nil {
		return PortSpec{}, err
	}
	return PortSpec{Outputs: []Port{{Name: "out", Type: pt}}}, nil
}

func (constantType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	v, _, err := decodeConstant(raw)
	if err != nil {
		return nil, err
	}
	return &constantNode{value: v}, nil
}

func decodeConstant(raw json.RawMessage) (Value, PortType, error) {
	var cfg constantConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return Value{}, 0, fmt.Errorf("constant: config: %w", err)
	}
	kind := cfg.Kind
	if kind == "" {
		kind = "float"
	}
	switch kind {
	case "bool":
		b := false
		if len(cfg.Value) > 0 {
			if err := json.Unmarshal(cfg.Value, &b); err != nil {
				return Value{}, 0, fmt.Errorf("constant: config.value must be a bool for kind=bool: %w", err)
			}
		}
		return BoolValue(b), TypeBool, nil
	case "float":
		f := 0.0
		if len(cfg.Value) > 0 {
			if err := json.Unmarshal(cfg.Value, &f); err != nil {
				return Value{}, 0, fmt.Errorf("constant: config.value must be a number for kind=float: %w", err)
			}
		}
		return FloatValue(f), TypeFloat64, nil
	default:
		return Value{}, 0, fmt.Errorf("constant: config.kind must be \"bool\" or \"float\", got %q", kind)
	}
}

type constantNode struct {
	value Value
}

func (*constantNode) Init(Clock) error { return nil }

func (n *constantNode) Eval(TickContext, []Value) ([]Value, error) {
	return []Value{n.value}, nil
}

func (*constantNode) Close() error { return nil }

// ---- compare ----

const compareEpsilon = 1e-9

type compareConfig struct {
	Op        string   `json:"op"`
	Threshold *float64 `json:"threshold"`
}

type compareType struct{}

func (compareType) Name() string { return "compare" }

func (compareType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	if err := validateCompareConfig(raw); err != nil {
		return PortSpec{}, err
	}
	return PortSpec{
		Inputs: []Port{
			{Name: "in", Type: TypeFloat64, Required: true},
			{Name: "threshold", Type: TypeFloat64, Required: false},
		},
		Outputs: []Port{{Name: "out", Type: TypeBool}},
	}, nil
}

func validateCompareConfig(raw json.RawMessage) error {
	var cfg compareConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return fmt.Errorf("compare: config: %w", err)
	}
	op := cfg.Op
	if op == "" {
		op = "gt"
	}
	switch op {
	case "gt", "lt", "eq", "gte", "lte", "neq":
	default:
		return fmt.Errorf("compare: config.op %q not one of gt/lt/gte/lte/eq/neq", cfg.Op)
	}
	return nil
}

func (compareType) NewInstance(raw json.RawMessage, deps NodeDeps) (Node, error) {
	var cfg compareConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return nil, fmt.Errorf("compare: config: %w", err)
	}
	op := cfg.Op
	if op == "" {
		op = "gt"
	}
	threshold := 0.0
	if cfg.Threshold != nil {
		threshold = *cfg.Threshold
	}
	return &compareNode{
		op:                 op,
		configThreshold:    threshold,
		thresholdConnected: deps.ConnectedInputs["threshold"],
	}, nil
}

type compareNode struct {
	op                 string
	configThreshold    float64
	thresholdConnected bool
}

func (*compareNode) Init(Clock) error { return nil }

func (n *compareNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	inVal := in[0].Float
	threshold := n.configThreshold
	if n.thresholdConnected {
		threshold = in[1].Float
	}
	var out bool
	switch n.op {
	case "gt":
		out = inVal > threshold
	case "lt":
		out = inVal < threshold
	case "gte":
		out = inVal >= threshold
	case "lte":
		out = inVal <= threshold
	case "eq":
		out = math.Abs(inVal-threshold) < compareEpsilon
	case "neq":
		out = math.Abs(inVal-threshold) >= compareEpsilon
	default:
		return nil, fmt.Errorf("compare: unknown op %q", n.op)
	}
	return []Value{BoolValue(out)}, nil
}

func (*compareNode) Close() error { return nil }

// ---- device-write ----

type deviceWriteConfig struct {
	DeviceID string `json:"deviceId"`
}

type deviceWriteType struct{}

func (deviceWriteType) Name() string { return "device-write" }

func (deviceWriteType) Ports(raw json.RawMessage, devices map[string]model.Device) (PortSpec, error) {
	var cfg deviceWriteConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return PortSpec{}, fmt.Errorf("device-write: config: %w", err)
	}
	if cfg.DeviceID == "" {
		return PortSpec{}, fmt.Errorf("device-write: config.deviceId is required")
	}
	dev, ok := devices[cfg.DeviceID]
	if !ok {
		return PortSpec{}, fmt.Errorf("device-write: unknown device %q", cfg.DeviceID)
	}
	pt, ok := deviceRoleForWrite(dev.Role)
	if !ok {
		return PortSpec{}, fmt.Errorf("device-write: device %q has role %q, want digital-out, pwm-out, or servo", cfg.DeviceID, dev.Role)
	}
	return PortSpec{Inputs: []Port{{Name: "in", Type: pt, Required: true}}}, nil
}

func (deviceWriteType) NewInstance(raw json.RawMessage, deps NodeDeps) (Node, error) {
	var cfg deviceWriteConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return nil, err
	}
	dev, ok := deps.Devices[cfg.DeviceID]
	if !ok {
		return nil, fmt.Errorf("device-write: unknown device %q", cfg.DeviceID)
	}
	pt, ok := deviceRoleForWrite(dev.Role)
	if !ok {
		return nil, fmt.Errorf("device-write: device %q has unwritable role %q", cfg.DeviceID, dev.Role)
	}
	return &deviceWriteNode{device: dev, inType: pt}, nil
}

type deviceWriteNode struct {
	device model.Device
	inType PortType

	lastVal Value // this tick's (validated/clamped) intent, read via SinkValue
}

func (*deviceWriteNode) Init(Clock) error { return nil }

func (n *deviceWriteNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	v := in[0]
	if v.Type != n.inType {
		return nil, fmt.Errorf("device-write %s: got %s input, want %s", n.device.ID, v.Type, n.inType)
	}
	switch n.device.Role {
	case model.RolePWMOut:
		v.Float = clamp(v.Float, 0, 255)
	case model.RoleServo:
		v.Float = clamp(v.Float, 0, 180)
	}
	n.lastVal = v
	return nil, nil
}

// SinkValue implements sinkNode: the runner reads it right after a
// successful Eval to build this tick's write intent (docs/07-pipeline-
// engine.md phase 3, "flush sink writes").
func (n *deviceWriteNode) SinkValue() (deviceID string, v Value) {
	return n.device.ID, n.lastVal
}

func (*deviceWriteNode) Close() error { return nil }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
