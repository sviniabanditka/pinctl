package board

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// --- fake BoardStore --------------------------------------------------------

type fakeBoardStore struct {
	mu     sync.Mutex
	boards map[string]model.Board
	seq    int
}

func newFakeBoardStore() *fakeBoardStore {
	return &fakeBoardStore{boards: map[string]model.Board{}}
}

func (s *fakeBoardStore) ListBoards() []model.Board {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Board, 0, len(s.boards))
	for _, b := range s.boards {
		out = append(out, b)
	}
	return out
}

func (s *fakeBoardStore) GetBoard(id string) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[id]
	if !ok {
		return model.Board{}, errors.New("not found")
	}
	return b, nil
}

func (s *fakeBoardStore) GetBoardByIdentity(kind, key string) (model.Board, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.boards {
		if b.Identity.Kind == kind && b.Identity.Key == key {
			return b, true
		}
	}
	return model.Board{}, false
}

func (s *fakeBoardStore) CreateBoard(b model.Board) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	b.ID = fmt.Sprintf("brd_test%d", s.seq)
	s.boards[b.ID] = b
	return b, nil
}

func (s *fakeBoardStore) UpdateBoardPortHint(id, portHint string) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[id]
	if !ok {
		return model.Board{}, errors.New("not found")
	}
	b.PortHint = portHint
	s.boards[id] = b
	return b, nil
}

func (s *fakeBoardStore) RenameBoard(id, name string) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[id]
	if !ok {
		return model.Board{}, errors.New("not found")
	}
	b.Name = name
	s.boards[id] = b
	return b, nil
}

// --- fake clock --------------------------------------------------------------

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// --- test helpers --------------------------------------------------------

// fakePortLister returns a fixed port list, swappable mid-test via a
// pointer so a test can simulate unplug/replug between Scan calls.
type fakePortLister struct {
	mu    sync.Mutex
	ports []PortInfo
}

func (l *fakePortLister) list() ([]PortInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]PortInfo, len(l.ports))
	copy(out, l.ports)
	return out, nil
}

func (l *fakePortLister) set(ports ...PortInfo) {
	l.mu.Lock()
	l.ports = ports
	l.mu.Unlock()
}

// scriptedOpener opens a real (in-memory, net.Pipe-backed) Session for ports
// not in the fail set, and returns failErr for ports that are, counting
// every call it makes.
type scriptedOpener struct {
	t    *testing.T
	mu   sync.Mutex
	fail map[string]error
	n    int
}

func (o *scriptedOpener) open(port string) (*Session, error) {
	o.mu.Lock()
	o.n++
	err, shouldFail := o.fail[port]
	o.mu.Unlock()
	if shouldFail {
		return nil, err
	}
	s, _, _ := newHandshakedSession(o.t)
	return s, nil
}

func (o *scriptedOpener) calls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}

func waitForBoardStatus(t *testing.T, m *Manager, id string, want Status) BoardInfo {
	t.Helper()
	var last BoardInfo
	waitFor(t, time.Second, func() bool {
		for _, b := range m.Boards() {
			if b.ID == id {
				last = b
				return b.Status == want
			}
		}
		return false
	})
	return last
}

// --- tests -----------------------------------------------------------------

func TestManagerScanRegistersAndConnectsNewBoard(t *testing.T) {
	fs := newFakeBoardStore()
	pl := &fakePortLister{}
	pl.set(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "SN1"})
	opener := &scriptedOpener{t: t, fail: map[string]error{}}

	var readyBoardID string
	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  pl.list,
		OpenSession: opener.open,
		Clock:       newFakeClock(),
		OnReady:     func(boardID string, sess *Session) { readyBoardID = boardID },
	})

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	boards := m.Boards()
	if len(boards) != 1 {
		t.Fatalf("len(Boards()) = %d, want 1 (auto-registered)", len(boards))
	}
	id := boards[0].ID

	info := waitForBoardStatus(t, m, id, StatusConnected)
	if info.Port != "/dev/ttyACM0" {
		t.Errorf("Port = %q, want /dev/ttyACM0", info.Port)
	}
	if readyBoardID != id {
		t.Errorf("OnReady called with boardID %q, want %q", readyBoardID, id)
	}

	sess, err := m.Session(id)
	if err != nil {
		t.Fatalf("Session(%s): %v", id, err)
	}
	if sess == nil {
		t.Fatal("Session returned nil session with nil error")
	}
}

func TestManagerUnknownAndOfflineSessionErrors(t *testing.T) {
	fs := newFakeBoardStore()
	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  func() ([]PortInfo, error) { return nil, nil },
		OpenSession: (&scriptedOpener{t: t}).open,
		Clock:       newFakeClock(),
	})

	if _, err := m.Session("brd_nope"); !errors.Is(err, ErrBoardUnknown) {
		t.Errorf("Session(unknown) = %v, want ErrBoardUnknown", err)
	}

	b, _ := fs.CreateBoard(model.Board{Name: "x", Identity: model.Identity{Kind: "port-path", Key: "/dev/ttyACM0"}})
	m2 := NewManager(ManagerConfig{Store: fs, PortLister: func() ([]PortInfo, error) { return nil, nil }, Clock: newFakeClock()})
	if _, err := m2.Session(b.ID); !errors.Is(err, ErrBoardOffline) {
		t.Errorf("Session(known but disconnected) = %v, want ErrBoardOffline", err)
	}
}

