package board

// Message is any decoded Firmata message the Parser can produce. It's a
// closed set (type switch in the session's read loop); external packages
// only ever receive these as opaque values.
type Message interface{ isMessage() }

// ProtocolVersionMsg is the unprompted 0xF9 report a board sends on boot.
type ProtocolVersionMsg struct {
	Major, Minor int
}

// FirmwareMsg is the decoded Report Firmware (0x79) sysex response.
type FirmwareMsg struct {
	Major, Minor int
	Name         string
}

// CapabilityResponseMsg is the decoded Capability Query response (0x6C).
type CapabilityResponseMsg struct {
	Pins []PinCapability
}

// AnalogMappingMsg is the decoded Analog Mapping Query response (0x6A).
// Keyed by Firmata pin number; only pins with analog capability are
// present.
type AnalogMappingMsg struct {
	PinToChannel map[int]int
}

// DigitalPortMsg is a Digital I/O Message (0x90+port): the 8-pin value of
// one port, sent unprompted when digital reporting is enabled for that
// port, or by the runtime to write it.
type DigitalPortMsg struct {
	Port  int
	Value uint16 // bit i = pin (port*8 + i)
}

// AnalogValueMsg is an Analog I/O Message (0xE0+channel): the board's
// unprompted analog value report for one channel.
type AnalogValueMsg struct {
	Channel int
	Value   int // 14-bit raw value (0-16383); AVR ADCs report 0-1023
}

func (ProtocolVersionMsg) isMessage()    {}
func (FirmwareMsg) isMessage()           {}
func (CapabilityResponseMsg) isMessage() {}
func (AnalogMappingMsg) isMessage()      {}
func (DigitalPortMsg) isMessage()        {}
func (AnalogValueMsg) isMessage()        {}
