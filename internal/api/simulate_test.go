package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
	"github.com/sviniabanditka/pinctl/internal/simulate"
)

// ldrLedExpressionPipelineBody is docs/18-simulation.md's simulate request
// pipeline field: the README's LDR->LED graph via an "expression" node
// (a < 500), so the pipeline never needs to exist in — or reference IDs
// from — the store.
func ldrLedExpressionSimBody(ldrID, ledID string) map[string]any {
	return map[string]any{
		"name":   "sim-ldr-led",
		"tickMs": 100,
		"nodes": []map[string]any{
			{"id": "n1", "type": "device-read", "config": map[string]any{"deviceId": ldrID}},
			{"id": "n2", "type": "expression", "config": map[string]any{
				"expression": "a < 500",
				"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
				"outputType": "bool",
			}},
			{"id": "n3", "type": "device-write", "config": map[string]any{"deviceId": ledID}},
		},
		"edges": []map[string]any{
			{"from": "n1.out", "to": "n2.a"},
			{"from": "n2.out", "to": "n3.in"},
		},
	}
}

func TestSimulate_OKShape(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/simulate", map[string]any{
		"pipeline": ldrLedExpressionSimBody(ldr.ID, led.ID),
		"steps":    5,
		"inputs":   map[string]any{ldr.ID: 800},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp simulateResponse
	mustDecode(t, rec, &resp)
	if resp.TickMs != 100 {
		t.Errorf("tickMs = %d, want 100", resp.TickMs)
	}
	if resp.Error != nil {
		t.Errorf("error = %+v, want nil", resp.Error)
	}
	if len(resp.Frames) != 5 {
		t.Fatalf("len(frames) = %d, want 5", len(resp.Frames))
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none (ldr was seeded)", resp.Warnings)
	}
	f0 := resp.Frames[0]
	if f0.Tick != 1 {
		t.Errorf("frames[0].tick = %d, want 1", f0.Tick)
	}
	if got, ok := f0.Writes[led.ID]; !ok || got != false {
		t.Errorf("frames[0].writes[led] = (%v, ok=%v), want (false, true) — 800 < 500 is false", got, ok)
	}
}

func TestSimulate_UnknownDeviceIn400(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/simulate", map[string]any{
		"pipeline": ldrLedExpressionSimBody("dev_doesnotexist_ldr", "dev_doesnotexist_led"),
		"steps":    1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSimulate_MalformedBody400(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/simulate", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSimulate_StepsOverCap400(t *testing.T) {
	h, _ := newTestServer(t)
	ldr := createTestDevice(t, h, "LDR", 14, "analog-in")
	led := createTestDevice(t, h, "LED", 13, "digital-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/simulate", map[string]any{
		"pipeline": ldrLedExpressionSimBody(ldr.ID, led.ID),
		"steps":    simulate.MaxSteps + 1,
		"inputs":   map[string]any{ldr.ID: 800},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSimulate_GraphValidationError400(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/simulate", map[string]any{
		"pipeline": map[string]any{
			"name":   "broken",
			"tickMs": 20,
			"nodes":  []map[string]any{{"id": "n1", "type": "bogus-type", "config": map[string]any{}}},
			"edges":  []map[string]any{},
		},
		"steps": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "validation_error" {
		t.Errorf("error code = %q, want validation_error", er.Error.Code)
	}
}

func TestSimulate_DivisionByZeroErrorField(t *testing.T) {
	h, _ := newTestServer(t)
	sw := createTestDevice(t, h, "SW", 14, "analog-in")
	out := createTestDevice(t, h, "OUT", 13, "pwm-out")

	rec := doRequest(t, h, http.MethodPost, "/api/v1/simulate", map[string]any{
		"pipeline": map[string]any{
			"name":   "sim-div-zero",
			"tickMs": 20,
			"nodes": []map[string]any{
				{"id": "n1", "type": "device-read", "config": map[string]any{"deviceId": sw.ID}},
				{"id": "n2", "type": "expression", "config": map[string]any{
					"expression": "1.0 / a",
					"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
					"outputType": "float64",
				}},
				{"id": "n3", "type": "device-write", "config": map[string]any{"deviceId": out.ID}},
			},
			"edges": []map[string]any{
				{"from": "n1.out", "to": "n2.a"},
				{"from": "n2.out", "to": "n3.in"},
			},
		},
		"steps":  3,
		"inputs": map[string]any{sw.ID: 0},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a node error mid-run is not a server fault); body=%s", rec.Code, rec.Body.String())
	}
	var resp simulateResponse
	mustDecode(t, rec, &resp)
	if resp.Error == nil {
		t.Fatal("error = nil, want set")
	}
	if resp.Error.Tick != 1 || resp.Error.NodeID != "n2" {
		t.Errorf("error = %+v, want {tick:1 nodeId:n2 ...}", resp.Error)
	}
	if len(resp.Frames) != 0 {
		t.Errorf("frames = %+v, want none (fails on the very first tick)", resp.Frames)
	}
}

// TestSimulate_DoesNotTouchLiveManager is the isolation guarantee
// docs/18-simulation.md's Principle #3 depends on: simulating a pipeline
// must not create, start, or otherwise disturb a runner in the Server's
// real engine.Manager. This starts a real pipeline (via the fake clock/
// ticker the test controls), then simulates a *different* graph, and
// checks the live pipeline's status/tick count is untouched by the
// simulate call.
func TestSimulate_DoesNotTouchLiveManager(t *testing.T) {
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
	io.SetRead(ldr.ID, engine.FloatValue(900))

	rec := doRequest(t, h, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("live-pipeline", ldr.ID, led.ID))
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
		t.Fatal("live pipeline did not reach tick 1")
	}
	before, ok := mgr.Status(created.ID)
	if !ok {
		t.Fatal("live pipeline has no status before simulate")
	}

	// Simulate a completely different graph — same device set, different
	// pipeline id/body, many steps — through the same server's /simulate.
	simRec := doRequest(t, h, http.MethodPost, "/api/v1/simulate", map[string]any{
		"pipeline": ldrLedExpressionSimBody(ldr.ID, led.ID),
		"steps":    50,
		"inputs":   map[string]any{ldr.ID: 100},
	})
	if simRec.Code != http.StatusOK {
		t.Fatalf("simulate: status = %d; body=%s", simRec.Code, simRec.Body.String())
	}

	after, ok := mgr.Status(created.ID)
	if !ok {
		t.Fatal("live pipeline has no status after simulate")
	}
	if after.Tick != before.Tick {
		t.Errorf("live pipeline tick changed from %d to %d after a simulate call", before.Tick, after.Tick)
	}
	if after.State != before.State {
		t.Errorf("live pipeline state changed from %v to %v after a simulate call", before.State, after.State)
	}

	// The live pipeline's runner must still be reachable through the exact
	// same manually-driven ticker/clock this test controls — no second
	// ticker/manager should have leaked in and replaced anything.
	mt.Fire(time.Unix(2, 0))
	if _, ok := waitForTick(mgr, created.ID, 2, 2*time.Second); !ok {
		t.Fatal("live pipeline did not advance to tick 2 after simulate call")
	}
}
