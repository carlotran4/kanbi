package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCreateTicketFromTemplateFlowCreatesOneSnapshot(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, "Bug report", "Bug:", "## Reproduction\n", "codex"); err != nil {
		t.Fatal(err)
	}
	model.reload()
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	model = next.(Model)
	if !model.templateOpen || model.templateMode != "pick" {
		t.Fatalf("N did not open template picker: %+v", model)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	if model.templateMode != "title" {
		t.Fatalf("Enter mode=%q want title", model.templateMode)
	}
	model.templateTitle.Set("Bug: resize")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	if !model.editing || model.templateOpen {
		t.Fatalf("ticket editor should open after creation")
	}
	tickets, err := store.ListTickets(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 || tickets[0].Title != "Bug: resize" || tickets[0].Body != "## Reproduction\n" || tickets[0].Harness != "codex" {
		t.Fatalf("unexpected template ticket: %+v", tickets)
	}
}

func TestTemplatePickerCancelCreatesNothingAndFits80x24(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, "Investigation", "", strings.Repeat("Long body ", 20), "pi"); err != nil {
		t.Fatal(err)
	}
	model.width, model.height = 80, 24
	model.startTemplateCreate()
	rendered := model.View()
	if !strings.Contains(rendered, "New ticket from template") || !strings.Contains(rendered, "Investigation") {
		t.Fatalf("template picker missing at 80x24:\n%s", rendered)
	}
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(Model)
	if model.templateOpen {
		t.Fatal("Esc should close picker")
	}
	tickets, err := store.ListTickets(ctx, false)
	if err != nil || len(tickets) != 0 {
		t.Fatalf("cancel created tickets: %+v err=%v", tickets, err)
	}
}

func TestMasterTemplateFlowBindsEditorWhenFilterHidesCreatedTicket(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, "Bug", "Bug:", "body", "pi"); err != nil {
		t.Fatal(err)
	}
	model.masterBoard = true
	model.boardID = 0
	model.masterFilter.Search = "will-not-match-created-ticket"
	model.reload()
	model.col = 0
	model.startTemplateCreate()
	for i, board := range model.boards {
		if board.ID == view.Board.ID {
			model.boardIndex = i + 1
		}
	}
	model = model.updateBoardPicker(tea.KeyMsg{Type: tea.KeyEnter})
	if !model.templateOpen || model.templateMode != "pick" {
		t.Fatalf("Master did not route to board template picker: status=%q", model.status)
	}
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	model.templateTitle.Set("Bug: hidden by filter")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	if !model.editing || model.editTicket.Title != "Bug: hidden by filter" {
		t.Fatalf("editor bound to wrong ticket: editing=%v ticket=%+v", model.editing, model.editTicket)
	}
}

