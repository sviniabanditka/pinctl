package main

// Adapters satisfying internal/mqtt's BoardStatus interface (Connected,
// Firmware), defined here rather than in internal/mqtt so that package
// never needs to import internal/board (docs/15-mqtt-bridge.md's
// "Architecture": "board.Manager's status (per-device availability)", kept
// narrow on purpose).

import (
	"github.com/sviniabanditka/pinctl/internal/api"
	"github.com/sviniabanditka/pinctl/internal/board"
)

// managerBoardStatus adapts *board.Manager to mqtt.BoardStatus, used
// whenever cmd/pinctl is running multi-board mode (the production path:
// no -port flag).
type managerBoardStatus struct {
	mgr *board.Manager
}

func (a managerBoardStatus) Connected(boardID string) bool {
	for _, b := range a.mgr.Boards() {
		if b.ID == boardID {
			return b.Status == board.StatusConnected
		}
	}
	return false
}

func (a managerBoardStatus) Firmware(boardID string) (name string, ok bool) {
	for _, b := range a.mgr.Boards() {
		if b.ID == boardID {
			return b.FirmwareName, b.FirmwareName != ""
		}
	}
	return "", false
}

// singleBoardStatus adapts the single dev-mode session (cmd/pinctl -port,
// which bypasses board.Manager entirely) to mqtt.BoardStatus. -port only
// ever has exactly one board, so boardID is accepted but ignored — every
// device in dev mode belongs to that one session.
type singleBoardStatus struct {
	sess api.Session
}

func (a singleBoardStatus) Connected(string) bool { return a.sess.Alive() }

func (a singleBoardStatus) Firmware(string) (name string, ok bool) {
	name, _, _ = a.sess.Firmware()
	return name, name != ""
}
