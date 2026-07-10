// Command pinctl is the v0.4 binary: config.yaml-driven startup, a
// multi-board Manager (discovery + per-board reconnect-with-backoff), a
// persisted device/pipeline store, the tick engine driving every enabled
// pipeline, and the REST/WS API over all of it. See
// docs/12-roadmap.md's "v0.4 — MVP" for scope.
//
// -port is a dev convenience that bypasses discovery entirely and opens
// exactly one board.Session directly, matching v0.1-v0.3's behavior — no
// Manager, no reconnect loop, no boards.json. It accepts either a serial
// port path or a network board address (tcp://host:port,
// docs/17-network-boards.md), since board.Open dispatches on the address
// itself. Omit it (the production path) to let config.yaml's serial
// allowlist / network.boards and the multi-board Manager drive everything.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sviniabanditka/pinctl/internal/api"
	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/boardio"
	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/history"
	"github.com/sviniabanditka/pinctl/internal/model"
	"github.com/sviniabanditka/pinctl/internal/mqtt"
	"github.com/sviniabanditka/pinctl/internal/store"
)

// historyRetentionInterval is how often, after the once-at-startup pass,
// the retention sweep runs (docs/16-history.md's "Retention": "deleted
// hourly"). Not configurable — it just needs to be small relative to
// history.retention, and an hour comfortably is for every documented
// default.
const historyRetentionInterval = time.Hour

// legacyBoardID is the well-known single-board id used by v0.2/v0.3
// deployments (internal/api's defaultBoardID) before boards.json existed.
const legacyBoardID = "brd_local"

