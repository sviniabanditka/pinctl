package api

import (
	"net/http"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/board"
)

// TestListBoards_FallbackSingleSession covers boards.go's fallback path
// (no board.Manager wired in — SetBoardManager never called): GET
// /api/v1/boards must still answer, synthesizing a single entry from the
// legacy sess/port fields.
func TestListBoards_FallbackSingleSession(t *testing.T) {
	h, fs := newTestServer(t)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/boards", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Boards []boardOut `json:"boards"`
	}
	mustDecode(t, rec, &resp)
	if len(resp.Boards) != 1 {
		t.Fatalf("len(boards) = %d, want 1", len(resp.Boards))
	}
	if resp.Boards[0].Status != "connected" {
		t.Errorf("status = %q, want connected", resp.Boards[0].Status)
	}

	fs.alive = false
	rec = doRequest(t, h, http.MethodGet, "/api/v1/boards", nil)
	mustDecode(t, rec, &resp)
	if resp.Boards[0].Status != "disconnected" {
		t.Errorf("status = %q, want disconnected after sess goes not-alive", resp.Boards[0].Status)
	}
}

func TestRescanBoards_ReturnsAccepted(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPost, "/api/v1/boards/rescan", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

// TestUpdateBoard_NoManagerReturns404 covers the fallback (no
// board.Manager, so there's no boards.json collection to rename into).
func TestUpdateBoard_NoManagerReturns404(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodPatch, "/api/v1/boards/brd_x", map[string]any{"name": "new name"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestUpdateBoard_WithManagerRenames wires a real board.Manager (no ports
// discovered, so nothing ever connects) into the Server and confirms PATCH
// renames the board and requires a non-empty name.
func TestUpdateBoard_WithManagerRenames(t *testing.T) {
	srv, boards := newTestServerWithBoards(t, "Board A")
	st := srv.store

	mgr := board.NewManager(board.ManagerConfig{
		Store:      st,
		PortLister: func() ([]board.PortInfo, error) { return nil, nil },
	})
	srv.SetBoardManager(mgr)

	rec := doRequest(t, srv, http.MethodPatch, "/api/v1/boards/"+boards[0].ID, map[string]any{"name": "Renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out boardOut
	mustDecode(t, rec, &out)
	if out.Name != "Renamed" {
		t.Errorf("Name = %q, want Renamed", out.Name)
	}

	rec = doRequest(t, srv, http.MethodPatch, "/api/v1/boards/"+boards[0].ID, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for empty name; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, srv, http.MethodPatch, "/api/v1/boards/brd_doesnotexist", map[string]any{"name": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown board; body=%s", rec.Code, rec.Body.String())
	}
}

// TestListBoards_WithManager confirms GET /api/v1/boards uses the real
// Manager snapshot (identity/portHint included) once one is wired in.
func TestListBoards_WithManager(t *testing.T) {
	srv, boards := newTestServerWithBoards(t, "Board A")
	st := srv.store

	mgr := board.NewManager(board.ManagerConfig{
		Store:      st,
		PortLister: func() ([]board.PortInfo, error) { return nil, nil },
	})
	srv.SetBoardManager(mgr)

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/boards", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Boards []boardOut `json:"boards"`
	}
	mustDecode(t, rec, &resp)
	if len(resp.Boards) != 1 {
		t.Fatalf("len(boards) = %d, want 1", len(resp.Boards))
	}
	if resp.Boards[0].ID != boards[0].ID {
		t.Errorf("ID = %q, want %q", resp.Boards[0].ID, boards[0].ID)
	}
	if resp.Boards[0].Status != "disconnected" {
		t.Errorf("status = %q, want disconnected (never discovered)", resp.Boards[0].Status)
	}
}
