package board

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Session owns one open Firmata connection to a board: the handshake
// (protocol version -> firmware -> capabilities -> analog mapping), and,
// once handshake completes, a read loop that keeps a cached last-known
// value for every digital pin and analog channel the board reports.
//
// Deviation from docs/05-serial-protocol.md worth flagging: the doc's
// subset table lists both Set Digital Pin Value (0xF5, "used for pins
// outside a full port write") and the Digital I/O Message port form
// (0x90+port, listed for both directions including runtime writes), and
// its "Digital write flow" sequence diagram illustrates the write path
// using the port form. Session.DigitalWrite uses 0xF5 exclusively: v0.1
// has no device model, so there's no registry of "other pins on this port"
// to preserve when writing one pin via the port form, and doing a
// read-modify-write against the last known port state to avoid clobbering
// neighboring pins is exactly the kind of statefulness the roadmap defers
// past the walking skeleton. 0xF5 is explicitly documented for this
// single-pin case, so this isn't a protocol violation, just picking the
// simpler of two documented options for v0.1.
//
// A single Session talks to a single board; there is no manager, no
// reconnect loop, no multi-board registry here (all v0.2+/v0.4 per
// docs/12-roadmap.md).
type Session struct {
	transport Transport

	// portPath is the serial port this session was opened against, set
	// once (via the internal withPortPath option, from Open) before the
	// read loop starts — read-only from then on, so it's safe to read
	// from any goroutine without s.mu. Empty when the session was built
	// directly over an injected Transport (tests, or newSession callers
	// that skip Open). Used only for logging.
	portPath string

	bootDelay          time.Duration
	handshakeTimeout   time.Duration
	samplingIntervalMS int

	writeMu sync.Mutex // serializes writes onto the transport

	mu            sync.Mutex
	capabilities  []PinCapability
	pinIndex      map[int]PinCapability
	handshakeDone bool // set at the end of handshake(); gates reboot detection
	onReboot      func()

	firmwareName                 string
	firmwareMajor, firmwareMinor int
	protocolMajor, protocolMinor int

	digital map[int]bool // pin -> last known value
	analog  map[int]int  // channel -> last known raw value (0-16383)

	dead    bool
	deadErr error

	// rebooting single-flights reboot handling: 1 while a reconfiguration
	// (sampling interval re-arm + onReboot callback) triggered by an
	// unsolicited post-handshake protocol version / firmware report is in
	// flight. StandardFirmata emits both back-to-back on boot, and this
	// coalesces that pair into one reconfiguration instead of two.
	rebooting int32

	closeOnce sync.Once
	done      chan struct{}
}

// Open opens the serial port at portPath, waits out the boot delay, runs
// the Firmata handshake, and returns a Session in steady state (read loop
// running). See docs/05-serial-protocol.md's "Session lifecycle".
func Open(portPath string, opts ...Option) (*Session, error) {
	t, err := openSerialTransport(portPath)
	if err != nil {
		return nil, fmt.Errorf("board: open %s: %w", portPath, err)
	}
	s, err := newSession(t, append(opts, withPortPath(portPath))...)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return s, nil
}

// withPortPath records portPath on the Session for logging (see
// Session.portPath's doc comment). Internal-only: applied by Open, after
// any caller-supplied opts, before the read loop starts.
func withPortPath(portPath string) Option {
	return func(s *Session) { s.portPath = portPath }
}

// defaults match docs/05-serial-protocol.md: ~2s boot delay, a handshake
// timeout comfortably longer than it, and a 20ms sampling interval
// matching the engine's default tick.
const (
	defaultBootDelay        = 2 * time.Second
	defaultHandshakeTimeout = 3 * time.Second
	defaultSamplingInterval = 20

	// rebootCooldown suppresses further reboot signals for this long after
	// one is handled: a booting board emits a version report and a firmware
	// report back to back, and both mean the same single reboot.
	rebootCooldown = 500 * time.Millisecond
)

// newSession builds a Session over an already-open Transport. It is
// unexported so tests (in this package) can inject a fake Transport
// directly, without going through a real serial port.
func newSession(t Transport, opts ...Option) (*Session, error) {
	s := &Session{
		transport:          t,
		bootDelay:          defaultBootDelay,
		handshakeTimeout:   defaultHandshakeTimeout,
		samplingIntervalMS: defaultSamplingInterval,
		digital:            make(map[int]bool),
		analog:             make(map[int]int),
		done:               make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}

	if s.bootDelay > 0 {
		time.Sleep(s.bootDelay)
	}

	if err := s.handshake(); err != nil {
		close(s.done)
		return nil, err
	}

	go s.readLoop()
	return s, nil
}

