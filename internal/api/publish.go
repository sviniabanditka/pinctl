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

	"github.com/sviniabanditka/pinctl/internal/devicebus"
)

const (
	// DevicePollInterval matches docs/10-api.md's devices topic throttle
	// ("coalesced to at most one update per device per ~200ms"). Exported
	// so cmd/pinctl can build the shared *devicebus.Bus (docs/15-mqtt-
	// bridge.md's "one poll, many readers") at the same cadence the WS
	// devices topic has always polled at.
	DevicePollInterval = 200 * time.Millisecond
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

// publishPipelineStatus broadcasts id's current status (and, for a paused
// pipeline, why — see engine.Status.Detail) on the "pipelines" topic.
// Called both directly from the enable/disable/create/update/delete
// handlers (immediate) and from runPipelineStatusPoller (catches
// engine-internal transitions, e.g. auto-disable or a board going/coming
// back online).
func (s *Server) publishPipelineStatus(id, status, detail string) {
	s.hub.broadcast(topicPipelines, pipelineEvent{PipelineID: id, Status: status, Detail: detail})
}

// StartPublishers launches the background goroutines driving the boards/
// devices/pipelines WS topics. It returns a stop func that cancels them and
// blocks until all have exited — call it during graceful shutdown,
// alongside engine.Manager.StopAll and before the WS hub is torn down, so
// no publisher goroutine sends on a hub whose clients are already gone.
func (s *Server) StartPublishers(ctx context.Context) (stop func()) {
	pubCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup

	// The devices WS topic is a subscriber of the shared devicebus.Bus
	// (docs/15-mqtt-bridge.md "one poll, many readers"), not a poller of
	// its own. If cmd/pinctl handed in an externally-owned bus via
	// SetDeviceBus, it also owns running it — this only subscribes. If not
	// (every existing test, and any caller that hasn't been updated to
	// build one), StartPublishers constructs and runs one internally,
	// scoped to pubCtx, exactly matching the polling this replaces.
	bus := s.bus
	if bus == nil {
		bus = devicebus.New(s.io, s.store, DevicePollInterval)
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Run(pubCtx)
		}()
	}

	runners := []func(context.Context){
		func(ctx context.Context) { s.runDevicePoller(ctx, bus) },
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

// runDevicePoller subscribes to bus and broadcasts every Update it
// delivers on the "devices" WS topic, until ctx is done. The bus itself
// does the polling and change-detection (docs/15-mqtt-bridge.md "one poll,
// many readers"); this is just the WS-hub-shaped consumer of that stream,
// same payload shape and publish-on-change behaviour as when this function
// polled the board directly.
func (s *Server) runDevicePoller(ctx context.Context, bus *devicebus.Bus) {
	updates, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-updates:
			if !ok {
				return
			}
			s.hub.broadcast(topicDevices, deviceEvent{
				DeviceID:  u.Device.ID,
				Value:     valueJSON(u.Value),
				Timestamp: u.At,
			})
		}
	}
}

// runBoardPoller drives the "boards" WS topic. With a multi-board Manager
// wired in (v0.4, docs/12-roadmap.md), it relays Manager.Watch's real
// per-board connect/disconnect/error events directly — no polling needed,
// since Manager already knows the instant a transition happens. Without one
// (single-session dev mode), it falls back to polling the one
// board.Session's Alive() state, same as v0.3.
func (s *Server) runBoardPoller(ctx context.Context) {
	if s.boardMgr != nil {
		s.runBoardWatch(ctx)
		return
	}
	s.runBoardPoll(ctx)
}

func (s *Server) runBoardWatch(ctx context.Context) {
	events := s.boardMgr.Watch(ctx)
	for ev := range events {
		var port *string
		if ev.Port != "" {
			p := ev.Port
			port = &p
		}
		s.hub.broadcast(topicBoards, boardEvent{BoardID: ev.BoardID, Status: string(ev.Status), Port: port})
	}
}

func (s *Server) runBoardPoll(ctx context.Context) {
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

// pipelineStatusDetailKey identifies a (status, detail) pair for
// runPipelineStatusPoller's last-seen tracking — a pipeline going from one
// unreachable device to a different one is still worth re-publishing even
// though status stays "paused" the whole time.
type pipelineStatusDetailKey struct {
	status string
	detail string
}

// runPipelineStatusPoller re-derives every pipeline's status+detail (the
// same s.pipelineStatusDetail REST responses use) at
// pipelineStatusPollInterval and broadcasts on "pipelines" whenever either
// differs from the last known value — this is what surfaces an
// engine-internal state transition (auto-disable after three consecutive
// failed ticks, or a pause/resume as a board goes offline/comes back,
// docs/07-pipeline-engine.md) to WS clients even though no REST call caused
// it.
func (s *Server) runPipelineStatusPoller(ctx context.Context) {
	ticker := time.NewTicker(pipelineStatusPollInterval)
	defer ticker.Stop()

	last := make(map[string]pipelineStatusDetailKey)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, p := range s.store.ListPipelines() {
				status, detail := s.pipelineStatusDetail(p)
				key := pipelineStatusDetailKey{status: status, detail: detail}
				if last[p.ID] == key {
					continue
				}
				last[p.ID] = key
				s.publishPipelineStatus(p.ID, status, detail)
			}
		}
	}
}
