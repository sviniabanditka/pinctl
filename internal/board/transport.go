package board

import (
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"go.bug.st/serial"
)

// Transport is the byte pipe a Session talks Firmata over. It's the
// abstraction docs/13-testing.md relies on: production code uses
// openSerialTransport (backed by go.bug.st/serial) or openTCPTransport
// (backed by net.Conn, see docs/17-network-boards.md), tests inject an
// in-memory fake instead of a real port/socket.
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

// networkScheme is the only address scheme docs/17-network-boards.md
// defines; anything else is treated as a serial port path.
const networkScheme = "tcp://"

// isNetworkAddr reports whether addr is a network board address
// (tcp://host:port) rather than a serial port path.
func isNetworkAddr(addr string) bool {
	return strings.HasPrefix(addr, networkScheme)
}

// openTCPTransport dials addr (a "host:port" pair, scheme already
// stripped) with dialTimeout, enables TCP keepalive at the given idle
// period, and returns a Transport whose Read refreshes a read deadline
// (readTimeout) on every call and whose Write sets a short write deadline
// — see docs/17-network-boards.md's "Liveness" section: a dead peer must
// surface as an error, never hang silently.
func openTCPTransport(addr string, dialTimeout, readTimeout, keepalive time.Duration) (Transport, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := applyTCPKeepalive(tcpConn, keepalive); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("board: setting keepalive on %s: %w", addr, err)
		}
	}
	// Write deadline is deliberately short (the dial timeout budget, not
	// the read timeout) — a write either lands on a healthy socket almost
	// immediately or the socket is wedged, in which case waiting
	// readTimeout to find out would stall the engine's tick goroutine for
	// no benefit.
	return &tcpTransport{conn: conn, readTimeout: readTimeout, writeTimeout: dialTimeout}, nil
}

// applyTCPKeepalive turns on TCP keepalive on conn with the given idle
// period, factored out of openTCPTransport so it's unit-testable against a
// *net.TCPConn without a full dial.
func applyTCPKeepalive(conn *net.TCPConn, keepalive time.Duration) error {
	if err := conn.SetKeepAlive(true); err != nil {
		return err
	}
	return conn.SetKeepAlivePeriod(keepalive)
}

// tcpTransport wraps a net.Conn so every Read refreshes a read deadline
// and every Write sets a write deadline — see docs/17-network-boards.md's
// "Liveness": a read deadline expiry must surface as an ordinary error (so
// Session.markDead fires and the Manager reconnects), never be silently
// retried.
type tcpTransport struct {
	conn         net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
}

func (t *tcpTransport) Read(p []byte) (int, error) {
	if err := t.conn.SetReadDeadline(time.Now().Add(t.readTimeout)); err != nil {
		return 0, err
	}
	return t.conn.Read(p)
}

func (t *tcpTransport) Write(p []byte) (int, error) {
	if err := t.conn.SetWriteDeadline(time.Now().Add(t.writeTimeout)); err != nil {
		return 0, err
	}
	return t.conn.Write(p)
}

func (t *tcpTransport) Close() error {
	return t.conn.Close()
}
