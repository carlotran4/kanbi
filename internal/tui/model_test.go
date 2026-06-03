package tui

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
)

func TestModelKeybindingsCreateMoveReorderArchive(t *testing.T) {
	store, ctx := newTestStore(t)
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "n")
	model, _ = mustUpdate(t, model, "esc") // dismiss auto-edit
	model, _ = mustUpdate(t, model, "n")
	model, _ = mustUpdate(t, model, "esc") // dismiss auto-edit
	if !strings.Contains(model.View(), "T-001") || !strings.Contains(model.View(), "T-002") {
		t.Fatalf("new tickets missing:\n%s", model.View())
	}
	model, _ = mustUpdate(t, model, "k") // move to T-001
	model, _ = mustUpdate(t, model, "J")
	view := defaultBoardView(t, ctx, store)
	if view.Columns[0].Tickets[0].DisplayID != "T-002" {
		t.Fatalf("reorder failed: %+v", view.Columns[0].Tickets)
	}
	model, _ = mustUpdate(t, model, "L")
	view = defaultBoardView(t, ctx, store)
	if len(view.Columns[1].Tickets) != 1 {
		t.Fatalf("move right failed: %+v", view.Columns)
	}
	model, _ = mustUpdate(t, model, "a")
	view = defaultBoardView(t, ctx, store)
	if len(view.Columns[1].Tickets) != 0 {
		t.Fatalf("archive failed: %+v", view.Columns[1].Tickets)
	}
}

func TestModelColumnEditingKeybindings(t *testing.T) {
	store, ctx := newTestStore(t)
	model := New(ctx, NewService(store, nil))
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
	model.columnInput = NewInputBuffer("")
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
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "First", "", "pi")
	createTicket(t, ctx, store, view.Columns[0].ID, "Second", "", "pi")
	model := New(ctx, NewService(store, nil))
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

func TestModelEnterSendPromptAndAttentionNavigation(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
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
	wrapped := &openingStore{Service: NewService(store, nil)}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = runCmd(t, model, cmd)
	if len(wrapped.opened) != 1 || wrapped.opened[0] != first.DisplayID || !wrapped.sentPrompt[0] {
		t.Fatalf("enter dispatch opened=%v sent=%v", wrapped.opened, wrapped.sentPrompt)
	}
	model, _ = mustUpdate(t, model, "!")
	if model.card != 1 {
		t.Fatalf("! did not jump to attention ticket: card=%d", model.card)
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
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Unified approach to QMD", "", "pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTicket(ctx, view.Columns[1].ID, "Wire Codex harness", "", "codex"); err != nil {
		t.Fatal(err)
	}

	model := New(ctx, NewService(store, nil))
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

func TestModelOHotkeyDoesNotOpenTicket(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Do not open", "", "pi"); err != nil {
		t.Fatal(err)
	}
	wrapped := &openingStore{Service: NewService(store, nil)}
	model := New(ctx, wrapped)
	model, cmd := mustUpdate(t, model, "o")
	model = runCmd(t, model, cmd)
	if len(wrapped.opened) != 0 {
		t.Fatalf("o hotkey should not open ticket, opened=%v", wrapped.opened)
	}
}

func TestModelEnterSendsUnstartedTicket(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Send me", "", "pi"); err != nil {
		t.Fatal(err)
	}
	wrapped := &openingStore{Service: NewService(store, nil)}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = runCmd(t, model, cmd)
	if len(wrapped.opened) != 1 || wrapped.opened[0] != "T-001" || !wrapped.sentPrompt[0] {
		t.Fatalf("enter dispatch opened=%v sent=%v", wrapped.opened, wrapped.sentPrompt)
	}
	if !strings.Contains(model.View(), "sent prompt T-001") {
		t.Fatalf("status missing:\n%s", model.View())
	}
}

