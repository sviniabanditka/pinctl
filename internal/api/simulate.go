package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/simulate"
)

// simulateRequest is the docs/18-simulation.md POST /api/v1/simulate body.
// pipeline reuses pipelineRequest's shape exactly ("a full pipeline body,
// exactly as PUT /pipelines/{id} takes it"); its enabled flag doesn't exist
// on pipelineRequest at all (same as PUT), and simulate.Run ignores
// whatever Enabled ends up as regardless.
type simulateRequest struct {
	Pipeline  pipelineRequest       `json:"pipeline"`
	Steps     int                   `json:"steps"`
	StartTime time.Time             `json:"startTime"`
	Inputs    map[string]float64    `json:"inputs"`
	Script    []simulateScriptEntry `json:"script"`
}

type simulateScriptEntry struct {
	Tick     int     `json:"tick"`
	DeviceID string  `json:"deviceId"`
	Value    float64 `json:"value"`
}

func (req simulateRequest) toSimRequest() simulate.Request {
	script := make([]simulate.ScriptEntry, len(req.Script))
	for i, e := range req.Script {
		script[i] = simulate.ScriptEntry{Tick: e.Tick, DeviceID: e.DeviceID, Value: e.Value}
	}
	return simulate.Request{
		Pipeline:  req.Pipeline.toModel(),
		Steps:     req.Steps,
		StartTime: req.StartTime,
		Inputs:    req.Inputs,
		Script:    script,
	}
}

// simulateWarningOut is the docs/18-simulation.md warnings[] shape:
// {code, deviceId}, distinct from validationWarningOut's {field, code,
// message} — a simulation warning names a device, not a pipeline field.
type simulateWarningOut struct {
	Code     string `json:"code"`
	DeviceID string `json:"deviceId"`
}

// simulateFrameOut is one docs/18-simulation.md frames[] entry. Nodes/
// Writes are already the plain bool/float64 shape simulate.Frame carries;
// Log is converted through the same wire logEntry type (and valueJSON) the
// WS pipeline:{id} stream uses (ws.go's snapshotLogJSON), so a simulated
// frame and a live tick's log render with identical client code.
type simulateFrameOut struct {
	Tick   int            `json:"tick"`
	At     time.Time      `json:"at"`
	Nodes  map[string]any `json:"nodes"`
	Writes map[string]any `json:"writes"`
	Log    []logEntry     `json:"log"`
}

type simulateErrorOut struct {
	Tick    int    `json:"tick"`
	NodeID  string `json:"nodeId"`
	Message string `json:"message"`
}

type simulateResponse struct {
	TickMs   int                  `json:"tickMs"`
	Warnings []simulateWarningOut `json:"warnings,omitempty"`
	Frames   []simulateFrameOut   `json:"frames"`
	Error    *simulateErrorOut    `json:"error,omitempty"`
}

func toSimulateResponse(res simulate.Result) simulateResponse {
	frames := make([]simulateFrameOut, len(res.Frames))
	for i, f := range res.Frames {
		frames[i] = simulateFrameOut{
			Tick:   f.Tick,
			At:     f.At,
			Nodes:  f.Nodes,
			Writes: f.Writes,
			Log:    snapshotLogJSON(f.Log),
		}
	}
	warnings := make([]simulateWarningOut, len(res.Warnings))
	for i, w := range res.Warnings {
		warnings[i] = simulateWarningOut{Code: w.Code, DeviceID: w.DeviceID}
	}
	var errOut *simulateErrorOut
	if res.Error != nil {
		errOut = &simulateErrorOut{Tick: res.Error.Tick, NodeID: res.Error.NodeID, Message: res.Error.Message}
	}
	return simulateResponse{TickMs: res.TickMs, Warnings: warnings, Frames: frames, Error: errOut}
}

// handleSimulate implements POST /api/v1/simulate (docs/18-simulation.md).
// It resolves devices from the store for role/pin metadata only — the
// simulated run itself (simulate.Run) never touches s.mgr or s.io, the
// live engine.Manager/DeviceIO this Server drives real pipelines through,
// so a simulated run can never affect (or be affected by) a running
// pipeline.
func (s *Server) handleSimulate(w http.ResponseWriter, r *http.Request) {
	var req simulateRequest
	if !decodeJSONBody(w, r, maxPipelineBodyBytes, &req) {
		return
	}

	if req.Steps > simulate.MaxSteps {
		writeBadRequest(w, fmt.Sprintf("steps %d exceeds the cap of %d", req.Steps, simulate.MaxSteps))
		return
	}

	devices := s.devicesMap()
	result, err := simulate.Run(req.toSimRequest(), devices)
	if err != nil {
		if ve, ok := err.(engine.ValidationErrors); ok {
			writeValidationError(w, ve, nil)
			return
		}
		writeBadRequest(w, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toSimulateResponse(result))
}
