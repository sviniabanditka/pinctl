package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/model"
)

func mustOpen(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	tmpDir := filepath.Join(dir, ".tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("reading tmp dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf(".tmp dir not empty after write: %v", names)
	}
}

func TestDeviceRoundTripAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	d, err := s.CreateDevice(model.Device{
		Name:    "LDR on A0",
		BoardID: "brd_1",
		Pin:     0,
		Role:    model.RoleAnalogIn,
	})
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if d.ID == "" || d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		t.Fatalf("CreateDevice did not populate ID/timestamps: %+v", d)
	}
	assertNoTempFiles(t, dir)

	s2 := mustOpen(t, dir)
	got, err := s2.GetDevice(d.ID)
	if err != nil {
		t.Fatalf("GetDevice after reopen: %v", err)
	}
	if got.Name != d.Name || got.BoardID != d.BoardID || got.Pin != d.Pin {
		t.Fatalf("device did not survive reopen: got %+v, want %+v", got, d)
	}
}

func TestPipelineRoundTripAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	p, err := s.CreatePipeline(model.Pipeline{
		Name:    "Porch light at dusk",
		Enabled: true,
		TickMs:  20,
	})
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	if p.ID == "" || p.SchemaVersion != currentPipelineSchemaVersion {
		t.Fatalf("CreatePipeline did not populate ID/schemaVersion: %+v", p)
	}
	assertNoTempFiles(t, dir)

	path := filepath.Join(dir, "pipelines", p.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected pipeline file %s: %v", path, err)
	}

	s2 := mustOpen(t, dir)
	got, err := s2.GetPipeline(p.ID)
	if err != nil {
		t.Fatalf("GetPipeline after reopen: %v", err)
	}
	if got.Name != p.Name || got.Enabled != p.Enabled || got.TickMs != p.TickMs {
		t.Fatalf("pipeline did not survive reopen: got %+v, want %+v", got, p)
	}
}

