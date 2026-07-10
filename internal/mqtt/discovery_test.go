package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/model"
)

func TestHAComponent(t *testing.T) {
	tests := []struct {
		role model.DeviceRole
		want string
	}{
		{model.RoleAnalogIn, "sensor"},
		{model.RoleDigitalIn, "binary_sensor"},
		{model.RoleDigitalOut, "switch"},
		{model.RolePWMOut, "number"},
		{model.RoleServo, "number"},
	}
	for _, tt := range tests {
		got, ok := haComponent(tt.role)
		if !ok || got != tt.want {
			t.Errorf("haComponent(%s) = %q, %v; want %q, true", tt.role, got, ok, tt.want)
		}
	}
}

func TestBuildDiscoveryConfig(t *testing.T) {
	d := model.Device{ID: "dev_9zq3v7ab4c", Name: "LDR on A0", BoardID: "brd_4kx9m2pqrs", Role: model.RoleAnalogIn}
	payload, err := buildDiscoveryConfig(discoveryInput{
		Device:    d,
		BoardName: "Arduino Uno #1",
		Firmware:  "StandardFirmata 2.5",
		BaseTopic: "pinctl",
		Version:   "v0.5.0",
	})
	if err != nil {
		t.Fatalf("buildDiscoveryConfig: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if got["unique_id"] != "pinctl_dev_9zq3v7ab4c" {
		t.Errorf("unique_id = %v", got["unique_id"])
	}
	if got["state_topic"] != "pinctl/dev_9zq3v7ab4c/state" {
		t.Errorf("state_topic = %v", got["state_topic"])
	}
	if got["availability_mode"] != "all" {
		t.Errorf("availability_mode = %v", got["availability_mode"])
	}
	avail, ok := got["availability"].([]any)
	if !ok || len(avail) != 2 {
		t.Fatalf("availability = %v", got["availability"])
	}
	a0 := avail[0].(map[string]any)
	a1 := avail[1].(map[string]any)
	if a0["topic"] != "pinctl/status" || a1["topic"] != "pinctl/dev_9zq3v7ab4c/available" {
		t.Errorf("availability topics = %v, %v", a0, a1)
	}
	if got["state_class"] != "measurement" {
		t.Errorf("state_class = %v, want measurement for analog-in", got["state_class"])
	}

	dev, ok := got["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", got["device"])
	}
	ids, ok := dev["identifiers"].([]any)
	if !ok || len(ids) != 1 || ids[0] != "pinctl_brd_4kx9m2pqrs" {
		t.Errorf("device.identifiers = %v", dev["identifiers"])
	}
	if dev["name"] != "Arduino Uno #1" {
		t.Errorf("device.name = %v", dev["name"])
	}
	if dev["manufacturer"] != "pinctl" {
		t.Errorf("device.manufacturer = %v", dev["manufacturer"])
	}
	if dev["model"] != "StandardFirmata 2.5" {
		t.Errorf("device.model = %v", dev["model"])
	}
	if dev["sw_version"] != "v0.5.0" {
		t.Errorf("device.sw_version = %v", dev["sw_version"])
	}
}

