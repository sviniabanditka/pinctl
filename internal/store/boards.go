package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

const currentBoardSchemaVersion = 1

// boardsFile is the on-disk shape of boards.json (docs/09-data-model.md
// "Board object").
type boardsFile struct {
	SchemaVersion int           `json:"schemaVersion"`
	Boards        []model.Board `json:"boards"`
}

func (s *Store) loadBoards() error {
	data, err := os.ReadFile(s.boardsPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: reading boards.json: %w", err)
	}

	var f boardsFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Printf("store: boards.json is corrupted, skipping (starting with an empty board list): %v", err)
		return nil
	}
	if f.SchemaVersion > currentBoardSchemaVersion {
		log.Printf("store: boards.json has schemaVersion %d, newer than this build understands (%d); upgrade pinctl. Skipping.", f.SchemaVersion, currentBoardSchemaVersion)
		return nil
	}

	for _, b := range f.Boards {
		s.boards[b.ID] = b
	}
	return nil
}

// saveBoardsLocked rewrites the whole boards.json collection. Caller must
// hold s.mu.
func (s *Store) saveBoardsLocked() error {
	list := make([]model.Board, 0, len(s.boards))
	for _, b := range s.boards {
		list = append(list, b)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	data, err := json.MarshalIndent(boardsFile{
		SchemaVersion: currentBoardSchemaVersion,
		Boards:        list,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshaling boards.json: %w", err)
	}
	return writeFileAtomic(s.dir, s.tmpDir, s.boardsPath, data, filePerm)
}

// ListBoards returns all known boards, ordered by ID.
func (s *Store) ListBoards() []model.Board {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]model.Board, 0, len(s.boards))
	for _, b := range s.boards {
		list = append(list, b)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// GetBoard returns the board with the given ID, or ErrNotFound.
func (s *Store) GetBoard(id string) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.boards[id]
	if !ok {
		return model.Board{}, fmt.Errorf("store: board %s: %w", id, ErrNotFound)
	}
	return b, nil
}

// GetBoardByIdentity looks up a board by its stable hardware identity
// (docs/05-serial-protocol.md "Board identity"), used by internal/board to
// re-associate a physical device with its persisted Board entry on every
// (re)connect.
func (s *Store) GetBoardByIdentity(kind, key string) (model.Board, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, b := range s.boards {
		if b.Identity.Kind == kind && b.Identity.Key == key {
			return b, true
		}
	}
	return model.Board{}, false
}

// CreateBoard assigns a new ID and timestamps to b and persists boards.json.
// Used both for explicit board registration and by internal/board.Manager
// to auto-register a newly-discovered board identity on first connect.
func (s *Store) CreateBoard(b model.Board) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if b.Name == "" {
		return model.Board{}, ErrNameEmpty
	}

	now := time.Now().UTC()
	b.ID = model.NewID("brd")
	b.CreatedAt = now
	b.UpdatedAt = now

	s.boards[b.ID] = b
	if err := s.saveBoardsLocked(); err != nil {
		delete(s.boards, b.ID)
		return model.Board{}, err
	}
	return b, nil
}

// UpdateBoardPortHint bumps a board's portHint (and UpdatedAt) after a
// rescan re-resolves the physical port it's plugged into — see
// docs/09-data-model.md's "portHint... diagnostic/tie-breaker only".
func (s *Store) UpdateBoardPortHint(id, portHint string) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.boards[id]
	if !ok {
		return model.Board{}, fmt.Errorf("store: board %s: %w", id, ErrNotFound)
	}
	if b.PortHint == portHint {
		return b, nil
	}

	prev := b
	b.PortHint = portHint
	b.UpdatedAt = time.Now().UTC()

	s.boards[id] = b
	if err := s.saveBoardsLocked(); err != nil {
		s.boards[id] = prev
		return model.Board{}, err
	}
	return b, nil
}

// RenameBoard changes a board's display name (only editable field via
// PATCH /api/v1/boards/{id}, per docs/10-api.md).
func (s *Store) RenameBoard(id, name string) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.boards[id]
	if !ok {
		return model.Board{}, fmt.Errorf("store: board %s: %w", id, ErrNotFound)
	}
	if name == "" {
		return model.Board{}, ErrNameEmpty
	}

	prev := b
	b.Name = name
	b.UpdatedAt = time.Now().UTC()

	s.boards[id] = b
	if err := s.saveBoardsLocked(); err != nil {
		s.boards[id] = prev
		return model.Board{}, err
	}
	return b, nil
}
