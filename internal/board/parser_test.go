package board

import (
	"reflect"
	"testing"
)

// feedAll runs every byte of b through p and returns the messages produced,
// in order.
func feedAll(t *testing.T, p *Parser, b []byte) []Message {
	t.Helper()
	var out []Message
	for _, c := range b {
		msg, err := p.Feed(c)
		if err != nil {
			t.Fatalf("Feed(0x%02x): unexpected error: %v", c, err)
		}
		if msg != nil {
			out = append(out, msg)
		}
	}
	return out
}

func TestEncode7BitRoundTrip(t *testing.T) {
	cases := []int{0, 1, 127, 128, 1023, 16383}
	for _, v := range cases {
		lsb, msb := encode7Bit(v)
		if lsb&0x80 != 0 || msb&0x80 != 0 {
			t.Fatalf("encode7Bit(%d): high bit set in output byte(s): %02x %02x", v, lsb, msb)
		}
		got := decode7Bit(lsb, msb)
		if got != v {
			t.Errorf("decode7Bit(encode7Bit(%d)) = %d, want %d", v, got, v)
		}
	}
}

func TestParserProtocolVersion(t *testing.T) {
	p := NewParser()
	msgs := feedAll(t, p, []byte{reportVersion, 2, 5})
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	want := ProtocolVersionMsg{Major: 2, Minor: 5}
	if msgs[0] != want {
		t.Errorf("got %+v, want %+v", msgs[0], want)
	}
}

func TestParserDigitalMessage(t *testing.T) {
	p := NewParser()
	// port 1, value 0b0_0000101 (pins 8 and 10 high within the port)
	lsb, msb := encode7Bit(0b0000101)
	msgs := feedAll(t, p, []byte{digitalMessage | 1, lsb, msb})
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	want := DigitalPortMsg{Port: 1, Value: 0b0000101}
	if msgs[0] != want {
		t.Errorf("got %+v, want %+v", msgs[0], want)
	}
}

func TestParserAnalogMessage(t *testing.T) {
	p := NewParser()
	lsb, msb := encode7Bit(742)
	msgs := feedAll(t, p, []byte{analogMessage | 3, lsb, msb})
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	want := AnalogValueMsg{Channel: 3, Value: 742}
	if msgs[0] != want {
		t.Errorf("got %+v, want %+v", msgs[0], want)
	}
}

func TestParserFirmwareResponse(t *testing.T) {
	p := NewParser()
	body := []byte{sysexReportFirmware, 2, 5}
	for _, r := range "StandardFirmata" {
		l, m := encode7Bit(int(r))
		body = append(body, l, m)
	}
	frame := append([]byte{startSysex}, body...)
	frame = append(frame, endSysex)

	msgs := feedAll(t, p, frame)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	want := FirmwareMsg{Major: 2, Minor: 5, Name: "StandardFirmata"}
	if msgs[0] != want {
		t.Errorf("got %+v, want %+v", msgs[0], want)
	}
}

func TestParserCapabilityResponse(t *testing.T) {
	p := NewParser()
	// Pin 0: digital input + output (resolution 1 each).
	// Pin 1: PWM (resolution 8) in addition to digital I/O.
	body := []byte{
		sysexCapabilityResponse,
		byte(ModeInput), 1, byte(ModeOutput), 1, modeCapabilityEnd,
		byte(ModeInput), 1, byte(ModeOutput), 1, byte(ModePWM), 8, modeCapabilityEnd,
	}
	frame := append([]byte{startSysex}, body...)
	frame = append(frame, endSysex)

	msgs := feedAll(t, p, frame)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	got, ok := msgs[0].(CapabilityResponseMsg)
	if !ok {
		t.Fatalf("got %T, want CapabilityResponseMsg", msgs[0])
	}
	want := []PinCapability{
		{Pin: 0, Modes: map[PinMode]int{ModeInput: 1, ModeOutput: 1}},
		{Pin: 1, Modes: map[PinMode]int{ModeInput: 1, ModeOutput: 1, ModePWM: 8}},
	}
	if !reflect.DeepEqual(got.Pins, want) {
		t.Errorf("got %+v, want %+v", got.Pins, want)
	}
}

func TestParserAnalogMappingResponse(t *testing.T) {
	p := NewParser()
	// Pins 0-13 non-analog, pin 14 (A0) -> channel 0, pin 15 (A1) -> channel 1.
	body := []byte{sysexAnalogMappingResponse}
	for i := 0; i < 14; i++ {
		body = append(body, modeCapabilityEnd)
	}
	body = append(body, 0, 1)
	frame := append([]byte{startSysex}, body...)
	frame = append(frame, endSysex)

	msgs := feedAll(t, p, frame)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	got, ok := msgs[0].(AnalogMappingMsg)
	if !ok {
		t.Fatalf("got %T, want AnalogMappingMsg", msgs[0])
	}
	want := map[int]int{14: 0, 15: 1}
	if !reflect.DeepEqual(got.PinToChannel, want) {
		t.Errorf("got %+v, want %+v", got.PinToChannel, want)
	}
}

func TestParserResyncsOnUnrecognizedStatusByte(t *testing.T) {
	p := NewParser()
	// 0xF6 isn't a status byte pinctl recognizes; it should be discarded
	// and parsing should resume cleanly on the next valid frame.
	msgs := feedAll(t, p, []byte{0xF6, reportVersion, 2, 5})
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	want := ProtocolVersionMsg{Major: 2, Minor: 5}
	if msgs[0] != want {
		t.Errorf("got %+v, want %+v", msgs[0], want)
	}
}

func TestParserUnterminatedSysexIsDiscarded(t *testing.T) {
	p := NewParser()
	// Feed a huge unterminated sysex body; the parser must bound its
	// buffer and resync rather than growing forever or panicking.
	var sawErr bool
	if _, err := p.Feed(startSysex); err != nil {
		t.Fatalf("unexpected error starting sysex: %v", err)
	}
	for i := 0; i < maxSysexBody+10; i++ {
		_, err := p.Feed(0x01)
		if err != nil {
			sawErr = true
			break
		}
	}
	if !sawErr {
		t.Fatal("expected an error once the sysex body exceeded the bound")
	}
	// Parser should be back in sync afterwards.
	msgs := feedAll(t, p, []byte{reportVersion, 2, 5})
	if len(msgs) != 1 || msgs[0] != (ProtocolVersionMsg{Major: 2, Minor: 5}) {
		t.Fatalf("parser did not resync after discarding oversized sysex: %+v", msgs)
	}
}

func TestEncodeDecodeMessageRoundTrips(t *testing.T) {
	p := NewParser()

	t.Run("set pin mode is not a message pinctl decodes (write-only)", func(t *testing.T) {
		// Sanity: EncodeSetPinMode's bytes are never fed back through
		// the parser in production (it's a write-only command); just
		// confirm the encoding shape here.
		got := EncodeSetPinMode(13, ModeOutput)
		want := []byte{setPinMode, 13, byte(ModeOutput)}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("EncodeSetPinMode = % x, want % x", got, want)
		}
	})

	t.Run("digital write round trips through the port message form", func(t *testing.T) {
		frame := EncodeDigitalPortWrite(1, 0b0000101)
		msgs := feedAll(t, p, frame)
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		want := DigitalPortMsg{Port: 1, Value: 0b0000101}
		if msgs[0] != want {
			t.Errorf("got %+v, want %+v", msgs[0], want)
		}
	})
}
