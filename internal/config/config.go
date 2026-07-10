// Package config loads pinctl's config.yaml, per docs/14-deployment.md's
// "Configuration" section: HTTP listen address, data directory, serial
// allowlist, and log level/format. A missing config file is not an error —
// Load falls back to documented defaults, matching a fresh dev checkout
// with no /etc/pinctl or ~/.pinctl/config.yaml yet.
package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the decoded shape of config.yaml (docs/14-deployment.md).
type Config struct {
	HTTP HTTPConfig `yaml:"http"`
	// DataDir is the root directory for boards.json/devices.json/pipelines
	// (docs/09-data-model.md), FR-PERSIST-1.
	DataDir string       `yaml:"data_dir"`
	Serial  SerialConfig `yaml:"serial"`
	// Network is config.yaml's network: section (docs/17-network-boards.md
	// "Configuration") — explicitly configured Firmata-over-TCP board
	// addresses. Absent/empty Boards means no network boards at all.
	Network Network   `yaml:"network"`
	Log     LogConfig `yaml:"log"`
	MQTT    MQTT      `yaml:"mqtt"`
	History History   `yaml:"history"`
}

// HTTPConfig is config.yaml's http: section.
type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

// SerialConfig is config.yaml's serial: section (FR-BOARD-4).
type SerialConfig struct {
	Allowlist []string `yaml:"allowlist"`
	Baud      int      `yaml:"baud"`
}

// LogConfig is config.yaml's log: section. Only Level is currently
// consulted (via log.Printf regardless of Format, per this milestone's
// scope); Format is parsed and carried through for GET /api/v1/system and
// forward-compatibility with a structured logger later.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// Secret is a string that must never appear in a log line, an error
// string, or the formatted output of any type that holds it — used for
// MQTT.Password (docs/15-mqtt-bridge.md "Configuration": "The password is
// never logged, never echoed in an error, and never leaves the process.").
// String and GoString both redact the value, which is what fmt's %v/%s/%q
// and %+v/%#v verbs call into for any value (including a struct field)
// that implements fmt.Stringer/fmt.GoStringer — so printing a Config, an
// MQTT, or a Secret directly all redact. Only an explicit string(secret)
// conversion exposes the real value; that happens exactly once, when the
// paho adapter hands it to the MQTT client library.
type Secret string

// String implements fmt.Stringer.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "REDACTED"
}

// GoString implements fmt.GoStringer, covering %#v.
func (s Secret) GoString() string { return s.String() }

// Duration is a time.Duration that unmarshals from a YAML string like "1s"
// or "60s" (time.ParseDuration syntax), per docs/15-mqtt-bridge.md's
// min_publish_interval/max_publish_interval.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("config: mqtt: invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// MQTT is config.yaml's mqtt: section (docs/15-mqtt-bridge.md
// "Configuration"). The bridge is off by default: with Enabled false, no
// client is constructed and no goroutine runs.
type MQTT struct {
	Enabled bool `yaml:"enabled"`
	// Broker is the connection URL, e.g. tcp://192.168.1.10:1883. Required,
	// and must have a supported scheme (tcp/tls/ssl/ws/wss), when Enabled.
	Broker   string `yaml:"broker"`
	ClientID string `yaml:"client_id"`
	Username string `yaml:"username"`
	// Password and PasswordFile are mutually exclusive. If neither is set,
	// the MQTT_PASSWORD environment variable is used. PasswordFile is read
	// once at Load, trimmed of a trailing newline, and must not be
	// group/other readable. After Load returns, Password holds whichever
	// of the three sources resolved (if any); PasswordFile is left as
	// originally configured for reference, but is no longer consulted.
	Password     Secret `yaml:"password"`
	PasswordFile string `yaml:"password_file"`
	BaseTopic    string `yaml:"base_topic"`
	// DiscoveryPrefix set to "" disables Home Assistant discovery entirely
	// while state/commands still work — a deliberate value, not "unset".
	DiscoveryPrefix string `yaml:"discovery_prefix"`
	// AnalogDeadband is the minimum raw ADC-count change (>= 0) that
	// triggers a state publish for an analog-in device.
	AnalogDeadband     int      `yaml:"analog_deadband"`
	MinPublishInterval Duration `yaml:"min_publish_interval"`
	MaxPublishInterval Duration `yaml:"max_publish_interval"`
}

