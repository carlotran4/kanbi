package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/multiplexer"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type fleetTestActions struct {
	Actions
	enters, returns int
	returnErr       error
	scope           int64
}

func (a *fleetTestActions) PreviewFleet(_ context.Context, b int64, _, _ int) (multiplexer.FleetSummary, error) {
	a.scope = b
	return multiplexer.FleetSummary{Scope: "all boards", Count: 4, Arrangement: "2 + 2", FocusHint: "ctrl+b then tab", ZoomHint: "ctrl+b then z"}, nil
}
func (a *fleetTestActions) EnterFleet(_ context.Context, _ int64, _, _ int, _ string) error {
	a.enters++
	return nil
}
func (a *fleetTestActions) RestoreFleet(context.Context) error { a.returns++; return a.returnErr }
func (a *fleetTestActions) ResizeFleet(context.Context) error  { return nil }
func (a *fleetTestActions) FleetStatus(context.Context) (multiplexer.FleetStatus, error) {
	return multiplexer.FleetStatus{Present: true, Count: 4, Live: 4}, nil
}
func fleetKey(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}
func fleetApply(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}
func TestFleetConfirmGatherReturnRestoresSelectionAndInspector(t *testing.T) {
	m, s, ctx := newTestModel(t)
	v := defaultBoardView(t, ctx, s)
	for i := 0; i < 8; i++ {
		createTicket(t, ctx, s, v.Columns[0].ID, "agent", "body", "pi")
	}
	m.reload()
	m.width, m.height = 160, 45
	m.card = 4
	m.colScroll[0] = 3
	m.inspector.open = true
	m.inspector.focused = true
	m.inspector.section = 2
	m.inspector.scroll = 3
	selected, _ := m.selectedTicket()
	actions := &fleetTestActions{Actions: m.actions}
	m.actions = actions
	m, cmd := fleetApply(t, m, fleetKey("v"))
	if cmd == nil || !m.fleet.busy {
		t.Fatal("no asynchronous preview")
	}
	m, _ = fleetApply(t, m, cmd())
	if !m.fleet.confirm || actions.enters != 0 {
		t.Fatal("entry moved without confirmation")
	}
	m, cmd = fleetApply(t, m, fleetKey("enter"))
	m, _ = fleetApply(t, m, cmd())
	if !m.fleet.active || actions.enters != 1 || strings.Contains(m.View(), "OPEN") {
		t.Fatal("header not active")
	}
	m, _ = fleetApply(t, m, tea.WindowSizeMsg{Width: 160, Height: 4})
	m, _ = fleetApply(t, m, fleetKey("j"))
	if m.card != 4 {
		t.Fatal("header key mutated hidden selection")
	}
	m, cmd = fleetApply(t, m, fleetKey("v"))
	m, _ = fleetApply(t, m, cmd())
	got, _ := m.selectedTicket()
	if m.fleet.active || got.ID != selected.ID || !m.inspector.open || !m.inspector.focused || m.inspector.section != 2 || m.inspector.scroll != 3 || m.height != 45 {
		t.Fatal("view not restored")
	}
}
func TestFleetHeaderReturnMouseAndFailureRetry(t *testing.T) {
	m, _, _ := newTestModel(t)
	a := &fleetTestActions{Actions: m.actions, returnErr: errors.New("server unavailable")}
	m.actions = a
	m.width = 80
	m.saveFleetView()
	m.fleet.active = true
	m.fleet.summary = multiplexer.FleetSummary{Scope: strings.Repeat("界", 80)}
	header := ansi.Strip(m.View())
	if ansi.StringWidth(header) != 80 || !strings.HasSuffix(header, "[Return to board · v]") || strings.Contains(header, "\n") {
		t.Fatalf("clipped return %q", header)
	}
	m, cmd := fleetApply(t, m, tea.MouseMsg{X: 79, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m, _ = fleetApply(t, m, cmd())
	if !m.fleet.active || m.fleet.err == "" || a.returns != 1 {
		t.Fatal("failure lost recovery header")
	}
	a.returnErr = nil
	m, cmd = fleetApply(t, m, fleetKey("enter"))
	m, _ = fleetApply(t, m, cmd())
	if m.fleet.active || a.returns != 2 {
		t.Fatal("retry failed")
	}
}
func TestFleetHotkeyDoesNotStealEditorOrSearchInput(t *testing.T) {
	m, _, _ := newTestModel(t)
	a := &fleetTestActions{Actions: m.actions}
	m.actions = a
	m.startBoardSearch()
	m, _ = fleetApply(t, m, fleetKey("v"))
	if m.fleet.busy || m.search.query.Value() != "v" {
		t.Fatal("Fleet stole search typing")
	}
	m.search.active = false
	m.boardPicker = true
	m, _ = fleetApply(t, m, fleetKey("v"))
	if m.fleet.busy {
		t.Fatal("Fleet stole picker input")
	}
}

func TestFleetDefaultBoardScopeAndBusyInterrupt(t *testing.T) {
	m, _, _ := newTestModel(t)
	a := &fleetTestActions{Actions: m.actions}
	m.actions = a
	m.width, m.height = 160, 45
	m, cmd := fleetApply(t, m, fleetKey("v"))
	m, _ = fleetApply(t, m, cmd())
	if a.scope != m.view.Board.ID || a.scope == 0 {
		t.Fatal("default board gathered Master scope")
	}
	m.fleet.confirm = false
	m.fleet.active = true
	m.fleet.busy = true
	m, cmd = fleetApply(t, m, fleetKey("ctrl+c"))
	if cmd == nil {
		t.Fatal("busy Fleet ignored global interrupt")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("busy interrupt did not quit safely")
	}
}

func TestFleetReturnRequestedDuringEntryRunsAfterEntryCompletes(t *testing.T) {
	m, _, _ := newTestModel(t)
	a := &fleetTestActions{Actions: m.actions}
	m.actions = a
	m.saveFleetView()
	m.fleet.active = true
	m.fleet.busy = true
	m, cmd := fleetApply(t, m, fleetKey("v"))
	if cmd != nil || !m.fleet.pendingReturn {
		t.Fatal("entry return request not queued")
	}
	m, cmd = fleetApply(t, m, fleetResult{operation: "enter", status: multiplexer.FleetStatus{Present: true, Live: 3, Count: 3}})
	if cmd == nil {
		t.Fatal("queued return not started")
	}
	m, _ = fleetApply(t, m, cmd())
	if m.fleet.active || a.returns != 1 {
		t.Fatal("queued return did not restore")
	}
}

func TestFleetMouseReleaseDuringReturnDoesNotExitTheNextVisit(t *testing.T) {
	m, _, _ := newTestModel(t)
	a := &fleetTestActions{Actions: m.actions}
	m.actions = a
	m.saveFleetView()
	m.fleet.active = true
	m.width = 80
	press := tea.MouseMsg{X: 79, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	m, cmd := fleetApply(t, m, press)
	m, _ = fleetApply(t, m, tea.MouseMsg{X: 79, Y: 0, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if !m.fleet.pendingReturn {
		t.Fatal("expected trailing mouse release during return")
	}
	m, _ = fleetApply(t, m, cmd())
	if m.fleet.pendingReturn {
		t.Fatal("trailing release survived return")
	}
	m, cmd = fleetApply(t, m, fleetKey("v"))
	m, _ = fleetApply(t, m, cmd())
	m, cmd = fleetApply(t, m, fleetKey("enter"))
	m, _ = fleetApply(t, m, cmd())
	if !m.fleet.active || a.returns != 1 {
		t.Fatal("next visit returned automatically")
	}
}
