// Package board implements a client for the Firmata subset described in
// docs/05-serial-protocol.md: enough of the wire protocol to open a serial
// port to a StandardFirmata-flashed Arduino Uno/Nano, perform the handshake,
// and read/write pin values in steady state. It intentionally implements
// only the message subset pinctl needs (see the doc's "Firmata subset used"
// table) — not a general-purpose Firmata library.
package board

// Firmata status bytes (single-byte commands, or the base of a
// port/channel-encoded command family).
const (
	digitalMessage     byte = 0x90 // 0x90-0x9F: digital I/O message, port in low nibble
	analogMessage      byte = 0xE0 // 0xE0-0xEF: analog I/O message, channel in low nibble
	digitalPortReport  byte = 0xD0 // 0xD0-0xDF: enable/disable digital port reporting
	analogReport       byte = 0xC0 // 0xC0-0xCF: enable/disable analog channel reporting
	setPinMode         byte = 0xF4
	setDigitalPinValue byte = 0xF5
	reportVersion      byte = 0xF9
	startSysex         byte = 0xF0
	endSysex           byte = 0xF7
)

// Sysex sub-command bytes (the first data byte inside a 0xF0 ... 0xF7 frame).
const (
	sysexAnalogMappingQuery    byte = 0x69
	sysexAnalogMappingResponse byte = 0x6A
	sysexCapabilityQuery       byte = 0x6B
	sysexCapabilityResponse    byte = 0x6C
	sysexExtendedAnalog        byte = 0x6F
	sysexServoConfig           byte = 0x70
	sysexSamplingInterval      byte = 0x7A
	sysexReportFirmware        byte = 0x79
)

// PinMode is a Firmata pin mode value, as used in Set Pin Mode (0xF4) and
// reported per-pin by the Capability Query response.
type PinMode byte

// Pin modes pinctl cares about. Firmata defines more (SHIFT, I2C, ONEWIRE,
// STEPPER, ENCODER, SERIAL) but none of those map to a v0.x device role
// (see docs/00-design-brief.md's domain model) so they are decoded (for
// capability reporting) but never set by pinctl. ModeServo is the
// exception: it backs the "servo" device role (docs/09-data-model.md) and
// is set by Session.SetPinMode/ServoConfig/ServoWrite.
const (
	ModeInput   PinMode = 0x00
	ModeOutput  PinMode = 0x01
	ModeAnalog  PinMode = 0x02
	ModePWM     PinMode = 0x03
	ModeServo   PinMode = 0x04
	ModeShift   PinMode = 0x05
	ModeI2C     PinMode = 0x06
	ModeOneWire PinMode = 0x07
	ModeStepper PinMode = 0x08
	ModeEncoder PinMode = 0x09
	ModeSerial  PinMode = 0x0A
	ModePullup  PinMode = 0x0B

	// modeCapabilityEnd (0x7F) terminates a pin's mode list in the
	// Capability Response and marks a non-analog pin in the Analog
	// Mapping Response.
	modeCapabilityEnd byte = 0x7F
)

// PinCapability describes what a single Firmata pin supports, as reported
// by the Capability Query / Analog Mapping Query handshake steps.
type PinCapability struct {
	Pin int // Firmata pin number (D0=0 ... D13=13, A0=14 ...)

	// Modes maps each mode the pin supports to its resolution in bits
	// (e.g. 8 for a PWM pin, 10 for an AVR ADC's ANALOG mode).
	Modes map[PinMode]int

	// AnalogChannel is the analog channel number this pin maps to (e.g.
	// A0 -> channel 0), and HasAnalogChannel reports whether the pin
	// appeared in the Analog Mapping Response at all (non-analog pins
	// don't).
	AnalogChannel    int
	HasAnalogChannel bool
}

// SupportsMode reports whether the pin's capability list includes mode.
func (c PinCapability) SupportsMode(mode PinMode) bool {
	_, ok := c.Modes[mode]
	return ok
}
