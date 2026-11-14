package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionSettingsCancelEditorAndModalBounds(t *testing.T) {
	m, store, ctx := newTestModel(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Healthy", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	m.sessionSettings = true
	m.sessionSettingsTicketID = ticket.ID
	m.refreshSessionSettings()
	m, _ = m.updateSessionSettings(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m.editInputs[2].Set("codex")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.editing || m.activeModalKind() != modalSessionSettings {
		t.Fatal("cancel must return to settings")
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if got.Harness != "pi" || got.SessionID.Valid {
		t.Fatal("cancel saved editor or created attempt")
	}
	for _, size := range [][2]int{{80, 24}, {160, 45}} {
		m.width, m.height = size[0], size[1]
		for _, confirm := range []bool{false, true} {
			m.sessionFreshConfirm = confirm
			rendered := fitModal(m.sessionSettingsView(), m.height, 0, true)
			if len(strings.Split(rendered, "\n")) > m.height {
				t.Fatal("modal too tall")
			}
			for _, line := range strings.Split(rendered, "\n") {
				if ansi.StringWidth(line) > m.width {
					t.Fatal("modal too wide")
				}
			}
			if confirm && !strings.Contains(rendered, "Enter start fresh") {
				t.Fatal("confirmation action clipped")
			}
		}
	}
}
