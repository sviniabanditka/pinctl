// Background publishers driving the boards/devices/pipelines WS topics
// (docs/10-api.md). Device values and board status are poll-based (there is
// no push interface from internal/board/boardio for either); pipeline
// lifecycle changes are published both immediately at the point internal/
// api itself mutates enabled state (pipelines.go's handleEnablePipeline/
// handleDisablePipeline etc.) and via a slower poll here that catches
// transitions the engine makes on its own (e.g. auto-disable after
// repeated tick failures, docs/07-pipeline-engine.md "Runtime error
// handling") that no REST call ever triggers.
package api

import (
	"context"
	"sync"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

const (
	// devicePollInterval matches docs/10-api.md's devices topic throttle
	// ("coalesced to at most one update per device per ~200ms").
	devicePollInterval = 200 * time.Millisecond
	// boardPollInterval: board status changes rarely; polling at the same
	// cadence as devices is cheap (single sess.Alive() call) and keeps the
	// two publishers on one simple ticker pattern.
	boardPollInterval = 200 * time.Millisecond
	// pipelineStatusPollInterval is coarser: it exists only to catch
	// engine-internal state transitions (e.g. auto-disable) that didn't
	// come through a REST mutation, so sub-second precision isn't needed.
	pipelineStatusPollInterval = time.Second
)

// deviceEvent is the docs/10-api.md "devices" topic payload.
type deviceEvent struct {
	DeviceID  string    `json:"deviceId"`
	Value     any       `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// boardEvent is the docs/10-api.md "boards" topic payload. v0.3 is still
// single-board (docs/12-roadmap.md), so BoardID is always defaultBoardID
// and Port is the one serial port the process was started against.
type boardEvent struct {
	BoardID string  `json:"boardId"`
	Status  string  `json:"status"`
	Port    *string `json:"port"`
}

// pipelineEvent is the docs/10-api.md "pipelines" topic payload.
type pipelineEvent struct {
	PipelineID string `json:"pipelineId"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
}

// publishPipelineStatus broadcasts id's current status on the "pipelines"
// topic. Called both directly from the enable/disable/create/update/delete
// handlers (immediate) and from runPipelineStatusPoller (catches
// engine-internal transitions).
func (s *Server) publishPipelineStatus(id, status string) {
	s.hub.broadcast(topicPipelines, pipelineEvent{PipelineID: id, Status: status})
}

// StartPublishers launches the background goroutines driving the boards/
// devices/pipelines WS topics. It returns a stop func that cancels them and
// blocks until all have exited — call it during graceful shutdown,
// alongside engine.Manager.StopAll and before the WS hub is torn down, so
// no publisher goroutine sends on a hub whose clients are already gone.
func (s *Server) StartPublishers(ctx context.Context) (stop func()) {
	pubCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup

	runners := []func(context.Context){
		s.runDevicePoller,
		s.runBoardPoller,
		s.runPipelineStatusPoller,
	}
	for _, run := range runners {
		wg.Add(1)
		go func(run func(context.Context)) {
			defer wg.Done()
			run(pubCtx)
		}(run)
	}

	return func() {
		cancel()
		wg.Wait()
	}
}

// runDevicePoller reads every readable device's live value (via s.io, the
// same engine.DeviceIO the pipeline engine itself reads through) at
// devicePollInterval and broadcasts on "devices" only when a device's value
// actually changed since the last poll, per docs/10-api.md's throttle.
func (s *Server) runDevicePoller(ctx context.Context) {
	ticker := time.NewTicker(devicePollInterval)
	defer ticker.Stop()

	last := make(map[string]engine.Value)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, d := range s.store.ListDevices() {
				if d.Role != model.RoleDigitalIn && d.Role != model.RoleAnalogIn {
					continue // not readable; nothing to publish
				}
				v, err := s.io.ReadDevice(d)
				if err != nil {
					continue // board offline or pin not yet reporting; skip this poll
				}
				if prev, ok := last[d.ID]; ok && prev.Equal(v) {
					continue
				}
				last[d.ID] = v
				s.hub.broadcast(topicDevices, deviceEvent{
					DeviceID:  d.ID,
					Value:     valueJSON(v),
					Timestamp: time.Now().UTC(),
				})
			}
		}
	}
}

// runBoardPoller watches the one board.Session's Alive() state and
// broadcasts on "boards" when it changes. v0.4's multi-board manager
// (docs/12-roadmap.md) replaces this with a real board.Manager.Watch feed;
// v0.3 only has the single always-open Session NewServer was built with.
func (s *Server) runBoardPoller(ctx context.Context) {
	ticker := time.NewTicker(boardPollInterval)
	defer ticker.Stop()

	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status := "disconnected"
			var port *string
			if s.sess.Alive() {
				status = "connected"
				p := s.port
				port = &p
			}
			if status == last {
				continue
			}
			last = status
			s.hub.broadcast(topicBoards, boardEvent{BoardID: defaultBoardID, Status: status, Port: port})
		}
	}
}

// runPipelineStatusPoller re-derives every pipeline's status (the same
// s.pipelineStatus REST responses use) at pipelineStatusPollInterval and
// broadcasts on "pipelines" whenever it differs from the last known value —
// this is what surfaces an engine-internal auto-disable (three consecutive
// failed ticks, docs/07-pipeline-engine.md) to WS clients even though no
// REST call caused it.
func (s *Server) runPipelineStatusPoller(ctx context.Context) {
	ticker := time.NewTicker(pipelineStatusPollInterval)
	defer ticker.Stop()

	last := make(map[string]string)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, p := range s.store.ListPipelines() {
				status := s.pipelineStatus(p)
				if last[p.ID] == status {
					continue
				}
				last[p.ID] = status
				s.publishPipelineStatus(p.ID, status)
			}
		}
	}
}
