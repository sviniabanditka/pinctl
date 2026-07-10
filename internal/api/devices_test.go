package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

func newTestServer(t *testing.T) (http.Handler, *fakeSession) {
	t.Helper()
	st := newTestStore(t)
	fs := newFakeSession()
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	io := newFakeDeviceIO()
	h := NewServer(fs, "/dev/ttyACM0", st, mgr, io)
	return h, fs
}

func TestCreateDevice(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "Porch LED",
		"pin":  13,
		"role": "digital-out",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var d model.Device
	mustDecode(t, rec, &d)
	if d.ID == "" {
		t.Error("expected an assigned id")
	}
	if d.BoardID != defaultBoardID {
		t.Errorf("boardId = %q, want default %q", d.BoardID, defaultBoardID)
	}
	if d.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestCreateDevice_BadRole(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "X", "pin": 13, "role": "bogus",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateDevice_Servo covers creating a servo device on a pin the
// (connected) fake session actually reports servo capability for (pin 2 in
// the fakeSession fixture).
func TestCreateDevice_Servo(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "Arm", "pin": 2, "role": "servo",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var d model.Device
	mustDecode(t, rec, &d)
	if d.Role != model.RoleServo {
		t.Errorf("role = %q, want servo", d.Role)
	}
}

// TestCreateDevice_UnsupportedPinMode_ConnectedBoard covers the new
// create-time gate: pin 13 in the fakeSession fixture supports
// Output/Input/PWM but not Servo, so binding a servo device to it while the
// board is connected (fs.alive == true, the default) must be rejected
// rather than silently accepted and fail later.
func TestCreateDevice_UnsupportedPinMode_ConnectedBoard(t *testing.T) {
	h, fs := newTestServer(t)
	if !fs.alive {
		t.Fatal("test setup: fake session should be alive/connected by default")
	}

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "Arm", "pin": 13, "role": "servo",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "unsupported_pin_mode" {
		t.Errorf("error code = %q, want unsupported_pin_mode", er.Error.Code)
	}
}

// TestCreateDevice_UnsupportedPinMode_OfflineBoardAccepted is the negative
// counterpart: the exact same (pin, role) combination that's rejected while
// connected must be accepted (device metadata stays editable) once the
// board is offline — the mismatch can't be checked against capabilities
// nobody has reported yet.
func TestCreateDevice_UnsupportedPinMode_OfflineBoardAccepted(t *testing.T) {
	h, fs := newTestServer(t)
	fs.alive = false

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "Arm", "pin": 13, "role": "servo",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (board offline: accept); body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateDevice_EmptyName(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"pin": 13, "role": "digital-out",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "validation_error" {
		t.Errorf("error code = %q, want validation_error", er.Error.Code)
	}
}

func TestCreateDevice_PinConflict(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LED", "pin": 13, "role": "digital-out",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "Another LED", "pin": 13, "role": "digital-out",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "conflict" {
		t.Errorf("error code = %q, want conflict", er.Error.Code)
	}
}

func TestCreateDevice_DifferentBoardSamePinOK(t *testing.T) {
	srv, boards := newTestServerWithBoards(t, "Board A", "Board B")
	boardA, boardB := boards[0].ID, boards[1].ID

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LED", "pin": 13, "role": "digital-out", "boardId": boardA,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, srv, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LED2", "pin": 13, "role": "digital-out", "boardId": boardB,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (different board, same pin ok); body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateDevice_UnknownBoardRejected covers the v0.4 boardId existence
// check (docs/12-roadmap.md multi-board support): once a store has any
// registered boards, an explicit boardId that isn't one of them is a 400
// unknown_board, per this milestone's device-create validation.
func TestCreateDevice_UnknownBoardRejected(t *testing.T) {
	srv, _ := newTestServerWithBoards(t, "Board A")

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LED", "pin": 13, "role": "digital-out", "boardId": "brd_does_not_exist",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "unknown_board" {
		t.Errorf("error code = %q, want unknown_board", er.Error.Code)
	}
}

// TestCreateDevice_KnownBoardAccepted is the positive counterpart: a
// registered boardId is accepted.
func TestCreateDevice_KnownBoardAccepted(t *testing.T) {
	srv, boards := newTestServerWithBoards(t, "Board A")

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LED", "pin": 13, "role": "digital-out", "boardId": boards[0].ID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetDevice_NotFound(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/devices/dev_doesnotexist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "not_found" {
		t.Errorf("error code = %q, want not_found", er.Error.Code)
	}
}

func TestListDevices(t *testing.T) {
	h, _ := newTestServer(t)

	doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{"name": "A", "pin": 13, "role": "digital-out"})
	doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{"name": "B", "pin": 14, "role": "analog-in"})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/devices", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Devices []model.Device `json:"devices"`
	}
	mustDecode(t, rec, &resp)
	if len(resp.Devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(resp.Devices))
	}
}

func TestUpdateDevice_NameAndPin(t *testing.T) {
	h, fs := newTestServer(t)
	_ = fs

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{"name": "A", "pin": 13, "role": "digital-out"})
	var d model.Device
	mustDecode(t, rec, &d)

	rec = doRequest(t, h, http.MethodPatch, "/api/v1/devices/"+d.ID, map[string]any{"name": "Renamed", "pin": 5})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var updated model.Device
	mustDecode(t, rec, &updated)
	if updated.Name != "Renamed" || updated.Pin != 5 {
		t.Errorf("updated = %+v, want name=Renamed pin=5", updated)
	}
	if updated.Role != model.RoleDigitalOut {
		t.Errorf("role changed unexpectedly to %q", updated.Role)
	}
}

