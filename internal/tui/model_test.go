package tui

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
)

func TestModelKeybindingsCreateMoveReorderArchive(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	model := New(ctx, store)
	model, _ = mustUpdate(t, model, "n")
	model, _ = mustUpdate(t, model, "esc") // dismiss auto-edit
	model, _ = mustUpdate(t, model, "n")
	model, _ = mustUpdate(t, model, "esc") // dismiss auto-edit
	if !strings.Contains(model.View(), "T-001") || !strings.Contains(model.View(), "T-002") {
		t.Fatalf("new tickets missing:\n%s", model.View())
	}
	model, _ = mustUpdate(t, model, "k") // move to T-001
	model, _ = mustUpdate(t, model, "J")
	view, _ := store.BoardView(ctx)
	if view.Columns[0].Tickets[0].DisplayID != "T-002" {
		t.Fatalf("reorder failed: %+v", view.Columns[0].Tickets)
	}
	model, _ = mustUpdate(t, model, "L")
	view, _ = store.BoardView(ctx)
	if len(view.Columns[1].Tickets) != 1 {
		t.Fatalf("move right failed: %+v", view.Columns)
	}
	model, _ = mustUpdate(t, model, "a")
	view, _ = store.BoardView(ctx)
	if len(view.Columns[1].Tickets) != 0 {
		t.Fatalf("archive failed: %+v", view.Columns[1].Tickets)
	}
}

func TestModelColumnEditingKeybindings(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	model := New(ctx, store)
	model, _ = mustUpdate(t, model, "c")
	model, _ = mustUpdate(t, model, "Blocked")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.View(), "Blocked") {
		t.Fatalf("added column missing:\n%s", model.View())
	}
	model, _ = mustUpdate(t, model, "l")
	model, _ = mustUpdate(t, model, "l")
	model, _ = mustUpdate(t, model, "l")
	model, _ = mustUpdate(t, model, "l")
	model, _ = mustUpdate(t, model, "r")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlU})
	model.columnName = ""
	model, _ = mustUpdate(t, model, "Later")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.View(), "Later") {
		t.Fatalf("renamed column missing:\n%s", model.View())
	}
	model, _ = mustUpdate(t, model, "D")
	if strings.Contains(firstLineContaining(model.View(), "Open"), "Later") {
		t.Fatalf("deleted empty column still visible:\n%s", model.View())
	}
}

func TestModelVimAndArrowNavigationMoveFocus(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "First", "", "pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Second", "", "pi"); err != nil {
		t.Fatal(err)
	}
	model := New(ctx, store)
	if model.col != 0 || model.card != 0 {
		t.Fatalf("initial focus col=%d card=%d", model.col, model.card)
	}

	model, _ = mustUpdate(t, model, "l")
	if model.col != 1 {
		t.Fatalf("l did not move right: col=%d", model.col)
	}
	model, _ = mustUpdate(t, model, "h")
	if model.col != 0 {
		t.Fatalf("h did not move left: col=%d", model.col)
	}
	model, _ = mustUpdate(t, model, "j")
	if model.card != 1 {
		t.Fatalf("j did not move down: card=%d", model.card)
	}
	model, _ = mustUpdate(t, model, "k")
	if model.card != 0 {
		t.Fatalf("k did not move up: card=%d", model.card)
	}

	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if model.col != 1 {
		t.Fatalf("right arrow did not move right: col=%d", model.col)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if model.col != 0 {
		t.Fatalf("left arrow did not move left: col=%d", model.col)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.card != 1 {
		t.Fatalf("down arrow did not move down: card=%d", model.card)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyUp})
	if model.card != 0 {
		t.Fatalf("up arrow did not move up: card=%d", model.card)
	}

	rendered := model.View()
	if !strings.Contains(rendered, "> Open") {
		t.Fatalf("focused column marker missing:\n%s", rendered)
	}
}

