package tui

import (
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
)

func TestActiveModalKindPrecedence(t *testing.T) {
	cases := []struct {
		name string
		set  func(*Model)
		want modalKind
	}{
		{"integration", func(m *Model) { m.integrationOpen = true }, modalIntegration},
		{"settings", func(m *Model) { m.focusSettingsOpen = true }, modalFocusSettings},
		{"replacement", func(m *Model) { m.focusReplaceOpen = true }, modalFocusReplace},
		{"pause", func(m *Model) { m.pauseOpen = true }, modalPause},
		{"resume", func(m *Model) { m.resumeOpen = true }, modalResume},
		{"onboarding", func(m *Model) { m.firstRun = true }, modalOnboarding},
		{"help", func(m *Model) { m.showHelp = true }, modalHelp},
		{"picker", func(m *Model) { m.boardPicker = true }, modalBoardPicker},
		{"filter", func(m *Model) { m.masterFilterOpen = true }, modalMasterFilter},
		{"repair", func(m *Model) { m.repairing = true }, modalRepair},
		{"branch", func(m *Model) { m.branchNaming = true }, modalBranchName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Model
			tc.set(&m)
			if got := m.activeModalKind(); got != tc.want {
				t.Fatalf("kind=%v want=%v", got, tc.want)
			}
		})
	}
	m := Model{resumeOpen: true, focusReplaceOpen: true, boardPicker: true, masterFilterOpen: true, repairing: true, branchNaming: true, firstRun: true}
	if got := m.activeModalKind(); got != modalFocusReplace {
		t.Fatalf("overlap kind=%v, want replacement", got)
	}
}

func TestModalRoutingAndStatusUseActiveKind(t *testing.T) {
	m := Model{width: 80, height: 24, boards: []storage.Board{{ID: 1, Name: "Board"}}, boardPicker: true, masterFilterOpen: true, status: "keep", statusBarCancel: func() {}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := focusTestModel(t, next)
	if got.boardIndex != 1 || got.masterFilterField != 0 || got.status != "keep" {
		t.Fatalf("picker did not own visible overlap: index=%d field=%d status=%q", got.boardIndex, got.masterFilterField, got.status)
	}
	m = Model{width: 80, height: 24, repairing: true, branchNaming: true, status: "keep", statusBarCancel: func() {}}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = focusTestModel(t, next)
	if got.repairing || !got.branchNaming || got.status == "" {
		t.Fatalf("repair did not own overlap: repairing=%v branch=%v status=%q", got.repairing, got.branchNaming, got.status)
	}
	m = Model{status: "stale", errOperation: "op", errNext: "next", statusBarCancel: func() {}}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got = focusTestModel(t, next)
	if got.status != "" || got.errOperation != "" || got.errNext != "" {
		t.Fatalf("board key retained stale status: status=%q operation=%q next=%q", got.status, got.errOperation, got.errNext)
	}
}

func TestActiveModalsRenderWithin80x24(t *testing.T) {
	setters := []func(*Model){
		func(m *Model) { m.integrationOpen = true }, func(m *Model) { m.startFocusSettings() },
		func(m *Model) { m.focusReplaceOpen = true }, func(m *Model) { m.startPause(storage.Ticket{ID: 1}) },
		func(m *Model) { m.resumeOpen = true }, func(m *Model) { m.firstRun = true },
		func(m *Model) { m.showHelp = true }, func(m *Model) { m.boardRenaming = true },
		func(m *Model) { m.boardWorktreeEnabling = true }, func(m *Model) { m.boardEditing = true },
		func(m *Model) { m.boardDeleting = true }, func(m *Model) { m.boardExporting = true },
		func(m *Model) { m.boardImporting = true }, func(m *Model) { m.boardPicker = true },
		func(m *Model) { m.masterFilterOpen = true }, func(m *Model) { m.editing = true; m.bodyTA = newBodyTextarea("", m.width) },
		func(m *Model) { m.stateMenu = true }, func(m *Model) { m.columnEditing = true },
		func(m *Model) { m.promptFallback = true }, func(m *Model) { m.repairing = true },
		func(m *Model) { m.branchNaming = true }, func(m *Model) { m.workspaceIntegrating = true },
	}
	for i, set := range setters {
		m := Model{width: 80, height: 24, statusBarCancel: func() {}}
		set(&m)
		view := m.View()
		if got := len(strings.Split(view, "\n")); got > 24 {
			t.Fatalf("modal %d rendered %d rows", i, got)
		}
	}
}

func TestModalViewShowsSelectedOverlay(t *testing.T) {
	m := Model{width: 80, height: 24, boardPicker: true, masterFilterOpen: true, statusBarCancel: func() {}}
	view := ansiStrip(m.View())
	if !strings.Contains(view, "Select board") || strings.Contains(view, "Master filters") {
		t.Fatalf("view did not use picker precedence:\n%s", view)
	}
}
