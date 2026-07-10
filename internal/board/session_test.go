package board

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- fixtures -------------------------------------------------------------
//
// A small, Uno-like pin set used across session tests: pins 0-13 digital
// I/O (3, 9, 10, 11 additionally PWM), pin 14 (A0) digital + analog
// (channel 0), pin 15 (A1) digital + analog (channel 1).

type modeRes struct {
	mode PinMode
	res  int
}

// fixturePinModes mirrors the ground-truth capability report from a real
// Uno running StandardFirmata 2.5: servo capability on pins 2-19 (wider than
// PWM, since servo pulses are generated in software by the Servo library),
// PWM only on 3/5/6/9/10/11. Pins 0/1 (RX/TX) get neither.
func fixturePinModes(pin int) []modeRes {
	var modes []modeRes
	switch pin {
	case 3, 9, 10, 11:
		modes = []modeRes{{ModeInput, 1}, {ModeOutput, 1}, {ModePWM, 8}}
	case 14, 15:
		modes = []modeRes{{ModeInput, 1}, {ModeOutput, 1}, {ModeAnalog, 10}}
	default:
		modes = []modeRes{{ModeInput, 1}, {ModeOutput, 1}}
	}
	if pin >= 2 {
		modes = append(modes, modeRes{ModeServo, 14})
	}
	return modes
}

func encodeSysexFrame(cmd byte, body ...byte) []byte {
	frame := append([]byte{startSysex, cmd}, body...)
	return append(frame, endSysex)
}

func fixtureCapabilityFrame(pins int) []byte {
	body := []byte{}
	for pin := 0; pin < pins; pin++ {
		for _, mr := range fixturePinModes(pin) {
			body = append(body, byte(mr.mode), byte(mr.res))
		}
		body = append(body, modeCapabilityEnd)
	}
	return encodeSysexFrame(sysexCapabilityResponse, body...)
}

func fixtureAnalogMappingFrame(pins int) []byte {
	body := []byte{}
	channel := 0
	for pin := 0; pin < pins; pin++ {
		isAnalog := false
		for _, mr := range fixturePinModes(pin) {
			if mr.mode == ModeAnalog {
				isAnalog = true
			}
		}
		if isAnalog {
			body = append(body, byte(channel))
			channel++
		} else {
			body = append(body, modeCapabilityEnd)
		}
	}
	return encodeSysexFrame(sysexAnalogMappingResponse, body...)
}

func fixtureFirmwareFrame() []byte {
	body := []byte{2, 5}
	for _, r := range "StandardFirmata" {
		l, m := encode7Bit(int(r))
		body = append(body, l, m)
	}
	return encodeSysexFrame(sysexReportFirmware, body...)
}

const fixturePinCount = 16

// --- recorder ---------------------------------------------------------------

// recorder accumulates raw bytes read from a net.Conn, safe for concurrent
// use by the reading goroutine and assertions from the test goroutine.
type recorder struct {
	mu  sync.Mutex
	buf []byte
}

func (r *recorder) append(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, b...)
}

func (r *recorder) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

// --- fake board harness -----------------------------------------------------

// readExactFrame reads bytes from r until a sysex terminator, returning the
// raw frame (used only to consume/ignore a query during the scripted
// handshake phase; the fixture responses don't depend on the query body).
func readSysexQuery(t *testing.T, r net.Conn) {
	t.Helper()
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if err != nil {
			t.Fatalf("fake board: read query: %v", err)
		}
		if n == 1 && b[0] == endSysex {
			return
		}
	}
}

