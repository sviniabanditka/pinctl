package boardio

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

func TestMultiIO_RoutesByBoardID(t *testing.T) {
	sessA := newFakeSession()
	sessA.digital[13] = true
	sessB := newFakeSession()
	sessB.digital[13] = false

	lookup := func(boardID string) (Session, error) {
		switch boardID {
		case "brd_a":
			return sessA, nil
		case "brd_b":
			return sessB, nil
		default:
			return nil, errors.New("unknown board")
		}
	}
	io := NewMulti(lookup)

	v, err := io.ReadDevice(model.Device{ID: "x", BoardID: "brd_a", Pin: 13, Role: model.RoleDigitalIn})
	if err != nil {
		t.Fatalf("ReadDevice(brd_a): %v", err)
	}
	if !v.Bool {
		t.Errorf("brd_a value = %v, want true", v)
	}

	v, err = io.ReadDevice(model.Device{ID: "y", BoardID: "brd_b", Pin: 13, Role: model.RoleDigitalIn})
	if err != nil {
		t.Fatalf("ReadDevice(brd_b): %v", err)
	}
	if v.Bool {
		t.Errorf("brd_b value = %v, want false", v)
	}
}

func TestMultiIO_UnknownBoardErrors(t *testing.T) {
	io := NewMulti(func(boardID string) (Session, error) { return nil, errors.New("no such board") })

	if _, err := io.ReadDevice(model.Device{ID: "x", BoardID: "brd_missing", Pin: 13, Role: model.RoleDigitalIn}); err == nil {
		t.Fatal("expected error for unknown board")
	}
	if err := io.WriteDevice(model.Device{ID: "x", BoardID: "brd_missing", Pin: 13, Role: model.RoleDigitalOut}, engine.BoolValue(true)); err == nil {
		t.Fatal("expected error for unknown board")
	}
}

func TestMultiIO_WriteRoutesByBoardID(t *testing.T) {
	sessA := newFakeSession()
	sessB := newFakeSession()
	lookup := func(boardID string) (Session, error) {
		if boardID == "brd_a" {
			return sessA, nil
		}
		return sessB, nil
	}
	io := NewMulti(lookup)

	if err := io.WriteDevice(model.Device{ID: "x", BoardID: "brd_a", Pin: 13, Role: model.RoleDigitalOut}, engine.BoolValue(true)); err != nil {
		t.Fatalf("WriteDevice: %v", err)
	}
	if !sessA.digital[13] {
		t.Error("expected sessA pin 13 written high")
	}
	if _, written := sessB.digital[13]; written {
		t.Error("sessB should not have been touched")
	}
}

func rawBool(b bool) *json.RawMessage {
	data, _ := json.Marshal(b)
	raw := json.RawMessage(data)
	return &raw
}

func rawFloat(f float64) *json.RawMessage {
	data, _ := json.Marshal(f)
	raw := json.RawMessage(data)
	return &raw
}

func TestApplySafeValues_DigitalOut(t *testing.T) {
	sess := newFakeSession()
	sess.digital[13] = false

	devices := []model.Device{
		{ID: "led", Pin: 13, Role: model.RoleDigitalOut, Options: model.DeviceOptions{
			OnDisconnect: "safe-value",
			SafeValue:    rawBool(true),
		}},
	}
	if err := ApplySafeValues(sess, devices); err != nil {
		t.Fatalf("ApplySafeValues: %v", err)
	}
	if !sess.digital[13] {
		t.Error("expected safe value (true) to be written")
	}
}

func TestApplySafeValues_PWMOut(t *testing.T) {
	sess := newFakeSession()

	devices := []model.Device{
		{ID: "pwm", Pin: 13, Role: model.RolePWMOut, Options: model.DeviceOptions{
			OnDisconnect: "safe-value",
			SafeValue:    rawFloat(42),
		}},
	}
	if err := ApplySafeValues(sess, devices); err != nil {
		t.Fatalf("ApplySafeValues: %v", err)
	}
	if sess.lastPWM != 42 {
		t.Errorf("lastPWM = %d, want 42", sess.lastPWM)
	}
}

