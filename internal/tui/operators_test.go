package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/runtime"
	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestOperatorPanelConfigurationAndBoardNavigation(t *testing.T) {
	s, ctx := newTestStore(t)
	cfg := config.Defaults(config.Paths{ConfigFile: filepath.Join(t.TempDir(), "config.yaml")})
	manager := runtime.NewManager(cfg, s)
	defer manager.Close()
	op, e := manager.SaveOperator(ctx, storage.Operator{Name: "Intake", Harness: "pi", CWD: t.TempDir(), Timezone: "UTC"}, "standing instructions")
	if e != nil {
		t.Fatal(e)
	}
	view := defaultBoardView(t, ctx, s)
	createTicket(t, ctx, s, view.Columns[0].ID, "Board remains visible", "", "pi")
	m := New(ctx, app.NewService(s, manager))
	m.firstRun = false
	m.width = 160
	m.height = 45
	m, _ = mustUpdate(t, m, "O")
	if !m.operators.open || len(m.operators.items) != 1 {
		t.Fatal(m.operators)
	}
	if v := m.View(); !strings.Contains(v, "Intake") || !strings.Contains(v, "Board remains visible") {
		t.Fatal(v)
	}
	m, _ = mustUpdate(t, m, "?")
	if !m.operators.menu || !strings.Contains(m.View(), "Operator actions") {
		t.Fatal("missing actions menu")
	}
	m, _ = mustUpdate(t, m, "esc")
	if m.operators.menu || !m.operators.focused {
		t.Fatal("actions back should keep the sidebar focused")
	}
	m, _ = mustUpdate(t, m, "e")
	if !m.operators.editing || m.operators.instructions.Value() != "standing instructions" {
		t.Fatal(m.operators)
	}
	m, _ = mustUpdate(t, m, "tab")
	if m.operators.field != 2 {
		t.Fatal("immutable harness field focused")
	}
	m, _ = mustUpdate(t, m, "esc")
	m, _ = mustUpdate(t, m, "p")
	if !m.operators.busy {
		t.Fatal("no pause action")
	}
	// Use the same application action the returned command invokes, then apply its result.
	a := m.actions.(operatorActions)
	if e = a.OperatorAction(ctx, op.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	next, _ := m.Update(operatorResult{})
	m = next.(Model)
	if !m.operators.items[0].SchedulePaused {
		t.Fatal(m.operators.items)
	}
	m, _ = mustUpdate(t, m, "esc")
	m, _ = mustUpdate(t, m, "b")
	if !m.boardPicker || !m.operators.open {
		t.Fatal("Operators disappeared on board navigation")
	}
	m, _ = mustUpdate(t, m, "esc")
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	if !strings.Contains(m.View(), "Operators [O]") {
		t.Fatal(m.View())
	}
	m, _ = mustUpdate(t, m, "O")
	if !strings.Contains(m.View(), "Intake") {
		t.Fatal(m.View())
	}
	m, _ = mustUpdate(t, m, "d")
	if m.operators.confirm != "delete" {
		t.Fatal("no deletion confirmation")
	}
	m, _ = mustUpdate(t, m, "n")
	m, _ = mustUpdate(t, m, "esc")
	m, _ = mustUpdate(t, m, "i")
	if m.operators.open || !m.inspector.open {
		t.Fatal("side panels overlap")
	}
	ops, _ := s.Operators(ctx)
	if len(ops) != 1 {
		t.Fatal("cancel deleted operator")
	}
}

func TestOperatorEditorKeepsScheduleInDraftUntilMainSave(t *testing.T) {
	s, ctx := newTestStore(t)
	cfg := config.Defaults(config.Paths{ConfigFile: filepath.Join(t.TempDir(), "config.yaml")})
	manager := runtime.NewManager(cfg, s)
	defer manager.Close()
	m := New(ctx, app.NewService(s, manager))
	m.firstRun, m.width, m.height = false, 160, 45
	m, _ = mustUpdate(t, m, "O")
	m, _ = mustUpdate(t, m, "n")
	m.operators.inputs[0] = NewInputBuffer("Intake")
	m.operators.inputs[2] = NewInputBuffer(t.TempDir())
	for _, field := range []int{1, 2, 6, 7} {
		m, _ = mustUpdate(t, m, "tab")
		if m.operators.field != field {
			t.Fatalf("focus %d, want %d", m.operators.field, field)
		}
	}
	m, _ = mustUpdate(t, m, "enter")
	if !m.operators.scheduleEditing {
		t.Fatal("schedule editor not opened")
	}
	m.operators.inputs[3] = NewInputBuffer("15m")
	m.operators.inputs[4] = NewInputBuffer("America/Chicago")
	m, _ = mustUpdate(t, m, "tab")
	m, _ = mustUpdate(t, m, "tab")
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("First line")})
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Second line")})
	m, _ = mustUpdate(t, m, "ctrl+s")
	if m.operators.scheduleEditing || !m.operators.editing {
		t.Fatal("done should return to the main draft")
	}
	ops, _ := s.Operators(ctx)
	if len(ops) != 0 {
		t.Fatal("schedule editor saved before the main save")
	}
	m, _ = mustUpdate(t, m, "shift+tab")
	if m.operators.field != 6 {
		t.Fatal("instructions not focused")
	}
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello world")})
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlH})
	if m.operators.instructions.Value() != "hello " {
		t.Fatal("shared word deletion missing")
	}
	m, _ = mustUpdate(t, m, "ctrl+s")
	if m.operators.editing || m.operators.error != "" {
		t.Fatal(m.operators.error)
	}
	ops, _ = s.Operators(ctx)
	if len(ops) != 1 || ops[0].Cron != "15m" || ops[0].ScheduledPrompt != "First line\nSecond line" {
		t.Fatalf("saved operators: %+v", ops)
	}
	m, _ = mustUpdate(t, m, "e")
	m, _ = mustUpdate(t, m, "tab")
	if m.operators.field != 2 {
		t.Fatal("immutable harness focused")
	}
	for _, key := range []string{"tab", "tab", "enter"} {
		m, _ = mustUpdate(t, m, key)
	}
	m.operators.inputs[3] = NewInputBuffer("daily")
	m, _ = mustUpdate(t, m, "esc")
	m, _ = mustUpdate(t, m, "esc")
	ops, _ = s.Operators(ctx)
	if ops[0].Cron != "15m" {
		t.Fatal("cancelled changes were saved")
	}
}