func TestBuildDiscoveryConfig_NumberHasMinMaxMode(t *testing.T) {
	d := model.Device{ID: "dev_pwm", Name: "LED", BoardID: "brd_1", Role: model.RolePWMOut}
	payload, err := buildDiscoveryConfig(discoveryInput{Device: d, BaseTopic: "pinctl"})
	if err != nil {
		t.Fatalf("buildDiscoveryConfig: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["min"] != float64(0) || got["max"] != float64(255) {
		t.Errorf("min/max = %v/%v, want 0/255", got["min"], got["max"])
	}
	if got["mode"] != "slider" {
		t.Errorf("mode = %v, want slider", got["mode"])
	}
	if got["command_topic"] != "pinctl/dev_pwm/set" {
		t.Errorf("command_topic = %v", got["command_topic"])
	}
}

func TestBuildDiscoveryConfig_ServoHasMinMaxStepUnit(t *testing.T) {
	d := model.Device{ID: "dev_servo", Name: "Pan servo", BoardID: "brd_1", Role: model.RoleServo}
	payload, err := buildDiscoveryConfig(discoveryInput{Device: d, BaseTopic: "pinctl"})
	if err != nil {
		t.Fatalf("buildDiscoveryConfig: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["min"] != float64(0) || got["max"] != float64(180) {
		t.Errorf("min/max = %v/%v, want 0/180", got["min"], got["max"])
	}
	if got["step"] != float64(1) {
		t.Errorf("step = %v, want 1", got["step"])
	}
	if got["unit_of_measurement"] != "°" {
		t.Errorf("unit_of_measurement = %v, want °", got["unit_of_measurement"])
	}
	if got["mode"] != "slider" {
		t.Errorf("mode = %v, want slider", got["mode"])
	}
	if got["command_topic"] != "pinctl/dev_servo/set" {
		t.Errorf("command_topic = %v", got["command_topic"])
	}
}

func TestBuildDiscoveryConfig_PWMHasNoUnit(t *testing.T) {
	d := model.Device{ID: "dev_pwm2", Name: "LED", BoardID: "brd_1", Role: model.RolePWMOut}
	payload, err := buildDiscoveryConfig(discoveryInput{Device: d, BaseTopic: "pinctl"})
	if err != nil {
		t.Fatalf("buildDiscoveryConfig: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, has := got["unit_of_measurement"]; has {
		t.Errorf("pwm-out should not have a unit_of_measurement, got %v", got["unit_of_measurement"])
	}
}

func TestBuildDiscoveryConfig_SwitchHasCommandTopicNoStateClass(t *testing.T) {
	d := model.Device{ID: "dev_sw", Name: "Relay", BoardID: "brd_1", Role: model.RoleDigitalOut}
	payload, err := buildDiscoveryConfig(discoveryInput{Device: d, BaseTopic: "pinctl"})
	if err != nil {
		t.Fatalf("buildDiscoveryConfig: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["command_topic"] != "pinctl/dev_sw/set" {
		t.Errorf("command_topic = %v", got["command_topic"])
	}
	if _, has := got["state_class"]; has {
		t.Error("switch should not have state_class")
	}
}

// ---- Bridge-level discovery behavior ----

func TestBridge_DiscoveryOnePerDeviceCorrectComponent(t *testing.T) {
	h := newHarness(t, config.MQTT{DiscoveryPrefix: "homeassistant"})
	h.store.putBoard(model.Board{ID: "brd_1", Name: "Board 1"})
	h.store.putDevice(testDevice("dev_a", "brd_1", 0, model.RoleAnalogIn))
	h.store.putDevice(testDevice("dev_b", "brd_1", 1, model.RoleDigitalIn))
	h.store.putDevice(testDevice("dev_c", "brd_1", 2, model.RoleDigitalOut))
	h.store.putDevice(testDevice("dev_d", "brd_1", 3, model.RolePWMOut))

	h.bridge.onConnect(h.client)

	wantTopics := map[string]string{
		"dev_a": discoveryTopic("homeassistant", "sensor", "dev_a"),
		"dev_b": discoveryTopic("homeassistant", "binary_sensor", "dev_b"),
		"dev_c": discoveryTopic("homeassistant", "switch", "dev_c"),
		"dev_d": discoveryTopic("homeassistant", "number", "dev_d"),
	}
	for id, topic := range wantTopics {
		if _, ok := h.client.find(topic); !ok {
			t.Errorf("no discovery config published for %s at %s", id, topic)
		}
	}
}

func TestBridge_DiscoveryEmptyPrefixPublishesNone(t *testing.T) {
	h := newHarness(t, config.MQTT{DiscoveryPrefix: ""})
	h.store.putDevice(testDevice("dev_a", "brd_1", 0, model.RoleAnalogIn))

	h.bridge.onConnect(h.client)

	for _, m := range h.client.snapshot() {
		if len(m.topic) >= 14 && m.topic[:14] == "homeassistant/" {
			t.Errorf("unexpected discovery publish with prefix disabled: %s", m.topic)
		}
	}
}

func TestBridge_DevicesChangedDeletePublishesEmptyRetained(t *testing.T) {
	h := newHarness(t, config.MQTT{DiscoveryPrefix: "homeassistant"})
	d := testDevice("dev_del", "brd_1", 0, model.RoleAnalogIn)
	h.store.putDevice(d)
	h.bridge.onConnect(h.client) // establishes the initial known-device cache

	h.store.deleteDevice("dev_del")
	h.bridge.DevicesChanged()

	topic := discoveryTopic("homeassistant", "sensor", "dev_del")
	msg, ok := h.client.find(topic)
	if !ok {
		t.Fatalf("expected an empty retained payload published to %s on delete", topic)
	}
	if len(msg.payload) != 0 {
		t.Errorf("delete payload = %q, want empty", msg.payload)
	}
	if !msg.retained {
		t.Error("delete payload should be retained")
	}
}

func TestBridge_DevicesChangedGroupsByBoard(t *testing.T) {
	h := newHarness(t, config.MQTT{DiscoveryPrefix: "homeassistant"})
	h.store.putBoard(model.Board{ID: "brd_1", Name: "Board One"})
	h.store.putDevice(testDevice("dev_x", "brd_1", 0, model.RoleAnalogIn))
	h.store.putDevice(testDevice("dev_y", "brd_1", 1, model.RoleDigitalIn))
	h.bridge.onConnect(h.client)

	for _, id := range []string{"dev_x", "dev_y"} {
		topic := discoveryTopic("homeassistant", componentFor(t, id), id)
		msg, ok := h.client.find(topic)
		if !ok {
			t.Fatalf("no discovery for %s", id)
		}
		var cfg map[string]any
		if err := json.Unmarshal(msg.payload, &cfg); err != nil {
			t.Fatalf("unmarshal %s: %v", id, err)
		}
		dev := cfg["device"].(map[string]any)
		ids := dev["identifiers"].([]any)
		if ids[0] != "pinctl_brd_1" {
			t.Errorf("%s device.identifiers = %v, want pinctl_brd_1", id, ids)
		}
	}
}

func componentFor(t *testing.T, id string) string {
	t.Helper()
	switch id {
	case "dev_x":
		return "sensor"
	case "dev_y":
		return "binary_sensor"
	default:
		t.Fatalf("unknown test device %s", id)
		return ""
	}
}