func TestUpdateDevice_RoleImmutable(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{"name": "A", "pin": 13, "role": "digital-out"})
	var d model.Device
	mustDecode(t, rec, &d)

	rec = doRequest(t, h, http.MethodPatch, "/api/v1/devices/"+d.ID, map[string]any{"role": "analog-in"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateDevice_BoardIDImmutable(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{"name": "A", "pin": 13, "role": "digital-out"})
	var d model.Device
	mustDecode(t, rec, &d)

	rec = doRequest(t, h, http.MethodPatch, "/api/v1/devices/"+d.ID, map[string]any{"boardId": "brd_other"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteDevice(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{"name": "A", "pin": 13, "role": "digital-out"})
	var d model.Device
	mustDecode(t, rec, &d)

	rec = doRequest(t, h, http.MethodDelete, "/api/v1/devices/"+d.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/devices/"+d.ID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 after delete", rec.Code)
	}
}

// TestDeleteDevice_ReferencedByPipelineConflict covers docs/10-api.md:
// "DELETE /api/v1/devices/{id} ... 409 if referenced by any pipeline."
func TestDeleteDevice_ReferencedByPipelineConflict(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodDelete, "/api/v1/devices/"+led.ID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "device_in_use" {
		t.Errorf("error code = %q, want device_in_use", er.Error.Code)
	}

	// The device must still be gettable (delete was rejected).
	rec = doRequest(t, h, http.MethodGet, "/api/v1/devices/"+led.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (device should still exist); body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateDevice_AppliesPinMode confirms the create handler calls
// boardio.ApplyPinModes against the session (not just persists to store).
// TestWriteDevice_OK covers the manual test write from docs/10-api.md:
// no enabled pipeline drives the device, so the write reaches s.io.
func TestWriteDevice_OK(t *testing.T) {
	st := newTestStore(t)
	fs := newFakeSession()
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	io := newFakeDeviceIO()
	h := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices/"+led.ID+"/write", map[string]any{"value": true})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	v, ok := io.LastWrite(led.ID)
	if !ok || !v.Equal(engine.BoolValue(true)) {
		t.Errorf("last write = %+v, ok=%v, want true", v, ok)
	}
}

// TestWriteDevice_ConflictWithEnabledPipeline covers docs/10-api.md's
// "device is driven by an enabled pipeline" 409: handleWriteDevice must
// refuse with device_driven_by_pipeline exactly as before the move to
// store.EnabledPipelineWritingDevice (docs/15-mqtt-bridge.md "Commands and
// the pipeline-conflict rule" reuses the same store method).
// TestWriteDevice_Servo covers the manual write's servo angle range check:
// 90 (in range) succeeds, 200 (out of range) is rejected.
func TestWriteDevice_Servo(t *testing.T) {
	st := newTestStore(t)
	fs := newFakeSession()
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	io := newFakeDeviceIO()
	h := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	arm := createTestDevice(t, h, "Arm", 2, "servo")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices/"+arm.ID+"/write", map[string]any{"value": 90})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	v, ok := io.LastWrite(arm.ID)
	if !ok || !v.Equal(engine.FloatValue(90)) {
		t.Errorf("last write = %+v, ok=%v, want float64 90", v, ok)
	}

	rec = doRequest(t, h, http.MethodPost, "/api/v1/devices/"+arm.ID+"/write", map[string]any{"value": 200})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (200 is out of 0-180 range); body=%s", rec.Code, rec.Body.String())
	}
}

func TestWriteDevice_ConflictWithEnabledPipeline(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created model.Pipeline
	mustDecode(t, rec, &created)

	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodPost, "/api/v1/devices/"+led.ID+"/write", map[string]any{"value": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "device_driven_by_pipeline" {
		t.Errorf("error code = %q, want device_driven_by_pipeline", er.Error.Code)
	}
}

func TestCreateDevice_AppliesPinMode(t *testing.T) {
	h, fs := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LDR", "pin": 14, "role": "analog-in",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if fs.lastPin != 14 {
		t.Errorf("lastPin = %d, want 14 (ApplyPinModes should have set mode on the new device's pin)", fs.lastPin)
	}
}

// TestCreateDevice_EmptyBoardID_ResolvesSingleBoard covers a hole the
// original capability check silently fell through: with a boards collection
// registered, an omitted boardId used to default to the well-known
// placeholder id, which matches no real board. Nothing could be validated
// against it, so a bad (pin, role) was accepted and the device was bound to
// a board that does not exist.
func TestCreateDevice_EmptyBoardID_ResolvesSingleBoard(t *testing.T) {
	srv, boards := newTestServerWithBoards(t, "Uno")

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LED", "pin": 13, "role": "digital-out",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var got model.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.BoardID != boards[0].ID {
		t.Errorf("boardId = %q, want the only registered board %q", got.BoardID, boards[0].ID)
	}
}

// ...and because the board now resolves, the capability check actually runs.
func TestCreateDevice_EmptyBoardID_StillChecksCapabilities(t *testing.T) {
	srv, _ := newTestServerWithBoards(t, "Uno")

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "Arm", "pin": 13, "role": "servo",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if er := decodeError(t, rec); er.Error.Code != "unsupported_pin_mode" {
		t.Errorf("error code = %q, want unsupported_pin_mode", er.Error.Code)
	}
}

func TestCreateDevice_EmptyBoardID_AmbiguousWithTwoBoards(t *testing.T) {
	srv, _ := newTestServerWithBoards(t, "Uno", "Nano")

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": "LED", "pin": 13, "role": "digital-out",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if er := decodeError(t, rec); er.Error.Code != "unknown_board" {
		t.Errorf("error code = %q, want unknown_board", er.Error.Code)
	}
}