// newHandshakedSession opens a Session against an in-memory fake board that
// scripts the full handshake per docs/05-serial-protocol.md, then keeps
// recording every further byte the Session writes (SetPinMode, DigitalWrite,
// etc.) into the returned recorder. The returned net.Conn is the board's
// end, free for the test to Write unprompted reports on.
func newHandshakedSession(t *testing.T, opts ...Option) (*Session, net.Conn, *recorder) {
	t.Helper()
	client, serverConn := net.Pipe()
	rec := &recorder{}

	go func() {
		// Session actively queries the version (single 0xF9 byte)
		// before listening; consume it or net.Pipe deadlocks.
		io.ReadFull(serverConn, make([]byte, 1))
		serverConn.Write([]byte{reportVersion, 2, 5})

		readSysexQuery(t, serverConn) // report firmware query
		serverConn.Write(fixtureFirmwareFrame())

		readSysexQuery(t, serverConn) // capability query
		serverConn.Write(fixtureCapabilityFrame(fixturePinCount))

		readSysexQuery(t, serverConn) // analog mapping query
		serverConn.Write(fixtureAnalogMappingFrame(fixturePinCount))

		// From here on: sampling interval (sent unprompted, no
		// response expected) and anything else the Session writes
		// for the rest of the test just gets recorded.
		buf := make([]byte, 256)
		for {
			n, err := serverConn.Read(buf)
			if err != nil {
				return
			}
			rec.append(buf[:n])
		}
	}()

	allOpts := append([]Option{WithBootDelay(0), WithHandshakeTimeout(2 * time.Second)}, opts...)
	s, err := newSession(client, allOpts...)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, serverConn, rec
}

// --- tests --------------------------------------------------------------

func TestSessionHandshakePopulatesCapabilitiesAndFirmware(t *testing.T) {
	s, _, _ := newHandshakedSession(t)

	name, major, minor := s.Firmware()
	if name != "StandardFirmata" || major != 2 || minor != 5 {
		t.Errorf("Firmware() = %q %d.%d, want StandardFirmata 2.5", name, major, minor)
	}

	pmajor, pminor := s.ProtocolVersion()
	if pmajor != 2 || pminor != 5 {
		t.Errorf("ProtocolVersion() = %d.%d, want 2.5", pmajor, pminor)
	}

	caps := s.Capabilities()
	if len(caps) != fixturePinCount {
		t.Fatalf("got %d pins, want %d", len(caps), fixturePinCount)
	}
	pin14 := caps[14]
	if !pin14.SupportsMode(ModeAnalog) || !pin14.HasAnalogChannel || pin14.AnalogChannel != 0 {
		t.Errorf("pin 14 (A0) capability = %+v, want analog channel 0", pin14)
	}
	pin3 := caps[3]
	if !pin3.SupportsMode(ModePWM) {
		t.Errorf("pin 3 capability = %+v, want PWM support", pin3)
	}
}

func TestSessionDigitalAndAnalogReportsUpdateCache(t *testing.T) {
	s, board, _ := newHandshakedSession(t)

	if err := s.SetPinMode(2, ModeInput); err != nil {
		t.Fatalf("SetPinMode(2, ModeInput): %v", err)
	}
	if err := s.SetPinMode(14, ModeAnalog); err != nil {
		t.Fatalf("SetPinMode(14, ModeAnalog): %v", err)
	}

	// Board reports port 0 (pins 0-7): pin 2 high.
	lsb, msb := encode7Bit(1 << 2)
	if _, err := board.Write([]byte{digitalMessage | 0, lsb, msb}); err != nil {
		t.Fatalf("fake board write digital report: %v", err)
	}
	// Board reports analog channel 0 (pin 14 / A0): value 512.
	alsb, amsb := encode7Bit(512)
	if _, err := board.Write([]byte{analogMessage | 0, alsb, amsb}); err != nil {
		t.Fatalf("fake board write analog report: %v", err)
	}

	waitFor(t, time.Second, func() bool {
		v, err := s.DigitalRead(2)
		return err == nil && v == true
	})
	waitFor(t, time.Second, func() bool {
		v, err := s.AnalogRead(0)
		return err == nil && v == 512
	})
}

func TestSessionWritesEmitExpectedBytes(t *testing.T) {
	s, _, rec := newHandshakedSession(t)

	if err := s.SetPinMode(13, ModeOutput); err != nil {
		t.Fatalf("SetPinMode: %v", err)
	}
	if err := s.DigitalWrite(13, true); err != nil {
		t.Fatalf("DigitalWrite: %v", err)
	}
	if err := s.AnalogWrite(9, 200); err != nil {
		t.Fatalf("AnalogWrite: %v", err)
	}

	wantSetMode := EncodeSetPinMode(13, ModeOutput)
	wantDigitalWrite := EncodeDigitalWrite(13, true)
	wantAnalogWrite := EncodeAnalogWrite(9, 200)

	waitFor(t, time.Second, func() bool {
		b := rec.Bytes()
		return bytes.Contains(b, wantSetMode) &&
			bytes.Contains(b, wantDigitalWrite) &&
			bytes.Contains(b, wantAnalogWrite)
	})
}

