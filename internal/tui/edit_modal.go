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
	// Notes tab (editField == 3) has its own key handling.
	if m.editField == 3 {
		return m.updateNotesTab(key)
	}
	switch key.String() {
	case "esc":
		m.editing = false
		m.bodyTA.Blur()
	case "ctrl+s":
		m.saveEdit()
	case "shift+tab":
		if m.editField == 1 {
			m.bodyTA.Blur()
		}
		m.editField--
		if m.editField < 0 {
			m.editField = 3
		}
		if m.editField == 1 {
			return m, m.bodyTA.Focus()
		}
	case "tab":
		newField := m.editField + 1
		if newField > 3 {
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
		if newField > 3 {
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
	contentW := inner - 4
	if contentW < 30 {
		contentW = 30
	}
	t, _ := m.selectedTicket()

	muted := lipgloss.NewStyle().Foreground(palette.muted)
	accent := lipgloss.NewStyle().Foreground(palette.accent)
	heading := lipgloss.NewStyle().Bold(true)
	chip := lipgloss.NewStyle().Foreground(palette.chipText).Background(palette.header).Padding(0, 1)
	focusChip := lipgloss.NewStyle().Foreground(palette.chipTextInverted).Background(palette.accent).Padding(0, 1).Bold(true)

	sectionTitle := func(i int, label string) string {
		prefix := "  "
		style := muted
		if m.editField == i {
			prefix = accent.Render("▸ ")
			style = accent.Bold(true)
		}
		return prefix + style.Render(strings.ToUpper(label))
	}

	var lines []string
	id := t.DisplayID
	if id == "" {
		id = "Ticket"
	}
	lines = append(lines, muted.Render(id))
	if m.editField == 0 {
		lines = append(lines, focusChip.Render("TITLE")+" "+m.editInputs[0].Render())
	} else {
		title := strings.TrimSpace(m.editInputs[0].Value())
		if title == "" {
			title = "Untitled ticket"
		}
		lines = append(lines, heading.Render(title))
	}

	columnName := ""
	for _, col := range m.view.Columns {
		if col.ID == t.ColumnID {
			columnName = col.Name
			break
		}
	}
	meta := []string{}
	if m.masterBoard && t.BoardName != "" {
		meta = append(meta, chip.Render(t.BoardName))
	}
	if columnName != "" {
		meta = append(meta, chip.Render(columnName))
	}
	runtime := runtimeLabel(t)
	if runtime != "" {
		meta = append(meta, statusChip(t.Runtime, runtime))
	}
	harnessValue := strings.TrimSpace(m.editInputs[2].Value())
	if harnessValue == "" {
		harnessValue = "pi"
	}
	if m.editField == 2 {
		meta = append(meta, focusChip.Render("HARNESS")+" "+m.editInputs[2].Render())
	} else {
		meta = append(meta, chip.Render(harnessValue))
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, " "))
	}

	lines = append(lines, "", sectionTitle(1, "Description"))
	if m.editField == 1 {
		lines = append(lines, m.bodyTA.View())
	} else {
		body := strings.TrimSpace(m.bodyTA.Value())
		if body == "" {
			lines = append(lines, muted.Italic(true).Render("No description yet. Focus this section and start typing, or press Ctrl+E for $EDITOR."))
		} else {
			lines = append(lines, renderMarkdownForInspector(body, contentW, 9))
		}
	}

	lines = append(lines, "", sectionTitle(3, "Notes"))
	if m.editField == 3 {
		lines = append(lines, m.notesThreadView(contentW))
	} else {
		lines = append(lines, m.notesCompactView())
	}

	lines = append(lines, "")
	lines = append(lines, muted.Render("Tab/Shift+Tab focus · Ctrl+S save · Ctrl+E editor · Esc cancel"))

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(inner).
		Render(content)
}

func statusChip(runtime string, label string) string {
	style := lipgloss.NewStyle().Foreground(palette.chipText).Background(palette.header).Padding(0, 1)
	switch runtime {
	case kanban.StateWaitingForUser:
		style = style.Background(palette.warning).Foreground(palette.warningChipText)
	case kanban.StateNeedsPermission, kanban.StateError:
		style = style.Background(palette.error_).Foreground(palette.chipTextInverted)
	case kanban.StateRunning:
		style = style.Background(palette.success).Foreground(palette.successChipText)
	}
	return style.Render(label)
}

func renderMarkdownForInspector(body string, width int, maxLines int) string {
	if width < 20 {
		width = 20
	}
	rendered := ""
	if gr, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width)); err == nil {
		if out, err := gr.Render(body); err == nil {
			rendered = strings.TrimSpace(out)
		}
	}
	if rendered == "" {
		rendered = strings.TrimSpace(body)
	}
	parts := strings.Split(rendered, "\n")
	var lines []string
	for _, line := range parts {
		if strings.TrimSpace(line) == "" && (len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "") {
			continue
		}
		lines = append(lines, line)
		if len(lines) == maxLines {
			break
		}
	}
	if len(parts) > len(lines) {
		lines = append(lines, lipgloss.NewStyle().Faint(true).Render("…"))
	}
	return strings.Join(lines, "\n")
}