func TestApplySafeValues_Servo(t *testing.T) {
	sess := newFakeSession()

	devices := []model.Device{
		{ID: "servo", Pin: 2, Role: model.RoleServo, Options: model.DeviceOptions{
			OnDisconnect: "safe-value",
			SafeValue:    rawFloat(45),
		}},
	}
	if err := ApplySafeValues(sess, devices); err != nil {
		t.Fatalf("ApplySafeValues: %v", err)
	}
	if sess.lastServoAngle != 45 {
		t.Errorf("lastServoAngle = %d, want 45", sess.lastServoAngle)
	}
}

func TestApplySafeValues_HoldSkipsWrite(t *testing.T) {
	sess := newFakeSession()
	sess.digital[13] = true

	devices := []model.Device{
		{ID: "led", Pin: 13, Role: model.RoleDigitalOut, Options: model.DeviceOptions{
			OnDisconnect: "hold",
			SafeValue:    rawBool(false),
		}},
	}
	if err := ApplySafeValues(sess, devices); err != nil {
		t.Fatalf("ApplySafeValues: %v", err)
	}
	if !sess.digital[13] {
		t.Error("hold should not have written the safe value, pin should remain true")
	}
}

func TestApplySafeValues_IgnoresReadRoles(t *testing.T) {
	sess := newFakeSession()
	devices := []model.Device{
		{ID: "ldr", Pin: 14, Role: model.RoleAnalogIn},
		{ID: "btn", Pin: 14, Role: model.RoleDigitalIn},
	}
	if err := ApplySafeValues(sess, devices); err != nil {
		t.Fatalf("ApplySafeValues on read-only devices should no-op: %v", err)
	}
}

func TestApplySafeValues_CollectsErrorsButContinues(t *testing.T) {
	sess := newFakeSession()
	sess.digitalWrErr = errors.New("write failed")

	devices := []model.Device{
		{ID: "a", Pin: 13, Role: model.RoleDigitalOut, Options: model.DeviceOptions{SafeValue: rawBool(true)}},
		{ID: "b", Pin: 13, Role: model.RolePWMOut, Options: model.DeviceOptions{SafeValue: rawFloat(10)}},
	}
	err := ApplySafeValues(sess, devices)
	if err == nil {
		t.Fatal("expected a joined error from the digital-out failure")
	}
	// The PWM device (no analogWrErr set) must still have been attempted.
	if sess.lastPWM != 10 {
		t.Errorf("lastPWM = %d, want 10 (pwm write should still have been attempted)", sess.lastPWM)
	}
}

// TestMultiIO_OfflineBoardIsUnreachable pins the production path: MultiIO is
// what every multi-board deployment uses, and a board with no live session
// must reach the engine as engine.ErrDeviceUnreachable so the pipeline pauses
// rather than counting logic failures and auto-disabling itself.
//
// The per-session IO already mapped this. MultiIO's own lookup failure did
// not, and no test noticed: TestMultiIO_UnknownBoardErrors asserted only that
// *an* error came back.
func TestMultiIO_OfflineBoardIsUnreachable(t *testing.T) {
	io := NewMulti(func(string) (Session, error) { return nil, board.ErrBoardOffline })
	in := model.Device{ID: "dev_x", BoardID: "brd_gone", Pin: 14, Role: model.RoleAnalogIn}
	out := model.Device{ID: "dev_y", BoardID: "brd_gone", Pin: 13, Role: model.RoleDigitalOut}

	_, readErr := io.ReadDevice(in)
	writeErr := io.WriteDevice(out, engine.BoolValue(true))

	for name, err := range map[string]error{"ReadDevice": readErr, "WriteDevice": writeErr} {
		if err == nil {
			t.Fatalf("%s: got nil error, want unreachable", name)
		}
		if !errors.Is(err, engine.ErrDeviceUnreachable) {
			t.Errorf("%s: %v is not engine.ErrDeviceUnreachable", name, err)
		}
		if !errors.Is(err, board.ErrBoardOffline) {
			t.Errorf("%s: %v lost the original board.ErrBoardOffline cause", name, err)
		}
	}
}
