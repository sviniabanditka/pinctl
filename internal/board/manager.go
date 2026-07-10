// Manager owns the lifecycle of every board session: discovery against the
// serial allowlist, connect, reconnect-with-backoff per board, and safe
// accessors for callers (internal/boardio, internal/api) — see
// docs/03-architecture.md's internal/board section and
// docs/05-serial-protocol.md's "Session lifecycle"/"Reconnect with backoff".
package board

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ErrBoardOffline is returned by Session/AnyConnected-derived accessors when
// the requested board is currently disconnected/reconnecting.
var ErrBoardOffline = errors.New("board: board is offline")

// ErrBoardUnknown is returned by Session when boardID has never been seen
// (not in the persisted boards collection and never discovered).
var ErrBoardUnknown = errors.New("board: unknown board id")

// Status is a board's live connection state, matching docs/10-api.md's
// boards.status enum.
type Status string

const (
	StatusConnected    Status = "connected"
	StatusDisconnected Status = "disconnected"
	StatusReconnecting Status = "reconnecting"
	StatusError        Status = "error" // permanent failure, e.g. unsupported protocol version
)

// BoardInfo is a Manager.Boards() snapshot: persisted identity/name merged
// with live connection state (docs/09-data-model.md: "the API composes
// persisted board metadata with live status on every GET /boards call").
type BoardInfo struct {
	ID       string
	Name     string
	Identity model.Identity
	PortHint string

	Status Status
	Port   string // only meaningful while Status == StatusConnected

	Capabilities                 []PinCapability
	FirmwareName                 string
	FirmwareMajor, FirmwareMinor int

	LastError error
}

// BoardEvent is broadcast on Watch for every board status transition.
type BoardEvent struct {
	BoardID string
	Status  Status
	Port    string
}

// Store is the persistence surface Manager needs from internal/store — a
// narrow interface (rather than depending on *store.Store directly) so
// tests can supply an in-memory fake.
type Store interface {
	ListBoards() []model.Board
	GetBoard(id string) (model.Board, error)
	GetBoardByIdentity(kind, key string) (model.Board, bool)
	CreateBoard(b model.Board) (model.Board, error)
	UpdateBoardPortHint(id, portHint string) (model.Board, error)
	RenameBoard(id, name string) (model.Board, error)
}

// Clock abstracts time.Now so reconnect backoff is deterministically
// testable (docs/13-testing.md's determinism hooks, same pattern as
// internal/engine.Clock).
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// SessionOpener opens a Session against portPath. Production defaults to
// Open; tests inject a fake that never touches a real serial port.
type SessionOpener func(portPath string) (*Session, error)

// ManagerConfig configures a Manager. Zero-value fields fall back to
// production defaults.
type ManagerConfig struct {
	// Allowlist is the set of serial port path patterns Manager is
	// permitted to open (docs/14-deployment.md serial.allowlist,
	// FR-BOARD-4). Empty means "allow any".
	Allowlist []string

	Store Store

	// PortLister/OpenSession/Clock: injectable seams for tests. Default to
	// DefaultPortLister, board.Open, and a real clock respectively.
	PortLister  PortLister
	OpenSession SessionOpener
	Clock       Clock

	// ScanInterval is how often Manager's Run loop re-discovers ports and
	// retries due reconnects. Default 2s.
	ScanInterval time.Duration

	// Backoff is the reconnect backoff schedule per board (docs/05's
	// "1s -> 2s -> 5s -> 10s, capped"); the last element repeats once
	// exhausted. Defaults to that exact schedule.
	Backoff []time.Duration

	// OnReady is called synchronously, in the connecting goroutine, right
	// after a session completes its handshake and before the board is
	// marked connected/broadcast to Watch subscribers or made available via
	// Session(). This is where a caller applies pin modes and onDisconnect
	// safe-values (docs/07-pipeline-engine.md cross-ref: safe-value must
	// land before the engine's next tick) — the ordering guarantee holds by
	// construction, since nothing observes the session as "connected" until
	// OnReady returns.
	OnReady func(boardID string, sess *Session)
}

type boardState struct {
	board model.Board

	status  Status
	session *Session
	port    string

	connecting  bool
	permanent   bool // true after a permanent failure (e.g. unsupported protocol version)
	attempt     int
	nextAttempt time.Time
	lastErr     error
}

