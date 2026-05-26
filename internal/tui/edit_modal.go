package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"agent-kanban/internal/kanban"
)

func (m *Model) startColumnEdit(action string) {
	if action != "add" && (m.col < 0 || m.col >= len(m.view.Columns)) {
		return
	}
	m.columnEditing = true
	m.columnAction = action
	m.columnInput = NewInputBuffer("")
	if action == "rename" {
		m.columnInput = NewInputBuffer(m.view.Columns[m.col].Name)
	}
}

func (m Model) updateColumnEdit(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.columnEditing = false
	case "enter":
		switch m.columnAction {
		case "add":
			boardID := int64(0)
			if len(m.view.Columns) > 0 {
				boardID = m.view.Columns[0].BoardID
			}
			if _, err := m.actions.AddColumn(m.ctx, boardID, m.columnInput.Value()); err != nil {
				m.status = err.Error()
			} else {
				m.status = "added column"
			}
		case "rename":
			if err := m.actions.RenameColumn(m.ctx, m.view.Columns[m.col].ID, m.columnInput.Value()); err != nil {
				m.status = err.Error()
			} else {
				m.status = "renamed column"
			}
		}
		m.columnEditing = false
		m.reload()
	default:
		m.columnInput.HandleKey(key.String(), key.Runes)
	}
	return m
}

func (m *Model) startStateMenu() {
	if _, ok := m.selectedTicket(); !ok {
		return
	}
	m.stateMenu = true
	m.stateIndex = 0
}

var manualStates = []string{kanban.StateRunning, kanban.StateWaitingForUser, kanban.StateIdleUnknown, kanban.StateError}

func (m Model) updateStateMenu(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.stateMenu = false
	case "j", "down":
		m.stateIndex = (m.stateIndex + 1) % len(manualStates)
	case "k", "up":
		m.stateIndex--
		if m.stateIndex < 0 {
			m.stateIndex = len(manualStates) - 1
		}
	case "enter":
		t, ok := m.selectedTicket()
		if !ok {
			m.stateMenu = false
			return m
		}
		state := manualStates[m.stateIndex]
		if err := m.actions.MarkTicketState(m.ctx, t.ID, state); err != nil {
			m.status = err.Error()
		} else {
			m.status = "marked " + t.DisplayID + " " + state
		}
		m.stateMenu = false
		m.reload()
	}
	return m
}

func (m *Model) moveAttention(delta int) {
	type pos struct{ col, card int }
	var positions []pos
	for ci, col := range m.view.Columns {
		for ti, ticket := range col.Tickets {
			if isAttention(ticket.Runtime) {
				positions = append(positions, pos{ci, ti})
			}
		}
	}
	if len(positions) == 0 {
		m.status = "no attention tickets"
		return
	}
	current := 0
	for i, p := range positions {
		if p.col == m.col && p.card == m.card {
			current = i
			break
		}
	}
	next := (current + delta) % len(positions)
	if next < 0 {
		next += len(positions)
	}
	m.col = positions[next].col
	m.card = positions[next].card
	m.status = "attention " + m.view.Columns[m.col].Tickets[m.card].DisplayID
}

func (m Model) updateEdit(key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.editing = false
	case "tab":
		newField := m.editField + 1
		if newField > 2 {
			m.saveEdit()
		} else {
			m.editField = newField
		}
	case "enter":
		if m.editField == 1 {
			// newline in body
			m.currentEditBuffer().Insert("\n")
		} else {
			newField := m.editField + 1
			if newField > 2 {
				m.saveEdit()
			} else {
				m.editField = newField
			}
		}
	case "ctrl+E":
		return m, m.openBodyEditor()
	default:
		m.currentEditBuffer().HandleKey(key.String(), key.Runes)
	}
	return m, nil
}

func (m *Model) saveEdit() {
	t, ok := m.selectedTicket()
	if !ok {
		m.editing = false
		return
	}
	title := m.editInputs[0].Value()
	body := m.editInputs[1].Value()
	harness := m.editInputs[2].Value()
	if strings.TrimSpace(harness) == "" {
		harness = "pi"
	}
	if err := m.actions.UpdateTicket(m.ctx, t.ID, strings.TrimSpace(title), body, strings.TrimSpace(harness)); err != nil {
		m.status = err.Error()
	} else {
		m.status = "updated " + t.DisplayID
	}
	m.editing = false
	m.reload()
}

func (m *Model) currentEditBuffer() *InputBuffer {
	if m.editField < 0 || m.editField >= len(m.editInputs) {
		m.editField = 0
	}
	return &m.editInputs[m.editField]
}

func renderWithCursor(value string, cursor int) string {
	buf := NewInputBuffer(value)
	buf.SetCursor(cursor)
	return buf.Render()
}

func (m Model) editView() string {
	rowCursor := func(i int) string {
		if m.editField == i {
			return ">"
		}
		return " "
	}
	render := func(i int) string {
		if m.editField == i {
			return m.editInputs[i].Render()
		}
		return m.editInputs[i].Value()
	}
	// For body, show it inline but with newlines rendered visibly for multiline.
	body := m.editInputs[1].Value()
	if m.editField != 1 {
		// Show a compact summary of the body when not editing it.
		newlines := strings.Count(body, "\n")
		if newlines > 0 {
			body = strings.SplitN(body, "\n", 2)[0] + lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(" (+%d lines)", newlines))
		}
	} else {
		body = render(1)
	}
	return fmt.Sprintf("Edit ticket\n\n%s title:   %s\n%s body:    %s\n%s harness: %s\n\nTab next field · Enter newline in body · ←/→ move · Home/End · Ctrl+E full editor · Esc cancel\n",
		rowCursor(0), render(0),
		rowCursor(1), body,
		rowCursor(2), render(2))
}

func (m Model) stateMenuView() string {
	var b strings.Builder
	t, _ := m.selectedTicket()
	fmt.Fprintf(&b, "Mark state for %s\n\n", t.DisplayID)
	for i, state := range manualStates {
		cursor := " "
		if i == m.stateIndex {
			cursor = ">"
		}
		fmt.Fprintf(&b, "%s %s\n", cursor, state)
	}
	b.WriteString("\nEnter mark, Esc cancel\n")
	if m.status != "" {
		b.WriteString(m.status + "\n")
	}
	return b.String()
}

func (m Model) columnEditView() string {
	title := "Add column"
	if m.columnAction == "rename" {
		title = "Rename column"
	}
	return fmt.Sprintf("%s\n\n> name: %s\n\nEnter save, Esc cancel\n", title, m.columnInput.Render())
}
