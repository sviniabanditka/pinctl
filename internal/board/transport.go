package board

import (
	"io"

	"go.bug.st/serial"
)

// Transport is the byte pipe a Session talks Firmata over. It's the
// abstraction docs/13-testing.md relies on: production code uses
// openSerialTransport (backed by go.bug.st/serial), tests inject an
// in-memory fake instead of a real port.
type Transport interface {
	io.Reader
	io.Writer
	io.Closer
}

// baudRate is Firmata's default and the only rate pinctl uses; see
// docs/00-design-brief.md's fixed decisions table.
const baudRate = 57600

// openSerialTransport opens portPath at Firmata's standard 57600 8N1
// framing. On most Uno/Nano boards this open toggles DTR and resets the
// MCU as a side effect (see docs/05-serial-protocol.md, "Board reset on
// serial open") — the caller (Session) is responsible for waiting out the
// boot delay before writing anything.
func openSerialTransport(portPath string) (Transport, error) {
	mode := &serial.Mode{
		BaudRate: baudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	port, err := serial.Open(portPath, mode)
	if err != nil {
		return nil, err
	}
	return port, nil
}
