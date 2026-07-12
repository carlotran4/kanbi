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
	store  *storage.Store
}

func (s *recordingTicketSyncer) SyncBoard(ctx context.Context, board storage.Board) (ticketbackend.Result, error) {
	s.boards <- board
	return ticketbackend.Result{}, nil
}

func (s *recordingTicketSyncer) ScheduleBoardSync(boardID int64) {
	if s == nil || s.store == nil || boardID == 0 {
		return
	}
	go func() {
		boards, err := s.store.ListBoards(context.Background())
		if err != nil {
			return
		}
		for _, board := range boards {
			if board.ID == boardID {
				_, _ = s.SyncBoard(context.Background(), board)
				return
			}
		}
	}()
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
	syncer := &recordingTicketSyncer{boards: make(chan storage.Board, 10), store: store}
	service := NewServiceWithSyncer(store, nil, syncer)

	ticket, err := service.CreateTicket(ctx, view.Columns[0].ID, "New ticket", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	// Local placeholders for remote boards schedule sync immediately so providers
	// receive create/push without waiting for the next periodic tick.
	assertSyncedBoard(t, syncer, board.ID)

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