func TestTemplateManagementCreateEditDelete(t *testing.T) {
	model, store, ctx := newTestModel(t)
	model.startTemplateManagement()
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	model = next.(Model)
	model.templateName.Set("Feature")
	model.templateSeedTitle.Set("Feature:")
	model.templateBodyTA.SetValue("## Outcome")
	model.templateHarness.Set("pi")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = next.(Model)
	if model.templateMode != "manage" || len(model.templates) != 1 {
		t.Fatalf("template not saved: mode=%q templates=%+v status=%q", model.templateMode, model.templates, model.status)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	model = next.(Model)
	model.templateSeedTitle.Set("Updated feature:")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = next.(Model)
	if len(model.templates) != 1 || model.templates[0].Title != "Updated feature:" {
		t.Fatalf("template not edited: %+v", model.templates)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	model = next.(Model)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	templates, err := store.ListTicketTemplates(ctx, model.view.Board.ID)
	if err != nil || len(templates) != 0 {
		t.Fatalf("template not deleted: %+v err=%v", templates, err)
	}
}

func TestTemplatePickerKeepsControlsWithLongListAndPreviewAt80x24(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	for i := 0; i < 30; i++ {
		body := strings.Repeat("Long preview content ", 40)
		if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, fmt.Sprintf("Template %02d", i), "Title", body, "pi"); err != nil {
			t.Fatal(err)
		}
	}
	model.width, model.height = 80, 24
	model.startTemplateCreate()
	model.templateIndex = 29
	rendered := ansiStrip(model.View())
	if !strings.Contains(rendered, "Template 29") || !strings.Contains(rendered, "Enter continue") || !strings.Contains(rendered, "Esc cancel") {
		t.Fatalf("long template picker lost focus or controls at 80x24:\n%s", rendered)
	}
}

func TestEmptyTemplatePickerTShortcutOpensManager(t *testing.T) {
	model, _, _ := newTestModel(t)
	model.startTemplateCreate()
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	model = next.(Model)
	if !model.templateOpen || model.templateMode != "manage" {
		t.Fatalf("T from empty picker mode=%q filter=%q", model.templateMode, model.templateFilter.Value())
	}
}

func TestTemplateEditorClearsValidationStatusWhenCancelled(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, "Bug", "", "body", "pi"); err != nil {
		t.Fatal(err)
	}
	model.startTemplateManagement()
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	model = next.(Model)
	model.templateName.Set("BUG")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = next.(Model)
	if model.status != "template name already exists on this board" {
		t.Fatalf("unexpected validation status %q", model.status)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(Model)
	if model.templateMode != "manage" || model.status != "" {
		t.Fatalf("cancel left stale status: mode=%q status=%q", model.templateMode, model.status)
	}
}

func TestTemplateManagerKeepsStatusAndFrameAt80x24(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	for i := 0; i < 24; i++ {
		if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, fmt.Sprintf("Template %02d", i), "Title", "body", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	model.width, model.height = 80, 24
	model.startTemplateManagement()
	model.status = "saved template"
	rendered := ansiStrip(model.View())
	if !strings.Contains(rendered, "Status: saved template") || !strings.Contains(rendered, "c create") {
		t.Fatalf("manager status or controls clipped at 80x24:\n%s", rendered)
	}
}

func TestTemplateManagerUseEscapeReturnsToManagerAndDeleteKeepsNearbySelection(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	for i := 0; i < 3; i++ {
		if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, fmt.Sprintf("Template %d", i), "", "body", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	model.startTemplateManagement()
	model.templateIndex = 2
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(Model)
	if model.templateMode != "manage" {
		t.Fatalf("Esc returned to %q, want manage", model.templateMode)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	model = next.(Model)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	model = next.(Model)
	if model.templateIndex != 1 {
		t.Fatalf("selection index=%d want 1 after deleting last item", model.templateIndex)
	}
}

func TestTemplateRowsPreserveHarnessAndLongPreviewShowsEllipsis(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	name := strings.Repeat("Very long template name ", 10)
	if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, name, "Long title", strings.Repeat("preview content ", 100), "claude"); err != nil {
		t.Fatal(err)
	}
	model.width, model.height = 80, 24
	model.startTemplateCreate()
	rendered := ansiStrip(model.View())
	if !strings.Contains(rendered, "[claude]") || !strings.Contains(rendered, "…") {
		t.Fatalf("picker lost harness or truncation marker:\n%s", rendered)
	}
}

func TestTemplateManagerBoundsLongListsAt80x24(t *testing.T) {
	model, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	for i := 0; i < 30; i++ {
		if _, err := store.CreateTicketTemplate(ctx, view.Board.ID, fmt.Sprintf("Template %02d", i), "", "body", "pi"); err != nil {
			t.Fatal(err)
		}
	}
	model.width, model.height = 80, 24
	model.startTemplateManagement()
	model.templateIndex = 29
	rendered := ansiStrip(model.View())
	if !strings.Contains(rendered, "Template 29") || !strings.Contains(rendered, "c create") || !strings.Contains(rendered, "?:help") {
		t.Fatalf("long template manager lost focus or controls at 80x24:\n%s", rendered)
	}
}