// TestManagerReconnectBackoffProgression drives the reconnect state machine
// entirely through a fake clock and a scripted opener that always fails —
// no real sleeps — asserting the backoff schedule (1s -> 2s -> 5s -> 10s,
// capped) and that a connect attempt is only made once nextAttempt has
// elapsed.
func TestManagerReconnectBackoffProgression(t *testing.T) {
	fs := newFakeBoardStore()
	pl := &fakePortLister{}
	pl.set(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "SN1"})
	opener := &scriptedOpener{t: t, fail: map[string]error{"/dev/ttyACM0": errors.New("boom")}}
	clk := newFakeClock()

	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  pl.list,
		OpenSession: opener.open,
		Clock:       clk,
		Backoff:     []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second},
	})

	must := func() {
		t.Helper()
		if err := m.Scan(context.Background()); err != nil {
			t.Fatalf("Scan: %v", err)
		}
	}

	must() // attempt 1: fails, schedules retry at +1s
	waitFor(t, time.Second, func() bool { return opener.calls() == 1 })
	waitForBoardStatus(t, m, m.Boards()[0].ID, StatusReconnecting)

	// Scanning again before backoff elapses must not retry.
	must()
	time.Sleep(20 * time.Millisecond)
	if got := opener.calls(); got != 1 {
		t.Fatalf("calls = %d after premature scan, want still 1 (backoff not elapsed)", got)
	}

	clk.Advance(time.Second)
	must()
	waitFor(t, time.Second, func() bool { return opener.calls() == 2 })

	clk.Advance(2 * time.Second)
	must()
	waitFor(t, time.Second, func() bool { return opener.calls() == 3 })

	clk.Advance(5 * time.Second)
	must()
	waitFor(t, time.Second, func() bool { return opener.calls() == 4 })

	// Backoff caps at the last schedule entry (10s) from here on.
	clk.Advance(10 * time.Second)
	must()
	waitFor(t, time.Second, func() bool { return opener.calls() == 5 })
}

// TestManagerReconnectSucceedsAfterFailures verifies a later successful
// connect resets backoff state and reaches StatusConnected.
func TestManagerReconnectSucceedsAfterFailures(t *testing.T) {
	fs := newFakeBoardStore()
	pl := &fakePortLister{}
	pl.set(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "SN1"})
	opener := &scriptedOpener{t: t, fail: map[string]error{"/dev/ttyACM0": errors.New("boom")}}
	clk := newFakeClock()

	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  pl.list,
		OpenSession: opener.open,
		Clock:       clk,
		Backoff:     []time.Duration{time.Second},
	})

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, time.Second, func() bool { return opener.calls() == 1 })
	id := m.Boards()[0].ID
	waitForBoardStatus(t, m, id, StatusReconnecting)

	// Now let the next attempt succeed.
	opener.mu.Lock()
	delete(opener.fail, "/dev/ttyACM0")
	opener.mu.Unlock()

	clk.Advance(time.Second)
	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitForBoardStatus(t, m, id, StatusConnected)
}

// TestManagerHealthCheckDetectsDeadSession verifies a session that dies
// (transport error) is detected on the next Scan and transitions the board
// to disconnected, without affecting other boards.
func TestManagerHealthCheckDetectsDeadSession(t *testing.T) {
	fs := newFakeBoardStore()
	pl := &fakePortLister{}
	pl.set(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "SN1"})
	opener := &scriptedOpener{t: t, fail: map[string]error{}}

	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  pl.list,
		OpenSession: opener.open,
		Clock:       newFakeClock(),
	})

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	id := m.Boards()[0].ID
	waitForBoardStatus(t, m, id, StatusConnected)

	sess, err := m.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, time.Second, func() bool { return !sess.Alive() })

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// The dead session is detected and, since the port is still present and
	// the opener always succeeds, immediately reconnects with a fresh
	// session in the same Scan pass.
	waitForBoardStatus(t, m, id, StatusConnected)
	newSess, err := m.Session(id)
	if err != nil {
		t.Fatalf("Session after reconnect: %v", err)
	}
	if newSess == sess {
		t.Error("expected a fresh Session after reconnect, got the same (dead) one")
	}
}

// TestManagerPermanentFailureNotRetried covers the unsupported-protocol-
// version case: the board is marked StatusError and never retried, per
// docs/05-serial-protocol.md's error handling table.
func TestManagerPermanentFailureNotRetried(t *testing.T) {
	fs := newFakeBoardStore()
	pl := &fakePortLister{}
	pl.set(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "SN1"})
	opener := &scriptedOpener{t: t, fail: map[string]error{"/dev/ttyACM0": ErrUnsupportedProtocolVersion}}
	clk := newFakeClock()

	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  pl.list,
		OpenSession: opener.open,
		Clock:       clk,
	})

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	id := m.Boards()[0].ID
	waitForBoardStatus(t, m, id, StatusError)

	clk.Advance(time.Hour)
	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := opener.calls(); got != 1 {
		t.Errorf("calls = %d, want 1 (permanent failure must never retry)", got)
	}
}

