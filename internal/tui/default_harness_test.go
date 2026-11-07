package tui

import (
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestConfiguredDefaultHarness(t *testing.T) {
	m, store, ctx := newTestModel(t)
	m.SetDefaultHarness("codex")
	view := defaultBoardView(t, ctx, store)
	m.createTicketInColumn(view.Columns[0].ID)
	if !m.editing || m.editInputs[2].Value() != "codex" {
		t.Fatalf("new ticket harness = %q, editing = %v", m.editInputs[2].Value(), m.editing)
	}
	// Clearing the editor field must display and save the same default.
	m.editInputs[2].Set("")
	if m.editDirty(m.editTicket) {
		t.Fatal("empty field should resolve to the saved default")
	}
	if !m.saveEdit() {
		t.Fatal(m.status)
	}
	tickets, err := store.ListTickets(ctx, false)
	if err != nil || len(tickets) != 1 || tickets[0].Harness != "codex" {
		t.Fatalf("tickets = %+v, err = %v", tickets, err)
	}
	m.startTemplateEditor(storage.TicketTemplate{}, true)
	if m.templateHarness.Value() != "codex" {
		t.Fatalf("template harness = %q", m.templateHarness.Value())
	}
	m.startTemplateEditor(storage.TicketTemplate{Harness: "claude"}, false)
	if m.templateHarness.Value() != "claude" {
		t.Fatal("existing template harness changed")
	}
}
