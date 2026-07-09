package board

import (
	"testing"

	"github.com/sviniabanditka/pinctl/internal/model"
)

func TestMatchesAllowlist_EmptyAllowsAny(t *testing.T) {
	if !MatchesAllowlist("/dev/ttyACM0", nil) {
		t.Error("empty allowlist should allow any path")
	}
}

func TestMatchesAllowlist_ExactMatch(t *testing.T) {
	allow := []string{"/dev/ttyACM0", "/dev/ttyUSB0"}
	if !MatchesAllowlist("/dev/ttyACM0", allow) {
		t.Error("expected exact match to pass")
	}
	if MatchesAllowlist("/dev/ttyACM1", allow) {
		t.Error("expected non-listed path to be rejected")
	}
}

func TestMatchesAllowlist_Glob(t *testing.T) {
	allow := []string{"/dev/ttyACM*"}
	if !MatchesAllowlist("/dev/ttyACM0", allow) {
		t.Error("expected glob to match /dev/ttyACM0")
	}
	if !MatchesAllowlist("/dev/ttyACM5", allow) {
		t.Error("expected glob to match /dev/ttyACM5")
	}
	if MatchesAllowlist("/dev/ttyUSB0", allow) {
		t.Error("expected glob not to match /dev/ttyUSB0")
	}
}

func TestIdentityForPort_USB(t *testing.T) {
	id := identityForPort(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "85033313938351E082D1"})
	want := model.Identity{Kind: "usb-serial", Key: "VID_2341:PID_0043:SN_85033313938351E082D1"}
	if id != want {
		t.Errorf("identityForPort = %+v, want %+v", id, want)
	}
}

func TestIdentityForPort_USBNoSerial(t *testing.T) {
	id := identityForPort(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043"})
	want := model.Identity{Kind: "usb-serial", Key: "VID_2341:PID_0043"}
	if id != want {
		t.Errorf("identityForPort = %+v, want %+v", id, want)
	}
}

func TestIdentityForPort_Fallback(t *testing.T) {
	id := identityForPort(PortInfo{Path: "/dev/ttyS0"})
	want := model.Identity{Kind: "port-path", Key: "/dev/ttyS0"}
	if id != want {
		t.Errorf("identityForPort = %+v, want %+v", id, want)
	}
}