// updateNotesTab handles keystrokes when the notes section (editField==3) is active.
func (m Model) updateNotesTab(key tea.KeyMsg) (Model, tea.Cmd) {
	if m.noteEditing {
		switch key.String() {
		case "esc":
			m.noteEditing = false
			m.noteIsNew = false
		case "ctrl+s":
			m.saveNote()
		default:
			var cmd tea.Cmd
			m.noteTA, cmd = m.noteTA.Update(key)
			return m, cmd
		}
		return m, nil
	}

	switch key.String() {
	case "esc":
		m.editField = 2
	case "ctrl+s", "tab":
		m.saveEdit()
	case "shift+tab":
		m.editField = 2
	case "j", "down":
		if m.noteIndex < len(m.notes)-1 {
			m.noteIndex++
		}
	case "k", "up":
		if m.noteIndex > 0 {
			m.noteIndex--
		}
	case "a":
		m.noteEditing = true
		m.noteIsNew = true
		m.noteEditID = 0
		m.noteTA = newNoteTextarea("", m.width)
		return m, m.noteTA.Focus()
	case "e":
		if len(m.notes) > 0 && m.noteIndex < len(m.notes) {
			n := m.notes[m.noteIndex]
			m.noteEditing = true
			m.noteIsNew = false
			m.noteEditID = n.ID
			m.noteTA = newNoteTextarea(n.Body, m.width)
			return m, m.noteTA.Focus()
		}
	case "d":
		if len(m.notes) > 0 && m.noteIndex < len(m.notes) {
			n := m.notes[m.noteIndex]
			t, ok := m.selectedTicket()
			if !ok {
				break
			}
			if err := m.actions.DeleteNote(m.ctx, n.ID); err != nil {
				m.status = err.Error()
			} else {
				m.status = "deleted note"
				m.loadNotes(t.ID)
			}
		}
	}
	return m, nil
}

func (m *Model) saveNote() {
	t, ok := m.selectedTicket()
	if !ok {
		m.noteEditing = false
		return
	}
	body := m.noteTA.Value()
	if strings.TrimSpace(body) == "" {
		m.noteEditing = false
		m.noteIsNew = false
		return
	}
	if m.noteIsNew {
		if _, err := m.actions.AddNote(m.ctx, t.ID, body); err != nil {
			m.status = err.Error()
		} else {
			m.status = "note added"
		}
	} else {
		if err := m.actions.UpdateNote(m.ctx, m.noteEditID, body); err != nil {
			m.status = err.Error()
		} else {
			m.status = "note updated"
		}
	}
	m.noteEditing = false
	m.noteIsNew = false
	m.loadNotes(t.ID)
}

// notesCompactView renders a one-line summary for when notes are not the active tab.
func (m Model) notesCompactView() string {
	faint := lipgloss.NewStyle().Faint(true)
	if len(m.notes) == 0 {
		return faint.Render("none  (Tab to add)")
	}
	count := fmt.Sprintf("%d note", len(m.notes))
	if len(m.notes) != 1 {
		count += "s"
	}
	latest := m.notes[len(m.notes)-1]
	snippet := strings.SplitN(strings.TrimSpace(latest.Body), "\n", 2)[0]
	if len([]rune(snippet)) > 40 {
		snippet = string([]rune(snippet)[:40]) + "…"
	}
	return fmt.Sprintf("%s  %s", count, faint.Render(snippet))
}

// notesThreadView renders the full notes thread for when notes is the active tab.
func (m Model) notesThreadView(innerWidth int) string {
	if m.noteEditing {
		action := "New note"
		if !m.noteIsNew {
			action = "Edit note"
		}
		lines := []string{
			lipgloss.NewStyle().Foreground(palette.accent).Render(action),
			m.noteTA.View(),
			"",
			lipgloss.NewStyle().Faint(true).Render("Ctrl+S save · Esc cancel"),
		}
		return strings.Join(lines, "\n")
	}

	if len(m.notes) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("No notes yet.\na add · Esc back")
	}

	previewWidth := innerWidth - 6
	if previewWidth < 20 {
		previewWidth = 20
	}

	var lines []string
	for i, n := range m.notes {
		ts := n.CreatedAt.Local().Format("2006-01-02 15:04")
		if !n.UpdatedAt.Equal(n.CreatedAt) {
			ts += " (edited)"
		}
		var header string
		if i == m.noteIndex {
			header = lipgloss.NewStyle().Foreground(palette.accent).Bold(true).Render(fmt.Sprintf("> [%d] %s", i+1, ts))
		} else {
			header = lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("  [%d] %s", i+1, ts))
		}
		lines = append(lines, header)

		body := strings.TrimSpace(n.Body)
		rendered := ""
		if gr, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(previewWidth)); err == nil {
			if out, err := gr.Render(body); err == nil {
				rendered = strings.TrimSpace(out)
			}
		}
		if rendered == "" {
			rendered = body
		}
		for _, bl := range strings.Split(rendered, "\n") {
			lines = append(lines, "  "+bl)
		}
		lines = append(lines, "")
	}
	lines = append(lines, lipgloss.NewStyle().Faint(true).Render("j/k navigate · a add · e edit · d delete · Tab save & close · Esc back"))
	return strings.Join(lines, "\n")
}

func newNoteTextarea(value string, termWidth int) textarea.Model {
	ta := textarea.New()
	ta.SetValue(value)
	ta.Placeholder = "(write your note here, markdown supported)"
	ta.ShowLineNumbers = false
	popupInner := popupWidth(termWidth) - 4
	if popupInner < 20 {
		popupInner = 20
	}
	ta.SetWidth(popupInner - 4)
	ta.SetHeight(6)
	return ta
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
