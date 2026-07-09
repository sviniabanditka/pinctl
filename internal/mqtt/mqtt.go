// Package mqtt implements pinctl's MQTT bridge and Home Assistant
// discovery, per docs/15-mqtt-bridge.md. It is an observer of the runtime,
// not a participant: the pipeline engine neither knows nor cares that it
// exists (docs/15's "Principles").
//
// The package is split into policy (this package, unit-tested against a
// fake Client) and transport (paho.go, a thin adapter over
// github.com/eclipse/paho.mqtt.golang that owns connect/reconnect/LWT and
// contains no bridge policy of its own). Nothing outside this package
// depends on it; deleting internal/mqtt leaves a compiling binary with one
// dead config section, per docs/15's "Architecture".
package mqtt

import (
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// Client is the narrow surface the bridge needs from an MQTT connection.
// Connect/reconnect/backoff/LWT are the transport's job (paho.go); Client
// itself is just publish/subscribe/disconnect, which is what lets the
// bridge's policy be unit-tested against a fake instead of a real broker.
type Client interface {
	Publish(topic string, qos byte, retained bool, payload []byte) error
	Subscribe(topic string, qos byte, cb func(topic string, payload []byte)) error
	Disconnect()
}

// ClientFactory builds and connects a Client for cfg. onConnect is invoked
// with the (now-connected) Client every time the underlying connection
// completes a CONNACK — including reconnects — so the bridge can
// republish discovery, availability and state (docs/15's "Failure modes":
// "on reconnect it republishes discovery, availability and every device's
// current state"). Passing the Client into the callback, rather than
// relying on the caller to have stored the return value of ClientFactory
// first, avoids a startup race: paho may invoke OnConnect before
// ClientFactory itself returns. Deps.NewClient defaults to NewPahoClient
// when nil.
type ClientFactory func(cfg config.MQTT, onConnect func(Client)) (Client, error)

// Clock abstracts time.Now so the publish/rate policy is deterministically
// testable, same pattern as engine.Clock and board.Clock.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Store is what the bridge needs from internal/store, narrowed (like
// board.Manager's Store interface) so tests can supply an in-memory fake
// instead of the file-backed *store.Store. *store.Store satisfies it.
type Store interface {
	ListDevices() []model.Device
	GetDevice(id string) (model.Device, error)
	ListBoards() []model.Board
	GetBoard(id string) (model.Board, error)
	// EnabledPipelineWritingDevice is the pipeline-conflict predicate
	// shared with the REST API's manual write (docs/15's "Commands and
	// the pipeline-conflict rule": "the same predicate the REST API
	// enforces, and it lives in one place ... so the two surfaces can
	// never drift apart").
	EnabledPipelineWritingDevice(deviceID string) (id, name string, ok bool)
}

// Bus is what the bridge needs from internal/devicebus. *devicebus.Bus
// satisfies it.
type Bus interface {
	Subscribe() (<-chan devicebus.Update, func())
	Snapshot() map[string]devicebus.Update
}

// BoardStatus is what the bridge needs from board.Manager, narrowed so
// internal/board is never imported here (docs/15's "Architecture":
// "board.Manager's status (per-device availability)").
//
// Firmware is a deliberate small extension beyond the bare
// Connected-only shape sketched in the task brief: discovery's shared
// device block documents a "model" field sourced from the board's
// firmware name (docs/15's example: "model": "StandardFirmata 2.5"), and
// there is no other way to reach it without importing internal/board.
// Firmware returns ok=false for a board that has never connected, in
// which case the discovery payload simply omits "model".
type BoardStatus interface {
	Connected(boardID string) bool
	Firmware(boardID string) (name string, ok bool)
}

// Deps carries everything the bridge depends on beyond its own config,
// per docs/15's "Architecture" list: the store, the devicebus, an
// engine.DeviceIO for writes, and a board-status lookup for availability.
type Deps struct {
	Store  Store
	Bus    Bus
	IO     engine.DeviceIO
	Boards BoardStatus

	// Clock is optional; defaults to the real wall clock. Tests inject a
	// fake so the deadband/rate policy needs no real sleeps.
	Clock Clock

	// NewClient is optional; defaults to NewPahoClient. Tests inject a
	// factory that returns a fake Client instead of dialing a broker.
	NewClient ClientFactory
}
