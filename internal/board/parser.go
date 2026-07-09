package board

import (
	"fmt"
	"strings"
)

// maxSysexBody caps how many bytes a sysex frame can accumulate before the
// Parser gives up and resyncs. Firmata gives no length prefix, so a missing
// end-of-sysex byte (corruption, a dropped byte) would otherwise grow the
// buffer forever; see docs/05-serial-protocol.md's Error Handling table
// ("Malformed / unrecognized message").
const maxSysexBody = 4096

type parserState int

const (
	stateIdle parserState = iota
	stateData
	stateSysex
)

// Parser is a byte-at-a-time Firmata frame decoder. It holds no I/O of its
// own (per docs/13-testing.md, the wire codec is tested independently of
// any real or fake transport) — callers feed it bytes read from wherever
// they came from and get back a decoded Message once a full frame has been
// seen.
type Parser struct {
	state parserState

	cmd  byte
	need int
	data []byte

	sysexBuf []byte
}

// NewParser returns a Parser ready to decode a fresh byte stream.
func NewParser() *Parser {
	return &Parser{state: stateIdle}
}

// Feed processes one byte and returns the Message it completed, if any.
// Both return values nil/false-equivalent (msg == nil, err == nil) means
// "byte consumed, frame not complete yet." A byte that can't be part of any
// recognized frame is silently discarded (resync), per the doc's malformed-
// message handling — Feed never errors on unrecognized bytes, only on a
// malformed body of an otherwise-recognized sysex message.
func (p *Parser) Feed(b byte) (Message, error) {
	switch p.state {
	case stateIdle:
		return p.feedIdle(b)
	case stateData:
		return p.feedData(b)
	case stateSysex:
		return p.feedSysex(b)
	default:
		p.state = stateIdle
		return nil, nil
	}
}

func (p *Parser) feedIdle(b byte) (Message, error) {
	if b < 0x80 {
		// Stray data byte with no preceding status byte: discard.
		return nil, nil
	}
	switch {
	case b == startSysex:
		p.sysexBuf = p.sysexBuf[:0]
		p.state = stateSysex
	case b == reportVersion:
		p.beginFixed(b, 2)
	case b&0xF0 == digitalMessage, b&0xF0 == analogMessage:
		p.beginFixed(b, 2)
	default:
		// Unrecognized status byte (e.g. a command pinctl only ever
		// sends, never receives): discard and stay in sync.
	}
	return nil, nil
}

func (p *Parser) beginFixed(cmd byte, need int) {
	p.cmd = cmd
	p.need = need
	p.data = p.data[:0]
	p.state = stateData
}

func (p *Parser) feedData(b byte) (Message, error) {
	p.data = append(p.data, b)
	if len(p.data) < p.need {
		return nil, nil
	}
	p.state = stateIdle
	return p.finishFixed()
}

func (p *Parser) feedSysex(b byte) (Message, error) {
	if b == endSysex {
		p.state = stateIdle
		return p.finishSysex()
	}
	if len(p.sysexBuf) >= maxSysexBody {
		// Runaway/unterminated sysex frame: drop it and resync rather
		// than growing forever.
		p.state = stateIdle
		return nil, fmt.Errorf("firmata: sysex frame exceeded %d bytes without terminator, discarded", maxSysexBody)
	}
	p.sysexBuf = append(p.sysexBuf, b)
	return nil, nil
}

func (p *Parser) finishFixed() (Message, error) {
	switch {
	case p.cmd == reportVersion:
		return ProtocolVersionMsg{Major: int(p.data[0]), Minor: int(p.data[1])}, nil
	case p.cmd&0xF0 == digitalMessage:
		port := int(p.cmd & 0x0F)
		return DigitalPortMsg{Port: port, Value: uint16(decode7Bit(p.data[0], p.data[1]))}, nil
	case p.cmd&0xF0 == analogMessage:
		channel := int(p.cmd & 0x0F)
		return AnalogValueMsg{Channel: channel, Value: decode7Bit(p.data[0], p.data[1])}, nil
	}
	return nil, nil
}

func (p *Parser) finishSysex() (Message, error) {
	if len(p.sysexBuf) == 0 {
		return nil, nil
	}
	cmd := p.sysexBuf[0]
	body := p.sysexBuf[1:]
	switch cmd {
	case sysexReportFirmware:
		return decodeFirmware(body)
	case sysexCapabilityResponse:
		msg := decodeCapabilityResponse(body)
		return msg, nil
	case sysexAnalogMappingResponse:
		msg := decodeAnalogMapping(body)
		return msg, nil
	}
	// Sysex sub-command pinctl doesn't consume (e.g. an echo of a query
	// pinctl itself sent): ignore.
	return nil, nil
}

// decodeFirmware parses a Report Firmware response body (major, minor,
// then the firmware name as pairs of 7-bit bytes, one pair per ASCII
// character — Firmata's string encoding, used here and nowhere else in
// pinctl's subset).
func decodeFirmware(body []byte) (Message, error) {
	if len(body) < 2 {
		return nil, fmt.Errorf("firmata: report firmware response too short (%d bytes)", len(body))
	}
	major, minor := int(body[0]), int(body[1])
	name := body[2:]
	var sb strings.Builder
	for i := 0; i+1 < len(name); i += 2 {
		sb.WriteByte(byte(decode7Bit(name[i], name[i+1])))
	}
	return FirmwareMsg{Major: major, Minor: minor, Name: sb.String()}, nil
}

// decodeCapabilityResponse parses a Capability Query response body: for
// each pin, a run of (mode, resolution) byte pairs terminated by 0x7F,
// pins in Firmata pin-number order.
func decodeCapabilityResponse(body []byte) CapabilityResponseMsg {
	var pins []PinCapability
	pin := 0
	modes := map[PinMode]int{}
	for i := 0; i < len(body); {
		if body[i] == modeCapabilityEnd {
			pins = append(pins, PinCapability{Pin: pin, Modes: modes})
			pin++
			modes = map[PinMode]int{}
			i++
			continue
		}
		if i+1 >= len(body) {
			break // truncated pair, nothing more to do
		}
		modes[PinMode(body[i])] = int(body[i+1])
		i += 2
	}
	return CapabilityResponseMsg{Pins: pins}
}

// decodeAnalogMapping parses an Analog Mapping Query response body: one
// byte per Firmata pin, either the analog channel number or 0x7F for a
// non-analog pin.
func decodeAnalogMapping(body []byte) AnalogMappingMsg {
	m := map[int]int{}
	for pin, ch := range body {
		if ch != modeCapabilityEnd {
			m[pin] = int(ch)
		}
	}
	return AnalogMappingMsg{PinToChannel: m}
}
