package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestStatic_SPAFallback covers docs/10-api.md's "Static UI serving":
// a non-/api/* path that isn't a real embedded file (a client-side route
// like /pipelines/pl_x/edit) falls back to index.html.
func TestStatic_SPAFallback(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodGet, "/pipelines/pl_x/edit", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("body doesn't look like index.html: %s", rec.Body.String())
	}
}

// TestStatic_RootServesIndex covers the plain "/" case, which must also
// resolve to index.html (the common entry point, not just the fallback
// case for unknown paths).
func TestStatic_RootServesIndex(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("body doesn't look like index.html: %s", rec.Body.String())
	}
}

// TestStatic_UnknownAPIPathIs404JSON covers docs/10-api.md: "/api/* paths
// that don't match a defined endpoint return the standard JSON error
// envelope with 404, never the SPA fallback."
func TestStatic_UnknownAPIPathIs404JSON(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	er := decodeError(t, rec)
	if er.Error.Code != "not_found" {
		t.Errorf("error code = %q, want not_found", er.Error.Code)
	}
}
