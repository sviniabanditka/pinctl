package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/board"
)

// fakeSession is a minimal in-memory stand-in for *board.Session, per
// docs/13-testing.md's fake-board pattern — no real serial port involved.
type fakeSession struct {
	alive        bool
	firmwareName string
	fwMajor      int
	fwMinor      int
	protoMajor   int
	protoMinor   int
	caps         []board.PinCapability

	digital map[int]bool
	analog  map[int]int

	setModeErr   error
	digitalWrErr error
	analogWrErr  error
	digitalRdErr error
	analogRdErr  error

	lastMode board.PinMode
	lastPin  int
	lastPWM  byte
	lastHigh bool
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		alive:        true,
		firmwareName: "StandardFirmata",
		fwMajor:      2,
		fwMinor:      5,
		protoMajor:   2,
		protoMinor:   5,
		caps: []board.PinCapability{
			{Pin: 13, Modes: map[board.PinMode]int{board.ModeOutput: 1, board.ModeInput: 1, board.ModePWM: 8}},
			{Pin: 14, Modes: map[board.PinMode]int{board.ModeInput: 1, board.ModeAnalog: 10}, AnalogChannel: 0, HasAnalogChannel: true},
		},
		digital: map[int]bool{},
		analog:  map[int]int{0: 512},
	}
}

func (f *fakeSession) Alive() bool { return f.alive }

func (f *fakeSession) Firmware() (string, int, int) { return f.firmwareName, f.fwMajor, f.fwMinor }

func (f *fakeSession) ProtocolVersion() (int, int) { return f.protoMajor, f.protoMinor }

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

func doRequest(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorResponse {
	t.Helper()
	var er errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil {
		t.Fatalf("decode error envelope: %v (body: %s)", err, rec.Body.String())
	}
	return er
}

func TestGetBoard(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodGet, "/api/v1/board", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp boardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Connected {
		t.Error("connected = false, want true")
	}
	if resp.Port != "/dev/ttyACM0" {
		t.Errorf("port = %q, want /dev/ttyACM0", resp.Port)
	}
	if resp.Firmware.Name != "StandardFirmata" || resp.Firmware.Major != 2 || resp.Firmware.Minor != 5 {
		t.Errorf("firmware = %+v", resp.Firmware)
	}
	if resp.ProtocolVersion != "2.5" {
		t.Errorf("protocolVersion = %q, want 2.5", resp.ProtocolVersion)
	}
	if len(resp.Capabilities) != 2 {
		t.Fatalf("capabilities len = %d, want 2", len(resp.Capabilities))
	}
}

func TestGetBoard_NotAlive(t *testing.T) {
	fs := newFakeSession()
	fs.alive = false
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodGet, "/api/v1/board", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp boardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Connected {
		t.Error("connected = true, want false")
	}
}

func TestSetPinMode(t *testing.T) {
	tests := []struct {
		name       string
		pin        string
		body       any
		setModeErr error
		wantStatus int
		wantCode   string
	}{
		{"ok output", "13", map[string]string{"mode": "output"}, nil, http.StatusNoContent, ""},
		{"ok input-pullup", "13", map[string]string{"mode": "input-pullup"}, nil, http.StatusNoContent, ""},
		{"bad mode string", "13", map[string]string{"mode": "servo"}, nil, http.StatusBadRequest, "bad_request"},
		{"bad json", "13", "not-json", nil, http.StatusBadRequest, "bad_request"},
		{"bad pin", "abc", map[string]string{"mode": "output"}, nil, http.StatusBadRequest, "bad_request"},
		{"unknown pin", "99", map[string]string{"mode": "output"}, board.ErrUnknownPin, http.StatusNotFound, "unknown_pin"},
		{"unsupported mode", "13", map[string]string{"mode": "analog"}, board.ErrUnsupportedMode, http.StatusConflict, "unsupported_mode"},
		{"session dead", "13", map[string]string{"mode": "output"}, board.ErrSessionDead, http.StatusServiceUnavailable, "session_dead"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeSession()
			fs.setModeErr = tc.setModeErr
			h := NewHandler(fs, "/dev/ttyACM0")

			var rec *httptest.ResponseRecorder
			if s, ok := tc.body.(string); ok {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/pins/"+tc.pin+"/mode", bytes.NewReader([]byte(s)))
				rec = httptest.NewRecorder()
				h.ServeHTTP(rec, req)
			} else {
				rec = doRequest(t, h, http.MethodPost, "/api/v1/pins/"+tc.pin+"/mode", tc.body)
			}

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" {
				er := decodeError(t, rec)
				if er.Error.Code != tc.wantCode {
					t.Errorf("error code = %q, want %q", er.Error.Code, tc.wantCode)
				}
			}
		})
	}
}

