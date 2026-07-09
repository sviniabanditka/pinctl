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
// docs/14-deployment.md's serial.allowlist (FR-BOARD-4): an empty allowlist
// means "allow any" (fine for a single-board dev box); otherwise portPath
// must match one of the patterns. Patterns are filepath.Match-style globs —
// a pattern with no glob metacharacters behaves as a plain exact match, so
// the deployment doc's example of literal paths ("/dev/ttyACM0") works
// unchanged, while an operator who wants "any ttyACM*" can write that glob.
func MatchesAllowlist(portPath string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, pat := range allowlist {
		if pat == portPath {
			return true
		}
		if ok, err := filepath.Match(pat, portPath); err == nil && ok {
			return true
		}
	}
	return false
}