func TestSessionSetPinModeRejectsUnsupportedMode(t *testing.T) {
	s, _, _ := newHandshakedSession(t)

	// Pin 2 has no PWM capability in the fixture.
	err := s.SetPinMode(2, ModePWM)
	if !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("SetPinMode(2, ModePWM) = %v, want ErrUnsupportedMode", err)
	}
}

func TestSessionDigitalWriteRejectsUnknownPin(t *testing.T) {
	s, _, rec := newHandshakedSession(t)

	err := s.DigitalWrite(999, true)
	if !errors.Is(err, ErrUnknownPin) {
		t.Fatalf("DigitalWrite(999, true) = %v, want ErrUnknownPin", err)
	}

	// Give any (incorrect) write a chance to land before asserting none did.
	time.Sleep(20 * time.Millisecond)
	if b := rec.Bytes(); bytes.Contains(b, EncodeDigitalWrite(999, true)) {
		t.Errorf("transport recorded bytes for rejected pin: %v", b)
	}
}

func TestSessionAnalogWriteRejectsUnknownPin(t *testing.T) {
	s, _, rec := newHandshakedSession(t)

	err := s.AnalogWrite(999, 100)
	if !errors.Is(err, ErrUnknownPin) {
		t.Fatalf("AnalogWrite(999, 100) = %v, want ErrUnknownPin", err)
	}

	time.Sleep(20 * time.Millisecond)
	if b := rec.Bytes(); bytes.Contains(b, EncodeAnalogWrite(999, 100)) {
		t.Errorf("transport recorded bytes for rejected pin: %v", b)
	}
}

func TestSessionServoConfigAndWriteEmitExpectedBytes(t *testing.T) {
	s, _, rec := newHandshakedSession(t)

	if err := s.SetPinMode(2, ModeServo); err != nil {
		t.Fatalf("SetPinMode(2, ModeServo): %v", err)
	}
	if err := s.ServoConfig(2, 544, 2400); err != nil {
		t.Fatalf("ServoConfig: %v", err)
	}
	if err := s.ServoWrite(2, 90); err != nil {
		t.Fatalf("ServoWrite: %v", err)
	}

	wantSetMode := EncodeSetPinMode(2, ModeServo)
	wantServoConfig := EncodeServoConfig(2, 544, 2400)
	wantServoWrite := EncodeAnalogWrite(2, 90)

	waitFor(t, time.Second, func() bool {
		b := rec.Bytes()
		return bytes.Contains(b, wantSetMode) &&
			bytes.Contains(b, wantServoConfig) &&
			bytes.Contains(b, wantServoWrite)
	})
}

func TestSessionServoWriteRejectsOutOfRangeAngle(t *testing.T) {
	s, _, rec := newHandshakedSession(t)

	for _, angle := range []int{-1, 181, 360} {
		if err := s.ServoWrite(2, angle); err == nil {
			t.Errorf("ServoWrite(2, %d) = nil, want error", angle)
		}
	}

	time.Sleep(20 * time.Millisecond)
	if b := rec.Bytes(); bytes.Contains(b, EncodeAnalogWrite(2, 181)) {
		t.Errorf("transport recorded bytes for out-of-range angle: %v", b)
	}
}

func TestSessionServoWriteRejectsNonServoPin(t *testing.T) {
	s, _, rec := newHandshakedSession(t)

	// Pin 0 (RX) has no servo capability in the fixture.
	err := s.ServoWrite(0, 90)
	if !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("ServoWrite(0, 90) = %v, want ErrUnsupportedMode", err)
	}

	time.Sleep(20 * time.Millisecond)
	if b := rec.Bytes(); bytes.Contains(b, EncodeAnalogWrite(0, 90)) {
		t.Errorf("transport recorded bytes for rejected pin: %v", b)
	}
}