func TestOperatorEditorResizeAndExternalResultIsolation(t *testing.T) {
	s, ctx := newTestStore(t)
	cfg := config.Defaults(config.Paths{ConfigFile: filepath.Join(t.TempDir(), "config.yaml")})
	manager := runtime.NewManager(cfg, s)
	defer manager.Close()
	m := New(ctx, app.NewService(s, manager))
	m.firstRun = false
	m.editOperator(storage.Operator{})
	oldGeneration := m.operators.editorGeneration
	m.editOperator(storage.Operator{})
	m.applyOperatorEditorResult(operatorEditorFinished{generation: oldGeneration, field: 6, text: "stale"})
	if m.operators.instructions.Value() != "" {
		t.Fatal("stale editor replaced a newer draft")
	}
	m.applyOperatorEditorResult(operatorEditorFinished{generation: m.operators.editorGeneration, field: 6, text: "current"})
	if m.operators.instructions.Value() != "current" {
		t.Fatal("current editor result lost")
	}
	for _, size := range []struct{ w, h int }{{160, 45}, {80, 24}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = next.(Model)
		for _, schedule := range []bool{false, true} {
			m.operators.scheduleEditing = schedule
			view := m.operatorEditorView()
			if lipgloss.Width(view) > size.w || lipgloss.Height(view) > size.h-2 {
				t.Fatalf("%dx%d schedule %t: rendered %dx%d", size.w, size.h, schedule, lipgloss.Width(view), lipgloss.Height(view))
			}
			if !strings.Contains(view, "Ctrl+S") || !strings.Contains(view, "Esc") {
				t.Fatal("footer missing")
			}
		}
	}
}