// handshake performs steps 3-5 of docs/05-serial-protocol.md's session
// lifecycle: protocol version, firmware, capabilities, analog mapping,
// then sets the sampling interval.
func (s *Session) handshake() error {
	parser := NewParser()

	// Actively query the protocol version instead of relying on the
	// board's unsolicited boot-time report: opening the port does not
	// always assert DTR (observed on macOS /dev/cu.*), so the board may
	// never have reset and its report was sent long before we listened.
	if err := s.write(EncodeReportVersionQuery()); err != nil {
		return fmt.Errorf("board: handshake: sending version query: %w", err)
	}
	msg, err := s.awaitMatchWithTimeout(parser, func(m Message) bool {
		_, ok := m.(ProtocolVersionMsg)
		return ok
	})
	if err != nil {
		return fmt.Errorf("board: handshake: waiting for protocol version: %w", err)
	}
	pv := msg.(ProtocolVersionMsg)
	if pv.Major < 2 {
		return fmt.Errorf("%w: reported %d.%d", ErrUnsupportedProtocolVersion, pv.Major, pv.Minor)
	}
	s.protocolMajor, s.protocolMinor = pv.Major, pv.Minor

	if err := s.write(EncodeReportFirmwareQuery()); err != nil {
		return fmt.Errorf("board: handshake: sending firmware query: %w", err)
	}
	msg, err = s.awaitMatchWithTimeout(parser, func(m Message) bool {
		_, ok := m.(FirmwareMsg)
		return ok
	})
	if err != nil {
		return fmt.Errorf("board: handshake: waiting for firmware response: %w", err)
	}
	fw := msg.(FirmwareMsg)
	s.firmwareName, s.firmwareMajor, s.firmwareMinor = fw.Name, fw.Major, fw.Minor

	if err := s.write(EncodeCapabilityQuery()); err != nil {
		return fmt.Errorf("board: handshake: sending capability query: %w", err)
	}
	msg, err = s.awaitMatchWithTimeout(parser, func(m Message) bool {
		_, ok := m.(CapabilityResponseMsg)
		return ok
	})
	if err != nil {
		return fmt.Errorf("board: handshake: waiting for capability response: %w", err)
	}
	capMsg := msg.(CapabilityResponseMsg)

	if err := s.write(EncodeAnalogMappingQuery()); err != nil {
		return fmt.Errorf("board: handshake: sending analog mapping query: %w", err)
	}
	msg, err = s.awaitMatchWithTimeout(parser, func(m Message) bool {
		_, ok := m.(AnalogMappingMsg)
		return ok
	})
	if err != nil {
		return fmt.Errorf("board: handshake: waiting for analog mapping response: %w", err)
	}
	mapMsg := msg.(AnalogMappingMsg)

	pins := make([]PinCapability, len(capMsg.Pins))
	pinIndex := make(map[int]PinCapability, len(capMsg.Pins))
	for i, pc := range capMsg.Pins {
		if ch, ok := mapMsg.PinToChannel[pc.Pin]; ok {
			pc.AnalogChannel = ch
			pc.HasAnalogChannel = true
		}
		pins[i] = pc
		pinIndex[pc.Pin] = pc
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i].Pin < pins[j].Pin })

	s.mu.Lock()
	s.capabilities = pins
	s.pinIndex = pinIndex
	s.mu.Unlock()

	if err := s.write(EncodeSamplingInterval(s.samplingIntervalMS)); err != nil {
		return fmt.Errorf("board: handshake: sending sampling interval: %w", err)
	}

	s.mu.Lock()
	s.handshakeDone = true
	s.mu.Unlock()
	return nil
}

// awaitMatchWithTimeout blocks until a message matching match arrives,
// skipping anything else: after a reset the board volunteers reports we
// didn't ask for (e.g. a firmware report right behind the version report),
// and a previously configured board may still be streaming analog values.
// Each call gets a fresh s.handshakeTimeout deadline of its own, so each
// handshake round trip has its own full timeout budget (see
// WithHandshakeTimeout's doc comment).
func (s *Session) awaitMatchWithTimeout(parser *Parser, match func(Message) bool) (Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.handshakeTimeout)
	defer cancel()
	for {
		msg, err := s.awaitMessage(ctx, parser)
		if err != nil {
			return nil, err
		}
		if match(msg) {
			return msg, nil
		}
	}
}

// awaitMessage blocks until parser produces a decoded Message from the
// transport, ctx expires, or a read error occurs. It's only used during
// handshake, where messages are awaited strictly one at a time.
func (s *Session) awaitMessage(ctx context.Context, parser *Parser) (Message, error) {
	type result struct {
		msg Message
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := s.transport.Read(buf)
			if err != nil {
				resCh <- result{nil, err}
				return
			}
			if n == 0 {
				continue
			}
			msg, err := parser.Feed(buf[0])
			if err != nil {
				// Malformed frame: discard and keep resyncing, per
				// docs/05-serial-protocol.md's error handling table.
				continue
			}
			if msg == nil {
				continue
			}
			resCh <- result{msg, nil}
			return
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resCh:
		return res.msg, res.err
	}
}

