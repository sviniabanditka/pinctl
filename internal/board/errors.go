package board

import "errors"

// ErrSessionDead is wrapped by any Session method called after the read
// loop has detected the board is gone (I/O error on read or write). Per
// docs/12-roadmap.md's v0.1 done-criteria, unplugging the board must not
// crash the process — callers get this error back instead.
var ErrSessionDead = errors.New("board: session is dead")

// ErrUnsupportedProtocolVersion is returned by Open/newSession when the
// board reports a Firmata major protocol version below 2, per
// docs/05-serial-protocol.md's Error Handling table ("Unsupported protocol
// version" — failed permanently, not retried).
var ErrUnsupportedProtocolVersion = errors.New("board: unsupported firmata protocol version")

// ErrUnknownPin is returned when a pin number outside the board's reported
// capabilities is used.
var ErrUnknownPin = errors.New("board: unknown pin")

// ErrUnsupportedMode is returned by SetPinMode when the target pin's
// capabilities (from the Capability Query response) don't include the
// requested mode.
var ErrUnsupportedMode = errors.New("board: pin does not support requested mode")

// ErrNoCachedValue is returned by DigitalRead/AnalogRead when no report has
// been received yet for the requested pin/channel — typically because
// reporting was never enabled for it (see SetPinMode).
var ErrNoCachedValue = errors.New("board: no cached value for pin")
