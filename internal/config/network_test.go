package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_NetworkDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	n := cfg.Network
	if len(n.Boards) != 0 {
		t.Errorf("Boards = %v, want empty (no network boards by default)", n.Boards)
	}
	if time.Duration(n.DialTimeout) != 5*time.Second {
		t.Errorf("DialTimeout = %v, want 5s", time.Duration(n.DialTimeout))
	}
	if time.Duration(n.ReadTimeout) != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want 10s", time.Duration(n.ReadTimeout))
	}
	if time.Duration(n.Keepalive) != 15*time.Second {
		t.Errorf("Keepalive = %v, want 15s", time.Duration(n.Keepalive))
	}
}

func TestLoad_NetworkParsesFullExample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlDoc := `
network:
  boards:
    - tcp://esp32-porch.local:3030
    - tcp://192.168.1.51:3030
  dial_timeout: 2s
  read_timeout: 8s
  keepalive: 20s
`
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	n := cfg.Network
	if len(n.Boards) != 2 || n.Boards[0] != "tcp://esp32-porch.local:3030" || n.Boards[1] != "tcp://192.168.1.51:3030" {
		t.Errorf("Boards = %v", n.Boards)
	}
	if time.Duration(n.DialTimeout) != 2*time.Second {
		t.Errorf("DialTimeout = %v, want 2s", time.Duration(n.DialTimeout))
	}
	if time.Duration(n.ReadTimeout) != 8*time.Second {
		t.Errorf("ReadTimeout = %v, want 8s", time.Duration(n.ReadTimeout))
	}
	if time.Duration(n.Keepalive) != 20*time.Second {
		t.Errorf("Keepalive = %v, want 20s", time.Duration(n.Keepalive))
	}
}

func TestLoad_NetworkEmptyBoardsListMeansNoNetworkBoards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// network: present but boards omitted entirely — must not error and
	// must leave Boards empty (docs/17-network-boards.md: "An empty or
	// absent network.boards means no network boards, no dialling, no
	// goroutine").
	if err := os.WriteFile(path, []byte("network:\n  dial_timeout: 3s\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Network.Boards) != 0 {
		t.Errorf("Boards = %v, want empty", cfg.Network.Boards)
	}
	if time.Duration(cfg.Network.DialTimeout) != 3*time.Second {
		t.Errorf("DialTimeout = %v, want 3s (explicit override)", time.Duration(cfg.Network.DialTimeout))
	}
	// read_timeout/keepalive weren't set in the YAML: must fall back to
	// documented defaults rather than staying zero.
	if time.Duration(cfg.Network.ReadTimeout) != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want default 10s", time.Duration(cfg.Network.ReadTimeout))
	}
	if time.Duration(cfg.Network.Keepalive) != 15*time.Second {
		t.Errorf("Keepalive = %v, want default 15s", time.Duration(cfg.Network.Keepalive))
	}
}

func TestLoad_NetworkRejectsInvalidAddresses(t *testing.T) {
	cases := []struct {
		name string
		addr string
	}{
		{"missing scheme", "esp32-porch.local:3030"},
		{"wrong scheme", "udp://esp32-porch.local:3030"},
		{"missing port", "tcp://esp32-porch.local"},
		{"missing host", "tcp://:3030"},
		{"non-numeric port", "tcp://esp32-porch.local:notaport"},
		{"port out of range", "tcp://esp32-porch.local:70000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			yamlDoc := "network:\n  boards:\n    - \"" + c.addr + "\"\n"
			if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := Load(path); err == nil {
				t.Errorf("Load with boards: [%q] = nil error, want a validation error", c.addr)
			}
		})
	}
}

func TestLoad_NetworkAcceptsHostnameAndIPAddresses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlDoc := `
network:
  boards:
    - tcp://esp32-porch.local:3030
    - tcp://192.168.1.51:3030
`
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("Load: %v, want no error for valid hostname/IP addresses", err)
	}
}

func TestLoad_NetworkRejectsNonPositiveTimeouts(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"zero dial_timeout", "network:\n  dial_timeout: 0s\n"},
		{"read_timeout below 1s", "network:\n  read_timeout: 500ms\n"},
		{"zero keepalive", "network:\n  keepalive: 0s\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(c.yaml), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := Load(path); err == nil {
				t.Errorf("Load(%s) = nil error, want a validation error", c.name)
			}
		})
	}
}

func TestLoad_NetworkUnknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlDoc := "network:\n  bogus_field: true\n"
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load with unknown network field = nil error, want strict-decoding error")
	}
}
