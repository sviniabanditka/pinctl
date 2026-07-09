package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/history"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// fakeHistoryStore is a HistoryStore test double: Query returns a fixed
// slice (or an error), DeleteDevice records what was deleted.
type fakeHistoryStore struct {
	samples   []history.Sample
	queryErr  error
	lastQuery history.Query
	lastID    string
	deleted   []string
}

func (f *fakeHistoryStore) Query(deviceID string, q history.Query) ([]history.Sample, error) {
	f.lastID = deviceID
	f.lastQuery = q
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return f.samples, nil
}

func (f *fakeHistoryStore) DeleteDevice(deviceID string) error {
	f.deleted = append(f.deleted, deviceID)
	return nil
}

// newHistoryTestServer builds a Server with one analog-in device and a
// fakeHistoryStore wired in via SetHistoryStore, unless enabled is false
// (history_disabled: no HistoryStore wired in at all).
func newHistoryTestServer(t *testing.T, enabled bool) (*Server, *fakeHistoryStore, string) {
	t.Helper()
	st := newTestStore(t)
	fs := newFakeSession()
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	io := newFakeDeviceIO()
	srv := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	d, err := st.CreateDevice(model.Device{
		Name: "Porch sensor",
		Pin:  14,
		Role: model.RoleAnalogIn,
	})
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	var fh *fakeHistoryStore
	if enabled {
		fh = &fakeHistoryStore{}
		srv.SetHistoryStore(fh)
	}
	return srv, fh, d.ID
}

func TestDeviceHistory_RawOK(t *testing.T) {
	srv, fh, id := newHistoryTestServer(t, true)
	now := time.Now().UTC().Truncate(time.Second)
	fh.samples = []history.Sample{
		{T: now.Add(-30 * time.Second), V: 500},
		{T: now, V: 512},
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/devices/"+id+"/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	mustDecode(t, rec, &out)
	if out["deviceId"] != id {
		t.Errorf("deviceId = %v, want %v", out["deviceId"], id)
	}
	if out["role"] != "analog-in" {
		t.Errorf("role = %v, want analog-in", out["role"])
	}
	samples, ok := out["samples"].([]any)
	if !ok || len(samples) != 2 {
		t.Fatalf("samples = %v, want 2 entries", out["samples"])
	}
	if fh.lastQuery.Bucket != 0 {
		t.Errorf("expected raw query (bucket=0), got %v", fh.lastQuery.Bucket)
	}
}

func TestDeviceHistory_BucketedOK(t *testing.T) {
	srv, fh, id := newHistoryTestServer(t, true)
	min0, max0 := 505.0, 519.0
	fh.samples = []history.Sample{
		{T: time.Now().UTC(), V: 512.4, Min: &min0, Max: &max0},
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/devices/"+id+"/history?bucket=30s", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	mustDecode(t, rec, &out)
	if out["bucket"] != "30s" {
		t.Errorf("bucket = %v, want 30s", out["bucket"])
	}
	samples := out["samples"].([]any)
	sample0 := samples[0].(map[string]any)
	if _, ok := sample0["min"]; !ok {
		t.Error("bucketed sample missing min")
	}
	if _, ok := sample0["max"]; !ok {
		t.Error("bucketed sample missing max")
	}
	if fh.lastQuery.Bucket != 30*time.Second {
		t.Errorf("Query.Bucket = %v, want 30s", fh.lastQuery.Bucket)
	}
}

func TestDeviceHistory_UnknownDevice404(t *testing.T) {
	srv, _, _ := newHistoryTestServer(t, true)
	rec := doRequest(t, srv, http.MethodGet, "/api/v1/devices/dev_nope/history", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeviceHistory_BadDuration400(t *testing.T) {
	srv, _, id := newHistoryTestServer(t, true)
	rec := doRequest(t, srv, http.MethodGet, "/api/v1/devices/"+id+"/history?bucket=notaduration", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeviceHistory_FromAfterTo400(t *testing.T) {
	srv, _, id := newHistoryTestServer(t, true)
	now := time.Now().UTC()
	url := fmt.Sprintf("/api/v1/devices/%s/history?from=%s&to=%s", id,
		now.Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339))
	rec := doRequest(t, srv, http.MethodGet, url, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeviceHistory_LimitOverCap400(t *testing.T) {
	srv, _, id := newHistoryTestServer(t, true)
	rec := doRequest(t, srv, http.MethodGet, "/api/v1/devices/"+id+"/history?limit=10001", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeviceHistory_Disabled503(t *testing.T) {
	srv, _, id := newHistoryTestServer(t, false)
	rec := doRequest(t, srv, http.MethodGet, "/api/v1/devices/"+id+"/history", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	mustDecode(t, rec, &out)
	errObj, _ := out["error"].(map[string]any)
	if errObj["code"] != "history_disabled" {
		t.Errorf("error.code = %v, want history_disabled", errObj["code"])
	}
}

func TestDeleteDevice_DeletesHistoryBestEffort(t *testing.T) {
	srv, fh, id := newHistoryTestServer(t, true)
	rec := doRequest(t, srv, http.MethodDelete, "/api/v1/devices/"+id, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(fh.deleted) != 1 || fh.deleted[0] != id {
		t.Errorf("history.DeleteDevice calls = %v, want [%s]", fh.deleted, id)
	}
}
