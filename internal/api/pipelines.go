package api

import (
	"log"
	"net/http"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// maxPipelineBodyBytes is generous: a pipeline body carries every node's
// config and position, unlike the small device bodies.
const maxPipelineBodyBytes = 1 << 20

// pipelineRequest is the shape accepted by POST /pipelines and PUT
// /pipelines/{id} — docs/10-api.md: "name, tickMs, nodes, edges" for both
// (PUT is a full replace of the graph, not of the whole persisted object).
// Deliberately has no `enabled` field: a pipeline's enabled state is only
// ever changed via POST .../enable and .../disable, never smuggled through
// a graph-replace body — POST create always starts disabled, and PUT
// preserves whatever enabled state the pipeline already had (see
// handleCreatePipeline/handleUpdatePipeline).
type pipelineRequest struct {
	Name   string       `json:"name"`
	TickMs int          `json:"tickMs"`
	Nodes  []model.Node `json:"nodes"`
	Edges  []model.Edge `json:"edges"`
}

func (req pipelineRequest) toModel() model.Pipeline {
	return model.Pipeline{
		Name:   req.Name,
		TickMs: req.TickMs,
		Nodes:  req.Nodes,
		Edges:  req.Edges,
	}
}

// validationErrorOut/validationWarningOut are the docs/10-api.md
// {field, code, message} shape. Deviation: engine.ValidationError only
// carries a NodeID and a free-form message, not a structured field path or
// a stable machine code per failure kind (docs' example shows
// "edges[1].to" / "dangling_edge") — Field is populated with NodeID (empty
// for pipeline-wide errors) and Code is a generic "validation_error"; the
// full detail lives in Message. Warning's Code is hardcoded to
// "multiple_writers" since that is, per validate.go, the only warning kind
// the engine currently produces.
type validationErrorOut struct {
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type validationWarningOut struct {
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func toValidationErrorsOut(errs engine.ValidationErrors) []validationErrorOut {
	out := make([]validationErrorOut, len(errs))
	for i, e := range errs {
		out[i] = validationErrorOut{Field: e.NodeID, Code: "validation_error", Message: e.Msg}
	}
	return out
}

func toWarningsOut(ws []engine.Warning) []validationWarningOut {
	out := make([]validationWarningOut, len(ws))
	for i, w := range ws {
		out[i] = validationWarningOut{Field: w.NodeID, Code: "multiple_writers", Message: w.Msg}
	}
	return out
}

// validationErrorDetails is the docs/10-api.md error envelope's
// endpoint-specific "details" payload for a failed pipeline validation.
type validationErrorDetails struct {
	Errors   []validationErrorOut   `json:"errors"`
	Warnings []validationWarningOut `json:"warnings,omitempty"`
}

type validationErrorEnvelope struct {
	Error struct {
		Code    string                 `json:"code"`
		Message string                 `json:"message"`
		Details validationErrorDetails `json:"details"`
	} `json:"error"`
}

// writeValidationError writes the docs/10-api.md error envelope for a
// failed pipeline validation (create/update/enable), details.errors and
// details.warnings carrying the full validation result.
func writeValidationError(w http.ResponseWriter, verrs engine.ValidationErrors, warnings []engine.Warning) {
	var env validationErrorEnvelope
	env.Error.Code = "validation_error"
	env.Error.Message = verrs.Error()
	env.Error.Details = validationErrorDetails{
		Errors:   toValidationErrorsOut(verrs),
		Warnings: toWarningsOut(warnings),
	}
	writeJSON(w, http.StatusBadRequest, env)
}

// pipelineSummary is the docs/10-api.md list-view shape: everything but
// nodes/edges, plus runtime status.
type pipelineSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	TickMs    int    `json:"tickMs"`
	NodeCount int    `json:"nodeCount"`
	Status    string `json:"status"`
}

// pipelineStatus maps a pipeline's persisted enabled flag plus its
// engine.Manager runtime Status to the docs/10-api.md status string.
// Deviation: docs lists a "paused" status for "board offline", which is a
// v0.4 concern (board disconnect/reconnect handling, docs/12-roadmap.md);
// v0.2 only distinguishes stopped/running/error.
func (s *Server) pipelineStatus(p model.Pipeline) string {
	if !p.Enabled {
		return "stopped"
	}
	st, ok := s.mgr.Status(p.ID)
	if !ok {
		return "stopped"
	}
	if st.State == engine.StateRunning {
		return "running"
	}
	return "error" // StateError or StateDisabled (auto-disabled after repeated tick failures)
}

func (s *Server) summarize(p model.Pipeline) pipelineSummary {
	return pipelineSummary{
		ID:        p.ID,
		Name:      p.Name,
		Enabled:   p.Enabled,
		TickMs:    p.TickMs,
		NodeCount: len(p.Nodes),
		Status:    s.pipelineStatus(p),
	}
}

func (s *Server) handleListPipelines(w http.ResponseWriter, r *http.Request) {
	list := s.store.ListPipelines()
	out := make([]pipelineSummary, len(list))
	for i, p := range list {
		out[i] = s.summarize(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipelines": out})
}

func (s *Server) handleGetPipeline(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPipeline(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// pipelineSaveResponse embeds the saved pipeline plus any non-fatal
// warnings from the validation pass that ran before saving it — docs/10:
// "PUT... runs the same validation before persisting and returns warnings
// alongside the saved pipeline".
type pipelineSaveResponse struct {
	model.Pipeline
	Warnings []validationWarningOut `json:"warnings,omitempty"`
}

// startOrStop hot-swaps (or stops) the runner for p via Manager.Start,
// which itself handles both cases: p.Enabled -> compile and swap in a new
// runner; !p.Enabled -> stop any existing runner. A failure here (should
// not happen — p was already validated against the same devices) is
// logged, not surfaced as an HTTP error, since the pipeline is already
// durably saved at this point.
func (s *Server) startOrStop(p model.Pipeline, devices map[string]model.Device) {
	if err := s.mgr.Start(p, devices, s.io); err != nil {
		log.Printf("api: starting pipeline %s: %v", p.ID, err)
	}
}

func (s *Server) handleCreatePipeline(w http.ResponseWriter, r *http.Request) {
	var req pipelineRequest
	if !decodeJSONBody(w, r, maxPipelineBodyBytes, &req) {
		return
	}

	p := req.toModel()
	devices := s.devicesMap()
	_, warnings, verr := engine.Validate(p, devices)
	if verr != nil {
		ve, _ := verr.(engine.ValidationErrors)
		writeValidationError(w, ve, nil)
		return
	}

	saved, err := s.store.CreatePipeline(p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.startOrStop(saved, devices)
	writeJSON(w, http.StatusCreated, pipelineSaveResponse{Pipeline: saved, Warnings: toWarningsOut(warnings)})
}

func (s *Server) handleUpdatePipeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req pipelineRequest
	if !decodeJSONBody(w, r, maxPipelineBodyBytes, &req) {
		return
	}

	// Fetch the existing pipeline first: it doubles as the 404 check (PUT
	// on an unknown id) and as the source of the enabled flag the request
	// body doesn't carry — PUT replaces the graph, not the running state
	// (docs/10-api.md).
	existing, err := s.store.GetPipeline(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	p := req.toModel()
	p.Enabled = existing.Enabled
	devices := s.devicesMap()
	_, warnings, verr := engine.Validate(p, devices)
	if verr != nil {
		ve, _ := verr.(engine.ValidationErrors)
		writeValidationError(w, ve, nil)
		return
	}

	saved, err := s.store.UpdatePipeline(id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.startOrStop(saved, devices)
	writeJSON(w, http.StatusOK, pipelineSaveResponse{Pipeline: saved, Warnings: toWarningsOut(warnings)})
}

func (s *Server) handleDeletePipeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mgr.Stop(id)
	if err := s.store.DeletePipeline(id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type pipelineActionResponse struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"`
}

func (s *Server) handleEnablePipeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.store.GetPipeline(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	devices := s.devicesMap()
	check := p
	check.Enabled = true
	_, _, verr := engine.Validate(check, devices)
	if verr != nil {
		ve, _ := verr.(engine.ValidationErrors)
		writeValidationError(w, ve, nil)
		return
	}

	updated, err := s.store.SetEnabled(id, true)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.startOrStop(updated, devices)
	writeJSON(w, http.StatusOK, pipelineActionResponse{ID: updated.ID, Enabled: updated.Enabled, Status: s.pipelineStatus(updated)})
}

func (s *Server) handleDisablePipeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	updated, err := s.store.SetEnabled(id, false)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.mgr.Stop(id)
	writeJSON(w, http.StatusOK, pipelineActionResponse{ID: updated.ID, Enabled: updated.Enabled, Status: s.pipelineStatus(updated)})
}

// validateResponse is the docs/10-api.md POST /pipelines/validate shape:
// always 200 once the request body itself parsed, valid reflects whether
// the pipeline passed.
type validateResponse struct {
	Valid    bool                   `json:"valid"`
	Errors   []validationErrorOut   `json:"errors,omitempty"`
	Warnings []validationWarningOut `json:"warnings,omitempty"`
}

func (s *Server) handleValidatePipeline(w http.ResponseWriter, r *http.Request) {
	var req pipelineRequest
	if !decodeJSONBody(w, r, maxPipelineBodyBytes, &req) {
		return
	}

	p := req.toModel()
	devices := s.devicesMap()
	_, warnings, verr := engine.Validate(p, devices)
	if verr != nil {
		ve, _ := verr.(engine.ValidationErrors)
		writeJSON(w, http.StatusOK, validateResponse{Valid: false, Errors: toValidationErrorsOut(ve)})
		return
	}
	writeJSON(w, http.StatusOK, validateResponse{Valid: true, Warnings: toWarningsOut(warnings)})
}