func TestSessionServoConfigRejectsNonServoPin(t *testing.T) {
	s, _, rec := newHandshakedSession(t)

	// Pin 0 (RX) has no servo capability in the fixture.
	err := s.ServoConfig(0, 544, 2400)
	if !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("ServoConfig(0, ...) = %v, want ErrUnsupportedMode", err)
	}

	time.Sleep(20 * time.Millisecond)
	if b := rec.Bytes(); bytes.Contains(b, EncodeServoConfig(0, 544, 2400)) {
		t.Errorf("transport recorded bytes for rejected pin: %v", b)
	}
}

func TestSessionServoConfigRejectsUnknownPin(t *testing.T) {
	s, _, _ := newHandshakedSession(t)

	err := s.ServoConfig(999, 544, 2400)
	if !errors.Is(err, ErrUnknownPin) {
		t.Fatalf("ServoConfig(999, ...) = %v, want ErrUnknownPin", err)
	}
}

func TestSessionDiesOnTransportErrorWithoutCrashing(t *testing.T) {
	s, board, _ := newHandshakedSession(t)

	if err := board.Close(); err != nil {
		t.Fatalf("close fake board conn: %v", err)
	}

	waitFor(t, time.Second, func() bool { return !s.Alive() })

	if s.Err() == nil {
		t.Error("Err() = nil after transport died, want non-nil")
	}
	if _, err := s.DigitalRead(2); !errors.Is(err, ErrSessionDead) {
		t.Errorf("DigitalRead after death = %v, want ErrSessionDead", err)
	}
	if err := s.DigitalWrite(13, true); !errors.Is(err, ErrSessionDead) {
		t.Errorf("DigitalWrite after death = %v, want ErrSessionDead", err)
	}
	if err := s.SetPinMode(13, ModeOutput); !errors.Is(err, ErrSessionDead) {
		t.Errorf("SetPinMode after death = %v, want ErrSessionDead", err)
	}
}

func TestSessionUnsupportedProtocolVersionFailsPermanently(t *testing.T) {
	client, serverConn := net.Pipe()
	go func() {
		io.ReadFull(serverConn, make([]byte, 1)) // version query
		// Report protocol version 1.0 (unsupported per
		// docs/05-serial-protocol.md: major < 2 is a hard failure).
		serverConn.Write([]byte{reportVersion, 1, 0})
		buf := make([]byte, 64)
		for {
			if _, err := serverConn.Read(buf); err != nil {
				return
			}
		}
	}()

	_, err := newSession(client, WithBootDelay(0), WithHandshakeTimeout(2*time.Second))
	if !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("newSession with protocol v1.0 = %v, want ErrUnsupportedProtocolVersion", err)
	}
	client.Close()
	serverConn.Close()
}

// --- mid-session reboot detection -----------------------------------------

