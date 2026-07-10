package boardio

import (
	"errors"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// fakeSession is a minimal Session fake, same pattern as internal/api's
// fakeSession (docs/13-testing.md's fake-board pattern).
type fakeSession struct {
	caps []board.PinCapability

	digital map[int]bool
	analog  map[int]int

	setModeErr   error
	digitalWrErr error
	analogWrErr  error
	digitalRdErr error
	analogRdErr  error
	servoCfgErr  error
	servoWrErr   error

	lastMode        board.PinMode
	lastPin         int
	lastPWM         byte
	lastHigh        bool
	lastServoAngle  int
	lastServoMinUs  int
	lastServoMaxUs  int
	servoConfigured bool
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		caps: []board.PinCapability{
			{Pin: 13, Modes: map[board.PinMode]int{board.ModeOutput: 1, board.ModeInput: 1, board.ModePWM: 8}},
			{Pin: 14, Modes: map[board.PinMode]int{board.ModeInput: 1, board.ModePullup: 1, board.ModeAnalog: 10}, AnalogChannel: 0, HasAnalogChannel: true},
			{Pin: 2, Modes: map[board.PinMode]int{board.ModeInput: 1, board.ModeOutput: 1, board.ModeServo: 14}},
		},
		digital: map[int]bool{},
		analog:  map[int]int{0: 512},
	}
}

func (f *fakeSession) Capabilities() []board.PinCapability { return f.caps }

func (f *fakeSession) SetPinMode(pin int, mode board.PinMode) error {
	f.lastPin, f.lastMode = pin, mode
	return f.setModeErr
}

func (f *fakeSession) DigitalWrite(pin int, high bool) error {
	f.lastPin, f.lastHigh = pin, high
	if f.digitalWrErr != nil {
		return f.digitalWrErr
	}
	f.digital[pin] = high
	return nil
}

func (f *fakeSession) AnalogWrite(pin int, value byte) error {
	f.lastPin, f.lastPWM = pin, value
	return f.analogWrErr
}

func (f *fakeSession) DigitalRead(pin int) (bool, error) {
	if f.digitalRdErr != nil {
		return false, f.digitalRdErr
	}
	v, ok := f.digital[pin]
	if !ok {
		return false, board.ErrNoCachedValue
	}
	return v, nil
}

func (f *fakeSession) AnalogRead(channel int) (int, error) {
	if f.analogRdErr != nil {
		return 0, f.analogRdErr
	}
	v, ok := f.analog[channel]
	if !ok {
		return 0, board.ErrNoCachedValue
	}
	return v, nil
}

func (f *fakeSession) ServoConfig(pin, minPulseUs, maxPulseUs int) error {
	f.lastPin, f.lastServoMinUs, f.lastServoMaxUs = pin, minPulseUs, maxPulseUs
	if f.servoCfgErr != nil {
		return f.servoCfgErr
	}
	f.servoConfigured = true
	return nil
}

func (f *fakeSession) ServoWrite(pin, angle int) error {
	f.lastPin, f.lastServoAngle = pin, angle
	return f.servoWrErr
}

func TestReadDevice_AnalogIn(t *testing.T) {
	sess := newFakeSession()
	sess.analog[0] = 733
	io := New(sess)

	v, err := io.ReadDevice(model.Device{ID: "ldr", Pin: 14, Role: model.RoleAnalogIn})
	if err != nil {
		t.Fatalf("ReadDevice: %v", err)
	}
	if v.Type != engine.TypeFloat64 || v.Float != 733 {
		t.Errorf("v = %+v, want float64 733", v)
	}
}

func TestReadDevice_AnalogIn_NoChannel(t *testing.T) {
	sess := newFakeSession()
	io := New(sess)

	// pin 13 has no analog channel in the fixture.
	if _, err := io.ReadDevice(model.Device{ID: "x", Pin: 13, Role: model.RoleAnalogIn}); err == nil {
		t.Fatal("expected error for pin with no analog channel")
	}
}