// Manager owns every board's connection lifecycle. Safe for concurrent use.
type Manager struct {
	cfg ManagerConfig

	mu     sync.RWMutex
	boards map[string]*boardState
	closed bool // set by Close; checked (under mu) before a connect goroutine installs a session or fires OnReady

	// wg tracks in-flight connect goroutines launched by Scan, so Close can
	// wait for them to observe `closed` and self-abort (closing whatever
	// session they just opened) instead of racing a session install/OnReady
	// past shutdown.
	wg sync.WaitGroup

	watchMu  sync.Mutex
	watchers map[chan BoardEvent]struct{}
}

// NewManager builds a Manager seeded with every board already known to
// cfg.Store (loaded disconnected; Scan/Run reconnects them as their ports
// reappear).
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.Store == nil {
		panic("board: NewManager requires a non-nil Store")
	}
	if cfg.PortLister == nil {
		cfg.PortLister = DefaultPortLister
	}
	if cfg.OpenSession == nil {
		cfg.OpenSession = func(port string) (*Session, error) { return Open(port) }
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = 2 * time.Second
	}
	if len(cfg.Backoff) == 0 {
		cfg.Backoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	}

	m := &Manager{
		cfg:      cfg,
		boards:   make(map[string]*boardState),
		watchers: make(map[chan BoardEvent]struct{}),
	}
	for _, b := range cfg.Store.ListBoards() {
		m.boards[b.ID] = &boardState{board: b, status: StatusDisconnected}
	}
	return m
}

// Boards returns the current known boards and their live status.
func (m *Manager) Boards() []BoardInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]BoardInfo, 0, len(m.boards))
	for _, st := range m.boards {
		info := BoardInfo{
			ID:        st.board.ID,
			Name:      st.board.Name,
			Identity:  st.board.Identity,
			PortHint:  st.board.PortHint,
			Status:    st.status,
			Port:      st.port,
			LastError: st.lastErr,
		}
		if st.status == StatusConnected && st.session != nil {
			info.Capabilities = st.session.Capabilities()
			info.FirmwareName, info.FirmwareMajor, info.FirmwareMinor = st.session.Firmware()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Session returns the live session for boardID, or ErrBoardOffline if the
// board is currently disconnected/reconnecting, or ErrBoardUnknown if the
// board id has never been seen.
func (m *Manager) Session(boardID string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	st, ok := m.boards[boardID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrBoardUnknown, boardID)
	}
	if st.status != StatusConnected || st.session == nil {
		return nil, fmt.Errorf("%w: %s", ErrBoardOffline, boardID)
	}
	return st.session, nil
}

// AnyConnected returns the session/id/port of any one connected board,
// deterministically the lowest board ID among connected boards — used by
// internal/api's v0.1-compat single-board endpoints (docs/10-api.md: "keep
// GET /board... working against the first/any connected board").
func (m *Manager) AnyConnected() (sess *Session, boardID, port string, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.boards))
	for id := range m.boards {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := m.boards[id]
		if st.status == StatusConnected && st.session != nil {
			return st.session, st.board.ID, st.port, true
		}
	}
	return nil, "", "", false
}

// RenameBoard persists a new display name for boardID and updates the live
// snapshot.
func (m *Manager) RenameBoard(id, name string) (model.Board, error) {
	b, err := m.cfg.Store.RenameBoard(id, name)
	if err != nil {
		return model.Board{}, err
	}
	m.mu.Lock()
	if st, ok := m.boards[id]; ok {
		st.board = b
	}
	m.mu.Unlock()
	return b, nil
}

// Watch streams board status-change events until ctx is done. The returned
// channel is closed when ctx is done; a slow consumer has events dropped
// (never blocks the connect goroutine), same drop-if-slow policy as the API
// WS hub.
func (m *Manager) Watch(ctx context.Context) <-chan BoardEvent {
	ch := make(chan BoardEvent, 16)
	m.watchMu.Lock()
	m.watchers[ch] = struct{}{}
	m.watchMu.Unlock()

	go func() {
		<-ctx.Done()
		m.watchMu.Lock()
		delete(m.watchers, ch)
		m.watchMu.Unlock()
		close(ch)
	}()
	return ch
}

