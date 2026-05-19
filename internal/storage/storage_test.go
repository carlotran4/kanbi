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

func TestUpdateTicketPersistsChanges(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Original", "body", "pi")
	if err := s.UpdateTicket(ctx, ticket.ID, "Updated Title", "new body", "codex"); err != nil {
		t.Fatal(err)
	}
	got, err := s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Updated Title" || got.Body != "new body" || got.Harness != "codex" {
		t.Fatalf("updated ticket = %+v", got)
	}
}

func TestRenameSessionWindowUpdatesLatestActive(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	_, err := s.UpsertActiveSession(ctx, ticket.ID, Session{
		Harness: "pi", TmuxSessionName: "agent-kanban",
		TmuxWindowName: "T-001-a", Status: "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RenameSessionWindow(ctx, ticket.ID, "T-001-new-name"); err != nil {
		t.Fatal(err)
	}
	ses, ok, err := s.ActiveSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("active session ok=%v err=%v", ok, err)
	}
	if ses.TmuxWindowName != "T-001-new-name" {
		t.Fatalf("window name = %q, want T-001-new-name", ses.TmuxWindowName)
	}
}

func TestUpdateSessionRefPersists(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	sessionID, _ := s.UpsertActiveSession(ctx, ticket.ID, Session{
		Harness: "pi", TmuxSessionName: "agent-kanban",
		TmuxWindowName: "T-001-a", Status: "running",
	})
	if err := s.UpdateSessionRef(ctx, sessionID, "019e-ref-abc"); err != nil {
		t.Fatal(err)
	}
	got, err := s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SessionRef.Valid || got.SessionRef.String != "019e-ref-abc" {
		t.Fatalf("session ref = %+v", got.SessionRef)
	}
}

func TestMarkTicketRuntimeUpdatesActiveSession(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	_, _ = s.UpsertActiveSession(ctx, ticket.ID, Session{
		Harness: "pi", TmuxSessionName: "agent-kanban",
		TmuxWindowName: "T-001-a", Status: "running",
	})
	if err := s.MarkTicketRuntime(ctx, ticket.ID, "waiting_for_user", "manual", "user override"); err != nil {
		t.Fatal(err)
	}
	got, err := s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "waiting_for_user" || got.LastDetectionSource.String != "manual" {
		t.Fatalf("runtime = %+v", got)
	}
}

func TestMarkTicketRuntimeFailsWithNoActiveSession(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	err := s.MarkTicketRuntime(ctx, ticket.ID, "running", "system", "")
	if err == nil {
		t.Fatal("expected error marking runtime with no active session")
	}
}

func TestMultipleSessionsHistoryPreservedOnUpsert(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")

	id1, _ := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "ak", TmuxWindowName: "T-001-a", Status: "running"})
	_ = s.MarkSessionClosed(ctx, id1, "closed", "tmux", "done")

	id2, _ := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "ak", TmuxWindowName: "T-001-a", Status: "running"})
	_ = s.MarkSessionClosed(ctx, id2, "closed", "tmux", "done")

	id3, _ := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "ak", TmuxWindowName: "T-001-a", Status: "running"})

	// All three session IDs must be distinct
	if id1 == id2 || id2 == id3 || id1 == id3 {
		t.Fatalf("session IDs should be distinct: %d %d %d", id1, id2, id3)
	}
	// Count rows in sessions table directly
	var count int
	if err := s.db.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=?`, ticket.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 session rows, got %d", count)
	}
	// Only the latest is active
	ses, ok, _ := s.ActiveSession(ctx, ticket.ID)
	if !ok || ses.ID != id3 {
		t.Fatalf("active session should be id3=%d, got id=%d ok=%v", id3, ses.ID, ok)
	}
}

func TestListTicketsIncludesArchived(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	t1, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Active", "", "pi")
	t2, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Archived", "", "pi")
	_ = s.ArchiveTicket(ctx, t2.ID)

	active, _ := s.ListTickets(ctx, false)
	if len(active) != 1 || active[0].ID != t1.ID {
		t.Fatalf("active list = %+v", active)
	}
	all, _ := s.ListTickets(ctx, true)
	if len(all) != 2 {
		t.Fatalf("all list = %+v", all)
	}
}

func TestTicketByDisplayIDCaseInsensitive(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	_, _ = s.CreateTicket(ctx, view.Columns[0].ID, "Case Test", "", "pi")
	got, err := s.TicketByDisplayID(ctx, "t-001")
	if err != nil {
		t.Fatalf("lowercase lookup failed: %v", err)
	}
	if got.DisplayID != "T-001" {
		t.Fatalf("display id = %q", got.DisplayID)
	}
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

func TestStartFreshPreservesSessionHistory(t *testing.T) {
	s, ctx := newTestStore(t)
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")

	// Create first session and mark it closed
	firstID, err := s.UpsertActiveSession(ctx, ticket.ID, Session{
		Harness:         "pi",
		TmuxSessionName: "agent-kanban",
		TmuxWindowName:  "T-001-a",
		Status:          "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSessionClosed(ctx, firstID, "closed", "tmux", "done"); err != nil {
		t.Fatal(err)
	}

	// Start fresh should create a NEW active session, not delete the old one
	secondID, err := s.UpsertActiveSession(ctx, ticket.ID, Session{
		Harness:         "pi",
		TmuxSessionName: "agent-kanban",
		TmuxWindowName:  "T-001-a",
		Status:          "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondID == firstID {
		t.Fatalf("start fresh should create new session row, got same id=%d", firstID)
	}

	// Verify old session is still in DB and is inactive
	var oldStatus string
	var oldActive int
	if err := s.db.QueryRowContext(ctx, `select status, is_active from sessions where id=?`, firstID).Scan(&oldStatus, &oldActive); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "closed" || oldActive != 0 {
		t.Fatalf("old session should remain as closed/inactive: status=%s active=%d", oldStatus, oldActive)
	}

	// Verify new session is active
	got, ok, err := s.ActiveSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("new session should be active ok=%v err=%v", ok, err)
	}
	if got.ID != secondID {
		t.Fatalf("active session should be new one: got id=%d want=%d", got.ID, secondID)
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