func TestModelEnterOpensStartedTicket(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Open started", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "pi",
		TmuxSessionName: "agent-kanban",
		TmuxWindowName:  "T-001-open-started",
		Status:          "running",
	}); err != nil {
		t.Fatal(err)
	}
	wrapped := &openingStore{Service: NewService(store, nil)}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = runCmd(t, model, cmd)
	if len(wrapped.opened) != 1 || wrapped.opened[0] != "T-001" || wrapped.sentPrompt[0] {
		t.Fatalf("enter dispatch opened=%v sent=%v", wrapped.opened, wrapped.sentPrompt)
	}
	if !strings.Contains(model.View(), "opened T-001") {
		t.Fatalf("status missing:\n%s", model.View())
	}
}

func TestModelPromptFallbackCanPasteNow(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Send me", "", "pi"); err != nil {
		t.Fatal(err)
	}
	wrapped := &openingStore{
		Service: NewService(store, nil),
		openErr: tmux.PromptReadyError{
			WindowName: "T-001-send-me",
			Prompt:     "# T-001: Send me\n\nSend me",
			Ready:      "READY",
			Err:        context.DeadlineExceeded,
		},
	}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = runCmd(t, model, cmd)
	if !model.promptFallback {
		t.Fatalf("prompt fallback not shown: %s", model.View())
	}
	model, _ = mustUpdate(t, model, "p")
	if wrapped.pastedWindow != "T-001-send-me" || wrapped.pastedPrompt == "" {
		t.Fatalf("paste fallback not invoked: window=%q prompt=%q", wrapped.pastedWindow, wrapped.pastedPrompt)
	}
}

func TestModelRepairStartFresh(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Repair me", "", "pi")
	wrapped := &openingStore{
		Service: NewService(store, nil),
		openErr: tmux.RepairNeededError{
			Ticket: ticket,
			Reason: "missing ref",
		},
	}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = runCmd(t, model, cmd)
	if !model.repairing {
		t.Fatalf("repair view not shown: %s", model.View())
	}
	model, _ = mustUpdate(t, model, "f")
	if !wrapped.startedFresh {
		t.Fatalf("start fresh not invoked")
	}
	if !wrapped.startedFreshSendPrompt {
		t.Fatalf("start fresh should send prompt")
	}
}

type openingStore struct {
	*Service
	opened                 []string
	sentPrompt             []bool
	openErr                error
	openErrFn              func() error
	pastedWindow           string
	pastedPrompt           string
	startedFresh           bool
	startedFreshSendPrompt bool
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
	return s.Service.Store.MarkTicketRuntime(ctx, ticketID, state, "manual", "manual override")
}

func (s *openingStore) PastePromptNow(ctx context.Context, windowName, text string) error {
	s.pastedWindow = windowName
	s.pastedPrompt = text
	return nil
}

func (s *openingStore) StartFreshTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	s.startedFresh = true
	s.startedFreshSendPrompt = sendPrompt
	return nil
}

func (s *openingStore) UpdateSessionRef(ctx context.Context, ticket storage.Ticket, ref string) error {
	if ticket.SessionID.Valid {
		return s.Service.Store.UpdateSessionRef(ctx, ticket.SessionID.Int64, ref)
	}
	return nil
}

