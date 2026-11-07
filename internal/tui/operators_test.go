package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/runtime"
	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
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
