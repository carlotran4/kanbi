package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestFocusAdmissionIsGlobalAndMoveClearsPause(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := s.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetColumnWorkflowKey(ctx, first.Columns[0].ID, "doing"); err != nil {
		t.Fatal(err)
	}
	board, err := s.CreateBoardWithWorkdir(ctx, "Other", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetColumnWorkflowKey(ctx, other.Columns[0].ID, "doing"); err != nil {
		t.Fatal(err)
	}
	s.SetFocusPolicy(FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"doing"}})
	one, err := s.CreateTicket(ctx, first.Columns[0].ID, "one", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(ctx, other.Columns[0].ID, "two", "", "pi"); !errors.Is(err, ErrFocusCapacity) {
		t.Fatalf("create error=%v, want capacity", err)
	}
	if err := s.PauseTicket(ctx, one.ID, "switching", "investigated", "resume test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(ctx, other.Columns[0].ID, "two", "", "pi"); err != nil {
		t.Fatalf("paused ticket should free global slot: %v", err)
	}
	if err := s.MoveTicket(ctx, one.ID, first.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	moved, err := s.TicketByID(ctx, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.FocusPaused {
		t.Fatal("moving outside focus must clear paused state")
	}
	if moved.LatestCheckpoint == nil || !moved.LatestCheckpoint.ResumedAt.Valid {
		t.Fatal("moving outside focus must close checkpoint")
	}
}

func TestMoveFromNonFocusNeverArrivesPausedAfterPolicyKeyChange(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenMemory()
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "focus"); err != nil {
		t.Fatal(err)
	}
	s.SetFocusPolicy(FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"focus"}})
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "one", "", "pi")
	if err := s.PauseTicket(ctx, ticket.ID, "why", "done", "next"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "old-focus"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[1].ID, "focus"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTicket(ctx, ticket.ID, view.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.TicketByID(ctx, ticket.ID)
	if got.FocusPaused || got.LatestCheckpoint == nil || !got.LatestCheckpoint.ResumedAt.Valid {
		t.Fatalf("move silently preserved pause: %+v", got)
	}
}

func TestFocusResumeCapacityAndCheckpointHistory(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenMemory()
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "doing"); err != nil {
		t.Fatal(err)
	}
	s.SetFocusPolicy(FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"doing"}})
	one, _ := s.CreateTicket(ctx, view.Columns[0].ID, "one", "", "pi")
	if err := s.PauseTicket(ctx, one.ID, "why", "done", "next"); err != nil {
		t.Fatal(err)
	}
	two, err := s.CreateTicket(ctx, view.Columns[0].ID, "two", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeTicket(ctx, one.ID); !errors.Is(err, ErrFocusCapacity) {
		t.Fatalf("resume error=%v, want capacity", err)
	}
	if err := s.PauseTicket(ctx, two.ID, "why2", "done2", "next2"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeTicket(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	one, _ = s.TicketByID(ctx, one.ID)
	if one.FocusPaused || one.LatestCheckpoint == nil || !one.LatestCheckpoint.ResumedAt.Valid {
		t.Fatal("resume should claim focus and close latest checkpoint")
	}
	status, err := s.FocusStatus(ctx)
	if err != nil || status.Used != 1 || status.OverCapacity {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestPauseAndMoveTransfersSlotAtomically(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenMemory()
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[1].ID, "doing"); err != nil {
		t.Fatal(err)
	}
	s.SetFocusPolicy(FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"doing"}})
	focused, _ := s.CreateTicket(ctx, view.Columns[1].ID, "focused", "", "pi")
	incoming, _ := s.CreateTicket(ctx, view.Columns[0].ID, "incoming", "", "pi")
	if err := s.PauseAndMove(ctx, focused.ID, incoming.ID, view.Columns[1].ID, "why", "done", "next"); err != nil {
		t.Fatal(err)
	}
	focused, _ = s.TicketByID(ctx, focused.ID)
	incoming, _ = s.TicketByID(ctx, incoming.ID)
	if !focused.FocusPaused || incoming.ColumnID != view.Columns[1].ID || incoming.FocusPaused {
		t.Fatalf("focused=%+v incoming=%+v", focused, incoming)
	}
	status, _ := s.FocusStatus(ctx)
	if status.Used != 1 {
		t.Fatalf("used=%d, want 1", status.Used)
	}
}

func TestConcurrentFinalSlotAdmission(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "focus.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	if err := s1.Init(ctx); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s1.BoardView(ctx)
	if err := s1.SetColumnWorkflowKey(ctx, view.Columns[1].ID, "doing"); err != nil {
		t.Fatal(err)
	}
	policy := FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"doing"}}
	s1.SetFocusPolicy(policy)
	s2.SetFocusPolicy(policy)
	t1, _ := s1.CreateTicket(ctx, view.Columns[0].ID, "one", "", "pi")
	t2, _ := s1.CreateTicket(ctx, view.Columns[0].ID, "two", "", "pi")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, attempt := range []struct {
		store  *Store
		ticket int64
	}{{s1, t1.ID}, {s2, t2.ID}} {
		wg.Add(1)
		go func(a struct {
			store  *Store
			ticket int64
		}) {
			defer wg.Done()
			<-start
			results <- a.store.MoveTicket(ctx, a.ticket, view.Columns[1].ID)
		}(attempt)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, full := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrFocusCapacity):
			full++
		default:
			t.Fatalf("unexpected admission error: %v", err)
		}
	}
	if succeeded != 1 || full != 1 {
		t.Fatalf("succeeded=%d capacity=%d", succeeded, full)
	}
}

func TestProviderMoveOutsideFocusClosesLocalPause(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenMemory()
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "doing"); err != nil {
		t.Fatal(err)
	}
	s.SetFocusPolicy(FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"doing"}})
	rt := RemoteTicket{BoardID: view.Board.ID, ColumnID: view.Columns[0].ID, ExternalID: "r1", DisplayID: "R-1", DisplayNumber: 1, Title: "one"}
	ticket, err := s.UpsertRemoteTicket(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PauseTicket(ctx, ticket.ID, "why", "done", "next"); err != nil {
		t.Fatal(err)
	}
	rt.ColumnID = view.Columns[1].ID
	if _, err := s.UpsertRemoteTicket(ctx, rt); err != nil {
		t.Fatal(err)
	}
	got, _ := s.TicketByID(ctx, ticket.ID)
	if got.FocusPaused || got.LatestCheckpoint == nil || !got.LatestCheckpoint.ResumedAt.Valid {
		t.Fatalf("provider exit did not close local pause: %+v", got)
	}
}

func TestProviderFocusOverflowIsPreserved(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenMemory()
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "doing"); err != nil {
		t.Fatal(err)
	}
	s.SetFocusPolicy(FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"doing"}})
	board := view.Board
	if _, err := s.UpsertRemoteTicket(ctx, RemoteTicket{BoardID: board.ID, ColumnID: view.Columns[0].ID, ExternalID: "r1", DisplayID: "R-1", DisplayNumber: 1, Title: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRemoteTicket(ctx, RemoteTicket{BoardID: board.ID, ColumnID: view.Columns[0].ID, ExternalID: "r2", DisplayID: "R-2", DisplayNumber: 2, Title: "two"}); err != nil {
		t.Fatal(err)
	}
	status, err := s.FocusStatus(ctx)
	if err != nil || status.Used != 2 || !status.OverCapacity {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}
