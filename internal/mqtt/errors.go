package mqtt

import "errors"

// errUnsupportedRole is returned internally when a device's role has no
// Home Assistant component mapping. Every model.DeviceRole in practice
// does (docs/15's discovery table covers all four), so this only fires if
// the role set grows without this package being updated.
var errUnsupportedRole = errors.New("mqtt: device role has no discovery component")
