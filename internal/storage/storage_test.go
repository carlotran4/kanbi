package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent-kanban/internal/kanban"
)

func TestBoardWorkdirExpandsTilde(t *testing.T) {
	s, ctx := newTestStore(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "Developer", "personal_finance")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	b, err := s.CreateBoardWithWorkdir(ctx, "personal-finance", "~/Developer/personal_finance")
	if err != nil {
		t.Fatal(err)
	}
	if b.Workdir != project {
		t.Fatalf("created workdir = %q, want %q", b.Workdir, project)
	}

	other := filepath.Join(home, "Developer", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBoardWorkdir(ctx, b.ID, "~/Developer/other"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.BoardByName(ctx, "personal-finance")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Workdir != other {
		t.Fatalf("updated workdir = %q, want %q", updated.Workdir, other)
	}
}

func TestCreateBoardWithOptionsPersistsBackendMetadata(t *testing.T) {
	s, ctx := newTestStore(t)
	workdir := t.TempDir()
	board, err := s.CreateBoardWithOptions(ctx, CreateBoardOptions{
		Name:          "Remote",
		Workdir:       workdir,
		TicketBackend: "atlassian",
		BackendQuery:  "project = AK ORDER BY updated DESC",
		BackendConfig: `{"site":"example.atlassian.net"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.BoardByName(ctx, board.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.TicketBackend != "atlassian" || got.BackendQuery != "project = AK ORDER BY updated DESC" || got.BackendConfig == "" {
		t.Fatalf("backend metadata not persisted: %+v", got)
	}
}

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

func defaultBoardView(t *testing.T, ctx context.Context, s *Store) BoardView {
	t.Helper()
	view, err := s.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func boardViewByID(t *testing.T, ctx context.Context, s *Store, boardID int64) BoardView {
	t.Helper()
	view, err := s.BoardViewByID(ctx, boardID)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func createTicket(t *testing.T, ctx context.Context, s *Store, columnID int64, title, body, harness string) Ticket {
	t.Helper()
	ticket, err := s.CreateTicket(ctx, columnID, title, body, harness)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func TestRemoteTicketAdvancesLocalTicketNumber(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoardWithWorkdir(ctx, "Remote", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	view := boardViewByID(t, ctx, s, board.ID)
	if _, err := s.UpsertRemoteTicket(ctx, RemoteTicket{BoardID: board.ID, ColumnID: view.Columns[0].ID, ExternalID: "github:1", DisplayID: "GH-1", DisplayNumber: 1, Title: "Remote", ExternalUpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	local, err := s.CreateTicket(ctx, view.Columns[0].ID, "Local", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if local.DisplayID != "T-002" {
		t.Fatalf("local display id = %s, want T-002", local.DisplayID)
	}
}

func TestUpdateTicketPersistsChanges(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
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
	view := defaultBoardView(t, ctx, s)
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
	view := defaultBoardView(t, ctx, s)
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
	view := defaultBoardView(t, ctx, s)
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
	view := defaultBoardView(t, ctx, s)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	err := s.MarkTicketRuntime(ctx, ticket.ID, "running", "system", "")
	if err == nil {
		t.Fatal("expected error marking runtime with no active session")
	}
}

func TestMultipleSessionsHistoryPreservedOnUpsert(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
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
	view := defaultBoardView(t, ctx, s)
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
	view := defaultBoardView(t, ctx, s)
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
	want := kanban.DefaultColumnNames()
	if len(view.Columns) != len(want) {
		t.Fatalf("columns = %d", len(view.Columns))
	}
	for i := range want {
		if view.Columns[i].Name != want[i] {
			t.Fatalf("column %d = %s", i, view.Columns[i].Name)
		}
	}
}

func TestCreateBoardUsesDefaultColumnTemplate(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoardWithWorkdir(ctx, "Client", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := kanban.DefaultColumns()
	if len(view.Columns) != len(want) {
		t.Fatalf("columns = %d, want %d", len(view.Columns), len(want))
	}
	for i, col := range want {
		if view.Columns[i].Name != col.Name || view.Columns[i].Position != col.Position {
			t.Fatalf("column %d = (%q,%d), want (%q,%d)", i, view.Columns[i].Name, view.Columns[i].Position, col.Name, col.Position)
		}
	}
}

func TestTicketDisplayIDsCreateListArchive(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
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
	view := defaultBoardView(t, ctx, s)
	a, _ := s.CreateTicket(ctx, view.Columns[0].ID, "A", "", "pi")
	b, _ := s.CreateTicket(ctx, view.Columns[0].ID, "B", "", "pi")
	c, _ := s.CreateTicket(ctx, view.Columns[1].ID, "C", "", "pi")
	d, _ := s.CreateTicket(ctx, view.Columns[1].ID, "D", "", "pi")
	if err := s.ReorderTicket(ctx, b.ID, -1); err != nil {
		t.Fatal(err)
	}
	tickets, _ := s.TicketsForColumn(ctx, view.Columns[0].ID)
	if tickets[0].DisplayID != b.DisplayID || tickets[1].DisplayID != a.DisplayID {
		t.Fatalf("reorder failed: %+v", tickets)
	}
	assertContiguousTicketPositions(t, tickets)
	if err := s.ReorderTicket(ctx, b.ID, -1); err != nil {
		t.Fatal(err)
	}
	tickets, _ = s.TicketsForColumn(ctx, view.Columns[0].ID)
	if tickets[0].DisplayID != b.DisplayID || tickets[1].DisplayID != a.DisplayID {
		t.Fatalf("reorder past first edge changed order: %+v", tickets)
	}
	if err := s.ReorderTicket(ctx, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	tickets, _ = s.TicketsForColumn(ctx, view.Columns[0].ID)
	if tickets[0].DisplayID != b.DisplayID || tickets[1].DisplayID != a.DisplayID {
		t.Fatalf("reorder past last edge changed order: %+v", tickets)
	}
	if err := s.MoveTicket(ctx, b.ID, view.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	open, _ := s.TicketsForColumn(ctx, view.Columns[0].ID)
	progress, _ := s.TicketsForColumn(ctx, view.Columns[1].ID)
	if len(open) != 1 || open[0].Position != 0 || len(progress) != 3 || progress[0].ColumnID != view.Columns[1].ID || progress[0].ID != b.ID || progress[1].ID != c.ID || progress[2].ID != d.ID {
		t.Fatalf("move/compact failed open=%+v progress=%+v", open, progress)
	}
	assertContiguousTicketPositions(t, open)
	assertContiguousTicketPositions(t, progress)
}

func TestArchiveThenMoveKeepsVisiblePositionsContiguous(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	archived, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Archived", "", "pi")
	moved, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Moved", "", "pi")
	stay, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Stay", "", "pi")
	dest, _ := s.CreateTicket(ctx, view.Columns[1].ID, "Dest", "", "pi")
	if err := s.ArchiveTicket(ctx, archived.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTicket(ctx, moved.ID, view.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	open, _ := s.TicketsForColumn(ctx, view.Columns[0].ID)
	progress, _ := s.TicketsForColumn(ctx, view.Columns[1].ID)
	if len(open) != 1 || open[0].ID != stay.ID || len(progress) != 2 || progress[0].ID != moved.ID || progress[1].ID != dest.ID {
		t.Fatalf("archive then move order open=%+v progress=%+v", open, progress)
	}
	assertContiguousTicketPositions(t, open)
	assertContiguousTicketPositions(t, progress)
	if err := s.MoveTicket(ctx, archived.ID, view.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	archivedMoved, err := s.TicketByID(ctx, archived.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archivedMoved.ColumnID != view.Columns[1].ID {
		t.Fatalf("archived ticket did not move columns: %+v", archivedMoved)
	}
	progress, _ = s.TicketsForColumn(ctx, view.Columns[1].ID)
	if len(progress) != 2 || progress[0].ID != moved.ID || progress[1].ID != dest.ID {
		t.Fatalf("moving archived ticket changed visible order: %+v", progress)
	}
	assertContiguousTicketPositions(t, progress)
}

func TestColumnEditOperations(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
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
	view = defaultBoardView(t, ctx, s)
	if view.Columns[3].Name != "Later" {
		t.Fatalf("reordered/renamed columns = %+v", view.Columns)
	}
	assertContiguousColumnPositions(t, view.Columns)
	firstBefore := view.Columns[0].ID
	if err := s.ReorderColumn(ctx, firstBefore, -1); err != nil {
		t.Fatal(err)
	}
	view = defaultBoardView(t, ctx, s)
	if view.Columns[0].ID != firstBefore {
		t.Fatalf("reorder past first column edge changed order: %+v", view.Columns)
	}
	lastBefore := view.Columns[len(view.Columns)-1].ID
	if err := s.ReorderColumn(ctx, lastBefore, 1); err != nil {
		t.Fatal(err)
	}
	view = defaultBoardView(t, ctx, s)
	if view.Columns[len(view.Columns)-1].ID != lastBefore {
		t.Fatalf("reorder past last column edge changed order: %+v", view.Columns)
	}
	assertContiguousColumnPositions(t, view.Columns)
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
	view = defaultBoardView(t, ctx, s)
	assertContiguousColumnPositions(t, view.Columns)
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
	view := defaultBoardView(t, ctx, s)
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

	// Verify old session is still in DB and is inactive.
	var oldStatus string
	var oldActive int
	if err := s.db.QueryRowContext(ctx, `select status, is_active from sessions where id=?`, firstID).Scan(&oldStatus, &oldActive); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "closed" || oldActive != 0 {
		t.Fatalf("old session should remain as closed/inactive: status=%s active=%d", oldStatus, oldActive)
	}
	var activeCount int
	if err := s.db.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=? and is_active=1`, ticket.ID).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("exactly one active session should remain after start fresh, got %d", activeCount)
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
	view := defaultBoardView(t, ctx, s)
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
	if listed.TmuxSessionName.String != "agent-kanban" || listed.WindowID.String != "@7" || listed.WindowName.String != "T-001-a" {
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

func TestTicketProjectionSharedByBoardMasterListAndLookup(t *testing.T) {
	s, ctx := newTestStore(t)
	defaultView := defaultBoardView(t, ctx, s)
	second, err := s.CreateBoardWithWorkdir(ctx, "Projection Client", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondView, _ := s.BoardViewByID(ctx, second.ID)
	defaultTicket, _ := s.CreateTicket(ctx, defaultView.Columns[0].ID, "Never started", "plain body", "pi")
	closedTicket, _ := s.CreateTicket(ctx, secondView.Columns[0].ID, "Closed latest", "body", "codex")
	activeID, err := s.UpsertActiveSession(ctx, closedTicket.ID, Session{
		Harness:         "codex",
		TmuxSessionName: "ak",
		TmuxWindowID:    sql.NullString{String: "@42", Valid: true},
		TmuxWindowName:  "b2-T-001-closed-latest",
		Status:          "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSessionClosed(ctx, activeID, "closed", "tmux", "done"); err != nil {
		t.Fatal(err)
	}

	board, err := s.BoardViewByID(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	projected := board.Columns[0].Tickets[0]
	if projected.Runtime != "closed" || projected.SessionActive {
		t.Fatalf("board projection should preserve inactive latest terminal state: %+v", projected)
	}
	if projected.BoardName != second.Name || projected.BoardWorkdir != second.Workdir || projected.SessionID.Int64 != activeID {
		t.Fatalf("board projection missing board/session fields: %+v", projected)
	}

	master, err := s.MasterBoardViewWithFilter(ctx, MasterFilter{Runtimes: []string{"closed"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := collectTicketTitles(master); len(got) != 1 || got[0] != closedTicket.Title {
		t.Fatalf("master closed filter got %v", got)
	}
	master, err = s.MasterBoardViewWithFilter(ctx, MasterFilter{Runtimes: []string{"not_started"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := collectTicketTitles(master); len(got) != 1 || got[0] != defaultTicket.Title {
		t.Fatalf("master not_started filter should not include inactive closed sessions, got %v", got)
	}

	listed, err := s.ListTickets(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var closedFromList Ticket
	for _, ticket := range listed {
		if ticket.ID == closedTicket.ID {
			closedFromList = ticket
		}
	}
	if closedFromList.Runtime != "closed" || closedFromList.SessionActive {
		t.Fatalf("list projection should match board/master path: %+v", closedFromList)
	}
	lookedUp, err := s.TicketByDisplayIDInBoard(ctx, closedTicket.DisplayID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lookedUp.Runtime != "closed" || lookedUp.SessionActive {
		t.Fatalf("lookup projection should match board/master path: %+v", lookedUp)
	}
}

func collectTicketTitles(view BoardView) []string {
	var titles []string
	for _, col := range view.Columns {
		for _, ticket := range col.Tickets {
			titles = append(titles, ticket.Title)
		}
	}
	return titles
}

func TestMultipleBoardsAndMasterView(t *testing.T) {
	s, ctx := newTestStore(t)
	defaultView := defaultBoardView(t, ctx, s)
	workdir := t.TempDir()
	second, err := s.CreateBoardWithWorkdir(ctx, "Client B", workdir)
	if err != nil {
		t.Fatal(err)
	}
	secondView := boardViewByID(t, ctx, s, second.ID)
	firstTicket := createTicket(t, ctx, s, defaultView.Columns[0].ID, "Default task", "", "pi")
	secondTicket := createTicket(t, ctx, s, secondView.Columns[0].ID, "Other task", "", "codex")
	if secondTicket.BoardWorkdir != workdir {
		t.Fatalf("ticket should project board workdir %q, got %q", workdir, secondTicket.BoardWorkdir)
	}
	if firstTicket.DisplayID != "T-001" || secondTicket.DisplayID != "T-001" {
		t.Fatalf("ticket numbering should be per-board: %s %s", firstTicket.DisplayID, secondTicket.DisplayID)
	}
	boards, err := s.ListBoards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) != 2 {
		t.Fatalf("boards len=%d, want 2", len(boards))
	}
	if boards[0].Name != "Client B" || boards[0].Workdir != workdir {
		t.Fatalf("board workdir not persisted: %+v", boards[0])
	}
	master, err := s.MasterBoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if master.Board.Name != "Master" {
		t.Fatalf("master name=%q", master.Board.Name)
	}
	if len(master.Columns) == 0 || len(master.Columns[0].Tickets) != 2 {
		t.Fatalf("master should aggregate open tickets: %+v", master.Columns)
	}
	if _, err := s.ColumnIDByBoardAndName(ctx, second.ID, "In Progress"); err != nil {
		t.Fatalf("resolve column by board/name: %v", err)
	}
}

func TestMigrateBackfillsBlankBoardWorkdir(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.db.ExecContext(ctx, `create table boards (
		id integer primary key autoincrement,
		name text not null,
		next_ticket_number integer not null default 1,
		created_at datetime not null,
		updated_at datetime not null
	)`)
	if err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	if _, err := s.db.ExecContext(ctx, `insert into boards(name,next_ticket_number,created_at,updated_at) values('Old',1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := s.BoardByName(ctx, "Old")
	if err != nil {
		t.Fatal(err)
	}
	if b.Workdir == "" {
		t.Fatal("migration should backfill existing board workdir")
	}
}

func assertContiguousTicketPositions(t *testing.T, tickets []Ticket) {
	t.Helper()
	for i, ticket := range tickets {
		if ticket.Position != i {
			t.Fatalf("ticket positions are not contiguous: %+v", tickets)
		}
	}
}

func assertContiguousColumnPositions(t *testing.T, columns []Column) {
	t.Helper()
	for i, column := range columns {
		if column.Position != i {
			t.Fatalf("column positions are not contiguous: %+v", columns)
		}
	}
}

func TestDeleteBoardRemovesBoardAndTickets(t *testing.T) {
	s, ctx := newTestStore(t)
	b, err := s.CreateBoard(ctx, "Delete Me")
	if err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardViewByID(ctx, b.ID)
	if _, err := s.CreateTicket(ctx, view.Columns[0].ID, "Gone", "", "pi"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBoard(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BoardByName(ctx, "Delete Me"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected board gone, got %v", err)
	}
}

func TestNoteCRUD(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	ticket := createTicket(t, ctx, s, view.Columns[0].ID, "Note Ticket", "", "pi")

	notes, err := s.ListNotes(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected 0 notes, got %d", len(notes))
	}

	n1, err := s.AddNote(ctx, ticket.ID, "First note")
	if err != nil {
		t.Fatal(err)
	}
	n2, err := s.AddNote(ctx, ticket.ID, "Second note")
	if err != nil {
		t.Fatal(err)
	}

	notes, err = s.ListNotes(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
	if notes[0].ID != n1.ID || notes[0].Body != "First note" {
		t.Fatalf("unexpected first note: %+v", notes[0])
	}
	if notes[1].ID != n2.ID || notes[1].Body != "Second note" {
		t.Fatalf("unexpected second note: %+v", notes[1])
	}

	if err := s.UpdateNote(ctx, n1.ID, "Updated first"); err != nil {
		t.Fatal(err)
	}
	notes, err = s.ListNotes(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if notes[0].Body != "Updated first" {
		t.Fatalf("updated body = %q, want 'Updated first'", notes[0].Body)
	}

	if err := s.DeleteNote(ctx, n2.ID); err != nil {
		t.Fatal(err)
	}
	notes, err = s.ListNotes(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].ID != n1.ID {
		t.Fatalf("after delete, notes = %+v", notes)
	}
}

func TestNotesCascadeDeleteWithTicket(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	ticket := createTicket(t, ctx, s, view.Columns[0].ID, "To Delete", "", "pi")
	if _, err := s.AddNote(ctx, ticket.ID, "Some note"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "delete from tickets where id=?", ticket.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "select count(*) from ticket_notes where ticket_id=?", ticket.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 notes after ticket deleted (cascade), got %d", count)
	}
}

func TestNoteCountInProjection(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	ticket := createTicket(t, ctx, s, view.Columns[0].ID, "Counted", "", "pi")

	projected, err := s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projected.NoteCount != 0 {
		t.Fatalf("initial NoteCount = %d, want 0", projected.NoteCount)
	}

	if _, err := s.AddNote(ctx, ticket.ID, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNote(ctx, ticket.ID, "two"); err != nil {
		t.Fatal(err)
	}

	projected, err = s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projected.NoteCount != 2 {
		t.Fatalf("NoteCount = %d, want 2", projected.NoteCount)
	}
}