func (m *Manager) broadcast(ev BoardEvent) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	for ch := range m.watchers {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Scan performs one discovery+connect/health-check pass: it lists
// allowlisted ports, resolves identity, auto-registers newly-seen boards in
// the store, and launches a connect attempt (in its own goroutine, so a
// slow/hung handshake for one board never blocks another) for every board
// whose backoff has elapsed and whose port is currently present. It also
// health-checks already-connected sessions and starts the offline/reconnect
// transition for any that have died. Scan itself never blocks on serial
// I/O — it returns as soon as attempts are launched.
func (m *Manager) Scan(ctx context.Context) error {
	ports, err := m.cfg.PortLister()
	if err != nil {
		return fmt.Errorf("board: listing ports: %w", err)
	}

	now := m.cfg.Clock.Now()

	discovered := make(map[model.Identity]PortInfo)
	for _, p := range ports {
		if !MatchesAllowlist(p.Path, m.cfg.Allowlist) {
			continue
		}
		discovered[identityForPort(p)] = p
	}

	m.mu.Lock()

	if m.closed {
		m.mu.Unlock()
		return nil
	}

	// Health-check connected boards; a dead session starts the reconnect
	// cycle immediately (docs/05: "read/write returns an OS error ->
	// offline immediately"). The dead session itself is still an open fd
	// that must be closed — captured here and closed (best-effort) after
	// mu is released below, since Close can block briefly.
	var deadSessions []*Session
	for _, st := range m.boards {
		if st.status == StatusConnected && st.session != nil && !st.session.Alive() {
			st.lastErr = st.session.Err()
			st.attempt = 0
			st.nextAttempt = now
			if dropped := m.transitionLocked(st, StatusDisconnected, ""); dropped != nil {
				deadSessions = append(deadSessions, dropped)
			}
		}
	}

	// Auto-register any identity discovered that the store doesn't know
	// about yet (docs/10-api.md rescan: "creates a new boards.json entry
	// with an auto-generated name for any board found whose identity.key
	// isn't already known").
	for id, p := range discovered {
		if m.hasIdentityLocked(id) {
			continue
		}
		name := fmt.Sprintf("Arduino on %s", p.Path)
		b, err := m.cfg.Store.CreateBoard(model.Board{Name: name, Identity: id, PortHint: p.Path})
		if err != nil {
			log.Printf("board: registering new board on %s: %v", p.Path, err)
			continue
		}
		m.boards[b.ID] = &boardState{board: b, status: StatusDisconnected}
	}

	type attempt struct {
		st   *boardState
		port string
	}
	var toConnect []attempt
	for _, st := range m.boards {
		if st.status == StatusConnected || st.connecting || st.permanent {
			continue
		}
		p, ok := discovered[st.board.Identity]
		if !ok {
			continue // not currently plugged in
		}
		if !st.nextAttempt.IsZero() && now.Before(st.nextAttempt) {
			continue // backoff not elapsed yet
		}
		st.connecting = true
		m.wg.Add(1)
		toConnect = append(toConnect, attempt{st: st, port: p.Path})
	}
	m.mu.Unlock()

	for _, sess := range deadSessions {
		if err := sess.Close(); err != nil {
			log.Printf("board: closing dead session: %v", err)
		}
	}

	for _, a := range toConnect {
		go m.connect(a.st, a.port)
	}
	return nil
}

func (m *Manager) hasIdentityLocked(id model.Identity) bool {
	for _, st := range m.boards {
		if st.board.Identity == id {
			return true
		}
	}
	return false
}

// connect attempts to open+handshake st's board on port, off the Scan hot
// path. On success it runs OnReady before marking the board connected (see
// ManagerConfig.OnReady's doc comment for the ordering guarantee this
// buys); on failure it schedules the next backoff attempt (or, for an
// unsupported protocol version, marks the board permanently failed per
// docs/05-serial-protocol.md's error handling table).
func (m *Manager) connect(st *boardState, port string) {
	defer m.wg.Done()

	sess, err := m.cfg.OpenSession(port)

	if err != nil {
		m.mu.Lock()
		st.connecting = false
		if m.closed {
			m.mu.Unlock()
			return
		}
		st.lastErr = err
		if errors.Is(err, ErrUnsupportedProtocolVersion) {
			st.permanent = true
			dropped := m.transitionLocked(st, StatusError, "")
			m.mu.Unlock()
			closeDropped(dropped)
			log.Printf("board: %s (%s): unsupported protocol version, not retrying: %v", st.board.ID, port, err)
			return
		}
		idx := st.attempt
		if idx >= len(m.cfg.Backoff) {
			idx = len(m.cfg.Backoff) - 1
		}
		wait := m.cfg.Backoff[idx]
		st.nextAttempt = m.cfg.Clock.Now().Add(wait)
		st.attempt++
		dropped := m.transitionLocked(st, StatusReconnecting, "")
		m.mu.Unlock()
		closeDropped(dropped)
		log.Printf("board: connect %s (%s) failed, retrying in %s: %v", st.board.ID, port, wait, err)
		return
	}

	// Close may have run (or be running) concurrently with this connect;
	// check the closed flag, still under the same lock that Close sets it
	// under, before installing the freshly-opened session or firing
	// OnReady — otherwise a session could be installed/observed-ready
	// after shutdown, or leaked because Close already made its one pass
	// over m.boards closing sessions.
	m.mu.Lock()
	if m.closed {
		st.connecting = false
		m.mu.Unlock()
		if err := sess.Close(); err != nil {
			log.Printf("board: closing session opened after shutdown for %s (%s): %v", st.board.ID, port, err)
		}
		return
	}
	st.connecting = false
	st.session = sess
	st.port = port
	st.attempt = 0
	st.nextAttempt = time.Time{}
	st.lastErr = nil
	if st.board.PortHint != port {
		if b, err := m.cfg.Store.UpdateBoardPortHint(st.board.ID, port); err == nil {
			st.board = b
		}
	}
	boardID := st.board.ID
	m.mu.Unlock()

	// Wire up mid-session reboot detection before OnReady runs, so a
	// reboot that happens to land in the (tiny) window right after
	// handshake but before OnReady's initial pin-mode/safe-value pass
	// still gets a callback registered — worst case it fires once more
	// than strictly necessary, never never fires at all. On a detected
	// reboot, re-run the exact same reconfiguration OnReady does for this
	// board — don't duplicate its logic. Must be a no-op once the manager
	// is closed (or this board's session has since been dropped/replaced)
	// so a reboot signal from a session Close() is racing against can
	// never resurrect it.
	sess.SetOnReboot(func() {
		m.mu.Lock()
		closed := m.closed
		current := st.session
		m.mu.Unlock()
		if closed || current != sess {
			return
		}
		if m.cfg.OnReady != nil {
			m.cfg.OnReady(boardID, sess)
		}
	})

	if m.cfg.OnReady != nil {
		m.cfg.OnReady(boardID, sess)
	}

	m.mu.Lock()
	dropped := m.transitionLocked(st, StatusConnected, port)
	m.mu.Unlock()
	closeDropped(dropped)
}

// closeDropped best-effort closes a session dropped by transitionLocked,
// logging any error. Must be called after the caller's m.mu section has
// been released — Close can block briefly on serial I/O.
func closeDropped(sess *Session) {
	if sess == nil {
		return
	}
	if err := sess.Close(); err != nil {
		log.Printf("board: closing dropped session: %v", err)
	}
}

// transitionLocked updates st's status/port and broadcasts a BoardEvent if
// it actually changed. Caller must hold m.mu; broadcast itself takes the
// separate watchMu, so this never deadlocks against Watch/Scan.
//
// If transitioning away from StatusConnected drops a live session,
// transitionLocked nils it out (so no other caller can observe/use it) and
// returns it — the caller must close it (best-effort, logging any error)
// after releasing m.mu, since Close can block briefly on serial I/O and
// must never be called while holding the manager mutex.
func (m *Manager) transitionLocked(st *boardState, status Status, port string) *Session {
	if st.status == status && st.port == port {
		return nil
	}
	st.status = status
	var dropped *Session
	if status == StatusConnected {
		st.port = port
	} else {
		dropped = st.session
		st.session = nil
		st.port = ""
	}
	m.broadcast(BoardEvent{BoardID: st.board.ID, Status: st.status, Port: st.port})
	return dropped
}

// Run starts the periodic scan loop (discovery + due reconnects) until ctx
// is done. It performs one Scan immediately, then one every ScanInterval.
func (m *Manager) Run(ctx context.Context) {
	if err := m.Scan(ctx); err != nil {
		log.Printf("board: scan: %v", err)
	}

	ticker := time.NewTicker(m.cfg.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Scan(ctx); err != nil {
				log.Printf("board: scan: %v", err)
			}
		}
	}
}

