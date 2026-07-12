package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestEmptyStartupShowsDismissibleActionableOnboarding(t *testing.T) {
	store, ctx := newTestStore(t)
	model := NewWithPicker(ctx, NewService(store, nil))
	model.width, model.height = 80, 24
	view := ansiStrip(model.View())
	for _, want := range []string{"Welcome to Kanbi", "tmux", "kanbi doctor", "Esc skip"} {
		if !strings.Contains(view, want) {
			t.Fatalf("first onboarding page missing %q:\n%s", want, view)
		}
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.firstRun {
		t.Fatal("Esc should dismiss onboarding without changing durable data")
	}
	if !model.boardPicker {
		t.Fatal("dismissing onboarding should reveal the board picker")
	}
}

func TestOnboardingCoversSessionAndBackupPath(t *testing.T) {
	store, ctx := newTestStore(t)
	model := NewWithPicker(ctx, NewService(store, nil))
	model.width, model.height = 80, 24
	model = model.updateOnboarding(tea.KeyMsg{Type: tea.KeyEnter})
	page2 := ansiStrip(model.View())
	for _, want := range []string{"working directory", "Enter sends", "After this guide", "safely closes"} {
		if !strings.Contains(page2, want) {
			t.Errorf("session onboarding missing %q", want)
		}
	}
	model = model.updateOnboarding(tea.KeyMsg{Type: tea.KeyEnter})
	page3 := ansiStrip(model.View())
	for _, want := range []string{"repair", "history is preserved", "kanbi backup"} {
		if !strings.Contains(page3, want) {
			t.Errorf("safety onboarding missing %q", want)
		}
	}
}

func TestMonochromeCardsDistinguishAttentionStates(t *testing.T) {
	waiting := storage.Ticket{DisplayID: "T-001", Title: "Wait", Harness: "pi", Runtime: kanban.StateWaitingForUser}
	permission := storage.Ticket{DisplayID: "T-002", Title: "Approve", Harness: "pi", Runtime: kanban.StateNeedsPermission}
	waitText := ansiStrip(strings.Join(cardView(false, waiting, 30, false), "\n"))
	permissionText := ansiStrip(strings.Join(cardView(false, permission, 30, false), "\n"))
	if !strings.Contains(waitText, "waiting for user") {
		t.Fatalf("waiting card lacks textual state:\n%s", waitText)
	}
	if !strings.Contains(permissionText, "permission required") {
		t.Fatalf("permission card lacks textual state:\n%s", permissionText)
	}
	if waitText == permissionText {
		t.Fatal("attention states must remain distinct with color removed")
	}
}

func TestHelpLegendFitsAndScrollsAt80x24(t *testing.T) {
	store, ctx := newTestStore(t)
	model := New(ctx, NewService(store, nil))
	model.width, model.height, model.showHelp = 80, 24, true
	first := model.View()
	if got := len(strings.Split(first, "\n")); got > 24 {
		t.Fatalf("help rendered %d lines at 80x24", got)
	}
	for i := 0; i < 20; i++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	last := ansiStrip(model.View())
	if first == model.View() {
		t.Fatal("help did not scroll")
	}
	if !strings.Contains(last, "Indicator legend") && !strings.Contains(last, "active container") {
		t.Fatalf("scrolling did not make the indicator legend reachable:\n%s", last)
	}
}

func TestActionErrorPreservesCauseAndRendersRemediation(t *testing.T) {
	model := Model{width: 80, height: 24}
	cause := errors.New("tmux executable not found")
	model.setActionError("open ticket session", cause, "Run `kanbi doctor`, then retry.")
	view := ansiStrip(model.baseView())
	for _, want := range []string{"Failed operation: open ticket session", "Cause: " + cause.Error(), "Next: Run `kanbi doctor`, then retry."} {
		if !strings.Contains(view, want) {
			t.Fatalf("rendered operation error missing %q:\n%s", want, view)
		}
	}
}

func TestStartingClaimIsNotReportedAsValidatedContainer(t *testing.T) {
	ticket := storage.Ticket{SessionActive: true, WindowName: sqlNullStr("reserved-name"), Runtime: kanban.StateStarting}
	if got := windowIndicator(ticket); got != "- no active container" {
		t.Fatalf("starting claim indicator = %q, want no validated container", got)
	}
}

func TestFitModalFollowsFocusedControlBelowFold(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		prefix := "  "
		if i == 35 {
			prefix = "> "
		}
		lines = append(lines, prefix+"control")
	}
	view := fitModal(strings.Join(lines, "\n"), 24, 0, true)
	if !strings.Contains(ansiStrip(view), "> control") {
		t.Fatalf("focused control should remain visible in a short terminal:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got > 22 {
		t.Fatalf("fit modal returned %d lines, want <= 22", got)
	}
}
