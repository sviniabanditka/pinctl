package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := Default()
	if cfg.HTTP.Addr != def.HTTP.Addr {
		t.Errorf("HTTP.Addr = %q, want default %q", cfg.HTTP.Addr, def.HTTP.Addr)
	}
	if cfg.Serial.Baud != 57600 {
		t.Errorf("Serial.Baud = %d, want 57600", cfg.Serial.Baud)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want info", cfg.Log.Level)
	}
}

func TestLoad_ParsesFullExample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
http:
  addr: "192.168.1.10:8080"
data_dir: /var/lib/pinctl
serial:
  allowlist:
    - /dev/ttyACM0
    - /dev/ttyUSB0
  baud: 57600
log:
  level: warn
  format: json
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != "192.168.1.10:8080" {
		t.Errorf("HTTP.Addr = %q", cfg.HTTP.Addr)
	}
	if cfg.DataDir != "/var/lib/pinctl" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if len(cfg.Serial.Allowlist) != 2 || cfg.Serial.Allowlist[0] != "/dev/ttyACM0" {
		t.Errorf("Serial.Allowlist = %v", cfg.Serial.Allowlist)
	}
	if cfg.Log.Level != "warn" || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v", cfg.Log)
	}
}

func TestLoad_PartialFileKeepsDefaultsForOmittedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("HTTP.Addr = %q, want default :8080 to survive a partial file", cfg.HTTP.Addr)
	}
}

func TestLoad_UnknownFieldIsHardError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("bogus_field: 1\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an unknown field (strict decoding)")
	}
}

func TestLoad_EmptyPathUsesDefaultSearchOrder(t *testing.T) {
	// Neither /etc/pinctl/config.yaml nor ~/.pinctl/config.yaml is expected
	// to exist in the test environment, so this should fall back to
	// defaults without error.
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if cfg.Serial.Baud != 57600 {
		t.Errorf("Serial.Baud = %d, want 57600", cfg.Serial.Baud)
	}
}
