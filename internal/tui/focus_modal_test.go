package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/storage"
)

func focusTestModel(t *testing.T, model tea.Model) Model {
	t.Helper()
	switch value := model.(type) {
	case Model:
		return value
	case *Model:
		return *value
	default:
		t.Fatalf("unexpected model type %T", model)
		return Model{}
	}
}

func TestFocusHeaderPreservesOverflowWithLongBoardName(t *testing.T) {
	for _, boardName := range []string{
		"A very long provider delivery board name that consumes header room",
		"重要な配送ボード🚀重要な配送ボード🚀重要な配送ボード🚀",
	} {
		line := focusHeaderLine(" Kanbi · "+boardName, "FOCUS 4/3 · pause 1 to continue ", 80)
		if displayWidth(line) != 80 || !strings.Contains(line, "FOCUS 4/3 · pause 1 to continue") {
			t.Fatalf("focus status was dropped from constrained header %q: %q", boardName, line)
		}
	}
}

func TestArchivedTicketIsNotRenderedAsFocusCommitment(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if err := store.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "focus"); err != nil {
		t.Fatal(err)
	}
	store.SetFocusPolicy(storage.FocusPolicy{Enabled: true, Limit: 3, WorkflowKeys: []string{"focus"}})
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "active", "", "pi"); err != nil {
		t.Fatal(err)
	}
	archived, err := store.CreateTicket(ctx, view.Columns[0].ID, "archived", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveTicket(ctx, archived.ID); err != nil {
		t.Fatal(err)
	}
	model := New(ctx, NewService(store, nil))
	model.masterBoard = true
	model.masterFilter.IncludeArchived = true
	model.reload()
	var archivedProjection storage.Ticket
	for _, col := range model.view.Columns {
		for _, ticket := range col.Tickets {
			if ticket.ID == archived.ID {
				archivedProjection = ticket
			}
		}
	}
	if archivedProjection.ID == 0 || archivedProjection.FocusMember {
		t.Fatalf("archived ticket projected as focus member: %+v", archivedProjection)
	}
	model.width, model.height = 160, 45
	rendered := ansiStrip(model.View())
	if !strings.Contains(rendered, "ARCHIVED · 1") {
		t.Fatalf("archived focus-column ticket lacks separate section:\n%s", rendered)
	}
	if got := strings.Count(rendered, "PAUSED · 0"); got != 1 {
		t.Fatalf("archived-only focus column rendered PAUSED section %d times:\n%s", got, rendered)
	}
}

