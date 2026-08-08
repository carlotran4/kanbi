package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/storage"
)

func (m *Model) startTemplateCreate() {
	if len(m.view.Columns) == 0 || m.col < 0 || m.col >= len(m.view.Columns) {
		return
	}
	if m.masterBoard {
		m.masterCreateCol = m.view.Columns[m.col].Name
		m.masterCreateKey = m.view.Columns[m.col].WorkflowKey
		if m.masterCreateKey == "" {
			m.masterCreateKey = m.masterCreateCol
		}
		m.reloadBoards()
		m.boardPicker, m.boardPickerMode, m.boardIndex = true, "template-create", 0
		m.status = "choose board for template ticket"
		return
	}
	m.openTemplateModal(m.view.Board, m.view.Columns[m.col].ID, "pick")
}

func (m *Model) startTemplateManagement() {
	if m.masterBoard {
		m.reloadBoards()
		m.boardPicker, m.boardPickerMode, m.boardIndex = true, "template-manage", 0
		m.status = "choose board whose templates to manage"
		return
	}
	columnID := int64(0)
	if len(m.view.Columns) > 0 && m.col >= 0 && m.col < len(m.view.Columns) {
		columnID = m.view.Columns[m.col].ID
	}
	m.openTemplateModal(m.view.Board, columnID, "manage")
}

func (m *Model) openTemplateModal(board storage.Board, columnID int64, mode string) {
	templates, err := m.actions.ListTicketTemplates(m.ctx, board.ID)
	if err != nil {
		m.status = err.Error()
		return
	}
	m.templateOpen, m.templateMode = true, mode
	m.templateBoard, m.templateColumnID, m.templates = board, columnID, templates
	m.templateIndex = 0
	m.templateFilter = NewInputBuffer("")
	m.status = ""
}

func (m Model) filteredTemplates() []storage.TicketTemplate {
	needle := strings.ToLower(strings.TrimSpace(m.templateFilter.Value()))
	if needle == "" {
		return m.templates
	}
	out := make([]storage.TicketTemplate, 0, len(m.templates))
	for _, tmpl := range m.templates {
		if strings.Contains(strings.ToLower(tmpl.Name+" "+tmpl.Title+" "+tmpl.Harness), needle) {
			out = append(out, tmpl)
		}
	}
	return out
}

func (m *Model) refreshTemplates() bool {
	templates, err := m.actions.ListTicketTemplates(m.ctx, m.templateBoard.ID)
	if err != nil {
		m.status = err.Error()
		return false
	}
	m.templates = templates
	if m.templateIndex >= len(m.templates) {
		m.templateIndex = maxInt(0, len(m.templates)-1)
	}
	return true
}

func (m *Model) startTemplateEditor(tmpl storage.TicketTemplate, create bool) {
	m.templateMode, m.templateEditNew, m.templateSelected, m.templateEditField = "edit", create, tmpl, 0
	m.templateName = NewInputBuffer(tmpl.Name)
	m.templateSeedTitle = NewInputBuffer(tmpl.Title)
	m.templateHarness = NewInputBuffer(tmpl.Harness)
	if create && strings.TrimSpace(tmpl.Harness) == "" {
		m.templateHarness = NewInputBuffer("pi")
	}
	m.templateBodyTA = newBodyTextarea(tmpl.Body, m.width)
	m.templateBodyTA.Placeholder = "(empty)"
	m.templateBodyTA.SetWidth(maxInt(20, modalContentWidth(focusModalWidth(m.width))-4))
	m.templateBodyTA.SetHeight(5)
	m.templateBodyTA.Blur()
	m.status = ""
}

func (m *Model) createFromSelectedTemplate() {
	title := m.templateTitle.Value()
	ticket, err := m.actions.CreateTicketFromTemplate(m.ctx, m.templateColumnID, m.templateSelected.ID, storage.TemplateTicketOverrides{Title: &title})
	if err != nil {
		m.status = err.Error()
		return
	}
	m.templateOpen = false
	m.status = "created " + ticket.DisplayID + " from " + m.templateSelected.Name
	m.reload()
	for ci, col := range m.view.Columns {
		for ti, candidate := range col.Tickets {
			if candidate.ID == ticket.ID {
				m.col, m.card = ci, ti
			}
		}
	}
	// Bind the editor to the returned ticket even when an active Master filter
	// excludes it from the reloaded projection.
	m.startEditTicket(ticket)
}

