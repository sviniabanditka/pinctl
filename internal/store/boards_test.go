package store

import (
	"errors"
	"testing"

	"github.com/sviniabanditka/pinctl/internal/model"
)

func TestBoardRoundTripAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	b, err := s.CreateBoard(model.Board{
		Name:     "Arduino Uno #1",
		Identity: model.Identity{Kind: "usb-serial", Key: "VID_2341:PID_0043:SN_ABC"},
		PortHint: "/dev/ttyACM0",
	})
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	if b.ID == "" || b.CreatedAt.IsZero() || b.UpdatedAt.IsZero() {
		t.Fatalf("CreateBoard did not populate ID/timestamps: %+v", b)
	}
	assertNoTempFiles(t, dir)

	s2 := mustOpen(t, dir)
	got, err := s2.GetBoard(b.ID)
	if err != nil {
		t.Fatalf("GetBoard after reopen: %v", err)
	}
	if got != b {
		t.Errorf("round-tripped board = %+v, want %+v", got, b)
	}
}

func TestGetBoardByIdentity(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	id := model.Identity{Kind: "usb-serial", Key: "VID_2341:PID_0043:SN_XYZ"}
	b, err := s.CreateBoard(model.Board{Name: "board", Identity: id})
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}

	got, ok := s.GetBoardByIdentity(id.Kind, id.Key)
	if !ok {
		t.Fatal("GetBoardByIdentity: not found")
	}
	if got.ID != b.ID {
		t.Errorf("GetBoardByIdentity id = %q, want %q", got.ID, b.ID)
	}

	if _, ok := s.GetBoardByIdentity("usb-serial", "VID_0000:PID_0000"); ok {
		t.Error("GetBoardByIdentity matched an unrelated identity")
	}
}

func TestUpdateBoardPortHint(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	b, err := s.CreateBoard(model.Board{Name: "board", Identity: model.Identity{Kind: "port-path", Key: "/dev/ttyACM0"}, PortHint: "/dev/ttyACM0"})
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}

	updated, err := s.UpdateBoardPortHint(b.ID, "/dev/ttyACM1")
	if err != nil {
		t.Fatalf("UpdateBoardPortHint: %v", err)
	}
	if updated.PortHint != "/dev/ttyACM1" {
		t.Errorf("PortHint = %q, want /dev/ttyACM1", updated.PortHint)
	}
	if !updated.UpdatedAt.After(b.UpdatedAt) && updated.UpdatedAt != b.UpdatedAt {
		t.Errorf("UpdatedAt not bumped")
	}
}

func TestRenameBoard(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	b, err := s.CreateBoard(model.Board{Name: "old name", Identity: model.Identity{Kind: "port-path", Key: "/dev/ttyACM0"}})
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}

	updated, err := s.RenameBoard(b.ID, "new name")
	if err != nil {
		t.Fatalf("RenameBoard: %v", err)
	}
	if updated.Name != "new name" {
		t.Errorf("Name = %q, want %q", updated.Name, "new name")
	}

	if _, err := s.RenameBoard("brd_doesnotexist", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RenameBoard(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := s.RenameBoard(b.ID, ""); !errors.Is(err, ErrNameEmpty) {
		t.Errorf("RenameBoard(empty name) = %v, want ErrNameEmpty", err)
	}
}

func TestListBoardsOrderedByID(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)

	if _, err := s.CreateBoard(model.Board{Name: "a", Identity: model.Identity{Kind: "port-path", Key: "/dev/ttyACM0"}}); err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	if _, err := s.CreateBoard(model.Board{Name: "b", Identity: model.Identity{Kind: "port-path", Key: "/dev/ttyACM1"}}); err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}

	list := s.ListBoards()
	if len(list) != 2 {
		t.Fatalf("len(ListBoards()) = %d, want 2", len(list))
	}
	if list[0].ID >= list[1].ID {
		t.Errorf("ListBoards() not sorted by ID: %v", list)
	}
}