func TestReadDevice_DigitalIn(t *testing.T) {
	sess := newFakeSession()
	sess.digital[14] = true
	io := New(sess)

	v, err := io.ReadDevice(model.Device{ID: "btn", Pin: 14, Role: model.RoleDigitalIn})
	if err != nil {
		t.Fatalf("ReadDevice: %v", err)
	}
	if v.Type != engine.TypeBool || v.Bool != true {
		t.Errorf("v = %+v, want bool true", v)
	}
}

func TestReadDevice_UnreadableRole(t *testing.T) {
	sess := newFakeSession()
	io := New(sess)

	if _, err := io.ReadDevice(model.Device{ID: "led", Pin: 13, Role: model.RoleDigitalOut}); err == nil {
		t.Fatal("expected error for unreadable role")
	}
}

func TestReadDevice_PropagatesSessionError(t *testing.T) {
	sess := newFakeSession()
	sess.digitalRdErr = board.ErrSessionDead
	io := New(sess)

	if _, err := io.ReadDevice(model.Device{ID: "btn", Pin: 14, Role: model.RoleDigitalIn}); !errors.Is(err, board.ErrSessionDead) {
		t.Errorf("err = %v, want ErrSessionDead", err)
	}
}

func TestWriteDevice_DigitalOut(t *testing.T) {
	sess := newFakeSession()
	io := New(sess)

	if err := io.WriteDevice(model.Device{ID: "led", Pin: 13, Role: model.RoleDigitalOut}, engine.BoolValue(true)); err != nil {
		t.Fatalf("WriteDevice: %v", err)
	}
	if !sess.digital[13] {
		t.Error("expected pin 13 written high")
	}
}

func TestWriteDevice_PWMOut_Clamps(t *testing.T) {
	sess := newFakeSession()
	io := New(sess)

	tests := []struct {
		in   float64
		want byte
	}{
		{-50, 0},
		{300, 255},
		{128, 128},
	}
	for _, tc := range tests {
		if err := io.WriteDevice(model.Device{ID: "pwm", Pin: 13, Role: model.RolePWMOut}, engine.FloatValue(tc.in)); err != nil {
			t.Fatalf("WriteDevice(%v): %v", tc.in, err)
		}
		if sess.lastPWM != tc.want {
			t.Errorf("WriteDevice(%v): lastPWM = %d, want %d", tc.in, sess.lastPWM, tc.want)
		}
	}
}

func TestWriteDevice_Servo_Clamps(t *testing.T) {
	sess := newFakeSession()
	io := New(sess)

	tests := []struct {
		in   float64
		want int
	}{
		{-5, 0},
		{200, 180},
		{90.6, 91},
		{90.4, 90},
	}
	for _, tc := range tests {
		if err := io.WriteDevice(model.Device{ID: "servo", Pin: 2, Role: model.RoleServo}, engine.FloatValue(tc.in)); err != nil {
			t.Fatalf("WriteDevice(%v): %v", tc.in, err)
		}
		if sess.lastServoAngle != tc.want {
			t.Errorf("WriteDevice(%v): lastServoAngle = %d, want %d", tc.in, sess.lastServoAngle, tc.want)
		}
	}
}

func TestReadDevice_Servo_NotReadable(t *testing.T) {
	sess := newFakeSession()
	io := New(sess)

	if _, err := io.ReadDevice(model.Device{ID: "servo", Pin: 2, Role: model.RoleServo}); err == nil {
		t.Fatal("expected error: servo is write-only")
	}
}

func TestWriteDevice_UnwritableRole(t *testing.T) {
	sess := newFakeSession()
	io := New(sess)

	if err := io.WriteDevice(model.Device{ID: "ldr", Pin: 14, Role: model.RoleAnalogIn}, engine.FloatValue(1)); err == nil {
		t.Fatal("expected error for unwritable role")
	}
}