func TestCardRuntimeLabelsAndWindowIndicators(t *testing.T) {
	cases := []struct {
		ticket   storage.Ticket
		wantMeta string
	}{
		{storage.Ticket{Harness: "pi", Runtime: "not_started"}, ""},
		{storage.Ticket{Harness: "pi", Runtime: "running", SessionActive: true, WindowName: sqlNullStr("T-001-demo")}, ""},
		{storage.Ticket{Harness: "pi", Runtime: "waiting_for_user"}, ""},
		{storage.Ticket{Harness: "pi", Runtime: "needs_permission"}, "permission!"},
		{storage.Ticket{Harness: "pi", Runtime: "idle_unknown"}, ""},
		{storage.Ticket{Harness: "pi", Runtime: "closed", SessionRef: sqlNullStr("abc")}, ""},
		{storage.Ticket{Harness: "pi", Runtime: "closed"}, ""},
		{storage.Ticket{Harness: "pi", Runtime: "error", SessionRef: sqlNullStr("abc")}, ""},
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

	// windowIndicator: ○ for resumable, even when the previous tmux window went missing.
	resumableTicket := storage.Ticket{SessionRef: sqlNullStr("abc"), Runtime: "closed"}
	if ind := windowIndicator(resumableTicket); ind != "○" {
		t.Errorf("windowIndicator for resumable = %q, want ○", ind)
	}
	resumableAfterMissingWindow := storage.Ticket{SessionRef: sqlNullStr("abc"), Runtime: "error"}
	if ind := windowIndicator(resumableAfterMissingWindow); ind != "○" {
		t.Errorf("windowIndicator for error-with-ref = %q, want ○", ind)
	}
}

func TestRepairViewShowsReasonAndOptions(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Repair me", "", "pi")
	wrapped := &openingStore{
		Service: NewService(store, nil),
		openErr: tmux.RepairNeededError{
			Ticket: ticket,
			Reason: "session started but no window or session ref is known",
		},
	}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = runCmd(t, model, cmd)
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

func TestTicketInspectorDescriptionUsesReadableFormattedMarkdown(t *testing.T) {
	rendered := renderMarkdownForInspector("# Heading\n\nThe ticket body should be **readable**, not faint grey text.\n\n- one thing", 80, 8)
	plain := ansiStrip(rendered)
	for _, want := range []string{"Heading", "readable", "• one thing"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("description content missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "\x1b[2m") {
		t.Fatalf("description should not use faint styling: %q", rendered)
	}
	_, headingStyle := markdownLineStyle("# Heading")
	if !headingStyle.GetBold() {
		t.Fatal("heading markdown should use bold styling")
	}
}

func TestModelTicketInspectorShowsResumableStatus(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Resume me", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:           "pi",
		HarnessSessionRef: sqlNullStr("session-ref-123"),
		TmuxSessionName:   "agent-kanban-test",
		TmuxWindowName:    "b1-T-001-resume-me",
		Status:            "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSessionClosed(ctx, sessionID, "closed", "tmux", "done"); err != nil {
		t.Fatal(err)
	}

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	rendered := ansiStrip(model.View())
	if !strings.Contains(rendered, "resumable") {
		t.Fatalf("inspector should show resumable status for inactive ticket with session ref:\n%s", model.View())
	}
	if strings.Contains(rendered, "not started") {
		t.Fatalf("inspector should not show not started for resumable ticket:\n%s", model.View())
	}
}

func TestModelEditTicketUpdatesStore(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	_, _ = store.CreateTicket(ctx, view.Columns[0].ID, "Original", "body", "pi")

	model := New(ctx, NewService(store, nil))
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
	// Tab to notes field
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

func TestModelBodyPasteStoresImageAttachmentAndInsertsMarkdown(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	_, _ = store.CreateTicket(ctx, view.Columns[0].ID, "Image paste", "Existing body", "pi")

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(base64.StdEncoding.EncodeToString(png)), Paste: true})

	wantDir := filepath.Join(dataHome, "agent-kanban", "attachments", "1")
	entries, err := os.ReadDir(wantDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".png" {
		t.Fatalf("attachments = %+v", entries)
	}
	wantRef := "![](" + filepath.ToSlash(filepath.Join(wantDir, entries[0].Name())) + ")"
	if !strings.Contains(model.bodyTA.Value(), "Existing body\n"+wantRef+"\n") {
		t.Fatalf("body = %q, want inserted ref %q", model.bodyTA.Value(), wantRef)
	}
	if !strings.Contains(model.status, "attached ") {
		t.Fatalf("status = %q", model.status)
	}
}

func TestModelTicketInspectorRendersPolishedInlineEditor(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Pretty editor", "# Problem\n\n- ugly raw form", "pi")
	if _, err := store.AddNote(ctx, ticket.ID, "Needs polish"); err != nil {
		t.Fatal(err)
	}

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	rendered := model.View()
	for _, want := range []string{"T-001", "Pretty editor", "Open", "not started", "Notes", "1 note", "Ctrl+S save"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("ticket inspector missing %q:\n%s", want, rendered)
		}
	}
	plain := ansiStrip(rendered)
	if !strings.Contains(plain, "╭─ T-001 Pretty editor") {
		t.Fatalf("ticket inspector title should be embedded in modal outline:\n%s", rendered)
	}
	if strings.Contains(plain, "TITLE") || strings.Contains(plain, "DESCRIPTION") || strings.Contains(plain, "title:") {
		t.Fatalf("ticket inspector should not render raw form labels:\n%s", rendered)
	}

	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	rendered = model.View()
	if strings.Contains(ansiStrip(rendered), "DESCRIPTION") {
		t.Fatalf("description focus should remain label-free:\n%s", rendered)
	}
}

func TestModelEditTicketUsesCursorAwareBuffer(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	_, _ = store.CreateTicket(ctx, view.Columns[0].ID, "abcd", "body", "pi")

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	model, _ = mustUpdate(t, model, "X")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyDelete})
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnd})
	model, _ = mustUpdate(t, model, "Z")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})

	got, err := store.TicketByDisplayID(ctx, "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "abXdZ" {
		t.Fatalf("title = %q, want abXdZ", got.Title)
	}
}

