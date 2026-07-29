package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/ticketbackend"
)

type recordingSyncer struct{ boardIDs []int64 }

func (s *recordingSyncer) SyncBoard(context.Context, storage.Board) (ticketbackend.Result, error) {
	return ticketbackend.Result{}, nil
}
func (s *recordingSyncer) ScheduleBoardSync(boardID int64) { s.boardIDs = append(s.boardIDs, boardID) }

type renameManager struct {
	SessionManager
	store         *storage.Store
	renames       []string
	titleAtRename string
	err           error
}

func (m *renameManager) RenameTicketWindow(ctx context.Context, ticket storage.Ticket, title string) error {
	m.renames = append(m.renames, title)
	if m.store != nil {
		current, err := m.store.TicketByID(ctx, ticket.ID)
		if err != nil {
			return err
		}
		m.titleAtRename = current.Title
	}
	return m.err
}

func newMutationService(t *testing.T) (context.Context, *storage.Store, storage.BoardView, *recordingSyncer, *renameManager, *Service) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	syncer := &recordingSyncer{}
	manager := &renameManager{store: store}
	return ctx, store, view, syncer, manager, NewServiceWithSyncer(store, manager, syncer)
}

func TestTicketMutationsScheduleOwningBoardExactlyOnce(t *testing.T) {
	ctx, store, view, syncer, _, service := newMutationService(t)
	ticket, err := service.CreateTicket(ctx, view.Columns[0].ID, "Created", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTicket(ctx, ticket.ID, "Updated", "body", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := service.MoveTicket(ctx, ticket.ID, view.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	if want := []int64{view.Board.ID, view.Board.ID, view.Board.ID}; !reflect.DeepEqual(syncer.boardIDs, want) {
		t.Fatalf("scheduled boards=%v, want %v", syncer.boardIDs, want)
	}
	updated, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Updated" || updated.ColumnID != view.Columns[1].ID {
		t.Fatalf("ticket after mutations=%+v", updated)
	}
}

func TestUpdateTicketRenamesBeforePersistenceAndSchedulesAfterSuccess(t *testing.T) {
	ctx, store, view, syncer, manager, service := newMutationService(t)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Original", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxWindowName: "live", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTicket(ctx, ticket.ID, "Updated", "body", "pi"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manager.renames, []string{"Updated"}) || manager.titleAtRename != "Original" {
		t.Fatalf("rename calls=%v title at rename=%q", manager.renames, manager.titleAtRename)
	}
	updated, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Updated" {
		t.Fatalf("stored title=%q, want Updated", updated.Title)
	}
	if !reflect.DeepEqual(syncer.boardIDs, []int64{view.Board.ID}) {
		t.Fatalf("scheduled boards=%v", syncer.boardIDs)
	}
}

func TestRejectedTicketMutationsHaveNoRuntimeOrSyncSideEffects(t *testing.T) {
	ctx, store, view, syncer, manager, service := newMutationService(t)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Original", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxWindowName: "live", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTicket(ctx, ticket.ID, "Changed", "changed", "unsupported"); err == nil {
		t.Fatal("invalid update succeeded")
	}
	manager.err = errors.New("rename failed")
	if err := service.UpdateTicket(ctx, ticket.ID, "Changed", "changed", "pi"); !errors.Is(err, manager.err) {
		t.Fatalf("rename failure=%v, want %v", err, manager.err)
	}
	if err := service.MoveTicket(ctx, ticket.ID, 9999); err == nil {
		t.Fatal("invalid move succeeded")
	}
	if _, err := service.CreateTicket(ctx, view.Columns[0].ID, " ", "", "pi"); err == nil {
		t.Fatal("invalid create succeeded")
	}
	updated, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Original" || updated.Body != "body" || updated.Harness != "pi" {
		t.Fatalf("rejected mutation changed ticket=%+v", updated)
	}
	if len(syncer.boardIDs) != 0 {
		t.Fatalf("rejected mutations scheduled boards=%v", syncer.boardIDs)
	}
}

func TestUpdateTicketSkipsRenameForUnchangedTitleOrNoWindow(t *testing.T) {
	ctx, store, view, syncer, manager, service := newMutationService(t)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Original", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTicket(ctx, ticket.ID, "Original", "new body", "pi"); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTicket(ctx, ticket.ID, "Changed", "new body", "pi"); err != nil {
		t.Fatal(err)
	}
	if len(manager.renames) != 0 {
		t.Fatalf("unexpected rename calls=%v", manager.renames)
	}
	if want := []int64{view.Board.ID, view.Board.ID}; !reflect.DeepEqual(syncer.boardIDs, want) {
		t.Fatalf("scheduled boards=%v, want %v", syncer.boardIDs, want)
	}
}