func TestSessionRebootDetectedFiresCallbackOnceAndReArmsSamplingInterval(t *testing.T) {
	s, boardConn, rec := newHandshakedSession(t)

	var calls int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s.SetOnReboot(func() {
		atomic.AddInt32(&calls, 1)
		entered <- struct{}{}
		<-release // held open until the test has injected the firmware report too
	})

	// Board rebooted: re-emits its unsolicited boot report, which should
	// trigger reconfiguration.
	if _, err := boardConn.Write([]byte{reportVersion, 2, 5}); err != nil {
		t.Fatalf("write version report: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("onReboot callback never fired for the version report")
	}

	// StandardFirmata sends a firmware report right behind the version
	// report on boot; injected here while the first callback is still in
	// flight (held on release), so this exercises the single-flight
	// coalescing deterministically rather than racing callback speed
	// against write/parse timing.
	if _, err := boardConn.Write(fixtureFirmwareFrame()); err != nil {
		t.Fatalf("write firmware report: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)

	// Give a stray second invocation a chance to land before asserting
	// there wasn't one.
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("onReboot called %d times, want exactly 1 (version+firmware must coalesce)", got)
	}

	if !s.Alive() {
		t.Error("session marked dead after a reboot signal, want still Alive() — the transport is fine")
	}

	wantSampling := EncodeSamplingInterval(s.samplingIntervalMS)
	waitFor(t, time.Second, func() bool {
		return bytes.Contains(rec.Bytes(), wantSampling)
	})
}

func TestSessionRebootCallbackRunsOffReadLoopAndCanCallSetPinMode(t *testing.T) {
	s, boardConn, rec := newHandshakedSession(t)

	done := make(chan error, 1)
	s.SetOnReboot(func() {
		// A callback that calls back into the session (SetPinMode writes
		// to the transport and takes locks) must not deadlock the read
		// loop that invoked triggerReboot.
		done <- s.SetPinMode(13, ModeOutput)
	})

	if _, err := boardConn.Write([]byte{reportVersion, 2, 5}); err != nil {
		t.Fatalf("write version report: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SetPinMode from within onReboot callback: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("onReboot callback (calling SetPinMode) never completed — read loop likely deadlocked")
	}

	wantSetMode := EncodeSetPinMode(13, ModeOutput)
	waitFor(t, time.Second, func() bool {
		return bytes.Contains(rec.Bytes(), wantSetMode)
	})

	// The read loop must still be alive and processing messages after the
	// callback ran (i.e. it never blocked on the callback).
	if err := s.SetPinMode(2, ModeInput); err != nil {
		t.Fatalf("SetPinMode after reboot handling: %v", err)
	}
}

func TestSessionVersionReportDuringHandshakeDoesNotFireReboot(t *testing.T) {
	client, serverConn := net.Pipe()
	t.Cleanup(func() { client.Close(); serverConn.Close() })

	go func() {
		io.ReadFull(serverConn, make([]byte, 1)) // version query
		serverConn.Write([]byte{reportVersion, 2, 5})

		readSysexQuery(t, serverConn) // firmware query
		// Stray duplicate version report arriving before the firmware
		// reply — awaitMatchWithTimeout must skip it rather than treat it
		// as a reboot (there's no callback registered yet at this point
		// anyway, but the handshakeDone flag-gate itself is what's under
		// test here).
		serverConn.Write([]byte{reportVersion, 2, 5})
		serverConn.Write(fixtureFirmwareFrame())

		readSysexQuery(t, serverConn)
		serverConn.Write(fixtureCapabilityFrame(fixturePinCount))

		readSysexQuery(t, serverConn)
		serverConn.Write(fixtureAnalogMappingFrame(fixturePinCount))

		buf := make([]byte, 256)
		for {
			if _, err := serverConn.Read(buf); err != nil {
				return
			}
		}
	}()

	var calls int32
	s, err := newSession(client, WithBootDelay(0), WithHandshakeTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	s.SetOnReboot(func() { atomic.AddInt32(&calls, 1) })

	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("onReboot called %d times from in-handshake reports, want 0", got)
	}
}

func TestSessionCloseDuringInFlightRebootCallbackDoesNotPanic(t *testing.T) {
	s, boardConn, _ := newHandshakedSession(t)

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	s.SetOnReboot(func() {
		entered <- struct{}{}
		<-release
	})

	if _, err := boardConn.Write([]byte{reportVersion, 2, 5}); err != nil {
		t.Fatalf("write version report: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("onReboot callback never entered")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close() }()

	// Give Close a moment to reach <-s.done before releasing the
	// in-flight callback; Close should still return promptly (it only
	// waits on the read loop, not on the reboot goroutine).
	time.Sleep(20 * time.Millisecond)
	close(release)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close during in-flight reboot callback: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
}

func TestSessionHandshakeTimesOutWithNoBoardResponse(t *testing.T) {
	client, serverConn := net.Pipe()
	t.Cleanup(func() { client.Close(); serverConn.Close() })
	// Board never sends anything — but must drain writes, or the
	// session's version query blocks forever on the synchronous pipe.
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := serverConn.Read(buf); err != nil {
				return
			}
		}
	}()

	_, err := newSession(client, WithBootDelay(0), WithHandshakeTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("newSession() = nil error, want handshake timeout error")
	}
}