func TestModelColumnEditUsesCursorAwareBuffer(t *testing.T) {
	store, ctx := newTestStore(t)

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "c")
	model, _ = mustUpdate(t, model, "Ac")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	model, _ = mustUpdate(t, model, "b")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnd})
	model, _ = mustUpdate(t, model, "d")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.View(), "Abcd") {
		t.Fatalf("added cursor-edited column missing:\n%s", model.View())
	}
}

func TestModelCloseSessionCallsCloser(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	_, _ = store.CreateTicket(ctx, view.Columns[0].ID, "Close Me", "", "pi")

	var closed bool
	wrapped := &closingStore{Service: NewService(store, nil), onClose: func() { closed = true }}
	model := New(ctx, wrapped)
	model, cmd := mustUpdate(t, model, "x")
	model = runCmd(t, model, cmd)
	if !closed {
		t.Fatal("close was not called")
	}
}

func TestModelRepairEditRefThenOpen(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Repair", "", "pi")

	var repairCallCount int
	wrapped := &openingStore{
		Service: NewService(store, nil),
		openErrFn: func() error {
			repairCallCount++
			if repairCallCount == 1 {
				return tmux.RepairNeededError{Ticket: ticket, Reason: "no ref"}
			}
			return nil // second call is the resume after ref entry
		},
	}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter}) // first default action → repair
	model = runCmd(t, model, cmd)
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
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Retry", "", "pi")

	callCount := 0
	wrapped := &openingStore{
		Service: NewService(store, nil),
		openErrFn: func() error {
			callCount++
			if callCount == 1 {
				return tmux.RepairNeededError{Ticket: ticket, Reason: "no ref"}
			}
			return nil // second call (retry) succeeds
		},
	}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter}) // first default action → repair
	model = runCmd(t, model, cmd)
	if !model.repairing {
		t.Fatal("should be repairing")
	}
	model, _ = mustUpdate(t, model, "r") // retry
	if model.repairing {
		t.Fatal("should have left repair after successful retry")
	}
}

