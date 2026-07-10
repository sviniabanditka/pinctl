package board

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// TestManagerNetworkBoardBypassesSerialAllowlistAndGetsNetworkIdentity
// covers docs/17-network-boards.md's "Identity" and the Manager doc
// comment's "not glob-matched against serial.allowlist — an explicitly
// configured address is allowlisted by definition": a configured network
// address is dialled and registered even when serial.allowlist doesn't
// (and couldn't sensibly) match it, and its persisted identity is
// {kind: "network", key: <address, verbatim>}.
func TestManagerNetworkBoardBypassesSerialAllowlistAndGetsNetworkIdentity(t *testing.T) {
	fs := newFakeBoardStore()
	opener := &scriptedOpener{t: t, fail: map[string]error{}}
	const addr = "tcp://esp32-porch.local:3030"

	var readyBoardID string
	m := NewManager(ManagerConfig{
		Allowlist:     []string{"/dev/ttyACM0"}, // deliberately never matches addr
		NetworkBoards: []string{addr},
		Store:         fs,
		PortLister:    func() ([]PortInfo, error) { return nil, nil },
		OpenSession:   opener.open,
		Clock:         newFakeClock(),
		OnReady:       func(boardID string, sess *Session) { readyBoardID = boardID },
	})

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	boards := m.Boards()
	if len(boards) != 1 {
		t.Fatalf("len(Boards()) = %d, want 1 (auto-registered network board)", len(boards))
	}
	wantIdentity := model.Identity{Kind: "network", Key: addr}
	if boards[0].Identity != wantIdentity {
		t.Errorf("Identity = %+v, want %+v", boards[0].Identity, wantIdentity)
	}

	id := boards[0].ID
	info := waitForBoardStatus(t, m, id, StatusConnected)
	if info.Port != addr {
		t.Errorf("Port = %q, want %q", info.Port, addr)
	}
	if readyBoardID != id {
		t.Errorf("OnReady called with boardID %q, want %q", readyBoardID, id)
	}
}

// TestManagerEmptyNetworkBoardsDialsNothing covers "An empty/absent list
// means no dialling and no goroutine": with NetworkBoards unset, a Scan
// against an otherwise-empty serial port list must register nothing and
// call OpenSession zero times.
func TestManagerEmptyNetworkBoardsDialsNothing(t *testing.T) {
	fs := newFakeBoardStore()
	opener := &scriptedOpener{t: t, fail: map[string]error{}}

	m := NewManager(ManagerConfig{
		Store:       fs,
		PortLister:  func() ([]PortInfo, error) { return nil, nil },
		OpenSession: opener.open,
		Clock:       newFakeClock(),
	})

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(m.Boards()) != 0 {
		t.Errorf("Boards() = %v, want empty", m.Boards())
	}
	if got := opener.calls(); got != 0 {
		t.Errorf("OpenSession called %d times, want 0", got)
	}
}

// TestManagerNetworkBoardRedialsWithBackoffAndRerunsOnReady drives a
// network board through the same drop/backoff/reconnect state machine
// docs/17-network-boards.md's failure-mode table requires ("WiFi drops
// and returns: the socket dies, the manager redials..."), asserting
// nothing about the reconnect path was forked for network boards.
func TestManagerNetworkBoardRedialsWithBackoffAndRerunsOnReady(t *testing.T) {
	fs := newFakeBoardStore()
	opener := &scriptedOpener{t: t, fail: map[string]error{}}
	clk := newFakeClock()
	const addr = "tcp://esp32-porch.local:3030"

	var mu sync.Mutex
	var onReadyCalls int
	m := NewManager(ManagerConfig{
		NetworkBoards: []string{addr},
		Store:         fs,
		PortLister:    func() ([]PortInfo, error) { return nil, nil },
		OpenSession:   opener.open,
		Clock:         clk,
		Backoff:       []time.Duration{time.Second},
		OnReady: func(boardID string, sess *Session) {
			mu.Lock()
			onReadyCalls++
			mu.Unlock()
		},
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

	// Simulate "WiFi drops": the socket dies without any Manager-level
	// intervention.
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, time.Second, func() bool { return !sess.Alive() })

	if err := m.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// The dead session is detected on this Scan; since OpenSession always
	// succeeds and the address is still "discovered" (network addresses
	// are always present, per the Manager doc comment), it reconnects
	// immediately in the same pass — the exact same behavior
	// TestManagerHealthCheckDetectsDeadSession exercises for a serial
	// board, reused unchanged here.
	waitForBoardStatus(t, m, id, StatusConnected)

	newSess, err := m.Session(id)
	if err != nil {
		t.Fatalf("Session after reconnect: %v", err)
	}
	if newSess == sess {
		t.Error("expected a fresh Session after reconnect, got the same (dead) one")
	}

	mu.Lock()
	got := onReadyCalls
	mu.Unlock()
	if got < 2 {
		t.Errorf("OnReady called %d times, want at least 2 (initial connect + reconnect)", got)
	}
}

// TestManagerCloseDuringNetworkDialDoesNotLeakOrFireOnReady mirrors
// TestManagerCloseDuringConnectDoesNotLeakOrFireOnReady for a network
// board: Close() must wait for an in-flight dial and, if it raced Close,
// must close the session it just opened rather than install it or fire
// OnReady — the exact same guarantee, exercised against the network
// discovery path instead of serial's.
func TestManagerCloseDuringNetworkDialDoesNotLeakOrFireOnReady(t *testing.T) {
	fs := newFakeBoardStore()
	opener := &slowOpener{t: t, release: make(chan struct{})}
	const addr = "tcp://esp32-porch.local:3030"

	var onReadyCalled bool
	var onReadyMu sync.Mutex
	m := NewManager(ManagerConfig{
		NetworkBoards: []string{addr},
		Store:         fs,
		PortLister:    func() ([]PortInfo, error) { return nil, nil },
		OpenSession:   opener.open,
		Clock:         newFakeClock(),
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

	closeDone := make(chan struct{})
	go func() {
		m.Close()
		close(closeDone)
	}()

	time.Sleep(20 * time.Millisecond)
	close(opener.release)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return — it should wait for the in-flight dial and then finish")
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
		t.Error("OnReady must not be called for a dial that races Close")
	}

	if _, err := m.Session(id); err == nil {
		t.Errorf("Session(%s) after Close = nil error, want ErrBoardOffline (no session installed)", id)
	}

	waitFor(t, time.Second, opener.allOpenedClosed)
}
