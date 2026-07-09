package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sviniabanditka/pinctl/internal/board"
)

type firmwareInfo struct {
	Name  string `json:"name"`
	Major int    `json:"major"`
	Minor int    `json:"minor"`
}

type pinCapabilityInfo struct {
	Pin   int      `json:"pin"`
	Modes []string `json:"modes"`
}

type boardResponse struct {
	Connected       bool                `json:"connected"`
	Port            string              `json:"port"`
	Firmware        firmwareInfo        `json:"firmware"`
	ProtocolVersion string              `json:"protocolVersion"`
	Capabilities    []pinCapabilityInfo `json:"capabilities"`
}

func handleGetBoard(sess Session, port string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, major, minor := sess.Firmware()
		protoMajor, protoMinor := sess.ProtocolVersion()

		caps := sess.Capabilities()
		capsOut := make([]pinCapabilityInfo, len(caps))
		for i, pc := range caps {
			capsOut[i] = pinCapabilityInfo{Pin: pc.Pin, Modes: modeStringsFor(pc)}
		}

		writeJSON(w, http.StatusOK, boardResponse{
			Connected:       sess.Alive(),
			Port:            port,
			Firmware:        firmwareInfo{Name: name, Major: major, Minor: minor},
			ProtocolVersion: strconv.Itoa(protoMajor) + "." + strconv.Itoa(protoMinor),
			Capabilities:    capsOut,
		})
	}
}

type setModeRequest struct {
	Mode string `json:"mode"`
}

func handleSetPinMode(sess Session) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pin, ok := pinFromPath(r)
		if !ok {
			writeBadRequest(w, "invalid pin in path")
			return
		}

		var req setModeRequest
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeBadRequest(w, "invalid JSON body")
			return
		}
		mode, ok := modeNames[req.Mode]
		if !ok {
			writeBadRequest(w, "unknown mode: "+req.Mode)
			return
		}

		if err := sess.SetPinMode(pin, mode); err != nil {
			writeBoardError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type digitalReadResponse struct {
	Pin   int  `json:"pin"`
	Value bool `json:"value"`
}

type analogReadResponse struct {
	Channel int `json:"channel"`
	Value   int `json:"value"`
}

// handleReadPin serves GET /api/v1/pins/{pin}. With ?analog=true, {pin} is
// looked up in the board's reported capabilities to find its analog
// channel, and the response is an analog channel reading; otherwise it's a
// plain digital pin reading.
func handleReadPin(sess Session) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pin, ok := pinFromPath(r)
		if !ok {
			writeBadRequest(w, "invalid pin in path")
			return
		}

		if r.URL.Query().Get("analog") == "true" {
			pc, found := capabilityFor(sess, pin)
			if !found || !pc.HasAnalogChannel {
				writeError(w, http.StatusNotFound, "unknown_pin", "pin has no analog channel")
				return
			}
			value, err := sess.AnalogRead(pc.AnalogChannel)
			if err != nil {
				writeBoardError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, analogReadResponse{Channel: pc.AnalogChannel, Value: value})
			return
		}

		value, err := sess.DigitalRead(pin)
		if err != nil {
			writeBoardError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, digitalReadResponse{Pin: pin, Value: value})
	}
}

func capabilityFor(sess Session, pin int) (board.PinCapability, bool) {
	for _, pc := range sess.Capabilities() {
		if pc.Pin == pin {
			return pc, true
		}
	}
	return board.PinCapability{}, false
}

// writePinRequest covers both the digital and PWM write bodies: {"value":
// true|false} for a digital write, {"value": 0-255, "pwm": true} for PWM.
type writePinRequest struct {
	Value json.RawMessage `json:"value"`
	PWM   bool            `json:"pwm"`
}

func handleWritePin(sess Session) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pin, ok := pinFromPath(r)
		if !ok {
			writeBadRequest(w, "invalid pin in path")
			return
		}

		var req writePinRequest
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeBadRequest(w, "invalid JSON body")
			return
		}

		if req.PWM {
			var n int
			if err := json.Unmarshal(req.Value, &n); err != nil || n < 0 || n > 255 {
				writeBadRequest(w, "value must be an integer 0-255 for a pwm write")
				return
			}
			if err := sess.AnalogWrite(pin, byte(n)); err != nil {
				writeBoardError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var high bool
		if err := json.Unmarshal(req.Value, &high); err != nil {
			writeBadRequest(w, "value must be a bool for a digital write")
			return
		}
		if err := sess.DigitalWrite(pin, high); err != nil {
			writeBoardError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
