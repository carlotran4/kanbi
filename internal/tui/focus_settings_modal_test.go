package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestFocusWorkflowKeyEscapingRoundTripsCommaAndBackslash(t *testing.T) {
	want := []string{"In Progress", "Ready, blocked", `Path\\Review`}
	encoded := formatFocusWorkflowKeys(want)
	got := parseFocusWorkflowKeys(encoded)
	if len(got) != len(want) {
		t.Fatalf("round trip=%q -> %#v", encoded, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round trip[%d]=%q, want %q (encoded %q)", i, got[i], want[i], encoded)
		}
	}
	manual := parseFocusWorkflowKeys(`Path\Review, Ready\, blocked`)
	if len(manual) != 2 || manual[0] != `Path\Review` || manual[1] != "Ready, blocked" {
		t.Fatalf("manual escaping changed keys: %#v", manual)
	}
}

func TestFocusSettingsFlowEnablesAndDisablesImmediately(t *testing.T) {
	store, ctx := newTestStore(t)
	service := NewService(store, nil)
	var saved []storage.FocusPolicy
	service.FocusPolicySaver = func(_ context.Context, policy storage.FocusPolicy) error {
		saved = append(saved, policy)
		return nil
	}
	model := New(ctx, service)
	model.width, model.height = 80, 24
	model, _ = mustUpdate(t, model, "F")
	if !model.focusSettingsOpen {
		t.Fatal("F did not open Focus settings")
	}
	next, _ := model.updateFocusSettings(tea.KeyMsg{Type: tea.KeySpace})
	model = focusTestModel(t, next)
	model.focusSettingsLimit.Set("2")
	model.focusSettingsKeys.Set("In Progress, Review, Review")
	next, cmd := model.updateFocusSettings(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = focusTestModel(t, next)
	model = focusTestModel(t, mustRunFocusCmd(t, model, cmd))
	if model.focusSettingsOpen || !model.focus.Enabled || model.focus.Limit != 2 {
		t.Fatalf("settings did not enable immediately: open=%v focus=%+v", model.focusSettingsOpen, model.focus)
	}
	if len(saved) != 1 || len(saved[0].WorkflowKeys) != 2 {
		t.Fatalf("saved policies=%+v", saved)
	}

	model, _ = mustUpdate(t, model, "F")
	next, _ = model.updateFocusSettings(tea.KeyMsg{Type: tea.KeySpace})
	model = focusTestModel(t, next)
	next, cmd = model.updateFocusSettings(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = focusTestModel(t, next)
	model = focusTestModel(t, mustRunFocusCmd(t, model, cmd))
	if model.focus.Enabled || model.focusSettingsOpen || len(saved) != 2 || saved[1].Enabled {
		t.Fatalf("settings did not disable immediately: focus=%+v saved=%+v", model.focus, saved)
	}
}

func TestFocusSettingsPersistenceFailureKeepsDraftAndRuntimePolicy(t *testing.T) {
	store, ctx := newTestStore(t)
	service := NewService(store, nil)
	service.FocusPolicySaver = func(context.Context, storage.FocusPolicy) error { return errors.New("read-only config") }
	model := New(ctx, service)
	model.startFocusSettings()
	model.focusSettingsEnabled = true
	model.focusSettingsLimit.Set("4")
	model.focusSettingsKeys.Set("Review")
	next, cmd := model.updateFocusSettings(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = focusTestModel(t, next)
	model = focusTestModel(t, mustRunFocusCmd(t, model, cmd))
	if !model.focusSettingsOpen || model.focus.Enabled || !strings.Contains(model.status, "read-only config") {
		t.Fatalf("failed save lost draft or changed runtime: open=%v focus=%+v status=%q", model.focusSettingsOpen, model.focus, model.status)
	}
}

func TestFocusSettingsValidationAnd80x24Layout(t *testing.T) {
	model := Model{width: 80, height: 24, focus: storage.FocusStatus{Limit: 3}}
	model.startFocusSettings()
	model.focusSettingsEnabled = true
	model.focusSettingsKeys.Set("")
	next, cmd := model.updateFocusSettings(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = focusTestModel(t, next)
	if cmd != nil || model.focusSettingsField != 2 || !strings.Contains(model.status, "workflow key") {
		t.Fatalf("missing keys validation failed: field=%d status=%q", model.focusSettingsField, model.status)
	}
	for i, line := range strings.Split(model.focusSettingsView(), "\n") {
		if displayWidth(line) > 80 {
			t.Fatalf("line %d exceeds 80 columns: %q", i, line)
		}
	}

	store, ctx := newTestStore(t)
	full := New(ctx, NewService(store, nil))
	full.width, full.height = 80, 24
	full.startFocusSettings()
	rendered := ansiStrip(full.View())
	if len(strings.Split(rendered, "\n")) > 24 || !strings.Contains(rendered, "Ctrl+S save") {
		t.Fatalf("80x24 full view hides controls:\n%s", rendered)
	}
	full.focusSettingsOpen = false
	full.showHelp = true
	if help := ansiStrip(full.View()); !strings.Contains(help, "global Focus Mode settings") {
		t.Fatalf("help omits Focus settings shortcut:\n%s", help)
	}
}