func TestManagerRenameBoard(t *testing.T) {
	fs := newFakeBoardStore()
	b, _ := fs.CreateBoard(model.Board{Name: "old", Identity: model.Identity{Kind: "port-path", Key: "/dev/ttyACM0"}})
	m := NewManager(ManagerConfig{Store: fs, PortLister: func() ([]PortInfo, error) { return nil, nil }, Clock: newFakeClock()})

	updated, err := m.RenameBoard(b.ID, "new name")
	if err != nil {
		t.Fatalf("RenameBoard: %v", err)
	}
	if updated.Name != "new name" {
		t.Errorf("Name = %q, want %q", updated.Name, "new name")
	}
	for _, info := range m.Boards() {
		if info.ID == b.ID && info.Name != "new name" {
			t.Errorf("Boards() snapshot not updated after rename: %+v", info)
		}
	}
}

// slowOpener opens a real (in-memory, net.Pipe-backed) session, but blocks
// until release is closed before returning it — used to widen the window
// between Scan launching a connect goroutine and that goroutine installing
// its session, so a concurrent Close can be tested deterministically.
type slowOpener struct {
	t       *testing.T
	release chan struct{}

	mu     sync.Mutex
	opened []*Session
	nCalls int
}

func (o *slowOpener) open(port string) (*Session, error) {
	o.mu.Lock()
	o.nCalls++
	o.mu.Unlock()

	<-o.release

	s, _, _ := newHandshakedSession(o.t)
	o.mu.Lock()
	o.opened = append(o.opened, s)
	o.mu.Unlock()
	return s, nil
}

func (o *slowOpener) calls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.nCalls
}

func (o *slowOpener) allOpenedClosed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.opened) == 0 {
		return false
	}
	for _, s := range o.opened {
		if s.Alive() {
			return false
		}
	}
	return true
}

// TestManagerCloseDuringConnectDoesNotLeakOrFireOnReady covers the
// Scan/Close race: Close must wait for an in-flight connect goroutine to
// finish, and that goroutine must not install a session or fire OnReady
// once the manager is closed — it must instead close the session it just
// opened, so no serial fd is leaked across the reconnect/shutdown cycle.
func TestManagerCloseDuringConnectDoesNotLeakOrFireOnReady(t *testing.T) {
	fs := newFakeBoardStore()
	pl := &fakePortLister{}
	pl.set(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "SN1"})
	opener := &slowOpener{t: t, release: make(chan struct{})}

	var onReadyCalled bool
	var onReadyMu sync.Mutex
	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  pl.list,
		OpenSession: opener.open,
		Clock:       newFakeClock(),
		OnReady: func(boardID string, sess *Session) {
			onReadyMu.Lock()
			onReadyCalled = true
			onReadyMu.Unlock()
		},
	})

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, time.Second, func() bool { return opener.calls() == 1 })

	// Close concurrently while the connect goroutine is still blocked
	// inside OpenSession; Close must block until that goroutine finishes.
	closeDone := make(chan struct{})
	go func() {
		m.Close()
		close(closeDone)
	}()

	// Give Close a moment to reach wg.Wait() before releasing the opener,
	// to actually exercise the race rather than serializing around it.
	time.Sleep(20 * time.Millisecond)
	close(opener.release)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return — it should wait for the in-flight connect and then finish")
	}

	id := m.Boards()[0].ID
	info := waitForBoardStatus(t, m, id, StatusDisconnected)
	if info.Status != StatusDisconnected {
		t.Errorf("board status after Close = %v, want %v", info.Status, StatusDisconnected)
	}

	onReadyMu.Lock()
	called := onReadyCalled
	onReadyMu.Unlock()
	if called {
		t.Error("OnReady must not be called for a connect that races Close")
	}

	if _, err := m.Session(id); !errors.Is(err, ErrBoardOffline) {
		t.Errorf("Session(%s) after Close = %v, want ErrBoardOffline (no session installed)", id, err)
	}

	waitFor(t, time.Second, opener.allOpenedClosed)
}

func TestManagerWatchReceivesEvents(t *testing.T) {
	fs := newFakeBoardStore()
	pl := &fakePortLister{}
	pl.set(PortInfo{Path: "/dev/ttyACM0", IsUSB: true, VID: "2341", PID: "0043", SerialNumber: "SN1"})
	opener := &scriptedOpener{t: t, fail: map[string]error{}}

	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  pl.list,
		OpenSession: opener.open,
		Clock:       newFakeClock(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := m.Watch(ctx)

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Status != StatusConnected {
			t.Errorf("first event status = %v, want %v", ev.Status, StatusConnected)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a BoardEvent")
	}
}
