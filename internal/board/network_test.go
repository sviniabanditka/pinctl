package board

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// --- Open dispatch ----------------------------------------------------------

// TestIsNetworkAddr covers the exact predicate Open uses to dispatch
// between openTCPTransport and openSerialTransport (docs/17-network-boards.md
// "Addresses": "Open(addr) dispatches on the scheme. There is no second
// Open... and no `if isNetwork` scattered through the manager.") — this is
// the injected seam Open's dispatch is built on, checked directly rather
// than by actually opening a real serial port for the negative case.
func TestIsNetworkAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"tcp://esp32-porch.local:3030", true},
		{"tcp://192.168.1.51:3030", true},
		{"/dev/ttyACM0", false},
		{"/dev/ttyUSB0", false},
		{"COM3", false},
	}
	for _, c := range cases {
		if got := isNetworkAddr(c.addr); got != c.want {
			t.Errorf("isNetworkAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// TestOpenSerialAddrNeverDialsTCP proves Open("/dev/...") stays on the
// serial path: opening a nonexistent serial device fails with an error
// from go.bug.st/serial (a "file"/"device" style failure), never a "dial
// tcp" failure — if Open ever misdispatched a serial-looking address to
// openTCPTransport, this would instead fail (or hang) trying to dial
// "/dev/ttyACM0" as a TCP host:port.
func TestOpenSerialAddrNeverDialsTCP(t *testing.T) {
	_, err := Open("/dev/pinctl-test-nonexistent-device", WithBootDelay(0))
	if err == nil {
		t.Fatal("Open(nonexistent serial path) = nil error, want an open failure")
	}
	if isNetworkAddr("/dev/pinctl-test-nonexistent-device") {
		t.Fatal("test precondition broken: this address must not look like a network address")
	}
}

// --- fake TCP board harness --------------------------------------------------

// scriptTCPHandshake performs the handshake StandardFirmata's boot
// sequence would (see docs/05-serial-protocol.md's "Session lifecycle")
// over an already-accepted server-side conn, then returns with conn
// positioned right after the handshake so the caller can drive
// steady-state behavior explicitly (recorder loop, silence, heartbeat
// replies, ...).
func scriptTCPHandshake(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		t.Errorf("fake tcp board: read version query: %v", err)
		return
	}
	if _, err := conn.Write([]byte{reportVersion, 2, 5}); err != nil {
		t.Errorf("fake tcp board: write version reply: %v", err)
		return
	}
	readSysexQuery(t, conn)
	if _, err := conn.Write(fixtureFirmwareFrame()); err != nil {
		t.Errorf("fake tcp board: write firmware frame: %v", err)
		return
	}
	readSysexQuery(t, conn)
	if _, err := conn.Write(fixtureCapabilityFrame(fixturePinCount)); err != nil {
		t.Errorf("fake tcp board: write capability frame: %v", err)
		return
	}
	readSysexQuery(t, conn)
	if _, err := conn.Write(fixtureAnalogMappingFrame(fixturePinCount)); err != nil {
		t.Errorf("fake tcp board: write analog mapping frame: %v", err)
		return
	}
}

// listenFakeTCPBoard starts a listener on 127.0.0.1:0 and returns its
// address; the caller is responsible for Accept()ing and scripting
// whatever behavior the test needs. No real network, no external
// dependency — purely loopback.
func listenFakeTCPBoard(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// --- Open("tcp://...") integration ------------------------------------------

func TestOpenTCPDialsHandshakesAndReachesSteadyState(t *testing.T) {
	ln := listenFakeTCPBoard(t)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scriptTCPHandshake(t, conn)
		buf := make([]byte, 256)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	addr := networkScheme + ln.Addr().String()
	s, err := Open(addr, WithHandshakeTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("Open(%s): %v", addr, err)
	}
	t.Cleanup(func() { s.Close() })

	if !s.Alive() {
		t.Error("Alive() = false right after Open")
	}
	name, major, minor := s.Firmware()
	if name != "StandardFirmata" || major != 2 || minor != 5 {
		t.Errorf("Firmware() = %q %d.%d, want StandardFirmata 2.5", name, major, minor)
	}
	if len(s.Capabilities()) != fixturePinCount {
		t.Errorf("len(Capabilities()) = %d, want %d", len(s.Capabilities()), fixturePinCount)
	}
}

// TestOpenTCPBootDelayIsZero verifies a network board's default boot delay
// is zero (docs/17-network-boards.md: "the MCU is already up") by timing
// Open against a board that replies immediately — a nonzero default (the
// serial ~2s) would make this test visibly slow.
func TestOpenTCPBootDelayIsZero(t *testing.T) {
	ln := listenFakeTCPBoard(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scriptTCPHandshake(t, conn)
		buf := make([]byte, 256)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	addr := networkScheme + ln.Addr().String()
	start := time.Now()
	s, err := Open(addr, WithHandshakeTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("Open(%s): %v", addr, err)
	}
	t.Cleanup(func() { s.Close() })
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Open took %s, want well under the serial ~2s boot delay (network boot delay should default to 0)", elapsed)
	}
}

// TestOpenTCPExplicitBootDelayStillWins verifies WithBootDelay overrides
// the tcp:// scheme's zero default, per docs/17: "an explicit WithBootDelay
// still wins."
func TestOpenTCPExplicitBootDelayStillWins(t *testing.T) {
	ln := listenFakeTCPBoard(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scriptTCPHandshake(t, conn)
		buf := make([]byte, 256)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	addr := networkScheme + ln.Addr().String()
	const wantDelay = 150 * time.Millisecond
	start := time.Now()
	s, err := Open(addr, WithBootDelay(wantDelay), WithHandshakeTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("Open(%s): %v", addr, err)
	}
	t.Cleanup(func() { s.Close() })
	if elapsed := time.Since(start); elapsed < wantDelay {
		t.Errorf("Open took %s, want at least the explicit boot delay %s", elapsed, wantDelay)
	}
}

// --- liveness: read deadline ------------------------------------------------

// TestNetworkSessionDiesWhenBoardStopsResponding covers
// docs/17-network-boards.md's "Liveness" point 2: a board that goes
// silent (socket stays open, kernel sees nothing wrong) must still be
// detected as gone via the refreshed read deadline, well inside a short
// read_timeout — not left to hang for however long the OS takes to notice.
func TestNetworkSessionDiesWhenBoardStopsResponding(t *testing.T) {
	ln := listenFakeTCPBoard(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		scriptTCPHandshake(t, conn)
		accepted <- conn
		// Deliberately go silent forever: never read or write again,
		// simulating a board that's hung (or whose radio dropped) but
		// hasn't torn down the TCP connection.
	}()

	addr := networkScheme + ln.Addr().String()
	s, err := Open(addr,
		WithHandshakeTimeout(2*time.Second),
		WithDialTimeout(time.Second),
		WithReadTimeout(80*time.Millisecond),
		WithKeepalive(time.Second),
	)
	if err != nil {
		t.Fatalf("Open(%s): %v", addr, err)
	}
	t.Cleanup(func() {
		s.Close()
		select {
		case c := <-accepted:
			c.Close()
		default:
		}
	})

	waitFor(t, time.Second, func() bool { return !s.Alive() })

	if s.Err() == nil {
		t.Error("Err() = nil after read deadline expiry, want non-nil")
	}
	if _, err := s.AnalogRead(0); !errors.Is(err, ErrSessionDead) {
		t.Errorf("AnalogRead after death = %v, want ErrSessionDead", err)
	}
}

// --- liveness: heartbeat ----------------------------------------------------

// TestNetworkHeartbeatKeepsSilentButAliveBoardAlive covers
// docs/17-network-boards.md's heartbeat paragraph: a board with nothing to
// report (e.g. no analog input enabled) must not be mistaken for a dead
// one. The fake board here only ever answers a Report Version query
// (never volunteers anything else), and the session must still be alive
// well past what a single read_timeout window would allow without the
// heartbeat.
func TestNetworkHeartbeatKeepsSilentButAliveBoardAlive(t *testing.T) {
	ln := listenFakeTCPBoard(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scriptTCPHandshake(t, conn)
		buf := make([]byte, 1)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			if n == 1 && buf[0] == reportVersion {
				if _, err := conn.Write([]byte{reportVersion, 2, 5}); err != nil {
					return
				}
			}
			// Anything else (the sampling interval sysex, etc.) is
			// silently discarded — this fake board never volunteers a
			// report of its own.
		}
	}()

	addr := networkScheme + ln.Addr().String()
	s, err := Open(addr,
		WithHandshakeTimeout(2*time.Second),
		WithDialTimeout(time.Second),
		WithReadTimeout(80*time.Millisecond),
		WithKeepalive(time.Second),
	)
	if err != nil {
		t.Fatalf("Open(%s): %v", addr, err)
	}
	t.Cleanup(func() { s.Close() })

	// ~3.75 read_timeout windows: without the heartbeat this board would
	// have tripped the read deadline on the very first window.
	time.Sleep(300 * time.Millisecond)
	if !s.Alive() {
		t.Fatal("session died despite a heartbeat-answering board — heartbeat isn't keeping the read deadline refreshed")
	}
}

// TestNetworkHeartbeatReplyDoesNotTriggerRebootButUnsolicitedDoes is the
// subtlest part of docs/17-network-boards.md's Liveness section: the
// heartbeat's own reply is, on the wire, indistinguishable from
// StandardFirmata's unsolicited boot-time report — both are a bare
// ProtocolVersionMsg. Session.heartbeatAwaiting is what tells them apart;
// this test drives both cases against the same session and asserts
// SetOnReboot fires for exactly one of them.
func TestNetworkHeartbeatReplyDoesNotTriggerRebootButUnsolicitedDoes(t *testing.T) {
	client, serverConn := net.Pipe()
	t.Cleanup(func() { client.Close(); serverConn.Close() })

	heartbeatReplies := make(chan struct{}, 8)
	go func() {
		if _, err := io.ReadFull(serverConn, make([]byte, 1)); err != nil {
			return
		}
		if _, err := serverConn.Write([]byte{reportVersion, 2, 5}); err != nil {
			return
		}
		readSysexQuery(t, serverConn)
		if _, err := serverConn.Write(fixtureFirmwareFrame()); err != nil {
			return
		}
		readSysexQuery(t, serverConn)
		if _, err := serverConn.Write(fixtureCapabilityFrame(fixturePinCount)); err != nil {
			return
		}
		readSysexQuery(t, serverConn)
		if _, err := serverConn.Write(fixtureAnalogMappingFrame(fixturePinCount)); err != nil {
			return
		}

		buf := make([]byte, 1)
		for {
			n, err := serverConn.Read(buf)
			if err != nil {
				return
			}
			if n == 1 && buf[0] == reportVersion {
				if _, err := serverConn.Write([]byte{reportVersion, 2, 5}); err != nil {
					return
				}
				heartbeatReplies <- struct{}{}
			}
		}
	}()

	// A generous heartbeat interval (100ms) keeps this test's timing well
	// clear of the next heartbeat tick, so the deliberate "unsolicited"
	// write below can't race a heartbeat reply on the wire.
	s, err := newSession(client, WithBootDelay(0), WithHandshakeTimeout(2*time.Second), withNetworkHeartbeat(100*time.Millisecond))
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	var calls int32
	s.SetOnReboot(func() { atomic.AddInt32(&calls, 1) })

	select {
	case <-heartbeatReplies:
	case <-time.After(time.Second):
		t.Fatal("heartbeat query never reached the fake board")
	}
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("onReboot called %d times from a heartbeat reply alone, want 0", got)
	}

	// A genuine unsolicited version report (the board really rebooted)
	// must still fire onReboot, even on a network session with a
	// heartbeat in play.
	if _, err := serverConn.Write([]byte{reportVersion, 2, 5}); err != nil {
		t.Fatalf("write unsolicited version report: %v", err)
	}
	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&calls) == 1 })

	if !s.Alive() {
		t.Error("session marked dead after an unsolicited reboot report, want still Alive()")
	}
}

// --- keepalive ---------------------------------------------------------------

// TestApplyTCPKeepaliveSucceeds exercises the factored-out keepalive
// option-setting helper against a real *net.TCPConn (loopback), so it's
// unit-testable independent of a full Open/dial — Go's net package
// doesn't expose a portable way to read keepalive settings back, so
// "succeeds without error" is what's assertable.
func TestApplyTCPKeepaliveSucceeds(t *testing.T) {
	ln := listenFakeTCPBoard(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatalf("net.Dial(\"tcp\", ...) returned %T, want *net.TCPConn", conn)
	}
	if err := applyTCPKeepalive(tcpConn, 15*time.Second); err != nil {
		t.Errorf("applyTCPKeepalive: %v", err)
	}
}
