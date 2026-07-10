// Package model holds the persisted domain types shared by store, engine
// and API. JSON shapes follow docs/09-data-model.md exactly.
package model

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Identity is a board's stable hardware identity (docs/09-data-model.md
// "Board object"), used by internal/board to re-associate a persisted Board
// with a physical device across reconnects/reboots, independent of which
// serial path it happens to enumerate as.
type Identity struct {
	Kind string `json:"kind"` // "usb-serial" (VID/PID+serial) or "port-path" (fallback)
	Key  string `json:"key"`  // opaque, scheme-specific; unique per physical board
}

// Board is a connected (or previously-seen) microcontroller. Only identity
// and user-facing metadata are persisted — live connection status, pin
// capabilities, and cached pin values are runtime-only state owned by
// internal/board (docs/09-data-model.md "Board object").
type Board struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Identity  Identity  `json:"identity"`
	PortHint  string    `json:"portHint,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DeviceRole is the pin role a device binds (docs/09 "Device object").
type DeviceRole string

const (
	RoleDigitalIn  DeviceRole = "digital-in"
	RoleDigitalOut DeviceRole = "digital-out"
	RoleAnalogIn   DeviceRole = "analog-in"
	RolePWMOut     DeviceRole = "pwm-out"
	RoleServo      DeviceRole = "servo"
)

// DeviceOptions carries the per-role options; zero values mean "absent".
type DeviceOptions struct {
	Pullup       bool             `json:"pullup,omitempty"`       // digital-in
	Initial      *json.RawMessage `json:"initial,omitempty"`      // outputs: bool or number
	OnDisconnect string           `json:"onDisconnect,omitempty"` // outputs: "hold" | "safe-value"
	SafeValue    *json.RawMessage `json:"safeValue,omitempty"`    // outputs, with "safe-value"
	MinPulseUs   int              `json:"minPulseUs,omitempty"`   // servo: SERVO_CONFIG min pulse width, default 544
	MaxPulseUs   int              `json:"maxPulseUs,omitempty"`   // servo: SERVO_CONFIG max pulse width, default 2400
}

// Device is a user-named binding to a board pin.
type Device struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	BoardID   string        `json:"boardId"`
	Pin       int           `json:"pin"`
	Role      DeviceRole    `json:"role"`
	Options   DeviceOptions `json:"options"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// Node is one typed unit in a pipeline graph.
type Node struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Config   json.RawMessage `json:"config"`
	Position Position        `json:"position"`
}

// Position is editor-only metadata; the engine ignores it.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Edge connects "nodeID.port" → "nodeID.port" (docs/09 edge format).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Endpoint splits an edge endpoint "n1.out" into node ID and port name.
func Endpoint(s string) (node, port string, err error) {
	i := strings.LastIndex(s, ".")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("model: bad edge endpoint %q, want \"node.port\"", s)
	}
	return s[:i], s[i+1:], nil
}

// Pipeline is a named dataflow graph, one JSON document per pipeline.
type Pipeline struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	TickMs        int       `json:"tickMs"`
	Nodes         []Node    `json:"nodes"`
	Edges         []Edge    `json:"edges"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// NewID returns a prefixed Crockford-base32 random ID per docs/09,
// e.g. NewID("dev") → "dev_9zq3v7ab4c".
func NewID(prefix string) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	for i := range b {
		b[i] = alphabet[int(b[i])&31]
	}
	return prefix + "_" + string(b)
}