// Close closes every currently-connected session (best-effort). Call once
// Run's context has been cancelled and no further Scan will run.
//
// Close first marks the manager closed and waits for every in-flight
// connect goroutine (launched by a Scan that ran just before Close) to
// notice and self-abort — closing whatever session it just opened rather
// than installing it — before doing its own sweep of already-installed
// sessions. This ordering (closed=true, then Wait, then sweep) is what
// prevents a race where a slow connect installs a session and/or fires
// OnReady after Close has already returned.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	m.wg.Wait()

	m.mu.Lock()
	var toClose []*Session
	for _, st := range m.boards {
		if st.session != nil {
			toClose = append(toClose, st.session)
			st.session = nil
		}
		st.status = StatusDisconnected
		st.port = ""
	}
	m.mu.Unlock()

	for _, sess := range toClose {
		if err := sess.Close(); err != nil {
			log.Printf("board: closing session on shutdown: %v", err)
		}
	}
}

// AnyConnectedSession adapts Manager to the single-Session-shaped interface
// internal/api's pre-multi-board (v0.1/v0.2) handlers use (GET /board,
// /pins/{pin}), resolving to "any one connected board" on every call — the
// backward-compat shim docs/10-api.md's "keep GET /board... working"
// requirement calls for, without touching those handlers' signatures.
type AnyConnectedSession struct{ mgr *Manager }