func TestDeviceUpdateAndDelete(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	d, err := s.CreateDevice(model.Device{Name: "LED", BoardID: "brd_1", Pin: 13, Role: model.RoleDigitalOut})
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	updated, err := s.UpdateDevice(d.ID, model.Device{Name: "Porch LED", BoardID: "brd_1", Pin: 13, Role: model.RoleDigitalOut})
	if err != nil {
		t.Fatalf("UpdateDevice: %v", err)
	}
	if updated.Name != "Porch LED" || updated.ID != d.ID || updated.CreatedAt != d.CreatedAt {
		t.Fatalf("UpdateDevice did not preserve ID/CreatedAt: %+v", updated)
	}
	if !updated.UpdatedAt.After(d.UpdatedAt) && updated.UpdatedAt != d.UpdatedAt {
		t.Fatalf("expected UpdatedAt to change or stay >= original")
	}
	assertNoTempFiles(t, dir)

	if err := s.DeleteDevice(d.ID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	assertNoTempFiles(t, dir)

	if _, err := s.GetDevice(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	s2 := mustOpen(t, dir)
	if _, err := s2.GetDevice(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted device reappeared after reopen: %v", err)
	}
}

func TestPipelineDeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	p, err := s.CreatePipeline(model.Pipeline{Name: "temp", TickMs: 20})
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	path := filepath.Join(dir, "pipelines", p.ID+".json")

	if err := s.DeletePipeline(p.ID); err != nil {
		t.Fatalf("DeletePipeline: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected pipeline file to be removed, stat err = %v", err)
	}
	if _, err := s.GetPipeline(p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestSetEnabled(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	p, err := s.CreatePipeline(model.Pipeline{Name: "p", TickMs: 20, Enabled: false})
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	updated, err := s.SetEnabled(p.ID, true)
	if err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if !updated.Enabled {
		t.Fatalf("expected Enabled true after SetEnabled")
	}

	s2 := mustOpen(t, dir)
	got, err := s2.GetPipeline(p.ID)
	if err != nil {
		t.Fatalf("GetPipeline after reopen: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("SetEnabled did not persist across reopen")
	}
}

func TestPinUniquenessConflict(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	if _, err := s.CreateDevice(model.Device{Name: "A", BoardID: "brd_1", Pin: 5, Role: model.RoleDigitalOut}); err != nil {
		t.Fatalf("CreateDevice A: %v", err)
	}
	_, err := s.CreateDevice(model.Device{Name: "B", BoardID: "brd_1", Pin: 5, Role: model.RoleDigitalOut})
	if !errors.Is(err, ErrPinInUse) {
		t.Fatalf("expected ErrPinInUse, got %v", err)
	}

	// Different board, same pin number is fine.
	if _, err := s.CreateDevice(model.Device{Name: "C", BoardID: "brd_2", Pin: 5, Role: model.RoleDigitalOut}); err != nil {
		t.Fatalf("CreateDevice on different board should succeed: %v", err)
	}
}

func TestPinUniquenessAllowsUpdatingSelf(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	d, err := s.CreateDevice(model.Device{Name: "A", BoardID: "brd_1", Pin: 5, Role: model.RoleDigitalOut})
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	// Updating the same device, keeping its own (boardId, pin), must not
	// conflict with itself.
	if _, err := s.UpdateDevice(d.ID, model.Device{Name: "A renamed", BoardID: "brd_1", Pin: 5, Role: model.RoleDigitalOut}); err != nil {
		t.Fatalf("UpdateDevice should not conflict with itself: %v", err)
	}
}

func TestNameEmptyRejected(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	if _, err := s.CreateDevice(model.Device{Name: "", BoardID: "brd_1", Pin: 1, Role: model.RoleDigitalOut}); !errors.Is(err, ErrNameEmpty) {
		t.Fatalf("expected ErrNameEmpty, got %v", err)
	}
	if _, err := s.CreatePipeline(model.Pipeline{Name: "", TickMs: 20}); !errors.Is(err, ErrNameEmpty) {
		t.Fatalf("expected ErrNameEmpty, got %v", err)
	}
}

func TestUnknownSchemaVersionSkippedOnLoad(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "pipelines"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A future-schema devices.json: must be skipped (treated as empty), not
	// fatal to Open.
	if err := os.WriteFile(filepath.Join(dir, "devices.json"), []byte(`{"schemaVersion":999,"devices":[{"id":"dev_x","name":"future","boardId":"brd_1","pin":1,"role":"digital-out"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A future-schema pipeline file: same treatment.
	if err := os.WriteFile(filepath.Join(dir, "pipelines", "pl_future.json"), []byte(`{"schemaVersion":999,"id":"pl_future","name":"future","tickMs":20}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := mustOpen(t, dir)

	if devices := s.ListDevices(); len(devices) != 0 {
		t.Fatalf("expected devices from unknown-schema-version file to be skipped, got %+v", devices)
	}
	if pipelines := s.ListPipelines(); len(pipelines) != 0 {
		t.Fatalf("expected pipelines from unknown-schema-version file to be skipped, got %+v", pipelines)
	}
}

func TestCorruptedJSONSkippedOnLoad(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "pipelines"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devices.json"), []byte(`{not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pipelines", "pl_broken.json"), []byte(`{not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A valid pipeline alongside the broken one must still load.
	validPath := filepath.Join(dir, "pipelines", "pl_good.json")
	if err := os.WriteFile(validPath, []byte(`{"schemaVersion":1,"id":"pl_good","name":"good","enabled":false,"tickMs":20,"nodes":[],"edges":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Open must not fail (corrupted files are skipped, not fatal to
	// startup, per docs/09-data-model.md's versioning policy).
	s := mustOpen(t, dir)

	if devices := s.ListDevices(); len(devices) != 0 {
		t.Fatalf("expected corrupted devices.json to be skipped, got %+v", devices)
	}
	pipelines := s.ListPipelines()
	if len(pipelines) != 1 || pipelines[0].ID != "pl_good" {
		t.Fatalf("expected only pl_good to load, got %+v", pipelines)
	}
}

func TestDeleteDeviceReferencedByPipelineRejected(t *testing.T) {
	// docs/09-data-model.md "Validation rules": a device cannot be deleted
	// while any pipeline references its id -> ErrDeviceInUse (409 at the
	// API layer).
	dir := t.TempDir()
	s := mustOpen(t, dir)

	d, err := s.CreateDevice(model.Device{Name: "A", BoardID: "brd_1", Pin: 1, Role: model.RoleDigitalOut})
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if _, err := s.CreatePipeline(model.Pipeline{
		Name:   "uses A",
		TickMs: 20,
		Nodes: []model.Node{
			{ID: "n1", Type: "device-write", Config: []byte(`{"deviceId":"` + d.ID + `"}`)},
		},
	}); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	if err := s.DeleteDevice(d.ID); !errors.Is(err, ErrDeviceInUse) {
		t.Fatalf("expected ErrDeviceInUse, got %v", err)
	}

	// The device must still exist after the rejected delete.
	if _, err := s.GetDevice(d.ID); err != nil {
		t.Fatalf("device should still exist after rejected delete: %v", err)
	}
}

func TestDeleteDeviceUnreferencedSucceeds(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	d, err := s.CreateDevice(model.Device{Name: "A", BoardID: "brd_1", Pin: 1, Role: model.RoleDigitalOut})
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	// A pipeline that exists but does not reference d must not block the
	// delete.
	other, err := s.CreateDevice(model.Device{Name: "B", BoardID: "brd_1", Pin: 2, Role: model.RoleDigitalOut})
	if err != nil {
		t.Fatalf("CreateDevice B: %v", err)
	}
	if _, err := s.CreatePipeline(model.Pipeline{
		Name:   "uses B",
		TickMs: 20,
		Nodes: []model.Node{
			{ID: "n1", Type: "device-write", Config: []byte(`{"deviceId":"` + other.ID + `"}`)},
		},
	}); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	if err := s.DeleteDevice(d.ID); err != nil {
		t.Fatalf("DeleteDevice unreferenced should succeed: %v", err)
	}
	if _, err := s.GetDevice(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