func TestModelSendPromptAndAttentionNavigation(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	first, _ := store.CreateTicket(ctx, view.Columns[0].ID, "First", "", "pi")
	second, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Second", "", "pi")
	sessionID, err := store.UpsertActiveSession(ctx, second.ID, storage.Session{
		Harness:         "pi",
		TmuxSessionName: "agent-kanban",
		TmuxWindowName:  "T-002-second",
		Status:          "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSessionRuntime(ctx, sessionID, "waiting_for_user", "manual", "waiting", "", false); err != nil {
		t.Fatal(err)
	}
	wrapped := &openingStore{Store: store}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "s")
	if len(wrapped.opened) != 1 || wrapped.opened[0] != first.DisplayID || !wrapped.sentPrompt[0] {
		t.Fatalf("send prompt dispatch opened=%v sent=%v", wrapped.opened, wrapped.sentPrompt)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	if model.card != 1 {
		t.Fatalf("tab did not jump to attention ticket: card=%d", model.card)
	}
	model, _ = mustUpdate(t, model, "m")
	model, _ = mustUpdate(t, model, "j")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	got, err := store.TicketByID(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "waiting_for_user" || got.LastDetectionSource.String != "manual" {
		t.Fatalf("manual state not persisted: %+v", got)
	}
}

func TestModelRendersHorizontalKanbanBoard(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Unified approach to QMD", "", "pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTicket(ctx, view.Columns[1].ID, "Wire Codex harness", "", "codex"); err != nil {
		t.Fatal(err)
	}

	model := New(ctx, store)
	rendered := model.View()
	headerLine := firstLineContaining(rendered, "Open")
	if !strings.Contains(headerLine, "In Progress") || !strings.Contains(headerLine, "Review") || !strings.Contains(headerLine, "Done") {
		t.Fatalf("columns are not rendered side-by-side:\n%s", rendered)
	}
	if !strings.Contains(rendered, "╭") || strings.Contains(rendered, "+---") {
		t.Fatalf("cards should use soft rounded borders instead of ASCII boxes:\n%s", rendered)
	}
	if !strings.Contains(rendered, "> T-001") {
		t.Fatalf("focused card marker missing from card box:\n%s", rendered)
	}
}

func TestModelOpenSelectedTicket(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Open me", "", "pi"); err != nil {
		t.Fatal(err)
	}
	wrapped := &openingStore{Store: store}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "o")
	if len(wrapped.opened) != 1 || wrapped.opened[0] != "T-001" {
		t.Fatalf("opened = %#v", wrapped.opened)
	}
	if !strings.Contains(model.View(), "opened T-001") {
		t.Fatalf("status missing:\n%s", model.View())
	}
}

func TestModelPromptFallbackCanPasteNow(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Send me", "", "pi"); err != nil {
		t.Fatal(err)
	}
	wrapped := &openingStore{
		Store: store,
		openErr: tmux.PromptReadyError{
			WindowName: "T-001-send-me",
			Prompt:     "# T-001: Send me\n\nSend me",
			Ready:      "READY",
			Err:        context.DeadlineExceeded,
		},
	}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "s")
	if !model.promptFallback {
		t.Fatalf("prompt fallback not shown: %s", model.View())
	}
	model, _ = mustUpdate(t, model, "p")
	if wrapped.pastedWindow != "T-001-send-me" || wrapped.pastedPrompt == "" {
		t.Fatalf("paste fallback not invoked: window=%q prompt=%q", wrapped.pastedWindow, wrapped.pastedPrompt)
	}
}

func TestModelRepairStartFresh(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Repair me", "", "pi")
	wrapped := &openingStore{
		Store: store,
		openErr: tmux.RepairNeededError{
			Ticket: ticket,
			Reason: "missing ref",
		},
	}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "o")
	if !model.repairing {
		t.Fatalf("repair view not shown: %s", model.View())
	}
	model, _ = mustUpdate(t, model, "f")
	if !wrapped.startedFresh {
		t.Fatalf("start fresh not invoked")
	}
}

type openingStore struct {
	*storage.Store
	opened       []string
	sentPrompt   []bool
	openErr      error
	openErrFn    func() error
	pastedWindow string
	pastedPrompt string
	startedFresh bool
}

func (s *openingStore) OpenTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	if s.openErrFn != nil {
		if err := s.openErrFn(); err != nil {
			return err
		}
	} else if s.openErr != nil {
		return s.openErr
	}
	s.opened = append(s.opened, ticket.DisplayID)
	s.sentPrompt = append(s.sentPrompt, sendPrompt)
	return nil
}

func (s *openingStore) MarkTicketState(ctx context.Context, ticketID int64, state string) error {
	return s.Store.MarkTicketRuntime(ctx, ticketID, state, "manual", "manual override")
}

func (s *openingStore) PastePromptNow(ctx context.Context, windowName, text string) error {
	s.pastedWindow = windowName
	s.pastedPrompt = text
	return nil
}

func (s *openingStore) StartFreshTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	s.startedFresh = true
	return nil
}

