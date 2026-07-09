package board

import "time"

// Option configures a Session at Open time.
type Option func(*Session)

// WithBootDelay overrides the delay a Session waits after opening the port
// before starting the handshake, letting the Uno's bootloader finish after
// the DTR-triggered reset (see docs/05-serial-protocol.md's "Board reset on
// serial open" row — default ~2s). Tests pass 0 so they never sleep.
func WithBootDelay(d time.Duration) Option {
	return func(s *Session) { s.bootDelay = d }
}

// WithHandshakeTimeout overrides how long Open waits for each expected
// handshake response (protocol version, firmware, capability, analog
// mapping) before failing the connect attempt.
func WithHandshakeTimeout(d time.Duration) Option {
	return func(s *Session) { s.handshakeTimeout = d }
}

// WithSamplingIntervalMS overrides the Sampling Interval sent once at
// handshake time (default 20ms, matching the pipeline engine's default
// tick — see docs/05-serial-protocol.md's Analog reporting section).
func WithSamplingIntervalMS(ms int) Option {
	return func(s *Session) { s.samplingIntervalMS = ms }
}
