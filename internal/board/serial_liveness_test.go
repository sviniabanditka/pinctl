package board

import (
	"errors"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// This file covers the serial silence watchdog (docs/05-serial-protocol.md
// "Liveness"): the same heartbeat/heartbeatAwaiting mechanism a network
// Session already used (network_test.go), now shared by a serial Session,
// plus the kill side network never needed (a network Session's read
// deadline already surfaces a dead board as a read error; a serial Session
// has no such kernel-level signal, so heartbeatLoop itself calls markDead).
//
// Tests here use withSerialWatchdog directly (the internal Option Open
// applies for a real serial address) over the same net.Pipe/newHandshakedSession
// harness session_test.go and network_test.go already use — no real serial
// port, no sleeps beyond a few ms.

// --- serialWatchdogOpts (Open's "0 disables" decision) ----------------------

// TestSerialWatchdogOptsDisabledAtZero covers the pure decision Open makes
// for a serial address: silence_timeout <= 0 must add no watchdog option at
// all (heartbeatInterval stays the Session zero value, so newSession never
// starts heartbeatLoop), while a positive value adds exactly one.
func TestSerialWatchdogOptsDisabledAtZero(t *testing.T) {
	if opts := serialWatchdogOpts(0); len(opts) != 0 {
		t.Errorf("serialWatchdogOpts(0) = %d opts, want 0 (disabled)", len(opts))
	}
	if opts := serialWatchdogOpts(-time.Second); len(opts) != 0 {
		t.Errorf("serialWatchdogOpts(negative) = %d opts, want 0 (disabled)", len(opts))
	}
	if opts := serialWatchdogOpts(5 * time.Second); len(opts) != 1 {
		t.Errorf("serialWatchdogOpts(5s) = %d opts, want 1 (enabled)", len(opts))
	}
}

// --- liveness: the kill side ------------------------------------------------

// TestSerialSilenceWatchdogKillsHungBoard is the core failure this whole
// change closes: a serial Read on a hung board never errors, so nothing
// short of an explicit watchdog would ever notice. newHandshakedSession's
// fake board answers the handshake and then goes completely silent for the
// rest of the test (see its doc comment) — exactly a brownout-hung Uno.
// With a tiny silence timeout, the session must be marked dead within
// roughly that timeout, not hang "connected" forever.
func TestSerialSilenceWatchdogKillsHungBoard(t *testing.T) {
	s, _, _ := newHandshakedSession(t, withSerialWatchdog(40*time.Millisecond))

	waitFor(t, time.Second, func() bool { return !s.Alive() })

	if s.Err() == nil {
		t.Error("Err() = nil after silence watchdog fired, want non-nil")
	}
	if _, err := s.AnalogRead(0); !errors.Is(err, ErrSessionDead) {
		t.Errorf("AnalogRead after death = %v, want ErrSessionDead", err)
	}
}

// --- liveness: the heartbeat side (legitimately idle board) ----------------

// TestSerialSilenceWatchdogKeepsSilentButAliveBoardAlive proves a
// legitimately idle board (no analog inputs, no changing digital inputs —
// it reports nothing on its own) is not mistaken for a hung one: the fake
// board here only ever answers a Report Version query, exactly like
// network_test.go's TestNetworkHeartbeatKeepsSilentButAliveBoardAlive, and
// the session must stay alive well past what a single silence-timeout
// window would allow without the heartbeat distinguishing the two cases.
func TestSerialSilenceWatchdogKeepsSilentButAliveBoardAlive(t *testing.T) {
	client, serverConn := net.Pipe()
	t.Cleanup(func() { client.Close(); serverConn.Close() })

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
			}
			// Anything else is silently discarded — this fake board never
			// volunteers a report of its own.
		}
	}()

	s, err := newSession(client, WithBootDelay(0), WithHandshakeTimeout(2*time.Second), withSerialWatchdog(40*time.Millisecond))
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// ~7.5 silence-timeout windows: without the heartbeat this board would
	// have been killed on the very first window.
	time.Sleep(300 * time.Millisecond)
	if !s.Alive() {
		t.Fatal("session died despite a heartbeat-answering board — silence watchdog isn't distinguishing idle from hung")
	}
}