func (s *openingStore) UpdateSessionRef(ctx context.Context, ticket storage.Ticket, ref string) error {
	if ticket.SessionID.Valid {
		return s.Store.UpdateSessionRef(ctx, ticket.SessionID.Int64, ref)
	}
	return nil
}

func TestCardRuntimeLabelsAndWindowIndicators(t *testing.T) {
	cases := []struct {
		ticket   storage.Ticket
		wantMeta string
	}{
		{storage.Ticket{Harness: "pi", Runtime: "not_started"}, "new"},
		{storage.Ticket{Harness: "pi", Runtime: "running", SessionActive: true, WindowName: sqlNullStr("T-001-demo")}, "running"},
		{storage.Ticket{Harness: "pi", Runtime: "waiting_for_user"}, "waiting"},
		{storage.Ticket{Harness: "pi", Runtime: "needs_permission"}, "permission!"},
		{storage.Ticket{Harness: "pi", Runtime: "idle_unknown"}, "idle"},
		{storage.Ticket{Harness: "pi", Runtime: "closed", SessionRef: sqlNullStr("abc")}, "resumable"},
		{storage.Ticket{Harness: "pi", Runtime: "closed"}, "closed"},
		{storage.Ticket{Harness: "pi", Runtime: "error"}, "error"},
	}
	for _, tc := range cases {
		if got := runtimeLabel(tc.ticket); !strings.Contains(got, tc.wantMeta) {
			t.Errorf("runtimeLabel(%s runtime=%s ref=%v) = %q, want %q",
				tc.ticket.Harness, tc.ticket.Runtime, tc.ticket.SessionRef.Valid, got, tc.wantMeta)
		}
	}

	// windowIndicator: - for prior session without ref (cleanly closed, not repair-needed)
	closedNoRef := storage.Ticket{SessionID: sqlNullInt64(5), Runtime: "closed"}
	if ind := windowIndicator(closedNoRef); ind != "-" {
		t.Errorf("windowIndicator for closed-no-ref = %q, want -", ind)
	}

	// windowIndicator: ○ for resumable
	resumableTicket := storage.Ticket{SessionRef: sqlNullStr("abc"), Runtime: "closed"}
	if ind := windowIndicator(resumableTicket); ind != "○" {
		t.Errorf("windowIndicator for resumable = %q, want ○", ind)
	}
}

func TestRepairViewShowsReasonAndOptions(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Repair me", "", "pi")
	wrapped := &openingStore{
		Store: store,
		openErr: tmux.RepairNeededError{
			Ticket: ticket,
			Reason: "session started but no window or session ref is known",
		},
	}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "o")
	if !model.repairing {
		t.Fatalf("repair view not shown: %s", model.View())
	}
	v := model.View()
	if !strings.Contains(v, "session started but no window") {
		t.Fatalf("repair reason missing from repair view:\n%s", v)
	}
	if !strings.Contains(v, "retry") || !strings.Contains(v, "start fresh") {
		t.Fatalf("repair options missing from repair view:\n%s", v)
	}
}

func sqlNullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func sqlNullInt64(n int64) sql.NullInt64 {
	return sql.NullInt64{Int64: n, Valid: true}
}

func TestModelEditTicketUpdatesStore(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	_, _ = store.CreateTicket(ctx, view.Columns[0].ID, "Original", "body", "pi")

	model := New(ctx, store)
	// Enter edit mode
	model, _ = mustUpdate(t, model, "e")
	if !model.editing {
		t.Fatal("should be editing")
	}
	// Clear title and type new one
	for range "Original" {
		model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	model, _ = mustUpdate(t, model, "Fixed")
	// Tab to body field
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	// Tab to harness field
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	// Tab again to save
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})

	if model.editing {
		t.Fatal("should have left edit mode")
	}
	got, err := store.TicketByDisplayID(ctx, "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fixed" {
		t.Fatalf("title = %q, want Fixed", got.Title)
	}
}

func TestModelCloseSessionCallsCloser(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	_, _ = store.CreateTicket(ctx, view.Columns[0].ID, "Close Me", "", "pi")

	var closed bool
	wrapped := &closingStore{Store: store, onClose: func() { closed = true }}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "x")
	if !closed {
		t.Fatal("close was not called")
	}
}

