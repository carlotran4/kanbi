package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/kanban"
)

func TestFocusStatusDoesNotWaitForWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "focus.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "focus", "", "pi"); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, policy := range []FocusPolicy{{Enabled: false, Limit: 3}, {Enabled: true, Limit: 3}, {Enabled: true, Limit: 3, WorkflowKeys: []string{view.Columns[0].WorkflowKey}}} {
		store.SetFocusPolicy(policy)
		deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		status, err := store.FocusStatus(deadline)
		cancel()
		if err != nil {
			t.Fatalf("read waited for writer: %v", err)
		}
		if status.Enabled != policy.Enabled || (len(policy.WorkflowKeys) > 0 && status.Used != 1) {
			t.Fatalf("unexpected status: %+v", status)
		}
	}
}

func TestRuntimeObservationCannotOverwriteLifecycleChanges(t *testing.T) {
	ctx := context.Background()
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "observe", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "fixture", TmuxWindowName: "ticket", Status: kanban.StateRunning})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{kanban.StateClosing, kanban.StateNeedsPermission, kanban.StateClosed} {
		observed, ok, err := store.SessionByID(ctx, id)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if state == kanban.StateClosed {
			err = store.MarkSessionClosed(ctx, id, state, "manual", "")
		} else {
			err = store.UpdateSessionRuntime(ctx, id, state, "manual", "", "", false)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateObservedSessionRuntime(ctx, observed, kanban.StateRunning, "heuristic", "", "old output", true); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkObservedSessionMissing(ctx, observed); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordObservedSessionFailure(ctx, observed, "tmux", "old failure"); err != nil {
			t.Fatal(err)
		}
		fresh, _, err := store.SessionByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if fresh.Status != state || fresh.LastDetectionSource.String != "manual" {
			t.Fatalf("stale poll overwrote %s: %+v", state, fresh)
		}
	}
}

func TestActiveRuntimeQueryOmitsInactiveMetadata(t *testing.T) {
	ctx := context.Background()
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.CreateTicket(ctx, view.Columns[0].ID, "active", "large body excluded", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "inactive", "other body", "pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, active.ID, Session{Harness: "pi", TmuxSessionName: "fixture", TmuxWindowName: "ticket", Status: kanban.StateRunning}); err != nil {
		t.Fatal(err)
	}
	tickets, err := store.ActiveRuntimeTickets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 || tickets[0].ID != active.ID || tickets[0].Body != "" || !tickets[0].SessionActive {
		t.Fatalf("wrong runtime projection: %+v", tickets)
	}
}
