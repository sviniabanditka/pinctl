package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_SerialSilenceTimeoutDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := time.Duration(cfg.Serial.SilenceTimeout); got != 10*time.Second {
		t.Errorf("SilenceTimeout = %v, want default 10s", got)
	}
}

// TestLoad_SerialSilenceTimeoutDefaultPreservedWhenSectionPartial covers the
// "strict decoding, defaults preserved when absent" contract
// network.read_timeout already follows: a serial: section present for one
// field must not zero out silence_timeout, which wasn't mentioned.
func TestLoad_SerialSilenceTimeoutDefaultPreservedWhenSectionPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("serial:\n  baud: 57600\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := time.Duration(cfg.Serial.SilenceTimeout); got != 10*time.Second {
		t.Errorf("SilenceTimeout = %v, want default 10s preserved", got)
	}
}

func TestLoad_SerialSilenceTimeoutExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("serial:\n  silence_timeout: 5s\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := time.Duration(cfg.Serial.SilenceTimeout); got != 5*time.Second {
		t.Errorf("SilenceTimeout = %v, want 5s", got)
	}
}

// TestLoad_SerialSilenceTimeoutZeroDisablesWatchdog covers the documented
// "0 disables the watchdog" escape hatch: an explicit 0 must survive
// Load unchanged, not get bumped back up to the 10s default the way an
// omitted field would.
func TestLoad_SerialSilenceTimeoutZeroDisablesWatchdog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("serial:\n  silence_timeout: 0s\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := time.Duration(cfg.Serial.SilenceTimeout); got != 0 {
		t.Errorf("SilenceTimeout = %v, want 0 (explicitly disabled, not defaulted)", got)
	}
}

func TestLoad_SerialSilenceTimeoutBelowMinimumRejected(t *testing.T) {
	cases := []string{"1s", "500ms", "1999ms"}
	for _, d := range cases {
		t.Run(d, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			yamlDoc := "serial:\n  silence_timeout: " + d + "\n"
			if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := Load(path); err == nil {
				t.Errorf("Load(silence_timeout: %s) = nil error, want a validation error (minimum 2s, or exactly 0 to disable)", d)
			}
		})
	}
}

func TestLoad_SerialSilenceTimeoutMinimumAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("serial:\n  silence_timeout: 2s\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("Load(silence_timeout: 2s) = %v, want no error (2s is the documented minimum)", err)
	}
}

func TestLoad_SerialUnknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("serial:\n  bogus_field: true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load with unknown serial field = nil error, want strict-decoding error")
	}
}