func TestWriteDevice_PropagatesSessionError(t *testing.T) {
	sess := newFakeSession()
	sess.digitalWrErr = board.ErrSessionDead
	io := New(sess)

	if err := io.WriteDevice(model.Device{ID: "led", Pin: 13, Role: model.RoleDigitalOut}, engine.BoolValue(true)); !errors.Is(err, board.ErrSessionDead) {
		t.Errorf("err = %v, want ErrSessionDead", err)
	}
}

func TestApplyPinModes(t *testing.T) {
	sess := newFakeSession()
	devices := []model.Device{
		{ID: "ldr", Pin: 14, Role: model.RoleAnalogIn},
		{ID: "btn", Pin: 14, Role: model.RoleDigitalIn, Options: model.DeviceOptions{Pullup: true}},
		{ID: "led", Pin: 13, Role: model.RoleDigitalOut},
		{ID: "pwm", Pin: 13, Role: model.RolePWMOut},
	}

	if err := ApplyPinModes(sess, devices); err != nil {
		t.Fatalf("ApplyPinModes: %v", err)
	}
	// Only the last device's mode is directly observable via lastMode/lastPin
	// on this fake, so just check it landed without error; per-device
	// assertions happen implicitly by no error being returned.
	if sess.lastMode != board.ModePWM || sess.lastPin != 13 {
		t.Errorf("last SetPinMode call = pin %d mode %v, want pin 13 mode PWM", sess.lastPin, sess.lastMode)
	}
}

func TestApplyPinModes_CollectsErrorsButContinues(t *testing.T) {
	sess := newFakeSession()
	sess.setModeErr = board.ErrUnsupportedMode
	devices := []model.Device{
		{ID: "a", Pin: 13, Role: model.RoleDigitalOut},
		{ID: "b", Pin: 13, Role: model.RolePWMOut},
	}

	err := ApplyPinModes(sess, devices)
	if err == nil {
		t.Fatal("expected joined error")
	}
	if !errors.Is(err, board.ErrUnsupportedMode) {
		t.Errorf("err = %v, want wrapping ErrUnsupportedMode", err)
	}
}

func TestApplyPinModes_Servo_SetsModeAndConfig(t *testing.T) {
	sess := newFakeSession()
	devices := []model.Device{
		{ID: "servo", Pin: 2, Role: model.RoleServo, Options: model.DeviceOptions{MinPulseUs: 600, MaxPulseUs: 2200}},
	}

	if err := ApplyPinModes(sess, devices); err != nil {
		t.Fatalf("ApplyPinModes: %v", err)
	}
	if sess.lastMode != board.ModeServo || sess.lastPin != 2 {
		t.Errorf("SetPinMode call = pin %d mode %v, want pin 2 mode Servo", sess.lastPin, sess.lastMode)
	}
	if !sess.servoConfigured {
		t.Fatal("expected ServoConfig to be called")
	}
	if sess.lastServoMinUs != 600 || sess.lastServoMaxUs != 2200 {
		t.Errorf("ServoConfig(min=%d, max=%d), want (600, 2200)", sess.lastServoMinUs, sess.lastServoMaxUs)
	}
}

func TestApplyPinModes_Servo_DefaultsPulseRange(t *testing.T) {
	sess := newFakeSession()
	devices := []model.Device{{ID: "servo", Pin: 2, Role: model.RoleServo}}

	if err := ApplyPinModes(sess, devices); err != nil {
		t.Fatalf("ApplyPinModes: %v", err)
	}
	if sess.lastServoMinUs != 544 || sess.lastServoMaxUs != 2400 {
		t.Errorf("ServoConfig(min=%d, max=%d), want defaults (544, 2400)", sess.lastServoMinUs, sess.lastServoMaxUs)
	}
}

func TestApplyPinModes_UnknownRole(t *testing.T) {
	sess := newFakeSession()
	devices := []model.Device{{ID: "x", Pin: 13, Role: "bogus"}}

	if err := ApplyPinModes(sess, devices); err == nil {
		t.Fatal("expected error for unknown role")
	}
}