func TestReadPin_Digital(t *testing.T) {
	fs := newFakeSession()
	fs.digital[13] = true
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodGet, "/api/v1/pins/13", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp digitalReadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Pin != 13 || !resp.Value {
		t.Errorf("resp = %+v, want pin=13 value=true", resp)
	}
}

func TestReadPin_DigitalNoCachedValue(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodGet, "/api/v1/pins/13", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "no_cached_value" {
		t.Errorf("error code = %q", er.Error.Code)
	}
}

func TestReadPin_Analog(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodGet, "/api/v1/pins/14?analog=true", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp analogReadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Channel != 0 || resp.Value != 512 {
		t.Errorf("resp = %+v, want channel=0 value=512", resp)
	}
}

func TestReadPin_AnalogNoChannel(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	// pin 13 has no analog channel in the fixture.
	rec := doRequest(t, h, http.MethodGet, "/api/v1/pins/13?analog=true", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadPin_SessionDead(t *testing.T) {
	fs := newFakeSession()
	fs.digitalRdErr = board.ErrSessionDead
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodGet, "/api/v1/pins/13", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "session_dead" {
		t.Errorf("error code = %q", er.Error.Code)
	}
}

func TestReadPin_BadPin(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodGet, "/api/v1/pins/abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestWritePin_Digital(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pins/13", map[string]any{"value": true})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !fs.digital[13] {
		t.Error("expected pin 13 written high")
	}
}

func TestWritePin_PWM(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pins/13", map[string]any{"value": 200, "pwm": true})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if fs.lastPWM != 200 {
		t.Errorf("lastPWM = %d, want 200", fs.lastPWM)
	}
}

func TestWritePin_PWMOutOfRange(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pins/13", map[string]any{"value": 999, "pwm": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestWritePin_BadValueType(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pins/13", map[string]any{"value": "notabool"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestWritePin_SessionDead(t *testing.T) {
	fs := newFakeSession()
	fs.digitalWrErr = board.ErrSessionDead
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pins/13", map[string]any{"value": true})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadPin_PinOutOfRange(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	for _, pin := range []string{"128", "300"} {
		t.Run(pin, func(t *testing.T) {
			rec := doRequest(t, h, http.MethodGet, "/api/v1/pins/"+pin, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			er := decodeError(t, rec)
			if er.Error.Code != "bad_request" {
				t.Errorf("error code = %q, want bad_request", er.Error.Code)
			}
		})
	}
}

func TestWritePin_OversizedBody(t *testing.T) {
	fs := newFakeSession()
	h := NewHandler(fs, "/dev/ttyACM0")

	big := bytes.Repeat([]byte(" "), 2<<10)
	body := append([]byte(`{"value": true, "padding": "`), big...)
	body = append(body, []byte(`"}`)...)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pins/13", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "bad_request" {
		t.Errorf("error code = %q, want bad_request", er.Error.Code)
	}
}

func TestWritePin_UnknownPin(t *testing.T) {
	fs := newFakeSession()
	fs.digitalWrErr = board.ErrUnknownPin
	h := NewHandler(fs, "/dev/ttyACM0")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pins/99", map[string]any{"value": true})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
