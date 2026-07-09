package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// dialWS opens a WebSocket connection to ts at path, failing the test on
// error. The returned conn is closed automatically via t.Cleanup.
func dialWS(t *testing.T, ts *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + path
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func wsSubscribe(t *testing.T, conn *websocket.Conn, topic string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "topic": topic, "data": map[string]any{}}); err != nil {
		t.Fatalf("subscribe %s: %v", topic, err)
	}
}

func wsUnsubscribe(t *testing.T, conn *websocket.Conn, topic string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"type": "unsubscribe", "topic": topic, "data": map[string]any{}}); err != nil {
		t.Fatalf("unsubscribe %s: %v", topic, err)
	}
}

// readEnvelope reads and decodes the next frame, failing the test if none
// arrives within timeout.
func readEnvelope(t *testing.T, conn *websocket.Conn, timeout time.Duration) wsEnvelope {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var env wsEnvelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	return env
}

// expectNoEnvelope asserts no frame arrives within timeout (a read timeout
// error is the success case).
func expectNoEnvelope(t *testing.T, conn *websocket.Conn, timeout time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var env wsEnvelope
	err := conn.ReadJSON(&env)
	if err == nil {
		t.Fatalf("expected no message, got %+v", env)
	}
	if ne, ok := err.(interface{ Timeout() bool }); !ok || !ne.Timeout() {
		t.Fatalf("expected a read timeout, got: %v", err)
	}
}

// TestWS_DevicesTopic subscribes to the "devices" topic, then drives the
// device poller for one pass (StartPublishers) against a fakeDeviceIO whose
// reading changes — docs/10-api.md's devices topic payload
// {deviceId, value, timestamp} must arrive once the poller notices the
// change.
func TestWS_DevicesTopic(t *testing.T) {
	st := newTestStore(t)
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	io := newFakeDeviceIO()
	fs := newFakeSession()
	srv := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	dev := createTestDevice(t, srv, "LDR", 14, "analog-in")
	io.SetRead(dev.ID, engine.FloatValue(287))

	ctx, cancel := context.WithCancel(context.Background())
	stop := srv.StartPublishers(ctx)
	defer func() { cancel(); stop() }()

	conn := dialWS(t, ts, "/api/v1/ws")
	wsSubscribe(t, conn, "devices")

	env := readEnvelope(t, conn, 2*time.Second)
	if env.Type != "event" || env.Topic != "devices" {
		t.Fatalf("envelope = %+v, want type=event topic=devices", env)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want an object", env.Data)
	}
	if data["deviceId"] != dev.ID {
		t.Errorf("deviceId = %v, want %v", data["deviceId"], dev.ID)
	}
	if v, _ := data["value"].(float64); v != 287 {
		t.Errorf("value = %v, want 287", data["value"])
	}
	if data["timestamp"] == nil || data["timestamp"] == "" {
		t.Errorf("timestamp missing: %+v", data)
	}
}

// TestWS_DevicesTopic_NoSubscriptionNoEvents confirms a connected-but-
// unsubscribed client receives nothing from the devices poller.
func TestWS_DevicesTopic_NoSubscriptionNoEvents(t *testing.T) {
	st := newTestStore(t)
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	io := newFakeDeviceIO()
	fs := newFakeSession()
	srv := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	dev := createTestDevice(t, srv, "LDR", 14, "analog-in")
	io.SetRead(dev.ID, engine.FloatValue(500))

	ctx, cancel := context.WithCancel(context.Background())
	stop := srv.StartPublishers(ctx)
	defer func() { cancel(); stop() }()

	conn := dialWS(t, ts, "/api/v1/ws")
	// Deliberately no subscribe.
	expectNoEnvelope(t, conn, 500*time.Millisecond)
}