func TestModelEscCancelsRepair(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Cancel", "", "pi")

	wrapped := &openingStore{
		Service: NewService(store, nil),
		openErr: tmux.RepairNeededError{Ticket: ticket, Reason: "no ref"},
	}
	model := New(ctx, wrapped)
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = runCmd(t, model, cmd)
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

// closingStore wraps Service and records close calls.
type closingStore struct {
	*Service
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

// --- Scroll tests ---

func TestVerticalScrollFollowsCursor(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	// Create enough tickets to overflow a small screen.
	for i := 0; i < 10; i++ {
		if _, err := store.CreateTicket(ctx, view.Columns[0].ID, fmt.Sprintf("Ticket %d", i), "", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	model := New(ctx, NewService(store, nil))
	// Force a small terminal so not all cards fit.
	model.width = 80
	model.height = 20
	model.syncScrollDimensions()

	// Initially scroll offset should be 0 and focused card is visible.
	if model.colScroll[0] != 0 {
		t.Fatalf("initial scroll should be 0, got %d", model.colScroll[0])
	}

	// Navigate down past the visible window; scroll offset must follow.
	for i := 0; i < 9; i++ {
		model, _ = mustUpdate(t, model, "j")
	}
	if model.card != 9 {
		t.Fatalf("expected card=9, got %d", model.card)
	}
	// scroll offset must have advanced so focused card is within viewport.
	if model.colScroll[0] == 0 {
		t.Fatal("scroll offset should have advanced when navigating past viewport")
	}
	if model.card < model.colScroll[0] {
		t.Fatalf("focused card %d is above scroll offset %d", model.card, model.colScroll[0])
	}

	// Navigate back to top; scroll offset must retreat.
	for i := 0; i < 9; i++ {
		model, _ = mustUpdate(t, model, "k")
	}
	if model.card != 0 {
		t.Fatalf("expected card=0, got %d", model.card)
	}
	if model.colScroll[0] != 0 {
		t.Fatalf("scroll offset should reset to 0, got %d", model.colScroll[0])
	}
}

// TestScrollFocusedCardAlwaysRendered verifies that after navigating down,
// the focused card actually appears in the rendered output (not just that
// m.card >= m.colScroll). The bug was that vScrollFollow did not account for
// the blank separator line between cards, so the scroll offset advanced one
// step too late — the focused card was logically "in window" but the render
// loop's tighter accounting meant it was actually cut off.
func TestScrollFocusedCardAlwaysRendered(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	for i := 0; i < 10; i++ {
		if _, err := store.CreateTicket(ctx, view.Columns[0].ID, fmt.Sprintf("Ticket %d", i), "", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	model := New(ctx, NewService(store, nil))
	model.width = 80
	model.height = 20
	model.syncScrollDimensions()

	// Navigate to each card one by one and verify it is present in the render.
	for step := 0; step < 9; step++ {
		model, _ = mustUpdate(t, model, "j")
		ticket := model.view.Columns[0].Tickets[model.card]
		rendered := model.View()
		if !strings.Contains(rendered, ticket.DisplayID) {
			t.Fatalf("step %d: focused card %d (%s) not found in rendered output (scroll=%d)\n%s",
				step, model.card, ticket.DisplayID, model.colScroll[0], rendered)
		}
	}
}

func TestScrollHintsAppearsWhenCardsHidden(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	for i := 0; i < 10; i++ {
		if _, err := store.CreateTicket(ctx, view.Columns[0].ID, fmt.Sprintf("Ticket %d", i), "", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	model := New(ctx, NewService(store, nil))
	model.width = 80
	model.height = 20
	model.syncScrollDimensions()

	// With a small height and 10 cards the "more ▼" hint should appear.
	rendered := model.View()
	if !strings.Contains(rendered, "more ▼") {
		t.Fatalf("expected '+N more ▼' hint, got:\n%s", rendered)
	}

	// Scroll down so cards are hidden above; the "more ▲" hint should appear.
	for i := 0; i < 9; i++ {
		model, _ = mustUpdate(t, model, "j")
	}
	rendered = model.View()
	if !strings.Contains(rendered, "more ▲") {
		t.Fatalf("expected '+N more ▲' hint after scrolling down, got:\n%s", rendered)
	}
}

func TestHorizontalScrollFollowsFocusedColumn(t *testing.T) {
	store, ctx := newTestStore(t)
	// Create extra columns beyond what fits on a narrow terminal.
	view := defaultBoardView(t, ctx, store)
	boardID := view.Columns[0].BoardID
	for i := 0; i < 5; i++ {
		if _, err := store.AddColumn(ctx, boardID, fmt.Sprintf("Extra%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	model := New(ctx, NewService(store, nil))
	// Narrow terminal: fits at most 2 columns (each 32 chars wide).
	model.width = 70
	model.height = 40
	model.syncScrollDimensions()

	if model.colOffset != 0 {
		t.Fatalf("initial colOffset should be 0, got %d", model.colOffset)
	}

	// Navigate right past the visible window.
	totalCols := len(model.view.Columns)
	for i := 0; i < totalCols-1; i++ {
		model, _ = mustUpdate(t, model, "l")
	}
	// colOffset must have advanced to keep focused column visible.
	if model.colOffset == 0 {
		t.Fatal("colOffset should have advanced when focus moved beyond visible columns")
	}
	if model.col < model.colOffset {
		t.Fatalf("focused col %d is left of colOffset %d", model.col, model.colOffset)
	}

	// Navigate back left; colOffset must retreat.
	for i := 0; i < totalCols-1; i++ {
		model, _ = mustUpdate(t, model, "h")
	}
	if model.col != 0 {
		t.Fatalf("expected col=0, got %d", model.col)
	}
	if model.colOffset != 0 {
		t.Fatalf("colOffset should reset to 0, got %d", model.colOffset)
	}
}

func TestHorizontalScrollHintAppearsAndUpdates(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	boardID := view.Columns[0].BoardID
	for i := 0; i < 5; i++ {
		if _, err := store.AddColumn(ctx, boardID, fmt.Sprintf("Extra%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	model := New(ctx, NewService(store, nil))
	// Narrow enough that not all columns fit.
	model.width = 70
	model.height = 40
	model.syncScrollDimensions()

	// At start: no columns hidden to the left, some hidden to the right.
	rendered := model.View()
	if !strings.Contains(rendered, "hidden ►") {
		t.Fatalf("expected 'hidden ►' hint on initial render, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "◄") {
		t.Fatalf("should not show left hint at colOffset=0, got:\n%s", rendered)
	}

	// Navigate right past the visible window.
	totalCols := len(model.view.Columns)
	for i := 0; i < totalCols-1; i++ {
		model, _ = mustUpdate(t, model, "l")
	}
	// Now there should be a left hint and no right hint.
	rendered = model.View()
	if !strings.Contains(rendered, "◄") {
		t.Fatalf("expected '◄' left hint after scrolling to last column, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "hidden ►") {
		t.Fatalf("should not show right hint at last column, got:\n%s", rendered)
	}
}

func TestWindowSizeMsgUpdatesTerminalDimensions(t *testing.T) {
	store, ctx := newTestStore(t)
	model := New(ctx, NewService(store, nil))
	next, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m := next.(Model)
	if m.width != 120 || m.height != 30 {
		t.Fatalf("expected 120x30, got %dx%d", m.width, m.height)
	}
}

// TestMoveTicketCursorFollowsTicket verifies that after moving a ticket to
// another column, the cursor remains focused on that same ticket in the
// destination column (T-016).
func TestMoveTicketCursorFollowsTicket(t *testing.T) {
	store, ctx := newTestStore(t)

	// Seed: 3 tickets in "In Progress" (col index 1) and 1 in "Review" (col
	// index 2) so the destination column already has a ticket before the move.
	view := defaultBoardView(t, ctx, store)
	inProgress := view.Columns[1].ID
	review := view.Columns[2].ID
	store.CreateTicket(ctx, inProgress, "Alpha", "", "")
	store.CreateTicket(ctx, inProgress, "Beta", "", "") // we will move this one
	store.CreateTicket(ctx, inProgress, "Gamma", "", "")
	store.CreateTicket(ctx, review, "Already Here", "", "")

	model := New(ctx, NewService(store, nil))

	// Navigate to "In Progress" (col 1)
	model, _ = mustUpdate(t, model, "l")
	if model.col != 1 {
		t.Fatalf("expected col 1, got %d", model.col)
	}

	// Navigate down to "Beta" (card index 1)
	model, _ = mustUpdate(t, model, "j")
	if model.card != 1 {
		t.Fatalf("expected card 1, got %d", model.card)
	}
	movedID := model.view.Columns[model.col].Tickets[model.card].DisplayID

	// Move "Beta" right into "Review" which already has "Already Here" at index 0.
	// Beta will be appended at index 1. Without the fix, cursor stays at card=1
	// which still points to index 1 — but that happens to be correct here.
	// The real failure is when Beta lands at the END (len=1 → index 1) but
	// clamp would have left card=1 too. Let's instead move from card=0 where
	// clamp leaves it at 0, pointing to the wrong ticket.
	model, _ = mustUpdate(t, model, "k")                                  // back to card=0, "Alpha"
	movedID = model.view.Columns[model.col].Tickets[model.card].DisplayID // "Alpha"

	// Dest col "Review" has [Already Here]. Moving Alpha there appends it at
	// index 1. m.card=0 after clamp → focuses "Already Here", not Alpha.
	model, _ = mustUpdate(t, model, "L") // move Alpha to Review

	// After the move, the cursor must still point to the moved ticket
	focusedTicket, ok := model.selectedTicket()
	if !ok {
		t.Fatalf("no ticket selected after move")
	}
	if focusedTicket.DisplayID != movedID {
		t.Fatalf("cursor lost: expected focus on %s (%s), got %s (%s)",
			movedID, "Alpha", focusedTicket.DisplayID, focusedTicket.Title)
	}
}

// TestMoveTicketScrollFollowsTicket verifies that when a ticket is moved into
// a destination column that requires scrolling to see it, the scroll offset is
// adjusted so the ticket is visible (T-016 follow-up).
func TestMoveTicketScrollFollowsTicket(t *testing.T) {
	store, ctx := newTestStore(t)

	view := defaultBoardView(t, ctx, store)
	srcCol := view.Columns[0].ID
	dstCol := view.Columns[1].ID

	// Fill the destination column with enough tickets to overflow a small screen.
	for i := 0; i < 10; i++ {
		if _, err := store.CreateTicket(ctx, dstCol, fmt.Sprintf("Dest %d", i), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	// One ticket in the source column — the one we will move.
	movedTicket, err := store.CreateTicket(ctx, srcCol, "Traveller", "", "")
	if err != nil {
		t.Fatal(err)
	}

	model := New(ctx, NewService(store, nil))
	model.width = 80
	model.height = 20
	model.syncScrollDimensions()

	// Move the source ticket right into the destination column.
	// It will be appended at the end (index 10), well past the visible window.
	model, _ = mustUpdate(t, model, "L")

	// The focused ticket must be "Traveller".
	focused, ok := model.selectedTicket()
	if !ok {
		t.Fatal("no ticket selected after move")
	}
	if focused.ID != movedTicket.ID {
		t.Fatalf("cursor lost: expected %s (Traveller), got %s (%s)", movedTicket.DisplayID, focused.DisplayID, focused.Title)
	}

	// The scroll offset of the destination column must have advanced so the
	// focused card is within the visible window.
	scroll := model.colScroll[model.col]
	if model.card < scroll {
		t.Fatalf("focused card %d is above scroll offset %d — ticket not visible", model.card, scroll)
	}

	// Verify the focused card is actually within the rendered viewport.
	inner := boardColumnWidth - 2
	avail := model.boardContentHeight()
	usedLines := 0
	visible := false
	for ti := scroll; ti < len(model.view.Columns[model.col].Tickets); ti++ {
		h := cardHeight(model.view.Columns[model.col].Tickets[ti], inner)
		if ti > scroll {
			h++
		}
		if usedLines+h > avail {
			break
		}
		usedLines += h
		if ti == model.card {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("focused card %d is not within viewport (scroll=%d)", model.card, scroll)
	}
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

// runCmd executes a tea.Cmd synchronously and feeds the resulting message back
// through Update. Use this after async dispatching actions (open, close, etc.).
func runCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	next, _ := m.Update(msg)
	model, ok := next.(Model)
	if !ok {
		t.Fatalf("model type %T", next)
	}
	return model
}

func firstLineContaining(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func TestBoardPickerSwitchesToMasterAndNamedBoard(t *testing.T) {
	store, ctx := newTestStore(t)
	second, err := store.CreateBoard(ctx, "Client B")
	if err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardViewByID(ctx, second.ID)
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "Other task", "", "pi"); err != nil {
		t.Fatal(err)
	}
	model := NewWithPicker(ctx, NewService(store, nil))
	if !model.boardPicker || !strings.Contains(model.View(), "Master") || !strings.Contains(model.View(), "Client B") {
		t.Fatalf("picker not shown:\n%s", model.View())
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.masterBoard || model.view.Board.Name != "Master" || !strings.Contains(model.View(), "Client B") {
		t.Fatalf("did not switch to master:\n%s", model.View())
	}
	model, _ = mustUpdate(t, model, "b")
	model, _ = mustUpdate(t, model, "j")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.masterBoard || model.view.Board.Name != "Client B" {
		t.Fatalf("did not switch to Client B: master=%v board=%s", model.masterBoard, model.view.Board.Name)
	}
}

func TestCreateTicketFromMasterPromptsForTargetBoard(t *testing.T) {
	store, ctx := newTestStore(t)
	client, err := store.CreateBoard(ctx, "Client B")
	if err != nil {
		t.Fatal(err)
	}
	model := New(ctx, NewService(store, nil))
	model.masterBoard = true
	model.reloadBoards()
	model.reload()
	model, _ = mustUpdate(t, model, "n")
	if !model.boardPicker || model.boardPickerMode != "create" || !strings.Contains(model.View(), "Create ticket in which board?") {
		t.Fatalf("create board picker not shown:\n%s", model.View())
	}
	// board list is sorted alphabetically: Client B is the first real board.
	model, _ = mustUpdate(t, model, "j")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.editing {
		t.Fatalf("new ticket should open edit form:\n%s", model.View())
	}
	clientView, err := store.BoardViewByID(ctx, client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clientView.Columns[0].Tickets) != 1 || clientView.Columns[0].Tickets[0].Title != "New ticket" {
		t.Fatalf("ticket not created on selected board: %+v", clientView.Columns[0].Tickets)
	}
}

func TestRenameBoardFromTUIBoardPicker(t *testing.T) {
	store, ctx := newTestStore(t)
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "b")
	model, _ = mustUpdate(t, model, "j")
	model, _ = mustUpdate(t, model, "r")
	if !model.boardRenaming || !model.boardRenameReturnPicker {
		t.Fatalf("picker rename modal not shown")
	}
	model.boardRenameName = "Again"
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.boardPicker || !strings.Contains(model.View(), "Again") {
		t.Fatalf("should return to picker with renamed board:\n%s", model.View())
	}
}

func TestBoardCreateShowsWorkdirErrorInModal(t *testing.T) {
	store, ctx := newTestStore(t)
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "b")
	model, _ = mustUpdate(t, model, "c")
	model.boardEditName = "Client"
	model.boardEditCWD = t.TempDir() + "/missing"
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.boardEditing {
		t.Fatalf("modal should stay open after invalid cwd")
	}
	if !strings.Contains(model.View(), "no such file or directory") {
		t.Fatalf("error not visible in board create modal:\n%s", model.View())
	}
}

func TestBoardPickerCreateSetCWDAndDeleteBoard(t *testing.T) {
	store, ctx := newTestStore(t)
	cwd := t.TempDir()
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "b")
	model, _ = mustUpdate(t, model, "c")
	if !model.boardEditing || model.boardEditAction != "create" {
		t.Fatalf("create modal not shown")
	}
	model.boardEditName = "Client"
	model.boardEditCWD = cwd
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.boardPicker || !strings.Contains(model.View(), "Client") {
		t.Fatalf("created board missing from picker:\n%s", model.View())
	}
	b, err := store.BoardByName(ctx, "Client")
	if err != nil || b.Workdir != cwd {
		t.Fatalf("created board = %+v err=%v", b, err)
	}
	newCWD := t.TempDir()
	model, _ = mustUpdate(t, model, "j")
	model, _ = mustUpdate(t, model, "w")
	if !model.boardEditing || model.boardEditAction != "cwd" {
		t.Fatalf("cwd modal not shown")
	}
	model.boardEditCWD = newCWD
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	b, _ = store.BoardByName(ctx, "Client")
	if b.Workdir != newCWD {
		t.Fatalf("cwd=%q want %q", b.Workdir, newCWD)
	}
	model, _ = mustUpdate(t, model, "d")
	if !model.boardDeleting {
		t.Fatalf("delete confirmation not shown")
	}
	model, _ = mustUpdate(t, model, "y")
	if _, err := store.BoardByName(ctx, "Client"); err == nil {
		t.Fatal("board should be deleted")
	}
}
