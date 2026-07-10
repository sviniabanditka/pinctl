package board

import (
	"path/filepath"

	"go.bug.st/serial/enumerator"
)

// PortInfo is one serial port as reported by port enumeration, trimmed to
// the fields identity resolution and allowlist filtering need (see
// docs/05-serial-protocol.md "Board identity").
type PortInfo struct {
	Path         string // e.g. "/dev/ttyACM0"
	IsUSB        bool
	VID          string
	PID          string
	SerialNumber string
}

// PortLister enumerates the serial ports currently present on the system.
// Injectable so tests never touch real hardware; production uses
// DefaultPortLister.
type PortLister func() ([]PortInfo, error)

// DefaultPortLister enumerates real serial ports via
// go.bug.st/serial/enumerator (sysfs on Linux, IOKit on macOS).
func DefaultPortLister() ([]PortInfo, error) {
	details, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]PortInfo, 0, len(details))
	for _, d := range details {
		out = append(out, PortInfo{
			Path:         d.Name,
			IsUSB:        d.IsUSB,
			VID:          d.VID,
			PID:          d.PID,
			SerialNumber: d.SerialNumber,
		})
	}
	return out, nil
}

// MatchesAllowlist reports whether portPath is permitted to be opened, per
// docs/14-deployment.md's serial.allowlist (FR-BOARD-4). Patterns are
// filepath.Match-style globs — one with no metacharacters behaves as a plain
// exact match, so literal paths ("/dev/ttyACM0") work unchanged, while an
// operator who wants "any ttyACM*" writes that glob. `"*"` allows any port.
//
// An **empty allowlist matches nothing**. It once meant "allow any", which
// read as "no ports permitted" and behaved as its opposite — the same trap as
// a firewall whose empty ruleset passes all traffic. On a machine with other
// serial devices it enumerated Bluetooth adapters and debug consoles as
// boards. Callers that want no serial discovery at all should skip the port
// enumeration entirely rather than rely on this returning false.
func MatchesAllowlist(portPath string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return false
	}
	for _, pat := range allowlist {
		// filepath.Match's `*` never crosses a `/`, so it would never match
		// an absolute device path. Treat a bare "*" as the explicit
		// "any port" opt-in it reads as.
		if pat == "*" {
			return true
		}
		if pat == portPath {
			return true
		}
		if ok, err := filepath.Match(pat, portPath); err == nil && ok {
			return true
		}
	}
	return false
}