// pipelineSetup wires up a Server backed by a manually-driven clock/ticker
// (per docs/13-testing.md's determinism hooks) so a test can fire ticks on
// demand instead of racing real time, and creates+enables an LDR->LED
// pipeline through the REST API (mirroring
// TestEndToEnd_LDRTurnsOnLEDViaREST in pipelines_test.go).
func pipelineSetup(t *testing.T) (srv *Server, pipelineID string, fire func(tick int64), io *fakeDeviceIO) {
	t.Helper()
	st := newTestStore(t)
	fs := newFakeSession()
	io = newFakeDeviceIO()
	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := engine.NewManager(engine.WithClock(clk), engine.WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)
	srv = NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	ldr := createTestDevice(t, srv, "LDR", 14, "analog-in")
	led := createTestDevice(t, srv, "LED", 13, "digital-out")
	io.SetRead(ldr.ID, engine.FloatValue(900)) // bright: LED off initially

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/pipelines", ldrLedPipelineBody("ldr-led", ldr.ID, led.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created model.Pipeline
	mustDecode(t, rec, &created)

	rec = doRequest(t, srv, http.MethodPost, "/api/v1/pipelines/"+created.ID+"/enable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable pipeline: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	mt := <-tickers
	fire = func(tick int64) { mt.Fire(time.Unix(tick, 0)) }
	return srv, created.ID, fire, io
}

// TestWS_PipelineDebugTopic subscribes to "pipeline:{id}" and asserts the
// per-tick snapshot (docs/10-api.md: {pipelineId, tick, nodes}) arrives
// with edge values matching the tick's evaluation.
func TestWS_PipelineDebugTopic(t *testing.T) {
	srv, pipelineID, fire, _ := pipelineSetup(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	conn := dialWS(t, ts, "/api/v1/ws")
	wsSubscribe(t, conn, "pipeline:"+pipelineID)

	// Give the subscribe message time to register before firing the tick —
	// subscription happens on the WS read goroutine, asynchronously from
	// this test goroutine.
	time.Sleep(50 * time.Millisecond)
	fire(1)

	env := readEnvelope(t, conn, 2*time.Second)
	if env.Type != "event" || env.Topic != "pipeline:"+pipelineID {
		t.Fatalf("envelope = %+v, want type=event topic=pipeline:%s", env, pipelineID)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want an object", env.Data)
	}
	if data["pipelineId"] != pipelineID {
		t.Errorf("pipelineId = %v, want %v", data["pipelineId"], pipelineID)
	}
	if tick, _ := data["tick"].(float64); tick != 1 {
		t.Errorf("tick = %v, want 1", data["tick"])
	}
	nodes, ok := data["nodes"].(map[string]any)
	if !ok {
		t.Fatalf("nodes = %#v, want an object", data["nodes"])
	}
	// n1 = device-read(LDR) = 900 (bright); n2 = compare(lt 300) = false
	// (900 is not < 300); device-write (n3) is a sink, omitted from nodes.
	if v, _ := nodes["n1"].(float64); v != 900 {
		t.Errorf("nodes[n1] = %v, want 900", nodes["n1"])
	}
	if v, ok := nodes["n2"].(bool); !ok || v != false {
		t.Errorf("nodes[n2] = %v, want false", nodes["n2"])
	}
	if _, ok := nodes["n3"]; ok {
		t.Errorf("nodes[n3] present, want omitted (device-write is a sink, no output port)")
	}
	if _, ok := data["log"]; ok {
		t.Errorf(`data["log"] present = %v, want omitted (v0.3 has no log node type)`, data["log"])
	}
}

// TestWS_PipelineDebugTopic_UnknownPipeline covers docs/10-api.md's
// "Invalid subscribe (unknown pipeline)" example: subscribing to a
// pipeline:{id} for an id the store doesn't know returns a "error" frame,
// not silence.
func TestWS_PipelineDebugTopic_UnknownPipeline(t *testing.T) {
	st := newTestStore(t)
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	srv := NewServer(newFakeSession(), "/dev/ttyACM0", st, mgr, newFakeDeviceIO())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	conn := dialWS(t, ts, "/api/v1/ws")
	wsSubscribe(t, conn, "pipeline:pl_doesnotexist")

	env := readEnvelope(t, conn, 2*time.Second)
	if env.Type != "error" || env.Topic != "pipeline:pl_doesnotexist" {
		t.Fatalf("envelope = %+v, want type=error topic=pipeline:pl_doesnotexist", env)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want an object", env.Data)
	}
	if data["code"] != "not_found" {
		t.Errorf("code = %v, want not_found", data["code"])
	}
}

// TestWS_PipelineDebugTopic_Unsubscribe verifies unsubscribing stops
// further per-tick snapshots from arriving.
func TestWS_PipelineDebugTopic_Unsubscribe(t *testing.T) {
	srv, pipelineID, fire, _ := pipelineSetup(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	conn := dialWS(t, ts, "/api/v1/ws")
	topic := "pipeline:" + pipelineID
	wsSubscribe(t, conn, topic)
	time.Sleep(50 * time.Millisecond)

	fire(1)
	_ = readEnvelope(t, conn, 2*time.Second) // tick 1 snapshot, confirms the subscription was live

	wsUnsubscribe(t, conn, topic)
	time.Sleep(50 * time.Millisecond)

	fire(2)
	expectNoEnvelope(t, conn, 500*time.Millisecond)
}

// TestWS_Ping covers the app-level ping/pong liveness exchange documented
// in docs/10-api.md.
func TestWS_Ping(t *testing.T) {
	st := newTestStore(t)
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	srv := NewServer(newFakeSession(), "/dev/ttyACM0", st, mgr, newFakeDeviceIO())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	conn := dialWS(t, ts, "/api/v1/ws")
	if err := conn.WriteJSON(map[string]any{"type": "ping", "topic": "", "data": map[string]any{}}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	env := readEnvelope(t, conn, 2*time.Second)
	if env.Type != "pong" {
		t.Fatalf("envelope = %+v, want type=pong", env)
	}
}