// version is the binary's version string, reported in MQTT Home Assistant
// discovery payloads (docs/15-mqtt-bridge.md's device.sw_version) and
// nowhere else today. "dev" is what every non-release build reports; a
// release build overrides it via -ldflags "-X main.version=vX.Y.Z" (see
// .github/workflows/release.yml).
var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to config.yaml (default: /etc/pinctl/config.yaml, then ~/.pinctl/config.yaml; missing file = defaults)")
	port := flag.String("port", "", "dev convenience: connect to exactly this board address, bypassing discovery/allowlist — a serial port (e.g. /dev/ttyACM0) or a network board address (tcp://host:port, see docs/17-network-boards.md)")
	listen := flag.String("listen", "", "override http.addr from config.yaml")
	dataDir := flag.String("data", "", "override data_dir from config.yaml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("pinctl: loading config: %v", err)
	}
	if *listen != "" {
		cfg.HTTP.Addr = *listen
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}

	log.Printf("pinctl: opening store at %s", cfg.DataDir)
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("pinctl: opening store: %v", err)
	}

	devicesForBoard := func(boardID string) []model.Device {
		var out []model.Device
		for _, d := range st.ListDevices() {
			if d.BoardID == boardID {
				out = append(out, d)
			}
		}
		return out
	}

	// applyDeviceConfig re-applies pin modes and onDisconnect safe-values
	// for devices on sess — the per-device reconfiguration a board forgets
	// on any (re)connect, including a mid-session reboot (the board never
	// closes the fd, so board.Manager's normal reconnect path never runs;
	// see board.Session.SetOnReboot). Shared by onReady (first
	// connect/reconnect) and both boards' reboot callbacks below, so
	// there's exactly one place that knows how to reconfigure a board's
	// devices.
	applyDeviceConfig := func(label string, sess boardio.Session, devices []model.Device) {
		if err := boardio.ApplyPinModes(sess, devices); err != nil {
			log.Printf("pinctl: board %s: applying pin modes: %v", label, err)
		}
		if err := boardio.ApplySafeValues(sess, devices); err != nil {
			log.Printf("pinctl: board %s: applying onDisconnect safe values: %v", label, err)
		}
	}

	// onReady applies pin modes and onDisconnect safe-values for a board's
	// devices right after its handshake completes, before the board is
	// marked connected anywhere — see board.ManagerConfig.OnReady's doc
	// comment for why this ordering is safe-by-construction (docs/07
	// cross-ref: safe-value must land before the engine's next tick). It
	// also runs a second time, later, whenever board.Session detects the
	// board rebooted mid-session (wired up in the multi-board branch
	// below) — same reconfiguration, no special-casing needed.
	// restartPipelines/restartAllPipelines are set once the engine manager
	// exists; onReady/the dev-mode reboot callback can fire before that
	// during startup, in which case they're a no-op (there's nothing
	// running yet to restart).
	var restartPipelines func(boardID string)
	var restartAllPipelines func()

	onReady := func(boardID string, sess *board.Session) {
		applyDeviceConfig(boardID, sess, devicesForBoard(boardID))
		if restartPipelines != nil {
			restartPipelines(boardID)
		}
	}

	var (
		sess       api.Session
		sessPort   string
		io         engine.DeviceIO
		boardMgr   *board.Manager
		closeBoard func()
	)

	if *port != "" {
		log.Printf("pinctl: dev mode: opening board on %s (bypassing discovery)", *port)
		realSess, err := board.Open(*port, board.WithSilenceTimeout(time.Duration(cfg.Serial.SilenceTimeout)))
		if err != nil {
			log.Fatalf("pinctl: opening board: %v", err)
		}
		closeBoard = func() { _ = realSess.Close() }

		fwName, fwMajor, fwMinor := realSess.Firmware()
		log.Printf("pinctl: connected: firmware %q %d.%d, %d pins reported", fwName, fwMajor, fwMinor, len(realSess.Capabilities()))

		applyDeviceConfig(*port, realSess, st.ListDevices())

		// -port has no board.Manager, so nothing else notices a mid-session
		// reboot (the fd never errors — see docs/05-serial-protocol.md's
		// "Mid-session board reset"). Re-apply the same device config and
		// restart any enabled pipeline that isn't running.
		realSess.SetOnReboot(func() {
			applyDeviceConfig(*port, realSess, st.ListDevices())
			if restartAllPipelines != nil {
				restartAllPipelines()
			}
		})

		sess = realSess
		sessPort = *port
		io = boardio.New(realSess)
	} else {
		log.Printf("pinctl: multi-board mode: serial allowlist %v, network boards %v", cfg.Serial.Allowlist, cfg.Network.Boards)
		boardMgr = board.NewManager(board.ManagerConfig{
			Allowlist:            cfg.Serial.Allowlist,
			NetworkBoards:        cfg.Network.Boards,
			NetworkDialTimeout:   time.Duration(cfg.Network.DialTimeout),
			NetworkReadTimeout:   time.Duration(cfg.Network.ReadTimeout),
			NetworkKeepalive:     time.Duration(cfg.Network.Keepalive),
			SerialSilenceTimeout: time.Duration(cfg.Serial.SilenceTimeout),
			Store:                st,
			OnReady:              onReady,
		})
		io = boardio.NewManagerIO(boardMgr)
		sess = board.NewAnyConnectedSession(boardMgr)
		closeBoard = boardMgr.Close
	}

	// The one poll driving the "devices" WS topic and the MQTT bridge
	// (docs/15-mqtt-bridge.md's "one poll, many readers"). Built here,
	// before engine.NewManager, so every writer — the engine, the REST
	// manual write, the MQTT command handler — can be wrapped through the
	// same seam below: io is reassigned to a devicebus.WrapWriter-wrapped
	// DeviceIO, and every remaining use of io in this function (the
	// pipeline loop, restartIfNotRunning, apiServer, the MQTT bridge) picks
	// up the wrapped version. This is what makes output device state
	// observable outside the engine at all — Firmata never reports an
	// output pin back, so without this, only devicebus's own poll (inputs
	// only) would ever publish an Update.
	deviceBus := devicebus.New(io, st, api.DevicePollInterval)
	io = devicebus.WrapWriter(io, deviceBus)

	mgr := engine.NewManager()

	deviceIndex := devicesByID(st.ListDevices())
	for _, p := range st.ListPipelines() {
		if !p.Enabled {
			continue
		}
		if err := mgr.Start(p, deviceIndex, io); err != nil {
			log.Printf("pinctl: starting pipeline %s (%s): %v", p.ID, p.Name, err)
		}
	}

	// restartIfNotRunning re-Starts every enabled pipeline matched by
	// match that isn't currently running — Manager.Start hot-swaps, so
	// this is idempotent. Shared by restartPipelines (multi-board mode,
	// filtered to one board) and restartAllPipelines (dev mode, no
	// per-board filter since -port only ever has one board).
	restartIfNotRunning := func(logPrefix string, match func(p model.Pipeline, devices map[string]model.Device) bool) {
		devices := devicesByID(st.ListDevices())
		for _, p := range st.ListPipelines() {
			if !p.Enabled || !match(p, devices) {
				continue
			}
			if status, ok := mgr.Status(p.ID); ok && status.State == engine.StateRunning {
				continue
			}
			log.Printf("pinctl: %s: restarting pipeline %s (%s)", logPrefix, p.ID, p.Name)
			if err := mgr.Start(p, devices, io); err != nil {
				log.Printf("pinctl: restarting pipeline %s: %v", p.ID, err)
			}
		}
	}

	// In multi-board mode the loop above races the first port scan: a
	// pipeline can burn through its 3-consecutive-failure budget and
	// auto-disable before its board's handshake finishes. Whenever a board
	// (re)connects — including a mid-session reboot, via board.Session's
	// reboot callback — re-Start every enabled pipeline that references it
	// and isn't running.
	restartPipelines = func(boardID string) {
		restartIfNotRunning(fmt.Sprintf("board %s ready", boardID), func(p model.Pipeline, devices map[string]model.Device) bool {
			return pipelineUsesBoard(p, devices, boardID)
		})
	}
	// Dev mode's counterpart: no boardID to filter by (-port is always
	// exactly one board), used from the reboot callback registered above.
	restartAllPipelines = func() {
		restartIfNotRunning(fmt.Sprintf("board %s ready", *port), func(model.Pipeline, map[string]model.Device) bool { return true })
	}

	apiServer := api.NewServer(sess, sessPort, st, mgr, io)
	if boardMgr != nil {
		apiServer.SetBoardManager(boardMgr)
	}
	apiServer.SetDeviceBus(deviceBus)

	// MQTT bridge (docs/15-mqtt-bridge.md): constructed only when enabled —
	// "disabled means absent", no client, no goroutine, nothing. deviceBus
	// and io are already the wrapped/shared instances every other writer
	// uses, so the bridge's commands and every other writer's changes are
	// indistinguishable once they reach it.
	var mqttBridge *mqtt.Bridge
	if cfg.MQTT.Enabled {
		var boardStatus mqtt.BoardStatus
		if boardMgr != nil {
			boardStatus = managerBoardStatus{boardMgr}
		} else {
			boardStatus = singleBoardStatus{sess}
		}
		b, err := mqtt.New(cfg.MQTT, version, mqtt.Deps{
			Store:  st,
			Bus:    deviceBus,
			IO:     io,
			Boards: boardStatus,
		})
		if err != nil {
			// A bridge failure must never be fatal: log it and keep
			// serving without MQTT, per docs/15's "the bridge is never on
			// the critical path".
			log.Printf("pinctl: mqtt: %v", err)
		} else {
			mqttBridge = b
			apiServer.SetDeviceWatcher(mqttBridge)
		}
	}

	// Value history (docs/16-history.md): opened only when enabled, and a
	// failure to open must never be fatal — pinctl serves and controls
	// hardware regardless of whether it can keep a history of it, per
	// docs/16's failure-mode table. deviceBus and st are the same shared
	// instances every other subscriber/store-user gets.
	var histStore *history.Store
	if cfg.History.Enabled {
		hs, err := history.Open(filepath.Join(cfg.DataDir, "history.db"), cfg.History)
		if err != nil {
			log.Printf("pinctl: history: opening: %v; continuing with history disabled", err)
		} else {
			histStore = hs
			apiServer.SetHistoryStore(histStore)
		}
	}

	srv := &http.Server{
		Addr:    cfg.HTTP.Addr,
		Handler: apiServer,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go deviceBus.Run(ctx)

	// histRecordDone is closed once Record's goroutine returns (which it
	// does as soon as ctx is done), so shutdown can wait for it before
	// calling Close — Record must not still be flushing against a closed
	// database handle.
	var histRecordDone chan struct{}
	if histStore != nil {
		histRecordDone = make(chan struct{})
		go func() {
			defer close(histRecordDone)
			histStore.Record(ctx, deviceBus, st)
		}()
		go func() {
			if err := histStore.Retain(time.Now()); err != nil {
				log.Printf("pinctl: history: retention: %v", err)
			}
			ticker := time.NewTicker(historyRetentionInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					if err := histStore.Retain(now); err != nil {
						log.Printf("pinctl: history: retention: %v", err)
					}
				}
			}
		}()
	}

	if mqttBridge != nil {
		go func() {
			// Run itself only ever returns non-nil for a startup failure
			// (e.g. the initial connect attempt errors before Run's own
			// backoff loop takes over) or a real transport error; either
			// way this must never take pinctl down — log and carry on
			// serving the UI/pipelines, per docs/15's "the bridge is never
			// on the critical path".
			if err := mqttBridge.Run(ctx); err != nil {
				log.Printf("pinctl: mqtt bridge: %v", err)
			}
		}()
	}

	if boardMgr != nil {
		go boardMgr.Run(ctx)
		// One-time migration: devices persisted under v0.2/v0.3's
		// single-board default id (legacyBoardID) get rewritten to the
		// real discovered board's id, but only when that's unambiguous
		// (exactly one board connected) — see the package doc comment and
		// docs/12-roadmap.md v0.4's multi-board scope. Bounded wait for the
		// first scan's connect attempts to settle; if nothing connects in
		// time the migration is simply skipped (nothing to migrate to yet)
		// and can run on a later restart once a board is present.
		waitForABoard(ctx, boardMgr, 5*time.Second)
		migrateLegacyBoardID(st, boardMgr)
	}

	// boards/devices/pipelines WS topic publishers (docs/10-api.md),
	// stopped alongside the pipeline engine on shutdown below.
	stopPublishers := apiServer.StartPublishers(ctx)

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("pinctl: listening on %s", cfg.HTTP.Addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("pinctl: http server: %v", err)
		}
	case <-ctx.Done():
		log.Print("pinctl: shutting down")

		// Stop every pipeline runner first (no pipeline is interrupted
		// mid-tick), then the WS publishers and every connected WS client
		// (docs/10-api.md: "the server closes every WebSocket cleanly...
		// as part of the graceful shutdown sequence"), then board
		// session(s), then the HTTP server.
		mgr.StopAll()
		stopPublishers()
		apiServer.CloseWS()
		if closeBoard != nil {
			closeBoard()
		}
		if histStore != nil {
			<-histRecordDone // Record's final flush must land before Close
			if err := histStore.Close(); err != nil {
				log.Printf("pinctl: history: closing: %v", err)
			}
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("pinctl: http shutdown: %v", err)
		}
	}
}