func (m Model) updateTemplates(key tea.KeyMsg) (Model, tea.Cmd) {
	switch m.templateMode {
	case "pick":
		items := m.filteredTemplates()
		if m.templateIndex >= len(items) {
			m.templateIndex = maxInt(0, len(items)-1)
		}
		switch key.String() {
		case "esc":
			m.templateOpen = false
		case "T":
			m.templateMode = "manage"
			m.templateFilter = NewInputBuffer("")
			m.templateIndex = 0
			m.status = ""
		case "j", "down":
			if len(items) > 0 {
				m.templateIndex = (m.templateIndex + 1) % len(items)
			}
		case "k", "up":
			if len(items) > 0 {
				m.templateIndex--
				if m.templateIndex < 0 {
					m.templateIndex = len(items) - 1
				}
			}
		case "enter":
			if len(items) > 0 {
				m.templateSelected = items[m.templateIndex]
				m.templateTitle = NewInputBuffer(m.templateSelected.Title)
				m.templateTitleReturn = "pick"
				m.templateMode = "title"
				m.status = ""
			}
		default:
			if m.templateFilter.HandleKey(key.String(), key.Runes) {
				m.templateIndex = 0
			}
		}
	case "title":
		switch key.String() {
		case "esc":
			m.templateMode = m.templateTitleReturn
			if m.templateMode == "" {
				m.templateMode = "pick"
			}
		case "enter":
			m.createFromSelectedTemplate()
		default:
			m.templateTitle.HandleKey(key.String(), key.Runes)
		}
	case "manage":
		if m.templateIndex >= len(m.templates) {
			m.templateIndex = maxInt(0, len(m.templates)-1)
		}
		switch key.String() {
		case "esc", "T":
			m.templateOpen = false
		case "j", "down":
			if len(m.templates) > 0 {
				m.templateIndex = (m.templateIndex + 1) % len(m.templates)
			}
		case "k", "up":
			if len(m.templates) > 0 {
				m.templateIndex--
				if m.templateIndex < 0 {
					m.templateIndex = len(m.templates) - 1
				}
			}
		case "c":
			m.startTemplateEditor(storage.TicketTemplate{}, true)
		case "e":
			if len(m.templates) > 0 {
				m.startTemplateEditor(m.templates[m.templateIndex], false)
			}
		case "d":
			if len(m.templates) > 0 {
				m.templateSelected = m.templates[m.templateIndex]
				m.templateMode = "delete"
				m.status = ""
			}
		case "enter":
			if len(m.templates) > 0 && m.templateColumnID != 0 {
				m.templateSelected = m.templates[m.templateIndex]
				m.templateTitle = NewInputBuffer(m.templateSelected.Title)
				m.templateTitleReturn = "manage"
				m.templateMode = "title"
				m.status = ""
			}
		}
	case "delete":
		switch key.String() {
		case "esc", "n":
			m.templateMode = "manage"
			m.status = ""
		case "enter", "y":
			if err := m.actions.DeleteTicketTemplate(m.ctx, m.templateSelected.ID); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.refreshTemplates()
			m.templateMode = "manage"
			m.status = "deleted " + m.templateSelected.Name + "; existing tickets unchanged"
		}
	case "edit":
		switch key.String() {
		case "esc":
			m.templateBodyTA.Blur()
			m.templateMode = "manage"
			m.status = ""
		case "ctrl+s":
			name, title, body, harnessName := m.templateName.Value(), m.templateSeedTitle.Value(), m.templateBodyTA.Value(), m.templateHarness.Value()
			var err error
			if m.templateEditNew {
				_, err = m.actions.CreateTicketTemplate(m.ctx, m.templateBoard.ID, name, title, body, harnessName)
			} else {
				err = m.actions.UpdateTicketTemplate(m.ctx, m.templateSelected.ID, name, title, body, harnessName)
			}
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.templateBodyTA.Blur()
			m.refreshTemplates()
			m.templateMode = "manage"
			m.status = "saved template"
		case "tab":
			if m.templateEditField == 2 {
				m.templateBodyTA.Blur()
			}
			m.templateEditField = (m.templateEditField + 1) % 4
			if m.templateEditField == 2 {
				return m, m.templateBodyTA.Focus()
			}
		case "shift+tab":
			if m.templateEditField == 2 {
				m.templateBodyTA.Blur()
			}
			m.templateEditField--
			if m.templateEditField < 0 {
				m.templateEditField = 3
			}
			if m.templateEditField == 2 {
				return m, m.templateBodyTA.Focus()
			}
		default:
			if m.templateEditField == 2 {
				var cmd tea.Cmd
				m.templateBodyTA, cmd = m.templateBodyTA.Update(key)
				return m, cmd
			}
			switch m.templateEditField {
			case 0:
				m.templateName.HandleKey(key.String(), key.Runes)
			case 1:
				m.templateSeedTitle.HandleKey(key.String(), key.Runes)
			case 3:
				m.templateHarness.HandleKey(key.String(), key.Runes)
			}
		}
	}
	return m, nil
}

