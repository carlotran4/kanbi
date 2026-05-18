package storage

import (
	"context"
	"database/sql"
	"testing"
)

func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func TestInitCreatesDefaultBoardAndColumnsIdempotently(t *testing.T) {
	s, ctx := newTestStore(t)
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Open", "In Progress", "Review", "Done"}
	if len(view.Columns) != len(want) {
		t.Fatalf("columns = %d", len(view.Columns))
	}
	for i := range want {
		if view.Columns[i].Name != want[i] {
			t.Fatalf("column %d = %s", i, view.Columns[i].Name)
		}
	}
}

func TestTicketDisplayIDsCreateListArchive(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	first, err := s.CreateTicket(ctx, view.Columns[0].ID, "One", "Body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateTicket(ctx, view.Columns[0].ID, "Two", "", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if first.DisplayID != "T-001" || second.DisplayID != "T-002" {
		t.Fatalf("display ids = %s %s", first.DisplayID, second.DisplayID)
	}
	tickets, err := s.ListTickets(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 {
		t.Fatalf("ticket count = %d", len(tickets))
	}
	if err := s.ArchiveTicket(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	tickets, _ = s.ListTickets(ctx, false)
	if len(tickets) != 1 || tickets[0].DisplayID != "T-002" {
		t.Fatalf("archive list = %+v", tickets)
	}
}

func TestTicketMoveAndReorder(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	a, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	b, _ := s.CreateTicket(ctx, view.Columns[0].ID, "B", "", "pi")
	if err := s.ReorderTicket(ctx, b.ID, -1); err != nil {
		t.Fatal(err)
	}
	tickets, _ := s.TicketsForColumn(ctx, view.Columns[0].ID)
	if tickets[0].DisplayID != b.DisplayID || tickets[1].DisplayID != a.DisplayID {
		t.Fatalf("reorder failed: %+v", tickets)
	}
	if err := s.MoveTicket(ctx, b.ID, view.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	open, _ := s.TicketsForColumn(ctx, view.Columns[0].ID)
	progress, _ := s.TicketsForColumn(ctx, view.Columns[1].ID)
	if len(open) != 1 || open[0].Position != 0 || len(progress) != 1 || progress[0].ColumnID != view.Columns[1].ID {
		t.Fatalf("move/compact failed open=%+v progress=%+v", open, progress)
	}
}

func TestColumnEditOperations(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	added, err := s.AddColumn(ctx, view.Board.ID, "Blocked")
	if err != nil {
		t.Fatal(err)
	}
	if added.Position != 4 {
		t.Fatalf("added position = %d", added.Position)
	}
	if err := s.RenameColumn(ctx, added.ID, "Later"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderColumn(ctx, added.ID, -1); err != nil {
		t.Fatal(err)
	}
	view, _ = s.BoardView(ctx)
	if view.Columns[3].Name != "Later" {
		t.Fatalf("reordered/renamed columns = %+v", view.Columns)
	}
	if _, err := s.CreateTicket(ctx, added.ID, "Do not delete", "", "pi"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteColumn(ctx, added.ID); err == nil {
		t.Fatal("expected non-empty delete to fail")
	}
	tickets, _ := s.TicketsForColumn(ctx, added.ID)
	if err := s.ArchiveTicket(ctx, tickets[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteColumn(ctx, added.ID); err != nil {
		t.Fatalf("archived-only column should delete: %v", err)
	}
	archived, err := s.TicketByDisplayID(ctx, tickets[0].DisplayID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ColumnID == added.ID {
		t.Fatalf("archived ticket still points at deleted column: %+v", archived)
	}
	empty, err := s.AddColumn(ctx, view.Board.ID, "Empty")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteColumn(ctx, empty.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSessionsRecordRuntimeMetadata(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	id, err := s.UpsertActiveSession(ctx, ticket.ID, Session{
		Harness:         "pi",
		TmuxSessionName: "agent-kanban",
		TmuxWindowID:    sql.NullString{String: "@7", Valid: true},
		TmuxWindowName:  "T-001-a",
		Status:          "running",
	})
	if err != nil || id == 0 {
		t.Fatalf("session id=%d err=%v", id, err)
	}
	got, ok, err := s.ActiveSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("active session ok=%v err=%v", ok, err)
	}
	if got.TmuxWindowName != "T-001-a" || got.TmuxWindowID.String != "@7" || got.Status != "running" {
		t.Fatalf("session = %+v", got)
	}
	if !got.StartedAt.Valid || !got.LastStateChangeAt.Valid || got.LastDetectedState.String != "running" {
		t.Fatalf("runtime metadata missing = %+v", got)
	}
	if err := s.UpdateSessionRuntime(ctx, got.ID, "waiting_for_user", "pattern", "waiting", "PROMPT_READY", true); err != nil {
		t.Fatal(err)
	}
	listed, err := s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if listed.WindowID.String != "@7" || listed.WindowName.String != "T-001-a" {
		t.Fatalf("ticket session metadata = %+v", listed)
	}
	if listed.Runtime != "waiting_for_user" || !listed.LastOutputAt.Valid || listed.LastAttentionReason.String != "waiting" {
		t.Fatalf("ticket runtime metadata = %+v", listed)
	}
	if err := s.MarkSessionMissing(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ActiveSession(ctx, ticket.ID); ok {
		t.Fatal("missing session should not remain active")
	}
	listed, err = s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Runtime != "error" || listed.SessionActive || listed.LastAttentionReason.String != "tmux window missing" {
		t.Fatalf("inactive error should project to ticket: %+v", listed)
	}
	id, err = s.UpsertActiveSession(ctx, ticket.ID, Session{
		Harness:         "pi",
		TmuxSessionName: "agent-kanban",
		TmuxWindowID:    sql.NullString{String: "@8", Valid: true},
		TmuxWindowName:  "T-001-a",
		Status:          "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSessionClosed(ctx, id, "closed", "tmux", "done"); err != nil {
		t.Fatal(err)
	}
	listed, err = s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Runtime != "closed" || listed.SessionActive || listed.LastAttentionReason.String != "done" {
		t.Fatalf("inactive closed should project to ticket: %+v", listed)
	}
}