// waitForABoard blocks (bounded by timeout or ctx) until at least one board
// reaches StatusConnected, or gives up — used only to give the one-time
// legacy-boardId migration a chance to run against a board that's already
// present at startup; it is not required for correct operation (a board
// that connects later just runs with no migration until the next restart).
func waitForABoard(ctx context.Context, mgr *board.Manager, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, b := range mgr.Boards() {
			if b.Status == board.StatusConnected {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// migrateLegacyBoardID rewrites devices.json entries still bound to
// legacyBoardID ("brd_local", v0.2/v0.3's single-board default) onto the
// real board id, when — and only when — that's unambiguous: exactly one
// board currently connected. With zero or several boards connected there is
// no safe automatic choice, so it logs and leaves the devices as-is; an
// operator can PATCH them individually (boardId is otherwise immutable, but
// the legacy value never corresponded to a real board to begin with).
func migrateLegacyBoardID(st *store.Store, mgr *board.Manager) {
	devices := st.ListDevices()
	hasLegacy := false
	for _, d := range devices {
		if d.BoardID == legacyBoardID {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		return
	}

	var connected []board.BoardInfo
	for _, b := range mgr.Boards() {
		if b.Status == board.StatusConnected {
			connected = append(connected, b)
		}
	}
	if len(connected) != 1 {
		log.Printf("pinctl: %d board(s) connected and some devices still reference legacy boardId %q; skipping auto-migration (need exactly one connected board to disambiguate) — rewrite them manually if needed", len(connected), legacyBoardID)
		return
	}

	target := connected[0].ID
	for _, d := range devices {
		if d.BoardID != legacyBoardID {
			continue
		}
		d.BoardID = target
		if _, err := st.UpdateDevice(d.ID, d); err != nil {
			log.Printf("pinctl: migrating device %s from %s to %s: %v", d.ID, legacyBoardID, target, err)
			continue
		}
		log.Printf("pinctl: migrated device %s: boardId %s -> %s", d.ID, legacyBoardID, target)
	}
}

// devicesByID indexes devices the way internal/engine wants them (keyed by
// ID) for Manager.Start.
func devicesByID(devices []model.Device) map[string]model.Device {
	m := make(map[string]model.Device, len(devices))
	for _, d := range devices {
		m[d.ID] = d
	}
	return m
}

// pipelineUsesBoard reports whether any node in p references (via its
// config's deviceId) a device bound to boardID.
func pipelineUsesBoard(p model.Pipeline, devices map[string]model.Device, boardID string) bool {
	for _, n := range p.Nodes {
		var cfg struct {
			DeviceID string `json:"deviceId"`
		}
		if json.Unmarshal(n.Config, &cfg) != nil || cfg.DeviceID == "" {
			continue
		}
		if d, ok := devices[cfg.DeviceID]; ok && d.BoardID == boardID {
			return true
		}
	}
	return false
}