func TestMoveCapacityErrorReloadsOverflowBeforeChoosingReplacement(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if err := store.SetColumnWorkflowKey(ctx, view.Columns[1].ID, "focus"); err != nil {
		t.Fatal(err)
	}
	store.SetFocusPolicy(storage.FocusPolicy{Enabled: true, Limit: 3, WorkflowKeys: []string{"focus"}})
	target, err := store.CreateTicket(ctx, view.Columns[0].ID, "target", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := store.CreateTicket(ctx, view.Columns[1].ID, fmt.Sprintf("focused %d", i), "", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	store.SetFocusPolicy(storage.FocusPolicy{Enabled: true, Limit: 1, WorkflowKeys: []string{"focus"}})
	model := New(ctx, NewService(store, nil))
	model.col, model.card = 0, 0
	if selected, _ := model.selectedTicket(); selected.ID != target.ID {
		t.Fatalf("unexpected target selection: %+v", selected)
	}
	// Simulate a provider/process changing usage after the last projection.
	model.focus = storage.FocusStatus{Enabled: true, Limit: 1, Used: 1, WorkflowKeys: []string{"focus"}}
	model.moveTicketColumn(1)
	if !model.focusReplaceOpen || !model.focusReplaceResolveOnly || model.focus.Used != 2 {
		t.Fatalf("move used stale focus status: chooser=%v resolveOnly=%v focus=%+v", model.focusReplaceOpen, model.focusReplaceResolveOnly, model.focus)
	}
}

func TestProviderOverflowResolutionCommitsPausesUntilAdmissionFits(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if err := store.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "focus"); err != nil {
		t.Fatal(err)
	}
	store.SetFocusPolicy(storage.FocusPolicy{Enabled: true, Limit: 5, WorkflowKeys: []string{"focus"}})
	target, err := store.CreateTicket(ctx, view.Columns[0].ID, "target", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PauseTicket(ctx, target.ID, "why", "done", "next"); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		if _, err := store.CreateTicket(ctx, view.Columns[0].ID, fmt.Sprintf("focused %d", i), "", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	store.SetFocusPolicy(storage.FocusPolicy{Enabled: true, Limit: 3, WorkflowKeys: []string{"focus"}})
	model := New(ctx, NewService(store, &quitTestManager{}))
	model.startResume(target)
	next, cmd := model.updateResume(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	model = focusTestModel(t, next)
	model = focusTestModel(t, mustRunFocusCmd(t, model, cmd))
	if !model.focusReplaceOpen || !model.focusReplaceResolveOnly {
		t.Fatalf("overflow did not request a resolution pause: chooser=%v resolveOnly=%v", model.focusReplaceOpen, model.focusReplaceResolveOnly)
	}

	model = openAndFillReplacement(t, model)
	next, cmd = model.updatePause(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = focusTestModel(t, next)
	model = focusTestModel(t, mustRunFocusCmd(t, model, cmd))
	if !model.focusReplaceOpen || model.focusReplaceResolveOnly {
		t.Fatalf("first committed pause should leave exact-capacity replacement: chooser=%v resolveOnly=%v", model.focusReplaceOpen, model.focusReplaceResolveOnly)
	}

	model = openAndFillReplacement(t, model)
	next, cmd = model.updatePause(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = focusTestModel(t, next)
	model = focusTestModel(t, mustRunFocusCmd(t, model, cmd))
	resumed, err := store.TicketByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := store.FocusStatus(ctx)
	if resumed.FocusPaused || status.Used != 3 || model.focusReplaceOpen {
		t.Fatalf("overflow resolution did not finish admission: paused=%v status=%+v chooser=%v", resumed.FocusPaused, status, model.focusReplaceOpen)
	}
}

func mustRunFocusCmd(t *testing.T, model Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	next, _ := model.Update(cmd())
	return next
}

func openAndFillReplacement(t *testing.T, model Model) Model {
	t.Helper()
	next, _ := model.updateFocusReplace(tea.KeyMsg{Type: tea.KeyEnter})
	model = focusTestModel(t, next)
	for i, value := range []string{"why", "done", "next"} {
		model.pauseInputs[i].SetValue(value)
	}
	return model
}

func TestReplacementChooserReceivesInputBeforeHiddenResumeModal(t *testing.T) {
	m := Model{
		resumeOpen:          true,
		focusReplaceOpen:    true,
		focusReplaceTickets: []storage.Ticket{{ID: 1}, {ID: 2}},
		statusBarCancel:     func() {},
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := focusTestModel(t, next)
	if got.focusReplaceIndex != 1 {
		t.Fatalf("visible replacement chooser did not receive input: index=%d", got.focusReplaceIndex)
	}
}

func TestPauseModalStaysBoundedAndResizesInputs(t *testing.T) {
	m := Model{width: 160, height: 45}
	m.startPause(storage.Ticket{ID: 1, DisplayID: "GH-334"})
	for i, line := range strings.Split(m.pauseView(), "\n") {
		if displayWidth(line) > 76 {
			t.Fatalf("initial pause line %d width=%d", i, displayWidth(line))
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = focusTestModel(t, next)
	for i, line := range strings.Split(m.pauseView(), "\n") {
		if displayWidth(line) > 60 {
			t.Fatalf("resized pause line %d width=%d", i, displayWidth(line))
		}
	}
}

func TestPauseAndResumeSubmissionCommandsAreSingleFlight(t *testing.T) {
	store, ctx := newTestStore(t)
	model := New(ctx, NewService(store, nil))
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "focus", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	model.startPause(ticket)
	for i, value := range []string{"why", "done", "next"} {
		model.pauseInputs[i].SetValue(value)
	}
	first, cmd := model.updatePause(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = focusTestModel(t, first)
	if cmd == nil {
		t.Fatal("first pause submit did not create a command")
	}
	_, duplicate := model.updatePause(tea.KeyMsg{Type: tea.KeyCtrlS})
	if duplicate != nil {
		t.Fatal("duplicate pause submit created another command")
	}

	model.pauseOpen = false
	model.startResume(storage.Ticket{ID: ticket.ID, DisplayID: ticket.DisplayID})
	first, cmd = model.updateResume(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	model = focusTestModel(t, first)
	if cmd == nil {
		t.Fatal("first resume submit did not create a command")
	}
	_, duplicate = model.updateResume(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if duplicate != nil {
		t.Fatal("duplicate resume submit created another command")
	}
}

func TestReloadPreservesSelectedTicketAcrossFocusResort(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if err := store.SetColumnWorkflowKey(ctx, view.Columns[0].ID, "focus"); err != nil {
		t.Fatal(err)
	}
	store.SetFocusPolicy(storage.FocusPolicy{Enabled: true, Limit: 2, WorkflowKeys: []string{"focus"}})
	first, err := store.CreateTicket(ctx, view.Columns[0].ID, "first", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "second", "", "pi"); err != nil {
		t.Fatal(err)
	}
	if err := store.PauseTicket(ctx, first.ID, "why", "done", "next"); err != nil {
		t.Fatal(err)
	}
	model := New(ctx, NewService(store, nil))
	for i, ticket := range model.view.Columns[0].Tickets {
		if ticket.ID == first.ID {
			model.card = i
		}
	}
	if err := store.ResumeTicket(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	model.reload()
	selected, ok := model.selectedTicket()
	if !ok || selected.ID != first.ID {
		t.Fatalf("focus resort changed selection: selected=%+v", selected)
	}
}

func TestWideningBackfillsVerticalFocusViewport(t *testing.T) {
	tickets := make([]storage.Ticket, 5)
	for i := range tickets {
		tickets[i] = storage.Ticket{ID: int64(i + 1), DisplayID: fmt.Sprintf("T-%03d", i+1), Title: "short", FocusMember: true}
	}
	m := Model{
		width: 160, height: 45, col: 0, card: 4,
		focus:     storage.FocusStatus{Enabled: true, WorkflowKeys: []string{"focus"}},
		view:      storage.BoardView{Columns: []storage.Column{{WorkflowKey: "focus", Tickets: tickets}}},
		colScroll: []int{3},
	}
	m.vScrollFollow()
	if m.colScroll[0] != 0 {
		t.Fatalf("widened viewport retained stale hidden-above offset %d", m.colScroll[0])
	}
}

func TestPausedSectionHeadingRemainsVisibleWhenScrolledInsideSection(t *testing.T) {
	m := Model{
		width: 80, height: 24, col: 0, card: 2,
		focus:     storage.FocusStatus{Enabled: true, WorkflowKeys: []string{"focus"}},
		colScroll: []int{2},
	}
	col := storage.Column{WorkflowKey: "focus", Tickets: []storage.Ticket{
		{ID: 1, DisplayID: "T-001", Title: "focused", FocusMember: true},
		{ID: 2, DisplayID: "T-002", Title: "paused one", FocusMember: true, FocusPaused: true},
		{ID: 3, DisplayID: "T-003", Title: "paused two", FocusMember: true, FocusPaused: true},
	}}
	view := ansiStrip(m.columnView(0, col, 39))
	if !strings.Contains(view, "PAUSED · 2") {
		t.Fatalf("paused heading disappeared after scrolling into section:\n%s", view)
	}
}

func TestResumeViewWrapsLongContentAndKeepsControlsAt80x24(t *testing.T) {
	long := strings.Repeat("long checkpoint content ", 20)
	m := Model{
		width:  80,
		height: 24,
		resumeTicket: storage.Ticket{
			DisplayID: "GH-334",
			Title:     long,
			Body:      long,
			LatestCheckpoint: &storage.PauseCheckpoint{
				Why: long, Completed: long, NextAction: long, PausedAt: time.Now(),
			},
		},
	}
	for _, scroll := range []int{0, 12, 1000} {
		m.modalScroll = scroll
		view := m.resumeView()
		lines := strings.Split(view, "\n")
		if len(lines) > 24 {
			t.Fatalf("scroll=%d rendered %d rows", scroll, len(lines))
		}
		for i, line := range lines {
			if displayWidth(line) > 80 {
				t.Fatalf("scroll=%d line %d width=%d: %q", scroll, i, displayWidth(line), line)
			}
		}
		if !strings.Contains(view, "Resume GH-334") || !strings.Contains(view, "r resume   s resume + send handoff   Esc cancel") {
			t.Fatalf("scroll=%d hid fixed title/actions:\n%s", scroll, view)
		}
	}
}
