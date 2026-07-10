package mqtt

import (
	"encoding/json"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// haComponent maps a device role to the Home Assistant MQTT discovery
// component, per docs/15-mqtt-bridge.md "Home Assistant discovery".
func haComponent(role model.DeviceRole) (component string, ok bool) {
	switch role {
	case model.RoleAnalogIn:
		return "sensor", true
	case model.RoleDigitalIn:
		return "binary_sensor", true
	case model.RoleDigitalOut:
		return "switch", true
	case model.RolePWMOut, model.RoleServo:
		return "number", true
	default:
		return "", false
	}
}

// haAvailability is one entry of a discovery config's "availability" list.
type haAvailability struct {
	Topic string `json:"topic"`
}

// haDevice is the shared "device" block grouping a board's pins under one
// HA device, per docs/15's example payload.
type haDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model,omitempty"`
	SWVersion    string   `json:"sw_version"`
}

// haConfig is a discovery config message body. Fields are ordered/tagged
// to match docs/15's example as closely as JSON field order allows (Go's
// encoding/json doesn't preserve struct field order in a meaningful way to
// HA, which parses it as a map, so this is for human readability only).
type haConfig struct {
	Name              string           `json:"name"`
	UniqueID          string           `json:"unique_id"`
	StateTopic        string           `json:"state_topic"`
	CommandTopic      string           `json:"command_topic,omitempty"`
	AvailabilityMode  string           `json:"availability_mode"`
	Availability      []haAvailability `json:"availability"`
	StateClass        string           `json:"state_class,omitempty"`
	Min               *int             `json:"min,omitempty"`
	Max               *int             `json:"max,omitempty"`
	Step              *int             `json:"step,omitempty"`
	UnitOfMeasurement string           `json:"unit_of_measurement,omitempty"`
	Mode              string           `json:"mode,omitempty"`
	Device            haDevice         `json:"device"`
}

// buildDiscoveryConfig renders d's discovery config payload. board is d's
// owning board (for the shared device block); firmware/firmwareOK come
// from BoardStatus.Firmware and populate device.model when known.
func buildDiscoveryConfig(cfg discoveryInput) ([]byte, error) {
	component, ok := haComponent(cfg.Device.Role)
	if !ok {
		return nil, errUnsupportedRole
	}

	c := haConfig{
		Name:             cfg.Device.Name,
		UniqueID:         "pinctl_" + cfg.Device.ID,
		StateTopic:       stateTopic(cfg.BaseTopic, cfg.Device.ID),
		AvailabilityMode: "all",
		Availability: []haAvailability{
			{Topic: statusTopic(cfg.BaseTopic)},
			{Topic: availableTopic(cfg.BaseTopic, cfg.Device.ID)},
		},
		Device: haDevice{
			Identifiers:  []string{"pinctl_" + cfg.Device.BoardID},
			Name:         cfg.BoardName,
			Manufacturer: "pinctl",
			Model:        cfg.Firmware,
			SWVersion:    cfg.Version,
		},
	}

	switch component {
	case "sensor":
		c.StateClass = "measurement"
	case "switch", "number":
		c.CommandTopic = setTopic(cfg.BaseTopic, cfg.Device.ID)
	}
	if component == "number" {
		min, max := 0, 255
		if cfg.Device.Role == model.RoleServo {
			max = 180
			step := 1
			c.Step = &step
			c.UnitOfMeasurement = "°"
		}
		c.Min, c.Max = &min, &max
		c.Mode = "slider"
	}

	return json.Marshal(c)
}

// discoveryInput bundles what buildDiscoveryConfig needs about a device
// and its board.
type discoveryInput struct {
	Device    model.Device
	BoardName string
	Firmware  string // "" if unknown
	BaseTopic string
	Version   string
}
