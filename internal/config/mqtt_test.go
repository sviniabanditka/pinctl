package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_MQTTDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := cfg.MQTT
	if m.Enabled {
		t.Errorf("Enabled = true, want false")
	}
	if m.ClientID != "pinctl" {
		t.Errorf("ClientID = %q, want pinctl", m.ClientID)
	}
	if m.BaseTopic != "pinctl" {
		t.Errorf("BaseTopic = %q, want pinctl", m.BaseTopic)
	}
	if m.DiscoveryPrefix != "homeassistant" {
		t.Errorf("DiscoveryPrefix = %q, want homeassistant", m.DiscoveryPrefix)
	}
	if m.AnalogDeadband != 4 {
		t.Errorf("AnalogDeadband = %d, want 4", m.AnalogDeadband)
	}
	if time.Duration(m.MinPublishInterval) != time.Second {
		t.Errorf("MinPublishInterval = %v, want 1s", time.Duration(m.MinPublishInterval))
	}
	if time.Duration(m.MaxPublishInterval) != 60*time.Second {
		t.Errorf("MaxPublishInterval = %v, want 60s", time.Duration(m.MaxPublishInterval))
	}
}

func TestLoad_MQTTParsesFullExample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlDoc := `
mqtt:
  enabled: true
  broker: tcp://192.168.1.10:1883
  client_id: pinctl-test
  username: pinctl
  password: hunter2
  base_topic: pt
  discovery_prefix: ha
  analog_deadband: 8
  min_publish_interval: 2s
  max_publish_interval: 90s
`
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := cfg.MQTT
	if !m.Enabled {
		t.Error("Enabled = false, want true")
	}
	if m.Broker != "tcp://192.168.1.10:1883" {
		t.Errorf("Broker = %q", m.Broker)
	}
	if m.ClientID != "pinctl-test" {
		t.Errorf("ClientID = %q", m.ClientID)
	}
	if string(m.Password) != "hunter2" {
		t.Errorf("Password = %q, want hunter2", string(m.Password))
	}
	if m.BaseTopic != "pt" || m.DiscoveryPrefix != "ha" {
		t.Errorf("BaseTopic/DiscoveryPrefix = %q/%q", m.BaseTopic, m.DiscoveryPrefix)
	}
	if m.AnalogDeadband != 8 {
		t.Errorf("AnalogDeadband = %d, want 8", m.AnalogDeadband)
	}
	if time.Duration(m.MinPublishInterval) != 2*time.Second {
		t.Errorf("MinPublishInterval = %v, want 2s", time.Duration(m.MinPublishInterval))
	}
	if time.Duration(m.MaxPublishInterval) != 90*time.Second {
		t.Errorf("MaxPublishInterval = %v, want 90s", time.Duration(m.MaxPublishInterval))
	}
}

func TestLoad_MQTTValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name:    "enabled without broker fails",
			yaml:    "mqtt:\n  enabled: true\n",
			wantErr: true,
		},
		{
			name:    "enabled with unsupported scheme fails",
			yaml:    "mqtt:\n  enabled: true\n  broker: http://broker:1883\n",
			wantErr: true,
		},
		{
			name:    "enabled with tcp scheme is fine",
			yaml:    "mqtt:\n  enabled: true\n  broker: tcp://broker:1883\n",
			wantErr: false,
		},
		{
			name:    "enabled with tls scheme is fine",
			yaml:    "mqtt:\n  enabled: true\n  broker: tls://broker:8883\n",
			wantErr: false,
		},
		{
			name:    "enabled with ws scheme is fine",
			yaml:    "mqtt:\n  enabled: true\n  broker: ws://broker:9001\n",
			wantErr: false,
		},
		{
			name:    "disabled without broker is fine",
			yaml:    "mqtt:\n  enabled: false\n",
			wantErr: false,
		},
		{
			name:    "negative analog_deadband fails",
			yaml:    "mqtt:\n  analog_deadband: -1\n",
			wantErr: true,
		},
		{
			name:    "password and password_file together fail",
			yaml:    "mqtt:\n  password: abc\n  password_file: /nonexistent\n",
			wantErr: true,
		},
		{
			name:    "malformed duration fails",
			yaml:    "mqtt:\n  min_publish_interval: not-a-duration\n",
			wantErr: true,
		},
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

func TestLoad_MQTTPasswordFile(t *testing.T) {
	dir := t.TempDir()
	pwPath := filepath.Join(dir, "mqtt-password")
	if err := os.WriteFile(pwPath, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	doc := fmt.Sprintf("mqtt:\n  password_file: %s\n", pwPath)
	if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(cfg.MQTT.Password) != "s3cret" {
		t.Errorf("Password = %q, want s3cret (trailing newline trimmed)", string(cfg.MQTT.Password))
	}
}

func TestLoad_MQTTPasswordFileWorldReadableFails(t *testing.T) {
	dir := t.TempDir()
	pwPath := filepath.Join(dir, "mqtt-password")
	if err := os.WriteFile(pwPath, []byte("s3cret\n"), 0o644); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	doc := fmt.Sprintf("mqtt:\n  password_file: %s\n", pwPath)
	if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load: expected an error for a world-readable password_file")
	}
}

func TestLoad_MQTTPasswordFromEnv(t *testing.T) {
	t.Setenv("MQTT_PASSWORD", "env-secret")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("mqtt:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(cfg.MQTT.Password) != "env-secret" {
		t.Errorf("Password = %q, want env-secret", string(cfg.MQTT.Password))
	}
}

func TestLoad_MQTTPasswordExplicitBeatsEnv(t *testing.T) {
	t.Setenv("MQTT_PASSWORD", "env-secret")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("mqtt:\n  password: explicit-secret\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(cfg.MQTT.Password) != "explicit-secret" {
		t.Errorf("Password = %q, want explicit-secret", string(cfg.MQTT.Password))
	}
}

func TestLoad_MQTTDiscoveryPrefixEmptyIsRespected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("mqtt:\n  discovery_prefix: \"\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MQTT.DiscoveryPrefix != "" {
		t.Errorf("DiscoveryPrefix = %q, want empty (explicitly disabled)", cfg.MQTT.DiscoveryPrefix)
	}
}

// TestConfig_PasswordNeverAppearsInFormattedOutput asserts docs/15-mqtt-
// bridge.md's "The password is never logged, never echoed in an error, and
// never leaves the process" for every fmt verb someone might reach for when
// debugging: %v, %+v, %#v, and %s on Config, MQTT, and Secret directly.
func TestConfig_PasswordNeverAppearsInFormattedOutput(t *testing.T) {
	const secret = "super-secret-password-xyz"
	cfg := Default()
	cfg.MQTT.Password = Secret(secret)
	cfg.MQTT.Enabled = true
	cfg.MQTT.Broker = "tcp://broker:1883"

	outputs := []string{
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%v", cfg.MQTT),
		fmt.Sprintf("%+v", cfg.MQTT),
		fmt.Sprintf("%#v", cfg.MQTT),
		fmt.Sprintf("%v", cfg.MQTT.Password),
		fmt.Sprintf("%s", cfg.MQTT.Password),
		fmt.Sprintf("%q", cfg.MQTT.Password),
		cfg.MQTT.Password.String(),
	}
	for _, out := range outputs {
		if strings.Contains(out, secret) {
			t.Errorf("formatted output leaked the password: %q", out)
		}
	}
}