func TestModelRepairEditRefThenOpen(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Repair", "", "pi")

	var repairCallCount int
	wrapped := &openingStore{
		Store: store,
		openErrFn: func() error {
			repairCallCount++
			if repairCallCount == 1 {
				return tmux.RepairNeededError{Ticket: ticket, Reason: "no ref"}
			}
			return nil // second call is the resume after ref entry
		},
	}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "o") // first open → repair
	if !model.repairing {
		t.Fatalf("should be in repair mode: %s", model.View())
	}
	model, _ = mustUpdate(t, model, "e") // enter ref edit
	if !model.repairEditingRef {
		t.Fatal("should be editing ref")
	}
	model, _ = mustUpdate(t, model, "019e-manual-ref")                 // type ref
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter}) // save

	if model.repairing {
		t.Fatal("should have left repair mode after successful ref entry")
	}
	// UpdateSessionRef must have been called, and OpenTicket with the ref
	if len(wrapped.opened) == 0 {
		t.Fatal("OpenTicket not called after ref edit")
	}
}

func TestModelRepairRetryCallsOpen(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Retry", "", "pi")

	callCount := 0
	wrapped := &openingStore{
		Store: store,
		openErrFn: func() error {
			callCount++
			if callCount == 1 {
				return tmux.RepairNeededError{Ticket: ticket, Reason: "no ref"}
			}
			return nil // second call (retry) succeeds
		},
	}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "o") // first open → repair
	if !model.repairing {
		t.Fatal("should be repairing")
	}
	model, _ = mustUpdate(t, model, "r") // retry
	if model.repairing {
		t.Fatal("should have left repair after successful retry")
	}
}

func TestModelEscCancelsRepair(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardView(ctx)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Cancel", "", "pi")

	wrapped := &openingStore{
		Store:   store,
		openErr: tmux.RepairNeededError{Ticket: ticket, Reason: "no ref"},
	}
	model := New(ctx, wrapped)
	model, _ = mustUpdate(t, model, "o")
	if !model.repairing {
		t.Fatal("should be repairing")
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.repairing {
		t.Fatal("esc should cancel repair")
	}
	if !strings.Contains(model.View(), "cancelled") {
		t.Fatalf("status should say cancelled:\n%s", model.View())
	}
}

func TestElapsedLabel(t *testing.T) {
	now := time.Now()
	cases := []struct {
		ticket storage.Ticket
		want   string
	}{
		{storage.Ticket{}, ""},
		{storage.Ticket{LastStateChangeAt: sqlNullTime(now.Add(-30 * time.Second))}, "now"},
		{storage.Ticket{LastStateChangeAt: sqlNullTime(now.Add(-5 * time.Minute))}, "5m"},
		{storage.Ticket{LastStateChangeAt: sqlNullTime(now.Add(-2 * time.Hour))}, "2h"},
		{storage.Ticket{LastStateChangeAt: sqlNullTime(now.Add(-48 * time.Hour))}, "2d"},
	}
	for _, tc := range cases {
		got := elapsedLabel(tc.ticket)
		if got != tc.want {
			t.Errorf("elapsedLabel = %q, want %q (ticket=%+v)", got, tc.want, tc.ticket)
		}
	}
}

func TestWrapText(t *testing.T) {
	// Short text fits on one line
	lines := wrapText("hello world", 20, 3)
	if len(lines) != 1 || lines[0] != "hello world" {
		t.Fatalf("lines = %v", lines)
	}
	// Long text wraps
	lines = wrapText("one two three four five", 10, 3)
	if len(lines) == 0 {
		t.Fatal("expected wrapped lines")
	}
	for _, l := range lines {
		if len([]rune(l)) > 10 {
			t.Errorf("line too long: %q", l)
		}
	}
	// Respects maxLines cap
	lines = wrapText("a b c d e f g h", 3, 2)
	if len(lines) > 2 {
		t.Fatalf("expected max 2 lines, got %d: %v", len(lines), lines)
	}
	// Empty string returns nil
	if wrapText("", 20, 3) != nil {
		t.Fatal("empty wrapText should return nil")
	}
}

// closingStore wraps storage.Store and records close calls.
type closingStore struct {
	*storage.Store
	onClose func()
}

func (s *closingStore) CloseTicketSession(ctx context.Context, ticket storage.Ticket) error {
	if s.onClose != nil {
		s.onClose()
	}
	return nil
}

func sqlNullTime(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: true}
}

func mustUpdate(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	return mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key), Alt: false})
}

func mustUpdateKey(t *testing.T, m Model, key tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(key)
	model, ok := next.(Model)
	if !ok {
		t.Fatalf("model type %T", next)
	}
	return model, cmd
}

func firstLineContaining(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}
