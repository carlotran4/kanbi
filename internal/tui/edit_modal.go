package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
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
		m.bodyTA.Blur()
	case "tab":
		newField := m.editField + 1
		if newField > 2 {
			m.saveEdit()
		} else {
			if m.editField == 1 {
				m.bodyTA.Blur()
			}
			m.editField = newField
			if newField == 1 {
				return m, m.bodyTA.Focus()
			}
		}
	case "enter":
		if m.editField == 1 {
			// Let textarea handle it (inserts newline).
			var cmd tea.Cmd
			m.bodyTA, cmd = m.bodyTA.Update(key)
			return m, cmd
		}
		newField := m.editField + 1
		if newField > 2 {
			m.saveEdit()
		} else {
			m.editField = newField
			if newField == 1 {
				return m, m.bodyTA.Focus()
			}
		}
	case "ctrl+E":
		if m.editField == 1 {
			// sync textarea value back before opening editor
			m.editInputs[1] = NewInputBuffer(m.bodyTA.Value())
		}
		return m, m.openBodyEditor()
	default:
		if m.editField == 1 {
			var cmd tea.Cmd
			m.bodyTA, cmd = m.bodyTA.Update(key)
			return m, cmd
		}
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
	body := m.bodyTA.Value()
	harness := m.editInputs[2].Value()
	if strings.TrimSpace(harness) == "" {
		harness = "pi"
	}
	if err := m.actions.UpdateTicket(m.ctx, t.ID, strings.TrimSpace(title), body, strings.TrimSpace(harness)); err != nil {
		m.status = err.Error()
	} else {
		m.status = "updated " + t.DisplayID
	}
	m.bodyTA.Blur()
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

func newBodyTextarea(value string, termWidth int) textarea.Model {
	ta := textarea.New()
	ta.SetValue(value)
	ta.Placeholder = "(no description)"
	ta.ShowLineNumbers = false
	// Width: popup inner width minus label prefix (~12 chars)
	popupInner := popupWidth(termWidth) - 4
	if popupInner < 20 {
		popupInner = 20
	}
	ta.SetWidth(popupInner - 12)
	ta.SetHeight(8)
	return ta
}

func (m Model) editView() string {
	popupW := popupWidth(m.width)
	inner := popupW - 4

	rowCursor := func(i int) string {
		if m.editField == i {
			return lipgloss.NewStyle().Foreground(palette.accent).Render(">")
		}
		return " "
	}
	render := func(i int) string {
		if m.editField == i {
			return m.editInputs[i].Render()
		}
		return m.editInputs[i].Value()
	}

	var lines []string
	header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Edit ticket")
	lines = append(lines, header, "")
	lines = append(lines, fmt.Sprintf("%s title:   %s", rowCursor(0), render(0)))

	// Body field: textarea when focused, compact summary otherwise.
	if m.editField == 1 {
		lines = append(lines, fmt.Sprintf("%s body:", rowCursor(1)))
		lines = append(lines, m.bodyTA.View())
	} else {
		body := m.bodyTA.Value()
		var bodyPreview string
		if strings.TrimSpace(body) == "" {
			bodyPreview = lipgloss.NewStyle().Faint(true).Render("(no description)")
		} else {
			// Render markdown with glamour in the popup (safe here — not inside card layout).
			previewWidth := inner - 14 // subtract label prefix
			if previewWidth < 20 {
				previewWidth = 20
			}
			grendered := ""
			if gr, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(previewWidth)); err == nil {
				if out, err := gr.Render(body); err == nil {
					grendered = strings.TrimSpace(out)
				}
			}
			if grendered != "" {
				// Show first 4 non-empty rendered lines inline.
				var rendLines []string
				for _, l := range strings.Split(grendered, "\n") {
					if strings.TrimSpace(l) == "" {
						continue
					}
					rendLines = append(rendLines, l)
					if len(rendLines) == 4 {
						break
					}
				}
				newlines := strings.Count(body, "\n")
				suffix := ""
				if newlines > 0 {
					suffix = lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(" (+%d lines)", newlines))
				}
				bodyPreview = strings.Join(rendLines, "\n") + suffix
			} else {
				// Fallback: first line + line count.
				first := strings.SplitN(body, "\n", 2)[0]
				newlines := strings.Count(body, "\n")
				if newlines > 0 {
					bodyPreview = first + lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(" (+%d lines)", newlines))
				} else {
					bodyPreview = first
				}
			}
		}
		lines = append(lines, fmt.Sprintf("%s body:", rowCursor(1)))
		lines = append(lines, bodyPreview)
	}
	lines = append(lines, fmt.Sprintf("%s harness: %s", rowCursor(2), render(2)))
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Faint(true).Render("Tab next · Enter newline in body · Ctrl+E editor · Esc cancel"))

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(inner).
		Render(content)
}

func (m Model) stateMenuView() string {
	t, _ := m.selectedTicket()
	var lines []string
	header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Mark state for " + t.DisplayID)
	lines = append(lines, header, "")
	for i, state := range manualStates {
		cursor := " "
		if i == m.stateIndex {
			cursor = lipgloss.NewStyle().Foreground(palette.accent).Render(">")
		}
		lines = append(lines, fmt.Sprintf("%s %s", cursor, state))
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Enter mark · Esc cancel"))
	if m.status != "" {
		lines = append(lines, statusStyle.Render(m.status))
	}
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}

func (m Model) columnEditView() string {
	title := "Add column"
	if m.columnAction == "rename" {
		title = "Rename column"
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render(title)
	content := header + "\n\n" +
		fmt.Sprintf("%s name: %s", lipgloss.NewStyle().Foreground(palette.accent).Render(">"), m.columnInput.Render()) +
		"\n\n" + lipgloss.NewStyle().Faint(true).Render("Enter save · Esc cancel")
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(content)
}
