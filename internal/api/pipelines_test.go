package api

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// errUnreachableFake stands in for a board.Err* cause, same rationale as
// internal/engine's own degraded_test.go: these tests only need something
// that engine.UnreachableError can wrap, not a real internal/board error.
var errUnreachableFake = errors.New("fake board: unreachable")

func createTestDevice(t *testing.T, h http.Handler, name string, pin int, role string) model.Device {
	t.Helper()
	rec := doRequest(t, h, http.MethodPost, "/api/v1/devices", map[string]any{
		"name": name, "pin": pin, "role": role,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create device %s: status = %d; body=%s", name, rec.Code, rec.Body.String())
	}
	var d model.Device
	mustDecode(t, rec, &d)
	return d
}

// ldrLedPipelineBody builds a POST/PUT /pipelines body — docs/10-api.md:
// name, tickMs, nodes, edges only. enabled is never part of the body; it is
// set only via the /enable and /disable endpoints.
func ldrLedPipelineBody(name string, ldrID, ledID string) map[string]any {
	return map[string]any{
		"name":   name,
		"tickMs": 20,
		"nodes": []map[string]any{
			{"id": "n1", "type": "device-read", "config": map[string]any{"deviceId": ldrID}, "position": map[string]any{"x": 0, "y": 0}},
			{"id": "n2", "type": "compare", "config": map[string]any{"op": "lt", "threshold": 300}, "position": map[string]any{"x": 0, "y": 0}},
			{"id": "n3", "type": "device-write", "config": map[string]any{"deviceId": ledID}, "position": map[string]any{"x": 0, "y": 0}},
		},
		"edges": []map[string]any{
			{"from": "n1.out", "to": "n2.in"},
			{"from": "n2.out", "to": "n3.in"},
		},
	}
}

func TestCreatePipeline_ValidationErrors(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", map[string]any{
		"name":   "broken",
		"tickMs": 20,
		"nodes": []map[string]any{
			{"id": "n1", "type": "device-read", "config": map[string]any{"deviceId": "dev_doesnotexist"}},
		},
		"edges": []map[string]any{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "validation_error" {
		t.Errorf("error code = %q, want validation_error", er.Error.Code)
	}
}

func TestCreateAndUpdatePipeline_HotSwap(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created model.Pipeline
	mustDecode(t, rec, &created)
	if created.ID == "" {
		t.Fatal("expected assigned pipeline id")
	}
	if created.Enabled {
		t.Fatal("POST /pipelines must always create a disabled pipeline (enable via /enable)")
	}

	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/pipelines", nil)
	var list struct {
		Pipelines []pipelineSummary `json:"pipelines"`
	}
	mustDecode(t, rec, &list)
	if len(list.Pipelines) != 1 || list.Pipelines[0].Status != "running" {
		t.Fatalf("pipelines summary = %+v, want one running pipeline", list.Pipelines)
	}

	// PUT full replace: rename, still enabled -> hot swap keeps it running.
	body := ldrLedPipelineBody("porch renamed", ldr.ID, led.ID)
	rec = doRequest(t, h, http.MethodPut, "/api/v1/pipelines/"+created.ID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var updated pipelineSaveResponse
	mustDecode(t, rec, &updated)
	if updated.Name != "porch renamed" {
		t.Errorf("name = %q, want %q", updated.Name, "porch renamed")
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/pipelines", nil)
	mustDecode(t, rec, &list)
	if list.Pipelines[0].Status != "running" {
		t.Errorf("status after hot-swap = %q, want running", list.Pipelines[0].Status)
	}
}

// TestPutPipeline_DoesNotTakeEnabledFromBody covers docs/10-api.md: PUT
// /pipelines/{id}'s body is name/tickMs/nodes/edges only — it must not be
// able to flip enabled, even if a client stuffs an "enabled" key into the
// JSON body (ignored, per decodeJSONBody's leniency toward unknown
// fields). A running pipeline must stay enabled and running across a PUT
// that includes "enabled": false in the body.
func TestPutPipeline_DoesNotTakeEnabledFromBody(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	var created model.Pipeline
	mustDecode(t, rec, &created)

	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	// PUT body deliberately tries to sneak "enabled": false in — the API
	// must ignore it and preserve the pipeline's current (enabled) state.
	body := ldrLedPipelineBody("porch renamed", ldr.ID, led.ID)
	body["enabled"] = false
	rec = doRequest(t, h, http.MethodPut, "/api/v1/pipelines/"+created.ID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var updated pipelineSaveResponse
	mustDecode(t, rec, &updated)
	if !updated.Enabled {
		t.Fatalf("PUT must not clear enabled from the request body; got enabled=%v", updated.Enabled)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/pipelines", nil)
	var list struct {
		Pipelines []pipelineSummary `json:"pipelines"`
	}
	mustDecode(t, rec, &list)
	if len(list.Pipelines) != 1 || !list.Pipelines[0].Enabled || list.Pipelines[0].Status != "running" {
		t.Fatalf("pipelines summary after PUT = %+v, want enabled=true status=running", list.Pipelines)
	}
}

func TestUpdatePipeline_InvalidRejects400(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	var created model.Pipeline
	mustDecode(t, rec, &created)

	// Dangling edge target.
	bad := map[string]any{
		"name":   "porch",
		"tickMs": 20,
		"nodes": []map[string]any{
			{"id": "n1", "type": "device-read", "config": map[string]any{"deviceId": ldr.ID}},
		},
		"edges": []map[string]any{
			{"from": "n1.out", "to": "n99.in"},
		},
	}
	rec = doRequest(t, h, http.MethodPut, "/api/v1/pipelines/"+created.ID, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeletePipeline(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	var created model.Pipeline
	mustDecode(t, rec, &created)

	rec = doRequest(t, h, http.MethodDelete, "/api/v1/pipelines/"+created.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, h, http.MethodGet, "/api/v1/pipelines/"+created.ID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 after delete", rec.Code)
	}
}

func TestEnableDisablePipeline(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	var created model.Pipeline
	mustDecode(t, rec, &created)

	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var action pipelineActionResponse
	mustDecode(t, rec, &action)
	if !action.Enabled || action.Status != "running" {
		t.Errorf("enable response = %+v, want enabled=true status=running", action)
	}

	// Idempotent: enabling again is still 200.
	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/disable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	mustDecode(t, rec, &action)
	if action.Enabled || action.Status != "stopped" {
		t.Errorf("disable response = %+v, want enabled=false status=stopped", action)
	}
}

// TestEnablePipeline_InvalidReferencesReturns400 covers docs/09-data-model.md's
// "stale reference" case: a pipeline node pointing at a deviceId that no
// longer exists must fail validation at enable time with 400. Since store
// now rejects deleting a device that's still referenced by a pipeline
// (ErrDeviceInUse, 409), a stale reference can no longer arise through the
// API's own delete/update paths — it's produced here the way it would be in
// practice (schema import, hand-edited pipeline file): by writing directly
// through the store, which does not itself enforce referential integrity
// (that's internal/engine's job, run at API validate/enable time).
func TestEnablePipeline_InvalidReferencesReturns400(t *testing.T) {
	st := newTestStore(t)
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	h := NewServer(newFakeSession(), "/dev/ttyACM0", st, mgr, newFakeDeviceIO())

	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	var created model.Pipeline
	mustDecode(t, rec, &created)

	// Point the device-write node at a deviceId that doesn't exist,
	// bypassing the API's own referential-integrity checks by writing
	// through the store directly.
	stale := created
	stale.Nodes = append([]model.Node{}, created.Nodes...)
	for i, n := range stale.Nodes {
		if n.Type == "device-write" {
			stale.Nodes[i].Config = []byte(`{"deviceId":"dev_doesnotexist"}`)
		}
	}
	if _, err := st.UpdatePipeline(created.ID, stale); err != nil {
		t.Fatalf("UpdatePipeline (store): %v", err)
	}

	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidatePipeline_Valid(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines/validate", ldrLedPipelineBody("porch", ldr.ID, led.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp validateResponse
	mustDecode(t, rec, &resp)
	if !resp.Valid || len(resp.Errors) != 0 {
		t.Errorf("resp = %+v, want valid with no errors", resp)
	}
}

func TestValidatePipeline_Invalid(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines/validate", map[string]any{
		"name":   "broken",
		"tickMs": 20,
		"nodes":  []map[string]any{{"id": "n1", "type": "bogus-type", "config": map[string]any{}}},
		"edges":  []map[string]any{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (validate always 200 on parseable body); body=%s", rec.Code, rec.Body.String())
	}
	var resp validateResponse
	mustDecode(t, rec, &resp)
	if resp.Valid || len(resp.Errors) == 0 {
		t.Errorf("resp = %+v, want invalid with errors", resp)
	}
}

// TestValidatePipeline_MultipleWritersWarning covers the one warning kind
// v0.2's engine produces: two device-write nodes targeting the same
// device is legal but surfaced as a warning, not an error.
func TestValidatePipeline_MultipleWritersWarning(t *testing.T) {
	h, _ := newTestServer(t)
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	body := map[string]any{
		"name":   "double writer",
		"tickMs": 20,
		"nodes": []map[string]any{
			{"id": "n1", "type": "constant", "config": map[string]any{"kind": "bool", "value": true}},
			{"id": "n2", "type": "constant", "config": map[string]any{"kind": "bool", "value": false}},
			{"id": "n3", "type": "device-write", "config": map[string]any{"deviceId": led.ID}},
			{"id": "n4", "type": "device-write", "config": map[string]any{"deviceId": led.ID}},
		},
		"edges": []map[string]any{
			{"from": "n1.out", "to": "n3.in"},
			{"from": "n2.out", "to": "n4.in"},
		},
	}

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines/validate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp validateResponse
	mustDecode(t, rec, &resp)
	if !resp.Valid {
		t.Fatalf("resp = %+v, want valid=true (warnings are non-blocking)", resp)
	}
	if len(resp.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly 1 (multiple_writers)", resp.Warnings)
	}
	if resp.Warnings[0].Code != "multiple_writers" {
		t.Errorf("warning code = %q, want multiple_writers", resp.Warnings[0].Code)
	}
}

// TestEndToEnd_LDRTurnsOnLEDViaREST is the roadmap's v0.2 done-criteria,
// exercised entirely through REST against a fake DeviceIO with a
// manually-driven clock/ticker: create two devices, create+enable the
// LDR->LED pipeline, fire a tick with a dark reading, and confirm the LED
// device write landed.
func TestEndToEnd_LDRTurnsOnLEDViaREST(t *testing.T) {
	st := newTestStore(t)
	fs := newFakeSession()
	io := newFakeDeviceIO()
	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := engine.NewManager(engine.WithClock(clk), engine.WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)
	h := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")
	io.SetRead(ldr.ID, engine.FloatValue(900)) // bright: LED should stay off

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("ldr-led", ldr.ID, led.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created model.Pipeline
	mustDecode(t, rec, &created)

	// POST /pipelines always creates disabled; enable it to start the
	// scheduler (and spawn the ticker this test drives manually).
	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	mt := <-tickers

	mt.Fire(time.Unix(1, 0))
	if _, ok := waitForTick(mgr, created.ID, 1, 2*time.Second); !ok {
		t.Fatal("pipeline did not reach tick 1")
	}
	if v, ok := io.LastWrite(led.ID); !ok || v.Bool != false {
		t.Fatalf("tick 1 (bright): led write = %+v, ok=%v, want false", v, ok)
	}

	// Cover the sensor: LDR reads low, LED should turn on.
	io.SetRead(ldr.ID, engine.FloatValue(200))
	mt.Fire(time.Unix(2, 0))
	if _, ok := waitForTick(mgr, created.ID, 2, 2*time.Second); !ok {
		t.Fatal("pipeline did not reach tick 2")
	}
	if v, ok := io.LastWrite(led.ID); !ok || v.Bool != true {
		t.Fatalf("tick 2 (dark): led write = %+v, ok=%v, want true", v, ok)
	}
}

// TestPipelineStatus_PausedSerializesWithDetail covers docs/10-api.md's
// "paused" status (board offline): once a pipeline's device-read hits
// engine.ErrDeviceUnreachable, both the list view (GET /pipelines) and the
// single-pipeline view (GET /pipelines/{id}) must serialize
// status:"paused" with a non-empty detail explaining why — not the old
// "error" catch-all docs/10-api.md's deviation comment used to fall back
// to.
func TestPipelineStatus_PausedSerializesWithDetail(t *testing.T) {
	st := newTestStore(t)
	fs := newFakeSession()
	io := newFakeDeviceIO()
	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := engine.NewManager(engine.WithClock(clk), engine.WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)
	h := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")
	io.SetReadErr(ldr.ID, &engine.UnreachableError{DeviceID: ldr.ID, BoardID: ldr.BoardID, Err: errUnreachableFake})

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("paused-pipeline", ldr.ID, led.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created model.Pipeline
	mustDecode(t, rec, &created)

	rec = doRequest(t, h, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	mt := <-tickers
	mt.Fire(time.Unix(1, 0))
	if _, ok := waitForTick(mgr, created.ID, 1, 2*time.Second); !ok {
		t.Fatal("pipeline did not reach tick 1")
	}

	// GET /pipelines (list/summary view).
	rec = doRequest(t, h, http.MethodGet, "/api/v1/pipelines", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pipelines: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Pipelines []pipelineSummary `json:"pipelines"`
	}
	mustDecode(t, rec, &listResp)
	var summary *pipelineSummary
	for i := range listResp.Pipelines {
		if listResp.Pipelines[i].ID == created.ID {
			summary = &listResp.Pipelines[i]
		}
	}
	if summary == nil {
		t.Fatalf("pipeline %s not found in list response: %+v", created.ID, listResp.Pipelines)
	}
	if summary.Status != "paused" {
		t.Errorf("list: status = %q, want %q", summary.Status, "paused")
	}
	if summary.Detail == "" {
		t.Error("list: detail is empty, want a non-empty reason")
	}

	// GET /pipelines/{id} (single/detail view).
	rec = doRequest(t, h, http.MethodGet, "/api/v1/pipelines/"+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var detail pipelineDetailResponse
	mustDecode(t, rec, &detail)
	if detail.Status != "paused" {
		t.Errorf("detail: status = %q, want %q", detail.Status, "paused")
	}
	if detail.Detail == "" {
		t.Error("detail: detail field is empty, want a non-empty reason")
	}
}
