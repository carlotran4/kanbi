package tui

import (
	"context"
	"testing"
	"time"

	"kanbi/internal/storage"
	"kanbi/internal/ticketbackend"
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