// NewAnyConnectedSession wraps mgr.
func NewAnyConnectedSession(mgr *Manager) *AnyConnectedSession {
	return &AnyConnectedSession{mgr: mgr}
}

func (a *AnyConnectedSession) resolve() (*Session, bool) {
	sess, _, _, ok := a.mgr.AnyConnected()
	return sess, ok
}

func (a *AnyConnectedSession) Alive() bool {
	sess, ok := a.resolve()
	return ok && sess.Alive()
}

func (a *AnyConnectedSession) Firmware() (string, int, int) {
	if sess, ok := a.resolve(); ok {
		return sess.Firmware()
	}
	return "", 0, 0
}

func (a *AnyConnectedSession) ProtocolVersion() (int, int) {
	if sess, ok := a.resolve(); ok {
		return sess.ProtocolVersion()
	}
	return 0, 0
}

func (a *AnyConnectedSession) Capabilities() []PinCapability {
	if sess, ok := a.resolve(); ok {
		return sess.Capabilities()
	}
	return nil
}

func (a *AnyConnectedSession) SetPinMode(pin int, mode PinMode) error {
	sess, ok := a.resolve()
	if !ok {
		return ErrBoardOffline
	}
	return sess.SetPinMode(pin, mode)
}

func (a *AnyConnectedSession) DigitalWrite(pin int, high bool) error {
	sess, ok := a.resolve()
	if !ok {
		return ErrBoardOffline
	}
	return sess.DigitalWrite(pin, high)
}

func (a *AnyConnectedSession) AnalogWrite(pin int, value byte) error {
	sess, ok := a.resolve()
	if !ok {
		return ErrBoardOffline
	}
	return sess.AnalogWrite(pin, value)
}

func (a *AnyConnectedSession) DigitalRead(pin int) (bool, error) {
	sess, ok := a.resolve()
	if !ok {
		return false, ErrBoardOffline
	}
	return sess.DigitalRead(pin)
}

func (a *AnyConnectedSession) AnalogRead(channel int) (int, error) {
	sess, ok := a.resolve()
	if !ok {
		return 0, ErrBoardOffline
	}
	return sess.AnalogRead(channel)
}

func (a *AnyConnectedSession) ServoConfig(pin, minPulseUs, maxPulseUs int) error {
	sess, ok := a.resolve()
	if !ok {
		return ErrBoardOffline
	}
	return sess.ServoConfig(pin, minPulseUs, maxPulseUs)
}

func (a *AnyConnectedSession) ServoWrite(pin, angle int) error {
	sess, ok := a.resolve()
	if !ok {
		return ErrBoardOffline
	}
	return sess.ServoWrite(pin, angle)
}