// readLoop is the steady-state read path: parse incoming bytes into
// messages and update the pin-state cache. On any read error (unplug, or
// Close closing the transport out from under it) the session is marked
// dead and the loop exits — it never panics or crashes the process.
func (s *Session) readLoop() {
	defer close(s.done)
	parser := NewParser()
	buf := make([]byte, 1)
	for {
		n, err := s.transport.Read(buf)
		if err != nil {
			s.markDead(err)
			return
		}
		if n == 0 {
			continue
		}
		msg, err := parser.Feed(buf[0])
		if err != nil {
			continue // malformed frame: discard, resync
		}
		if msg == nil {
			continue
		}
		s.applyMessage(msg)
	}
}

func (s *Session) applyMessage(msg Message) {
	s.mu.Lock()
	switch m := msg.(type) {
	case DigitalPortMsg:
		base := m.Port * 8
		for i := 0; i < 8; i++ {
			s.digital[base+i] = m.Value&(1<<uint(i)) != 0
		}
		s.mu.Unlock()
	case AnalogValueMsg:
		s.analog[m.Channel] = m.Value
		s.mu.Unlock()
	case ProtocolVersionMsg, FirmwareMsg:
		// StandardFirmata sends these unprompted, exactly once, right
		// after boot. Seeing either one again after handshakeDone is set
		// means the board rebooted mid-session (brownout, another
		// process toggling DTR, the reset button) — the MCU has forgotten
		// every pin mode, reporting subscription, and sampling interval
		// it had. The transport itself is fine (no read/write error), so
		// this is the only signal we get; see docs/05-serial-protocol.md
		// "Mid-session board reset".
		reboot := s.handshakeDone
		s.mu.Unlock()
		if reboot {
			s.triggerReboot()
		}
	default:
		// Capability / analog mapping responses are only expected during
		// handshake; nothing else to do if a board sends one again later
		// (a reboot is already fully handled via the version/firmware
		// case above, which fires first).
	}
}

// triggerReboot single-flights a post-handshake reboot: it re-sends the
// sampling interval (session-level state the caller has no way to know
// about) and then invokes the registered onReboot callback, both off the
// read loop's goroutine so a callback that itself calls back into the
// session (e.g. SetPinMode, which writes to the transport and takes s.mu)
// can never deadlock or stall message parsing. Concurrent/rapid
// version+firmware reports (StandardFirmata sends both back-to-back on
// boot) collapse into a single reconfiguration: further signals are
// ignored while one is already in flight.
func (s *Session) triggerReboot() {
	if !atomic.CompareAndSwapInt32(&s.rebooting, 0, 1) {
		return
	}
	go func() {
		// Hold the single-flight flag for a cooldown after the work is
		// done, not just during it. Observed on a real Uno: reconfiguring
		// finishes before the firmware report that trails the boot-time
		// version report arrives, so the in-flight flag alone coalesces
		// nothing and the pair fires two reconfigurations.
		defer func() {
			time.Sleep(rebootCooldown)
			atomic.StoreInt32(&s.rebooting, 0)
		}()

		label := s.portPath
		if label == "" {
			label = "session"
		}
		log.Printf("board: %s rebooted, reconfiguring", label)

		if err := s.write(EncodeSamplingInterval(s.samplingIntervalMS)); err != nil {
			log.Printf("board: %s: re-sending sampling interval after reboot: %v", label, err)
		}

		s.mu.Lock()
		cb := s.onReboot
		s.mu.Unlock()
		if cb != nil {
			cb()
		}
	}()
}

// SetOnReboot registers fn to be invoked, asynchronously and off the read
// loop, whenever the session detects the board rebooted mid-session (see
// applyMessage/triggerReboot). fn is expected to redo per-device
// reconfiguration the board forgot on reboot (pin modes, reporting, safe
// values) — it is safe for fn to call back into the session (e.g.
// SetPinMode).
//
// Safe to call once, right after Open/newSession returns and before the
// session is used by anything else; it must not be called concurrently
// with in-flight reads (i.e. don't call this from a second goroutine while
// the session may already be processing incoming messages for it).
func (s *Session) SetOnReboot(fn func()) {
	s.mu.Lock()
	s.onReboot = fn
	s.mu.Unlock()
}

func (s *Session) markDead(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dead {
		s.dead = true
		s.deadErr = err
	}
}

// checkAlive returns ErrSessionDead (wrapping the underlying cause) if the
// read loop has already detected the board is gone.
func (s *Session) checkAlive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return fmt.Errorf("%w: %v", ErrSessionDead, s.deadErr)
	}
	return nil
}