func TestOperatorCardsKeepSelectionAndFooterVisible(t *testing.T) {
	m := Model{}
	m.operators.focused = true
	for i := 0; i < 20; i++ {
		m.operators.items = append(m.operators.items, storage.Operator{Name: fmt.Sprintf("Operator %02d", i), Harness: "copilot", State: "not_started"})
	}
	m.operators.index = 19
	for _, size := range []struct{ w, h int }{{34, 39}, {38, 20}} {
		for _, message := range []string{"", "An action failed with a long message that wraps across multiple lines"} {
			m.operators.error = message
			view := m.operatorsView(size.w, size.h)
			if lipgloss.Width(view) != size.w || lipgloss.Height(view) != size.h {
				t.Fatalf("panel %dx%d rendered %dx%d", size.w, size.h, lipgloss.Width(view), lipgloss.Height(view))
			}
			lines := strings.Split(ansiStrip(view), "\n")
			if strings.Contains(strings.Join(lines, "\n"), "Enter open") || !strings.Contains(view, "Operator 19") || !strings.Contains(view, "more ↑") || !strings.Contains(view, "copilot") {
				t.Fatal(view)
			}
		}
	}
	m.operators.menu = true
	if view := m.operatorsView(38, 20); lipgloss.Height(view) != 20 || !strings.Contains(view, "Delete Operator") {
		t.Fatal(view)
	}
}

func TestOperatorNarrowOverlayIsRightAligned(t *testing.T) {
	m := Model{width: 80, height: 24}
	m.operators.focused = true
	popup := m.operatorsOverlay()
	if lipgloss.Width(popup) != 40 || lipgloss.Height(popup) != 22 {
		t.Fatalf("overlay %dx%d", lipgloss.Width(popup), lipgloss.Height(popup))
	}
	board := strings.Repeat(strings.Repeat("B", 80)+"\n", 23) + strings.Repeat("B", 80)
	view := overlayModalPosition(board, popup, 80, 24, true)
	lines := strings.Split(ansiStrip(view), "\n")
	if len(lines) != 24 || strings.Index(lines[1], "╭") != 40 || !strings.HasPrefix(lines[1], strings.Repeat("B", 40)) {
		t.Fatal(view)
	}
	if lines[0] != strings.Repeat("B", 80) || lines[23] != strings.Repeat("B", 80) {
		t.Fatal("header/footer were covered")
	}
}

func TestOperatorShortcutsUseTheSingleContextBar(t *testing.T) {
	for _, width := range []int{80, 160} {
		m := Model{width: width, height: 24}
		m.operators.open, m.operators.focused = true, true
		m.operators.items = []storage.Operator{{Name: "Intake", State: "failed"}}
		if bar := m.contextBar(); !strings.Contains(bar, "?:actions") || !strings.Contains(bar, "r:retry") || !strings.Contains(bar, "Esc:board") || strings.Contains(bar, "/:search") {
			t.Fatal(bar)
		}
		if panel := m.operatorsView(34, 20); strings.Contains(panel, "Enter open") || strings.Contains(panel, "? actions") {
			t.Fatal(panel)
		}
		m.operators.menu = true
		if bar := m.contextBar(); bar != "Operator actions  Esc/?:back" {
			t.Fatal(bar)
		}
		m.operators.menu = false
		m.operators.confirm = "delete"
		if bar := m.contextBar(); bar != "y:confirm  n/Esc:cancel" {
			t.Fatal(bar)
		}
		m.operators.confirm = ""
		m.operators.focused = false
		if bar := m.contextBar(); !strings.Contains(bar, "/:search") || strings.Contains(bar, "?:actions") {
			t.Fatal(bar)
		}
	}
}
