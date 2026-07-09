package mqtt

import (
	"testing"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

func TestEncodeState(t *testing.T) {
	tests := []struct {
		role model.DeviceRole
		v    engine.Value
		want string
	}{
		{model.RoleDigitalIn, engine.BoolValue(true), "ON"},
		{model.RoleDigitalIn, engine.BoolValue(false), "OFF"},
		{model.RoleDigitalOut, engine.BoolValue(true), "ON"},
		{model.RoleAnalogIn, engine.FloatValue(512), "512"},
		{model.RolePWMOut, engine.FloatValue(200), "200"},
	}
	for _, tt := range tests {
		if got := string(encodeState(tt.role, tt.v)); got != tt.want {
			t.Errorf("encodeState(%s, %v) = %q, want %q", tt.role, tt.v, got, tt.want)
		}
	}
}

func TestDecodeCommand(t *testing.T) {
	tests := []struct {
		name    string
		role    model.DeviceRole
		payload string
		wantErr bool
		wantVal engine.Value
	}{
		{"digital-out ON", model.RoleDigitalOut, "ON", false, engine.BoolValue(true)},
		{"digital-out OFF", model.RoleDigitalOut, "OFF", false, engine.BoolValue(false)},
		{"digital-out garbage", model.RoleDigitalOut, "TOGGLE", true, engine.Value{}},
		{"pwm-out in range", model.RolePWMOut, "200", false, engine.FloatValue(200)},
		{"pwm-out boundary 255", model.RolePWMOut, "255", false, engine.FloatValue(255)},
		{"pwm-out boundary 0", model.RolePWMOut, "0", false, engine.FloatValue(0)},
		{"pwm-out out of range", model.RolePWMOut, "300", true, engine.Value{}},
		{"pwm-out negative", model.RolePWMOut, "-1", true, engine.Value{}},
		{"pwm-out non-numeric", model.RolePWMOut, "abc", true, engine.Value{}},
		{"analog-in rejected", model.RoleAnalogIn, "ON", true, engine.Value{}},
		{"digital-in rejected", model.RoleDigitalIn, "ON", true, engine.Value{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := decodeCommand(tt.role, []byte(tt.payload))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("decodeCommand(%s, %q): expected an error, got nil", tt.role, tt.payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeCommand(%s, %q): unexpected error: %v", tt.role, tt.payload, err)
			}
			if !v.Equal(tt.wantVal) {
				t.Errorf("decodeCommand(%s, %q) = %v, want %v", tt.role, tt.payload, v, tt.wantVal)
			}
		})
	}
}