// validate checks MQTT's fields for internal consistency and, when
// Enabled, that a usable broker URL was given. It assumes Password has
// already been resolved (password_file/env applied) by resolvePassword.
func (m MQTT) validate() error {
	if m.AnalogDeadband < 0 {
		return fmt.Errorf("config: mqtt: analog_deadband must be >= 0")
	}
	if !m.Enabled {
		return nil
	}
	if m.Broker == "" {
		return fmt.Errorf("config: mqtt: broker is required when mqtt.enabled is true")
	}
	u, err := url.Parse(m.Broker)
	if err != nil {
		return fmt.Errorf("config: mqtt: parsing broker url: %w", err)
	}
	switch u.Scheme {
	case "tcp", "tls", "ssl", "ws", "wss":
	default:
		return fmt.Errorf("config: mqtt: broker scheme %q is not supported (want one of tcp, tls, ssl, ws, wss)", u.Scheme)
	}
	return nil
}

// Network is config.yaml's network: section (docs/17-network-boards.md
// "Configuration"). An empty/absent Boards list means no network boards,
// no dialling, no goroutine — the same "disabled means absent" shape as
// MQTT/History.
type Network struct {
	// Boards is the set of explicitly configured Firmata-over-TCP
	// addresses, each "tcp://host:port" (docs/17's "Addresses"). There is
	// no discovery/scanning: only addresses listed here are ever dialled.
	Boards []string `yaml:"boards"`
	// DialTimeout bounds establishing the TCP connection. Default 5s.
	DialTimeout Duration `yaml:"dial_timeout"`
	// ReadTimeout is the read deadline refreshed on every successful read;
	// a board silent for this long is treated as gone (docs/17
	// "Liveness"). Default 10s.
	ReadTimeout Duration `yaml:"read_timeout"`
	// Keepalive is the TCP keepalive idle period. Default 15s.
	Keepalive Duration `yaml:"keepalive"`
}

// validate checks Network's fields: every address must parse as
// tcp://host:port with a non-empty host and a valid port, and every
// timeout must be positive — read_timeout additionally must be at least
// 1s (docs/17-network-boards.md "Configuration"). read_timeout > dial_timeout
// is not required.
func (n Network) validate() error {
	if time.Duration(n.DialTimeout) <= 0 {
		return fmt.Errorf("config: network: dial_timeout must be > 0")
	}
	if time.Duration(n.ReadTimeout) < time.Second {
		return fmt.Errorf("config: network: read_timeout must be >= 1s")
	}
	if time.Duration(n.Keepalive) <= 0 {
		return fmt.Errorf("config: network: keepalive must be > 0")
	}
	for _, addr := range n.Boards {
		if err := validateNetworkAddr(addr); err != nil {
			return fmt.Errorf("config: network: boards: %w", err)
		}
	}
	return nil
}

