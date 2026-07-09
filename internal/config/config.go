// Package config loads pinctl's config.yaml, per docs/14-deployment.md's
// "Configuration" section: HTTP listen address, data directory, serial
// allowlist, and log level/format. A missing config file is not an error —
// Load falls back to documented defaults, matching a fresh dev checkout
// with no /etc/pinctl or ~/.pinctl/config.yaml yet.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the decoded shape of config.yaml (docs/14-deployment.md).
type Config struct {
	HTTP HTTPConfig `yaml:"http"`
	// DataDir is the root directory for boards.json/devices.json/pipelines
	// (docs/09-data-model.md), FR-PERSIST-1.
	DataDir string       `yaml:"data_dir"`
	Serial  SerialConfig `yaml:"serial"`
	Log     LogConfig    `yaml:"log"`
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

// Default returns the documented defaults (docs/14-deployment.md), used
// both as Load's starting point (so a partial config.yaml only overrides
// what it specifies) and when no config file is found at all.
func Default() Config {
	return Config{
		HTTP:    HTTPConfig{Addr: ":8080"},
		DataDir: defaultDataDir(),
		Serial:  SerialConfig{Baud: 57600},
		Log:     LogConfig{Level: "info", Format: "text"},
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
	if data == nil {
		return cfg, nil
	}

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
	return cfg, nil
}
