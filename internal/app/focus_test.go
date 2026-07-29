package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

type focusTestManager struct {
	SessionManager
	store      *storage.Store
	closeErr   error
	closeCalls int
	openCalls  int
	openSend   bool
	message    string
}

func (m *focusTestManager) OpenTicket(_ context.Context, _ storage.Ticket, sendPrompt bool) error {
	m.openCalls++
	m.openSend = sendPrompt
	return nil
}

func (m *focusTestManager) SendTicketMessage(_ context.Context, _ storage.Ticket, message string) error {
	m.message = message
	return nil
}

func (m *focusTestManager) CloseSession(ctx context.Context, ticket storage.Ticket) error {
	m.closeCalls++
	if m.closeErr != nil {
		return m.closeErr
	}
	return m.store.MarkSessionClosed(ctx, ticket.SessionID.Int64, "closed", "test", "paused")
}

func focusTestService(t *testing.T) (*Service, *focusTestManager, storage.Ticket) {
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
	if err := store.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "focus"); err != nil {
		t.Fatal(err)
	}
	store.SetFocusPolicy(storage.FocusPolicy{Enabled: true, Limit: 3, WorkflowKeys: []string{"focus"}})
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "focus work", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "test", TmuxWindowName: "ticket", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	ticket, err = store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager := &focusTestManager{store: store}
	return NewService(store, manager), manager, ticket
}

func TestPauseValidatesBeforeClosingSession(t *testing.T) {
	svc, manager, ticket := focusTestService(t)
	if err := svc.PauseTicket(context.Background(), ticket.ID, "", "done", "next"); err == nil {
		t.Fatal("expected validation error")
	}
	if manager.closeCalls != 0 {
		t.Fatalf("close calls=%d, want 0", manager.closeCalls)
	}
	got, _ := svc.Store.TicketByID(context.Background(), ticket.ID)
	if got.FocusPaused || !got.SessionActive {
		t.Fatalf("invalid handoff changed lifecycle: %+v", got)
	}
}

func TestResumeUsesNormalOpenWithoutSendingPrompt(t *testing.T) {
	svc, manager, ticket := focusTestService(t)
	ctx := context.Background()
	if err := svc.PauseTicket(ctx, ticket.ID, "why", "done", "next"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumePausedTicket(ctx, ticket.ID, false); err != nil {
		t.Fatal(err)
	}
	if manager.openCalls != 1 || manager.openSend {
		t.Fatalf("open calls=%d sendPrompt=%v, want one prompt-free open", manager.openCalls, manager.openSend)
	}
}

func TestSendPauseHandoffAfterRepairUsesStructuredCheckpoint(t *testing.T) {
	svc, manager, ticket := focusTestService(t)
	checkpoint := &storage.PauseCheckpoint{Why: "waiting", Completed: "storage", NextAction: "finish UI"}
	if err := svc.SendPauseHandoff(context.Background(), ticket, checkpoint); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Resuming paused work", "**Why this was paused**\nwaiting", "**Already completed**\nstorage", "**Next action**\nfinish UI"} {
		if !strings.Contains(manager.message, want) {
			t.Fatalf("handoff missing %q: %q", want, manager.message)
		}
	}
}

func TestPauseCloseFailureDoesNotCreateCheckpoint(t *testing.T) {
	svc, manager, ticket := focusTestService(t)
	manager.closeErr = errors.New("close failed")
	if err := svc.PauseTicket(context.Background(), ticket.ID, "why", "done", "next"); err == nil {
		t.Fatal("expected close error")
	}
	got, _ := svc.Store.TicketByID(context.Background(), ticket.ID)
	if got.FocusPaused || got.LatestCheckpoint != nil || !got.SessionActive {
		t.Fatalf("close failure changed pause state: %+v", got)
	}
}
