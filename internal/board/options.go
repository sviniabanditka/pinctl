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

// WithDialTimeout overrides how long Open waits to establish a TCP
// connection for a network board address (tcp://host:port). No effect on
// a serial address. Default 5s, matching docs/17-network-boards.md's
// network.dial_timeout.
func WithDialTimeout(d time.Duration) Option {
	return func(s *Session) { s.dialTimeout = d }
}

// WithReadTimeout overrides the read deadline refreshed on every
// successful read from a network board's socket (docs/17-network-boards.md
// "Liveness": a board silent for this long is treated as gone). No effect
// on a serial address. Default 10s. Also determines the heartbeat interval
// (readTimeout/2, see docs/17's heartbeat paragraph).
func WithReadTimeout(d time.Duration) Option {
	return func(s *Session) { s.readTimeout = d }
}

// WithKeepalive overrides the TCP keepalive idle period set on a network
// board's connection. No effect on a serial address. Default 15s.
func WithKeepalive(d time.Duration) Option {
	return func(s *Session) { s.keepalive = d }
}
