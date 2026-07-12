package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/attachments"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/ticketbackend"
)

type recordingTicketSyncer struct {
	boards chan storage.Board
}

func (s *recordingTicketSyncer) SyncBoard(ctx context.Context, board storage.Board) (ticketbackend.Result, error) {
	s.boards <- board
	return ticketbackend.Result{}, nil
}

func TestServiceSyncsBoardAfterSavedTicketChanges(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote", Workdir: t.TempDir(), TicketBackend: ticketbackend.KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	syncer := &recordingTicketSyncer{boards: make(chan storage.Board, 10)}
	service := NewServiceWithSyncer(store, nil, syncer)

	ticket, err := service.CreateTicket(ctx, view.Columns[0].ID, "New ticket", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	assertNoImmediateSync(t, syncer)

	if err := service.UpdateTicket(ctx, ticket.ID, "Created", "body", "codex"); err != nil {
		t.Fatal(err)
	}
	assertSyncedBoard(t, syncer, board.ID)

	if err := service.MoveTicket(ctx, ticket.ID, view.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	assertSyncedBoard(t, syncer, board.ID)

	note, err := service.AddNote(ctx, ticket.ID, "note")
	if err != nil {
		t.Fatal(err)
	}
	assertSyncedBoard(t, syncer, board.ID)

	if err := service.UpdateNote(ctx, note.ID, "updated note"); err != nil {
		t.Fatal(err)
	}
	assertSyncedBoard(t, syncer, board.ID)

	if err := service.DeleteNote(ctx, note.ID); err != nil {
		t.Fatal(err)
	}
	assertSyncedBoard(t, syncer, board.ID)

	if err := service.ArchiveTicket(ctx, ticket.ID); err != nil {
		t.Fatal(err)
	}
	assertSyncedBoard(t, syncer, board.ID)
}

func TestServiceDeleteBoardRemovesOwnedAttachments(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := store.CreateBoard(ctx, "Disposable")
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Delete with board", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	attachment := filepath.Join(attachments.TicketDir(ticket.ID), "image.png")
	if err := os.MkdirAll(filepath.Dir(attachment), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachment, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewService(store, nil).DeleteBoard(ctx, board.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attachments.TicketDir(ticket.ID)); !os.IsNotExist(err) {
		t.Fatalf("attachment directory remains after board deletion: %v", err)
	}
}

func assertNoImmediateSync(t *testing.T, syncer *recordingTicketSyncer) {
	t.Helper()
	select {
	case board := <-syncer.boards:
		t.Fatalf("unexpected sync for board %d", board.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertSyncedBoard(t *testing.T, syncer *recordingTicketSyncer, boardID int64) {
	t.Helper()
	select {
	case board := <-syncer.boards:
		if board.ID != boardID {
			t.Fatalf("synced board id = %d, want %d", board.ID, boardID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for board %d sync", boardID)
	}
}
