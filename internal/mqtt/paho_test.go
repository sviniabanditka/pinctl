package mqtt

import (
	"testing"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/sviniabanditka/pinctl/internal/config"
)

// TestBuildClientOptions_WillAndReconnect asserts the paho adapter's
// connection options without dialing a real broker, per the task's
// "assert via the adapter's option builder if you factor it out" —
// buildClientOptions is factored out for exactly this reason.
func TestBuildClientOptions_WillAndReconnect(t *testing.T) {
	cfg := config.MQTT{
		Broker:    "tcp://broker:1883",
		ClientID:  "pinctl",
		BaseTopic: "pinctl",
		Username:  "pinctl",
		Password:  "hunter2",
	}
	opts := buildClientOptions(cfg, func(paho.Client) {})

	if !opts.WillEnabled {
		t.Fatal("WillEnabled = false, want true")
	}
	if opts.WillTopic != "pinctl/status" {
		t.Errorf("WillTopic = %q, want pinctl/status", opts.WillTopic)
	}
	if string(opts.WillPayload) != "offline" {
		t.Errorf("WillPayload = %q, want offline", opts.WillPayload)
	}
	if opts.WillQos != 1 {
		t.Errorf("WillQos = %d, want 1", opts.WillQos)
	}
	if !opts.WillRetained {
		t.Error("WillRetained = false, want true")
	}
	if !opts.AutoReconnect {
		t.Error("AutoReconnect = false, want true")
	}
	if !opts.ConnectRetry {
		t.Error("ConnectRetry = false, want true")
	}
	if opts.ClientID != "pinctl" {
		t.Errorf("ClientID = %q", opts.ClientID)
	}
	if opts.Username != "pinctl" {
		t.Errorf("Username = %q", opts.Username)
	}
	if opts.Password != "hunter2" {
		t.Errorf("Password not wired through to paho options")
	}
	if len(opts.Servers) != 1 || opts.Servers[0].String() != "tcp://broker:1883" {
		t.Errorf("Servers = %v", opts.Servers)
	}
}

func TestBuildClientOptions_NoCredentialsWhenUnset(t *testing.T) {
	cfg := config.MQTT{Broker: "tcp://broker:1883", ClientID: "pinctl", BaseTopic: "pinctl"}
	opts := buildClientOptions(cfg, func(paho.Client) {})
	if opts.Username != "" {
		t.Errorf("Username = %q, want empty", opts.Username)
	}
	if opts.Password != "" {
		t.Error("Password should be empty when unset")
	}
}

func TestPahoClient_ImplementsClient(t *testing.T) {
	var _ Client = (*pahoClient)(nil)
}