// validateNetworkAddr checks addr parses as tcp://host:port with a
// non-empty host and a valid (1-65535) port.
func validateNetworkAddr(addr string) error {
	u, err := url.Parse(addr)
	if err != nil {
		return fmt.Errorf("%q: %w", addr, err)
	}
	if u.Scheme != "tcp" {
		return fmt.Errorf("%q: scheme must be tcp:// (got %q)", addr, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%q: missing host", addr)
	}
	portStr := u.Port()
	if portStr == "" {
		return fmt.Errorf("%q: missing port", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%q: invalid port %q", addr, portStr)
	}
	return nil
}

// History is config.yaml's history: section (docs/16-history.md
// "Configuration"). History is on by default: enabled false is what
// operators set to keep an SD card completely untouched by pinctl's own
// writes.
type History struct {
	Enabled bool `yaml:"enabled"`
	// Retention is how long a sample lives before the hourly (and
	// startup) retention pass deletes it.
	Retention Duration `yaml:"retention"`
	// Deadband is the minimum raw-counts change (>= 0) that counts as
	// movement for analog-in devices; booleans ignore it. Shared shape
	// with MQTT.AnalogDeadband, but expressed as float64 to match
	// internal/sampling.Policy.Deadband directly.
	Deadband float64 `yaml:"deadband"`
	// MinInterval is the floor between two recorded samples for one
	// device, unless MaxInterval's heartbeat overrides it.
	MinInterval Duration `yaml:"min_interval"`
	// MaxInterval is the heartbeat ceiling: a sample is recorded even
	// with no movement once this much time has passed.
	MaxInterval Duration `yaml:"max_interval"`
	// FlushInterval is the batching window: buffered samples are written
	// in one transaction at most this often (sooner if the buffer fills).
	FlushInterval Duration `yaml:"flush_interval"`
}

// validate checks History's fields for internal consistency.
func (h History) validate() error {
	if time.Duration(h.Retention) < 0 {
		return fmt.Errorf("config: history: retention must be >= 0")
	}
	if h.Deadband < 0 {
		return fmt.Errorf("config: history: deadband must be >= 0")
	}
	if time.Duration(h.MinInterval) < 0 {
		return fmt.Errorf("config: history: min_interval must be >= 0")
	}
	if time.Duration(h.MaxInterval) < 0 {
		return fmt.Errorf("config: history: max_interval must be >= 0")
	}
	if time.Duration(h.FlushInterval) < 0 {
		return fmt.Errorf("config: history: flush_interval must be >= 0")
	}
	if time.Duration(h.MinInterval) > time.Duration(h.MaxInterval) {
		return fmt.Errorf("config: history: min_interval must be <= max_interval")
	}
	return nil
}

// resolvePassword fills in m.Password from password_file or the
// MQTT_PASSWORD environment variable, per docs/15-mqtt-bridge.md
// "Configuration". password and password_file are mutually exclusive;
// password_file must not be group/other readable.
func resolvePassword(m *MQTT) error {
	if m.Password != "" && m.PasswordFile != "" {
		return fmt.Errorf("config: mqtt: password and password_file are mutually exclusive")
	}

	if m.PasswordFile != "" {
		info, err := os.Stat(m.PasswordFile)
		if err != nil {
			return fmt.Errorf("config: mqtt: reading password_file: %w", err)
		}
		if info.Mode().Perm()&0o044 != 0 {
			return fmt.Errorf("config: mqtt: password_file %s must not be group- or other-readable (mode %s)", m.PasswordFile, info.Mode().Perm())
		}
		data, err := os.ReadFile(m.PasswordFile)
		if err != nil {
			return fmt.Errorf("config: mqtt: reading password_file: %w", err)
		}
		m.Password = Secret(strings.TrimRight(string(data), "\r\n"))
		return nil
	}

	if m.Password == "" {
		if env := os.Getenv("MQTT_PASSWORD"); env != "" {
			m.Password = Secret(env)
		}
	}
	return nil
}

// Default returns the documented defaults (docs/14-deployment.md), used
// both as Load's starting point (so a partial config.yaml only overrides
// what it specifies) and when no config file is found at all.
func Default() Config {
	return Config{
		HTTP:    HTTPConfig{Addr: ":8080"},
		DataDir: defaultDataDir(),
		Serial:  SerialConfig{Allowlist: defaultSerialAllowlist(), Baud: 57600},
		Network: defaultNetwork(),
		Log:     LogConfig{Level: "info", Format: "text"},
		MQTT:    defaultMQTT(),
		History: defaultHistory(),
	}
}

// defaultSerialAllowlist is what an omitted `serial.allowlist` means: the
// paths USB-attached boards actually appear at on Linux. It deliberately
// does not include every serial device on the machine — a Bluetooth adapter
// or a debug console is not a board. An explicitly empty list disables
// serial discovery; `"*"` opts back in to any port.
func defaultSerialAllowlist() []string {
	return []string{"/dev/ttyACM*", "/dev/ttyUSB*"}
}

// defaultHistory returns docs/16-history.md's documented history: defaults.
func defaultHistory() History {
	return History{
		Enabled:       true,
		Retention:     Duration(168 * time.Hour),
		Deadband:      8,
		MinInterval:   Duration(5 * time.Second),
		MaxInterval:   Duration(5 * time.Minute),
		FlushInterval: Duration(5 * time.Second),
	}
}

// defaultNetwork returns docs/17-network-boards.md's documented network:
// defaults (an empty Boards list — no network boards unless configured).
func defaultNetwork() Network {
	return Network{
		DialTimeout: Duration(5 * time.Second),
		ReadTimeout: Duration(10 * time.Second),
		Keepalive:   Duration(15 * time.Second),
	}
}

// defaultMQTT returns docs/15-mqtt-bridge.md's documented mqtt: defaults.
func defaultMQTT() MQTT {
	return MQTT{
		Enabled:            false,
		ClientID:           "pinctl",
		BaseTopic:          "pinctl",
		DiscoveryPrefix:    "homeassistant",
		AnalogDeadband:     4,
		MinPublishInterval: Duration(time.Second),
		MaxPublishInterval: Duration(60 * time.Second),
	}
}

// defaultDataDir is docs/09-data-model.md's dev default (~/.pinctl/);
// production configs set data_dir: /var/lib/pinctl explicitly.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pinctl"
	}
	return filepath.Join(home, ".pinctl")
}

