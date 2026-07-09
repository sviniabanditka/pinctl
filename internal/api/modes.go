package api

import "github.com/sviniabanditka/pinctl/internal/board"

// modeNames maps the pin-mode strings accepted/reported by the API to their
// board.PinMode values. Only the modes v0.1 actually drives are named here
// (see board.PinMode's doc comment on the modes pinctl cares about) — a
// pin's other Firmata capabilities (servo, i2c, ...) are neither settable
// nor reported through this API.
var modeNames = map[string]board.PinMode{
	"input":        board.ModeInput,
	"input-pullup": board.ModePullup,
	"output":       board.ModeOutput,
	"analog":       board.ModeAnalog,
	"pwm":          board.ModePWM,
}

var modeStrings = func() map[board.PinMode]string {
	m := make(map[board.PinMode]string, len(modeNames))
	for name, mode := range modeNames {
		m[mode] = name
	}
	return m
}()

// modeStringsFor returns the API-known mode names a pin supports, in a
// stable order.
func modeStringsFor(pc board.PinCapability) []string {
	order := []board.PinMode{board.ModeInput, board.ModePullup, board.ModeOutput, board.ModeAnalog, board.ModePWM}
	var out []string
	for _, mode := range order {
		if _, ok := pc.Modes[mode]; ok {
			out = append(out, modeStrings[mode])
		}
	}
	return out
}
