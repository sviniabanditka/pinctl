package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_HistoryDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	h := cfg.History
	if !h.Enabled {
		t.Error("Enabled = false, want true")
	}
	if time.Duration(h.Retention) != 168*time.Hour {
		t.Errorf("Retention = %v, want 168h", time.Duration(h.Retention))
	}
	if h.Deadband != 8 {
		t.Errorf("Deadband = %v, want 8", h.Deadband)
	}
	if time.Duration(h.MinInterval) != 5*time.Second {
		t.Errorf("MinInterval = %v, want 5s", time.Duration(h.MinInterval))
	}
	if time.Duration(h.MaxInterval) != 5*time.Minute {
		t.Errorf("MaxInterval = %v, want 5m", time.Duration(h.MaxInterval))
	}
	if time.Duration(h.FlushInterval) != 5*time.Second {
		t.Errorf("FlushInterval = %v, want 5s", time.Duration(h.FlushInterval))
	}
}

func TestLoad_HistoryParsesFullExample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlDoc := `
history:
  enabled: false
  retention: 24h
  deadband: 2
  min_interval: 1s
  max_interval: 30s
  flush_interval: 2s
`
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	h := cfg.History
	if h.Enabled {
		t.Error("Enabled = true, want false")
	}
	if time.Duration(h.Retention) != 24*time.Hour {
		t.Errorf("Retention = %v, want 24h", time.Duration(h.Retention))
	}
	if h.Deadband != 2 {
		t.Errorf("Deadband = %v, want 2", h.Deadband)
	}
	if time.Duration(h.MinInterval) != time.Second {
		t.Errorf("MinInterval = %v, want 1s", time.Duration(h.MinInterval))
	}
	if time.Duration(h.MaxInterval) != 30*time.Second {
		t.Errorf("MaxInterval = %v, want 30s", time.Duration(h.MaxInterval))
	}
	if time.Duration(h.FlushInterval) != 2*time.Second {
		t.Errorf("FlushInterval = %v, want 2s", time.Duration(h.FlushInterval))
	}
}

func TestLoad_HistoryValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{name: "negative retention fails", yaml: "history:\n  retention: -1s\n", wantErr: true},
		{name: "negative deadband fails", yaml: "history:\n  deadband: -1\n", wantErr: true},
		{name: "negative min_interval fails", yaml: "history:\n  min_interval: -1s\n", wantErr: true},
		{name: "negative max_interval fails", yaml: "history:\n  max_interval: -1s\n", wantErr: true},
		{name: "negative flush_interval fails", yaml: "history:\n  flush_interval: -1s\n", wantErr: true},
		{
			name:    "min_interval greater than max_interval fails",
			yaml:    "history:\n  min_interval: 10s\n  max_interval: 5s\n",
			wantErr: true,
		},
		{
			name:    "min_interval equal to max_interval is fine",
			yaml:    "history:\n  min_interval: 5s\n  max_interval: 5s\n",
			wantErr: false,
		},
		{name: "malformed duration fails", yaml: "history:\n  retention: not-a-duration\n", wantErr: true},
		{name: "zero retention (keep forever) is fine", yaml: "history:\n  retention: 0s\n", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := Load(path)
			if tt.wantErr && err == nil {
				t.Fatal("Load: expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Load: unexpected error: %v", err)
			}
		})
	}
}

func TestLoad_HistoryUnknownFieldIsHardError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("history:\n  bogus_field: true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load: expected an error for an unknown history field")
	}
}
