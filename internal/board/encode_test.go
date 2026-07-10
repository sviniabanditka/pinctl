package board

import (
	"reflect"
	"testing"
)

// TestEncodeServoConfig checks the exact byte layout of the Servo Config
// sysex (0xF0 0x70 <pin> <min lsb> <min msb> <max lsb> <max msb> 0xF7), per
// docs/05-serial-protocol.md's Servo subsection, against hand-computed
// bytes.
func TestEncodeServoConfig(t *testing.T) {
	tests := []struct {
		name       string
		pin        int
		minPulseUs int
		maxPulseUs int
		want       []byte
	}{
		{
			name:       "typical hobby servo range",
			pin:        9,
			minPulseUs: 544,
			maxPulseUs: 2400,
			// 544 = 4*128 + 32 -> lsb 0x20, msb 0x04
			// 2400 = 18*128 + 96 -> lsb 0x60, msb 0x12
			want: []byte{startSysex, sysexServoConfig, 9, 0x20, 0x04, 0x60, 0x12, endSysex},
		},
		{
			name:       "zero pulse widths",
			pin:        2,
			minPulseUs: 0,
			maxPulseUs: 0,
			want:       []byte{startSysex, sysexServoConfig, 2, 0x00, 0x00, 0x00, 0x00, endSysex},
		},
		{
			name:       "max 14-bit pulse width",
			pin:        13,
			minPulseUs: 16383,
			maxPulseUs: 16383,
			want:       []byte{startSysex, sysexServoConfig, 13, 0x7F, 0x7F, 0x7F, 0x7F, endSysex},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeServoConfig(tc.pin, tc.minPulseUs, tc.maxPulseUs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EncodeServoConfig(%d, %d, %d) = % x, want % x",
					tc.pin, tc.minPulseUs, tc.maxPulseUs, got, tc.want)
			}
		})
	}
}
