package engine

import (
	"errors"
	"fmt"
)

// ErrDeviceUnreachable is returned by DeviceIO.ReadDevice/WriteDevice when
// the underlying hardware could not be reached: the board is offline, its
// session has died, or (for a read) the pin has no cached value yet.
// internal/boardio maps the transport-level board errors (board.
// ErrBoardOffline, board.ErrSessionDead, board.ErrNoCachedValue) onto this
// sentinel, wrapping the original with %w so errors.Is still matches it too
// — the engine itself must not import internal/board, so this is the seam
// that mapping happens across.
//
// Per docs/07-pipeline-engine.md "Degraded ticks": hardware being
// unreachable is a pause, not a logic error. A tick whose only trouble is
// an ErrDeviceUnreachable from a read or a write is "degraded" — it does
// not count toward the scheduler's consecutive-failure/auto-disable policy
// (see runner.runTick), unlike a genuine node evaluation error.
var ErrDeviceUnreachable = errors.New("engine: device unreachable")

// UnreachableError is the concrete error internal/boardio returns (wrapped
// further by device-read/device-write's own "device-read %s: %w"-style
// context) for an unreachable device, carrying the device and board IDs
// needed to build a Status.Detail string without parsing an error message.
//
// Unwrap returns both ErrDeviceUnreachable and the original board.Err*
// cause, so errors.Is matches either one, and errors.As still finds the
// original board-layer error too (e.g. a caller checking specifically for
// board.ErrNoCachedValue).
type UnreachableError struct {
	DeviceID string
	BoardID  string
	Err      error // the original board.ErrBoardOffline / ErrSessionDead / ErrNoCachedValue
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("device %s (board %s) is unreachable: %v", e.DeviceID, e.BoardID, e.Err)
}

func (e *UnreachableError) Unwrap() []error { return []error{ErrDeviceUnreachable, e.Err} }