// --- liveness: heartbeat reply must not look like a reboot ------------------

// TestSerialSilenceWatchdogHeartbeatReplyDoesNotTriggerRebootButUnsolicitedDoes
// is network_test.go's
// TestNetworkHeartbeatReplyDoesNotTriggerRebootButUnsolicitedDoes, asserted
// for a serial Session: since heartbeatLoop/heartbeatAwaiting/applyMessage
// are now shared code, not a per-transport fork, the same
// indistinguishable-on-the-wire hazard applies here too, and it would be
// easy to regress one path while fixing the other.
func TestSerialSilenceWatchdogHeartbeatReplyDoesNotTriggerRebootButUnsolicitedDoes(t *testing.T) {
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

	// A generous watchdog interval keeps this test's timing well clear of
	// the next heartbeat tick, so the deliberate "unsolicited" write below
	// can't race a heartbeat reply on the wire.
	s, err := newSession(client, WithBootDelay(0), WithHandshakeTimeout(2*time.Second), withSerialWatchdog(200*time.Millisecond))
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

	// A genuine unsolicited version report (the board really rebooted) must
	// still fire onReboot, even on a serial session with the watchdog in
	// play.
	if _, err := serverConn.Write([]byte{reportVersion, 2, 5}); err != nil {
		t.Fatalf("write unsolicited version report: %v", err)
	}
	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&calls) == 1 })

	if !s.Alive() {
		t.Error("session marked dead after an unsolicited reboot report, want still Alive()")
	}
}

// --- silence_timeout: 0 disables the watchdog -------------------------------

// TestSerialWatchdogDisabledStaysAliveOnHungBoard covers
// serial.silence_timeout: 0 end to end at the Session level:
// withSerialWatchdog(0) is exactly what serialWatchdogOpts(0) would *not*
// add (see TestSerialWatchdogOptsDisabledAtZero), so a Session built
// without it has heartbeatInterval == 0 and never starts heartbeatLoop at
// all — a board that never speaks (the documented case this trades away
// detection for) stays "alive" indefinitely, exactly like a Session opened
// against a serial address with silence_timeout explicitly set to 0.
func TestSerialWatchdogDisabledStaysAliveOnHungBoard(t *testing.T) {
	s, _, _ := newHandshakedSession(t) // no watchdog option applied: heartbeatInterval stays 0

	time.Sleep(200 * time.Millisecond)
	if !s.Alive() {
		t.Fatal("session died despite the silence watchdog being disabled (heartbeatInterval 0)")
	}
}

// --- no goroutine leaks ------------------------------------------------------

// TestSerialWatchdogCloseDuringInFlightHeartbeatDoesNotLeak closes a
// session while heartbeatLoop has an outstanding heartbeat query (the fake
// board from newHandshakedSession never answers anything post-handshake),
// so heartbeatLoop is guaranteed to still be running at Close time, and
// asserts Close returns promptly and the goroutine count settles back down
// rather than growing unbounded.
func TestSerialWatchdogCloseDuringInFlightHeartbeatDoesNotLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	s, _, _ := newHandshakedSession(t, withSerialWatchdog(20*time.Millisecond))
	// Let the first heartbeat go out (interval/2 == 10ms) and sit
	// unanswered before closing.
	time.Sleep(15 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}

	waitFor(t, time.Second, func() bool { return runtime.NumGoroutine() <= before+1 })
}

// TestSerialWatchdogCloseAfterMarkDeadDoesNotLeak lets the watchdog itself
// kill the session (heartbeatLoop calls markDead and returns on its own),
// then closes it, and asserts the goroutine count settles back down —
// covering the other half of "no leaks" alongside the in-flight-heartbeat
// case above: heartbeatLoop already exited before Close ever runs here.
func TestSerialWatchdogCloseAfterMarkDeadDoesNotLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	s, _, _ := newHandshakedSession(t, withSerialWatchdog(30*time.Millisecond))
	waitFor(t, time.Second, func() bool { return !s.Alive() })

	if err := s.Close(); err != nil {
		t.Fatalf("Close after markDead: %v", err)
	}

	waitFor(t, time.Second, func() bool { return runtime.NumGoroutine() <= before+1 })
}