func (m Model) templatesView() string {
	width := focusModalWidth(m.width)
	contentWidth := modalContentWidth(width)
	header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent)
	var lines []string
	switch m.templateMode {
	case "pick":
		lines = append(lines, header.Render("New ticket from template · "+m.templateBoard.Name), "Search: "+m.templateFilter.Viewport(maxInt(1, contentWidth-8)), "")
		items := m.filteredTemplates()
		if len(items) == 0 {
			lines = append(lines, "No matching templates.", "Press T to manage templates, or Esc to cancel.")
		} else {
			// Reserve rows for header/search, preview, controls, and modal borders so
			// Enter/Esc never scroll out of view at 80x24.
			start, end := templateWindow(len(items), m.templateIndex, maxInt(1, m.height-19))
			if start > 0 {
				lines = append(lines, fmt.Sprintf("  ↑ %d more", start))
			}
			for i := start; i < end; i++ {
				tmpl := items[i]
				marker := "  "
				if i == m.templateIndex {
					marker = "> "
				}
				lines = append(lines, templatePickerRow(marker, tmpl, contentWidth))
			}
			if end < len(items) {
				lines = append(lines, fmt.Sprintf("  ↓ %d more", len(items)-end))
			}
			tmpl := items[minInt(m.templateIndex, len(items)-1)]
			lines = append(lines, "", "Preview: "+emptyDefaultUI(tmpl.Title, tmpl.Name))
			preview := strings.TrimSpace(tmpl.Body)
			if preview != "" {
				previewLines := wrapText(preview, contentWidth, 5)
				if len(previewLines) > 4 {
					previewLines = previewLines[:4]
					previewLines[3] = trimToWidth(previewLines[3], maxInt(1, contentWidth-1)) + "…"
				}
				lines = append(lines, previewLines...)
			}
		}
		lines = append(lines, "", "Type to filter   ↑/↓ select   Enter continue   Esc cancel")
	case "title":
		lines = append(lines, header.Render("Ticket title · "+m.templateSelected.Name), "", m.templateTitle.Viewport(contentWidth), "", "Enter create   Esc back")
	case "manage":
		lines = append(lines, header.Render("Ticket templates · "+m.templateBoard.Name), "")
		if len(m.templates) == 0 {
			lines = append(lines, "No templates for this board.")
		}
		listRows := m.height - 15
		if m.status != "" {
			listRows = m.height - 17
		}
		start, end := templateWindow(len(m.templates), m.templateIndex, maxInt(1, listRows))
		if start > 0 {
			lines = append(lines, fmt.Sprintf("  ↑ %d more", start))
		}
		for i := start; i < end; i++ {
			tmpl := m.templates[i]
			marker := "  "
			if i == m.templateIndex {
				marker = "> "
			}
			lines = append(lines, templateManagerRow(marker, tmpl, contentWidth))
		}
		if end < len(m.templates) {
			lines = append(lines, fmt.Sprintf("  ↓ %d more", len(m.templates)-end))
		}
		controls := "c create   e edit   d delete   Esc close"
		if m.templateColumnID != 0 {
			controls = "Enter use   " + controls
		}
		lines = append(lines, "", controls)
	case "delete":
		lines = append(lines, header.Render("Delete template?"), "", m.templateSelected.Name, "Existing tickets will not change.", "", "Enter/y delete   Esc cancel")
	case "edit":
		marker := func(i int) string {
			if i == m.templateEditField {
				return "> "
			}
			return "  "
		}
		name, title, harnessName := m.templateName.Value(), m.templateSeedTitle.Value(), m.templateHarness.Value()
		if m.templateEditField == 0 {
			name = m.templateName.Viewport(maxInt(1, contentWidth-8))
		}
		if m.templateEditField == 1 {
			title = m.templateSeedTitle.Viewport(maxInt(1, contentWidth-9))
		}
		if m.templateEditField == 3 {
			harnessName = m.templateHarness.Viewport(maxInt(1, contentWidth-11))
		}
		lines = append(lines, header.Render("Edit ticket template · "+m.templateBoard.Name), marker(0)+"Name: "+name, marker(1)+"Title: "+title, marker(2)+"Body:")
		bodyLines := strings.Split(m.templateBodyTA.View(), "\n")
		if len(bodyLines) > 6 {
			bodyLines = bodyLines[:6]
		}
		for _, line := range bodyLines {
			lines = append(lines, trimToWidth(line, contentWidth))
		}
		lines = append(lines, marker(3)+"Harness: "+harnessName, "", "Tab/Shift+Tab fields   Ctrl+S save   Esc cancel")
	}
	if m.status != "" {
		lines = append(lines, "", trimToWidth("Status: "+m.status, contentWidth))
	}
	return modalFrame(lines, width, palette.accent)
}

func templateWindow(total, selected, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit < 1 {
		limit = 1
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - limit/2
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > total {
		end = total
		start = maxInt(0, end-limit)
	}
	return start, end
}

func templatePickerRow(marker string, tmpl storage.TicketTemplate, width int) string {
	suffix := "[" + tmpl.Harness + "]"
	leftWidth := maxInt(1, width-lipgloss.Width(suffix)-1)
	left := trimToWidth(marker+tmpl.Name, leftWidth)
	return spaceBetween(left, suffix, width)
}

func templateManagerRow(marker string, tmpl storage.TicketTemplate, width int) string {
	suffix := "[" + tmpl.Harness + "]"
	leftWidth := maxInt(1, width-lipgloss.Width(suffix)-1)
	left := marker + tmpl.Name + "  " + emptyDefaultUI(tmpl.Title, "(title prompted)")
	left = trimToWidth(left, leftWidth)
	return spaceBetween(left, suffix, width)
}

func emptyDefaultUI(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
