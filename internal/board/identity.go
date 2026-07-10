package board

import (
	"fmt"
	"strings"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// identityForPort resolves a port's stable hardware identity, per
// docs/05-serial-protocol.md's "Board identity": USB VID/PID + serial
// number when the OS reports USB metadata for the port, falling back to the
// port path itself (kind "port-path") when it doesn't — e.g. a non-USB
// serial device, or a platform/driver that doesn't expose VID/PID/serial.
// The port-path fallback is not stable across replug-into-a-different-port,
// but it's the best available signal short of requiring a udev symlink
// (documented in docs/14-deployment.md as a further upgrade).
func identityForPort(p PortInfo) model.Identity {
	if p.IsUSB && p.VID != "" && p.PID != "" {
		key := fmt.Sprintf("VID_%s:PID_%s", strings.ToUpper(p.VID), strings.ToUpper(p.PID))
		if p.SerialNumber != "" {
			key += ":SN_" + p.SerialNumber
		}
		return model.Identity{Kind: "usb-serial", Key: key}
	}
	return model.Identity{Kind: "port-path", Key: p.Path}
}

// identityForNetwork resolves a network board's identity, per
// docs/17-network-boards.md's "Identity": Firmata offers no unique
// identifier and a TCP socket has none either, so the configured address
// itself — verbatim, scheme included ("tcp://esp32-porch.local:3030") —
// is the identity key. Unlike identityForPort above, there's no fallback:
// an address is either explicitly configured (and therefore known) or not
// dialled at all.
func identityForNetwork(addr string) model.Identity {
	return model.Identity{Kind: "network", Key: addr}
}