// write serializes b onto the transport. A write error marks the session
// dead, same as a read error.
func (s *Session) write(b []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.transport.Write(b); err != nil {
		s.markDead(err)
		return fmt.Errorf("%w: %v", ErrSessionDead, err)
	}
	return nil
}

// Alive reports whether the session is still usable (no read/write error
// has been observed yet).
func (s *Session) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.dead
}

// Err returns the error that killed the session, or nil if it's still
// alive.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadErr
}

// Close closes the underlying transport and waits for the read loop to
// exit. Safe to call more than once.
func (s *Session) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		closeErr = s.transport.Close()
		<-s.done
	})
	return closeErr
}

// Capabilities returns the board's per-pin capabilities as reported by the
// Capability Query / Analog Mapping Query handshake steps, ordered by
// Firmata pin number.
func (s *Session) Capabilities() []PinCapability {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PinCapability, len(s.capabilities))
	copy(out, s.capabilities)
	return out
}

// Firmware returns the board's reported firmware name and version, as
// captured from the Report Firmware handshake step.
func (s *Session) Firmware() (name string, major, minor int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firmwareName, s.firmwareMajor, s.firmwareMinor
}

// ProtocolVersion returns the board's reported Firmata protocol version.
func (s *Session) ProtocolVersion() (major, minor int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protocolMajor, s.protocolMinor
}

func (s *Session) pinCapability(pin int) (PinCapability, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pc, ok := s.pinIndex[pin]
	return pc, ok
}

// SetPinMode switches pin to mode (per docs/05-serial-protocol.md's Set
// Pin Mode message) and, for the modes pinctl actually drives, enables the
// matching unprompted reporting: Analog Reporting for ModeAnalog, Digital
// Port Reporting for ModeInput/ModePullup (per the doc's "Analog
// reporting"/"Digital reporting" sections — reporting is enabled at
// mode-set time since v0.1 has no device-binding step to drive it from).
func (s *Session) SetPinMode(pin int, mode PinMode) error {
	if err := s.checkAlive(); err != nil {
		return err
	}
	pc, ok := s.pinCapability(pin)
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownPin, pin)
	}
	if !pc.SupportsMode(mode) {
		return fmt.Errorf("%w: pin %d, mode %v", ErrUnsupportedMode, pin, mode)
	}
	if err := s.write(EncodeSetPinMode(pin, mode)); err != nil {
		return err
	}
	switch mode {
	case ModeAnalog:
		if pc.HasAnalogChannel {
			if err := s.write(EncodeReportAnalog(pc.AnalogChannel, true)); err != nil {
				return err
			}
		}
	case ModeInput, ModePullup:
		if err := s.write(EncodeReportDigitalPort(pin/8, true)); err != nil {
			return err
		}
	}
	return nil
}

// DigitalWrite sets a single digital output pin high or low (Set Digital
// Pin Value, 0xF5 — see the deviation note on Session).
func (s *Session) DigitalWrite(pin int, high bool) error {
	if err := s.checkAlive(); err != nil {
		return err
	}
	if _, ok := s.pinCapability(pin); !ok {
		return fmt.Errorf("%w: %d", ErrUnknownPin, pin)
	}
	if err := s.write(EncodeDigitalWrite(pin, high)); err != nil {
		return err
	}
	s.mu.Lock()
	s.digital[pin] = high
	s.mu.Unlock()
	return nil
}

// AnalogWrite sets a PWM duty cycle (0-255) on a PWM-capable pin, via
// Extended Analog (see docs/05-serial-protocol.md's "PWM write" row).
func (s *Session) AnalogWrite(pin int, value byte) error {
	if err := s.checkAlive(); err != nil {
		return err
	}
	if _, ok := s.pinCapability(pin); !ok {
		return fmt.Errorf("%w: %d", ErrUnknownPin, pin)
	}
	return s.write(EncodeAnalogWrite(pin, value))
}

// DigitalRead returns the last known value of a digital pin, from the
// session's cache. It does not perform a synchronous read against the
// board — Firmata has no such operation; see
// docs/05-serial-protocol.md#session-lifecycle step 5.
func (s *Session) DigitalRead(pin int) (bool, error) {
	if err := s.checkAlive(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.digital[pin]
	if !ok {
		return false, fmt.Errorf("%w: pin %d (enable input reporting via SetPinMode first)", ErrNoCachedValue, pin)
	}
	return v, nil
}

// AnalogRead returns the last known raw value (0-16383, though AVR ADCs
// only ever report 0-1023) of an analog channel, from the session's
// cache.
func (s *Session) AnalogRead(channel int) (int, error) {
	if err := s.checkAlive(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.analog[channel]
	if !ok {
		return 0, fmt.Errorf("%w: analog channel %d (enable analog reporting via SetPinMode first)", ErrNoCachedValue, channel)
	}
	return v, nil
}
