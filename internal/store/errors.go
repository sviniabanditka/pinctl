package store

import "errors"

// ErrNotFound is returned by Get/Update/Delete/SetEnabled when no object
// with the given ID exists.
var ErrNotFound = errors.New("store: not found")

// ErrPinInUse is returned by device create/update when another device
// already binds the same (boardId, pin) pair (docs/09-data-model.md
// "Validation rules", FR-DEVICE-4).
var ErrPinInUse = errors.New("store: pin already in use")

// ErrNameEmpty is returned by device/pipeline create/update when name is
// the empty string.
var ErrNameEmpty = errors.New("store: name must not be empty")

// ErrDeviceInUse is returned by DeleteDevice when at least one pipeline's
// node still references the device id, per docs/09-data-model.md
// "Validation rules": "A device cannot be deleted while any pipeline
// references its id" -> 409 Conflict.
var ErrDeviceInUse = errors.New("store: device is referenced by a pipeline")
