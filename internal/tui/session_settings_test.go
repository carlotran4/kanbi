package tui

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSessionSettingsHealthyTicketFlow(t *testing.T) {
	for _, state := range []string{"none", "active", "closed"} {
		t.Run(state, func(t *testing.T) {
			m, store, ctx := newTestModel(t)
			view := defaultBoardView(t, ctx, store)
			ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Healthy", "body", "pi")
			if err != nil {
				t.Fatal(err)
			}
			if state != "none" {
				id, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", Status: "running", HarnessSessionRef: sql.NullString{String: "saved-ref", Valid: true}})
				if err != nil {
					t.Fatal(err)
				}
				if state == "closed" {
					if err := store.MarkSessionClosed(ctx, id, "closed", "manual", "test"); err != nil {
						t.Fatal(err)
					}
				}
			}
			actions := &openingStore{Service: app.NewService(store, nil)}
			m.actions = actions
			m.reload()
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
			m = updated.(Model)
			if !m.sessionSettings || m.repairing {
				t.Fatal("S must open settings, not repair")
			}
			rendered := m.sessionSettingsView()
			if !strings.Contains(rendered, "Session settings") || !strings.Contains(rendered, "Fresh launch harness: pi") {
				t.Fatal(rendered)
			}
			if state == "none" && !strings.Contains(rendered, "no prior session") {
				t.Fatal(rendered)
			}
			m, _ = m.updateSessionSettings(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
			if !m.editing || m.editField != 2 || m.editSessionRefVisible != (state != "none") {
				t.Fatal("must reuse editor")
			}
			m.editInputs[2].Set("codex")
			if !m.saveEdit() {
				t.Fatal(m.status)
			}
			if actions.startedFresh || !strings.Contains(m.sessionSettingsView(), "Fresh launch harness: codex") {
				t.Fatal("save must only change preference and refresh info")
			}
			m, _ = m.updateSessionSettings(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			if actions.startedFresh || !strings.Contains(m.sessionSettingsView(), "NOT reused") {
				t.Fatal("fresh requires confirmation")
			}
			m, _ = m.updateSessionSettings(tea.KeyMsg{Type: tea.KeyEsc})
			if m.sessionFreshConfirm || actions.startedFresh {
				t.Fatal("cancel mutated session")
			}
			got, _ := store.TicketByID(ctx, ticket.ID)
			if got.SessionActive != (state == "active") || (state != "none" && got.SessionRef.String != "saved-ref") {
				t.Fatalf("cancel changed session: %+v", got)
			}
			m, _ = m.updateSessionSettings(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			var cmd tea.Cmd
			m, cmd = m.updateSessionSettings(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("no fresh command")
			}
			cmd()
			if !actions.startedFresh || !actions.startedFreshSendPrompt {
				t.Fatal("fresh must send ticket prompt")
			}
		})
	}
}
