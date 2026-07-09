package board

// This file holds the encode side of the codec: pure functions turning a
// request into the bytes to write to the wire. No I/O happens here — the
// caller (Session) is responsible for actually writing the returned bytes.

// encode7Bit splits a value into Firmata's little-endian 7-bit-per-byte
// encoding (LSB byte, MSB byte), the form used throughout Firmata for any
// value that must keep its high bit clear (status bytes have the high bit
// set, so no data byte ever may).
func encode7Bit(v int) (lsb, msb byte) {
	return byte(v & 0x7F), byte((v >> 7) & 0x7F)
}

// decode7Bit is the inverse of encode7Bit.
func decode7Bit(lsb, msb byte) int {
	return int(lsb&0x7F) | int(msb&0x7F)<<7
}

func sysex(cmd byte, body ...byte) []byte {
	msg := make([]byte, 0, len(body)+3)
	msg = append(msg, startSysex, cmd)
	msg = append(msg, body...)
	msg = append(msg, endSysex)
	return msg
}

// EncodeReportVersionQuery builds the single-byte Report Version query
// (0xF9): the board answers with a protocol version report.
func EncodeReportVersionQuery() []byte {
	return []byte{reportVersion}
}

// EncodeReportFirmwareQuery builds the Report Firmware query (0xF0 0x79 0xF7).
func EncodeReportFirmwareQuery() []byte {
	return sysex(sysexReportFirmware)
}

// EncodeCapabilityQuery builds the Capability Query (0xF0 0x6B 0xF7).
func EncodeCapabilityQuery() []byte {
	return sysex(sysexCapabilityQuery)
}

// EncodeAnalogMappingQuery builds the Analog Mapping Query (0xF0 0x69 0xF7).
func EncodeAnalogMappingQuery() []byte {
	return sysex(sysexAnalogMappingQuery)
}

// EncodeSamplingInterval builds the Sampling Interval sysex, ms in [0, 16383].
func EncodeSamplingInterval(ms int) []byte {
	lsb, msb := encode7Bit(ms)
	return sysex(sysexSamplingInterval, lsb, msb)
}

// EncodeSetPinMode builds the Set Pin Mode message (0xF4 <pin> <mode>).
func EncodeSetPinMode(pin int, mode PinMode) []byte {
	return []byte{setPinMode, byte(pin), byte(mode)}
}

// EncodeDigitalWrite builds a single-pin digital write via Set Digital Pin
// Value (0xF5 <pin> <0/1>). See docs/05-serial-protocol.md's subset table:
// 0xF5 is documented as "used for pins outside a full port write" and is
// what Session.DigitalWrite uses for its single-pin writes; see the
// package doc comment on Session for why the full port-write form
// (0x90+port) is not used here.
func EncodeDigitalWrite(pin int, high bool) []byte {
	v := byte(0)
	if high {
		v = 1
	}
	return []byte{setDigitalPinValue, byte(pin), v}
}

// EncodeDigitalPortWrite builds a full 8-pin port write (0x90+port <lsb>
// <msb>). Provided for completeness/tests; Session does not use this for
// DigitalWrite (see EncodeDigitalWrite).
func EncodeDigitalPortWrite(port int, value uint16) []byte {
	lsb, msb := encode7Bit(int(value))
	return []byte{digitalMessage | byte(port), lsb, msb}
}

// EncodeAnalogWrite builds an Extended Analog write (0xF0 0x6F <pin>
// <value 7-bit chunks> 0xF7), used for PWM per docs/05-serial-protocol.md's
// subset table ("PWM write" row).
func EncodeAnalogWrite(pin int, value byte) []byte {
	lsb, msb := encode7Bit(int(value))
	return sysex(sysexExtendedAnalog, byte(pin), lsb, msb)
}

// EncodeReportAnalog builds the Analog Reporting toggle (0xC0+channel <0/1>).
func EncodeReportAnalog(channel int, enable bool) []byte {
	v := byte(0)
	if enable {
		v = 1
	}
	return []byte{analogReport | byte(channel), v}
}

// EncodeReportDigitalPort builds the Digital Port Reporting toggle
// (0xD0+port <0/1>).
func EncodeReportDigitalPort(port int, enable bool) []byte {
	v := byte(0)
	if enable {
		v = 1
	}
	return []byte{digitalPortReport | byte(port), v}
}