// DefaultPaths returns the config file search order used when Load is
// called with an empty path: /etc/pinctl/config.yaml first (the systemd
// deployment location, docs/14-deployment.md), then ~/.pinctl/config.yaml
// (the dev-mode location).
func DefaultPaths() []string {
	paths := []string{"/etc/pinctl/config.yaml"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".pinctl", "config.yaml"))
	}
	return paths
}

// Load reads path (if non-empty), or the first existing file among
// DefaultPaths() (if path is empty), and decodes it over Default() —
// fields absent from the YAML keep their default value. Unknown fields are
// a hard error (docs/14-deployment.md: "unknown fields are a hard load
// error — strict decoding"). A missing file — whether path was explicit or
// every default path is absent — is not an error; Load returns Default()
// unchanged, per this milestone's "-config path... missing file = defaults"
// behavior (a stricter "explicit path must exist" policy is left to a
// later version if operators want it).
func Load(path string) (Config, error) {
	cfg := Default()

	candidates := DefaultPaths()
	if path != "" {
		candidates = []string{path}
	}

	var data []byte
	var found string
	for _, c := range candidates {
		b, err := os.ReadFile(c)
		if err == nil {
			data, found = b, c
			break
		}
		if !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("config: reading %s: %w", c, err)
		}
	}
	if data != nil {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("config: parsing %s: %w", found, err)
		}

		if cfg.HTTP.Addr == "" {
			cfg.HTTP.Addr = ":8080"
		}
		if cfg.DataDir == "" {
			cfg.DataDir = defaultDataDir()
		}
		if cfg.Serial.Baud == 0 {
			cfg.Serial.Baud = 57600
		}
		if cfg.Log.Level == "" {
			cfg.Log.Level = "info"
		}
		if cfg.Log.Format == "" {
			cfg.Log.Format = "text"
		}
	}

	if err := resolvePassword(&cfg.MQTT); err != nil {
		return Config{}, err
	}
	if err := cfg.MQTT.validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.History.validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Network.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
