package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/attachments"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
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

type bodyClipboardPasteMsg struct {
	ticketID int64
	request  uint64
	image    []byte
	ext      string
	text     string
	err      error
}

func readBodyClipboardCmd(ticketID int64, request uint64) tea.Cmd {
	return func() tea.Msg {
		image, ext, isImage, err := attachments.ReadClipboardImage()
		if err != nil {
			return bodyClipboardPasteMsg{ticketID: ticketID, request: request, err: err}
		}
		if isImage {
			if len(image) == 0 {
				return bodyClipboardPasteMsg{ticketID: ticketID, request: request, err: fmt.Errorf("image clipboard is empty")}
			}
			return bodyClipboardPasteMsg{ticketID: ticketID, request: request, image: image, ext: ext}
		}
		text, err := attachments.ReadClipboardText()
		return bodyClipboardPasteMsg{ticketID: ticketID, request: request, text: text, err: err}
	}
}

func (m Model) applyBodyClipboardPaste(msg bodyClipboardPasteMsg) Model {
	ticket := m.editTicket
	if !m.editing || m.editField != 1 || !m.bodyPastePending || ticket.ID == 0 || ticket.ID != msg.ticketID || msg.request != m.bodyPasteRequest {
		return m
	}
	m.bodyPastePending = false
	if msg.err != nil {
		m.status = "paste failed: " + msg.err.Error()
		return m
	}
	if len(msg.image) > 0 {
		path, ref, err := attachments.SaveImage(ticket.ID, msg.image, msg.ext, time.Now())
		if err != nil {
			m.status = "image paste failed: " + err.Error()
			return m
		}
		m.insertBodyImageReference(ref)
		m.status = "attached " + filepath.Base(path)
		return m
	}
	m.bodyTA.InsertString(msg.text)
	if msg.text == "" {
		m.status = "clipboard is empty"
	} else {
		m.status = "pasted clipboard text"
	}
	return m
}

func (m Model) updateEdit(key tea.KeyMsg) (Model, tea.Cmd) {
	for i := range m.editInputs {
		clean := stripKittyGraphicsResponseFragments(m.editInputs[i].Value())
		if clean != m.editInputs[i].Value() {
			m.editInputs[i].Set(clean)
		}
	}
	if m.editField == 1 && key.Paste {
		m.bodyPasteRequest++
		m.bodyPastePending = false
		m.status = ""
		return m.handleBodyPaste(key), nil
	}
	if m.editField == 1 && key.String() == "ctrl+v" {
		if m.bodyPastePending {
			return m, nil
		}
		ticket := m.editTicket
		if ticket.ID == 0 {
			return m, nil
		}
		m.bodyPasteRequest++
		m.bodyPastePending = true
		m.status = "reading clipboard…"
		return m, readBodyClipboardCmd(ticket.ID, m.bodyPasteRequest)
	}
	if m.bodyPastePending {
		// Any subsequent edit invalidates the pending asynchronous paste so a
		// late clipboard read cannot modify a newer editor state.
		m.bodyPasteRequest++
		m.bodyPastePending = false
		m.status = ""
	}
	// Notes tab (editField == 3) has its own key handling.
	if m.editField == 3 {
		return m.updateNotesTab(key)
	}
	switch key.String() {
	case "esc":
		m.editing = false
		m.bodyTA.Blur()
		return m, clearKittyImagesCmd()
	case "ctrl+s":
		m.saveEdit()
		return m, clearKittyImagesCmd()
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
			return m, clearKittyImagesCmd()
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
			return m, clearKittyImagesCmd()
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

func (m Model) handleBodyPaste(key tea.KeyMsg) Model {
	t := m.editTicket
	if t.ID == 0 {
		return m
	}
	pasted := string(key.Runes)
	path, ref, isImage, err := attachments.SavePastedImage(t.ID, pasted, time.Now())
	if err != nil {
		m.status = "image paste failed: " + err.Error()
		return m
	}
	if !isImage {
		var cmd tea.Cmd
		m.bodyTA, cmd = m.bodyTA.Update(key)
		_ = cmd
		return m
	}
	m.insertBodyImageReference(ref)
	m.status = "attached " + filepath.Base(path)
	return m
}

func (m *Model) insertBodyImageReference(ref string) {
	value := m.bodyTA.Value()
	insert := ref
	if strings.TrimSpace(value) != "" && !strings.HasSuffix(value, "\n") {
		insert = "\n" + insert
	}
	m.bodyTA.InsertString(insert + "\n")

	// Number generated image labels by their final order in the prompt, not by
	// paste time. Preserve meaningful user-authored alt text while counting it.
	targetLine := m.bodyTA.Line()
	targetColumn := m.bodyTA.LineInfo().StartColumn + m.bodyTA.LineInfo().ColumnOffset
	m.bodyTA.SetValue(numberPromptImages(m.bodyTA.Value()))
	for m.bodyTA.Line() > targetLine {
		m.bodyTA.CursorUp()
	}
	m.bodyTA.SetCursor(targetColumn)
}

func numberPromptImages(value string) string {
	matches := markdownImageRE.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var b strings.Builder
	last := 0
	for i, match := range matches {
		b.WriteString(value[last:match[0]])
		image := value[match[0]:match[1]]
		close := strings.Index(image, "](")
		alt := image[2:close]
		if alt == "" || generatedImageAltRE.MatchString(alt) {
			image = fmt.Sprintf("![image %d%s", i+1, image[close:])
		}
		b.WriteString(image)
		last = match[1]
	}
	b.WriteString(value[last:])
	return b.String()
}

func (m *Model) saveEdit() {
	t := m.editTicket
	if t.ID == 0 {
		m.editing = false
		return
	}
	title := stripKittyGraphicsResponseFragments(m.editInputs[0].Value())
	body := numberPromptImages(m.bodyTA.Value())
	harness := stripKittyGraphicsResponseFragments(m.editInputs[2].Value())
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
	boxW := inspectorPopupWidth(termWidth)
	contentW := boxW - 6
	if contentW < 20 {
		contentW = 20
	}
	ta.SetWidth(contentW)
	ta.SetHeight(12)
	return ta
}

func (m Model) editView() string {
	boxW := inspectorPopupWidth(m.width)
	contentW := boxW - 6
	if contentW < 30 {
		contentW = 30
	}
	descriptionLines := inspectorDescriptionLines(m.height)
	t := m.editTicket

	muted := lipgloss.NewStyle().Foreground(palette.muted)
	accent := lipgloss.NewStyle().Foreground(palette.accent)
	chip := lipgloss.NewStyle().Foreground(palette.muted).Bold(true)
	focusChip := lipgloss.NewStyle().Foreground(palette.accent).Bold(true).Underline(true)
	metaText := lipgloss.NewStyle().Foreground(palette.muted)
	dirty := m.editDirty(t)

	var lines []string

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
	status := inspectorStatusLabel(t)
	meta = append(meta, statusChipForTicket(t, status))
	harnessValue := strings.TrimSpace(m.editInputs[2].Value())
	if harnessValue == "" {
		harnessValue = "pi"
	}
	if m.editField == 2 {
		meta = append(meta, focusChip.Render("harness")+" "+m.editInputs[2].Render())
	} else {
		meta = append(meta, chip.Render(harnessValue))
	}
	if !t.UpdatedAt.IsZero() {
		meta = append(meta, metaText.Render("updated "+relativeTime(t.UpdatedAt)))
	}
	if dirty {
		meta = append(meta, lipgloss.NewStyle().Foreground(palette.warning).Bold(true).Render("unsaved"))
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, metaText.Render("  ·  ")))
	}
	if t.WorkspaceID.Valid && strings.TrimSpace(t.WorkspaceBranch.String) != "" {
		for _, line := range inspectorBranchLines(t.WorkspaceBranch.String, contentW) {
			lines = append(lines, metaText.Render(line))
		}
	}

	lines = append(lines, "")
	if m.editField == 1 {
		m.bodyTA.SetWidth(contentW)
		m.bodyTA.SetHeight(descriptionLines)
		lines = append(lines, m.bodyTA.View())
	} else {
		body := strings.TrimSpace(m.bodyTA.Value())
		if body == "" {
			lines = append(lines, muted.Italic(true).Render("No description yet. Start typing to add context."))
		} else {
			lines = append(lines, renderMarkdownForInspector(body, contentW, descriptionLines))
		}
	}

	notesHeading := metaText.Render("Notes")
	if m.editField == 3 {
		notesHeading = accent.Bold(true).Render("▸ Notes")
	}
	lines = append(lines, "", notesHeading)
	if m.editField == 3 {
		lines = append(lines, m.notesThreadView(contentW))
	} else {
		lines = append(lines, m.notesCompactView())
	}

	lines = append(lines, "")
	lines = append(lines, muted.Render(strings.Repeat("─", contentW)))
	lines = append(lines, muted.Render(m.editFooter(dirty)))

	content := strings.Join(lines, "\n")
	titleWidth := boxW - 6
	if titleWidth < 10 {
		titleWidth = 10
	}
	title := ticketInspectorTitle(t.DisplayID, m.editInputs[0], m.editField == 0, dirty, titleWidth)
	return ticketInspectorBox(content, title, boxW)
}

func inspectorBranchLines(branch string, width int) []string {
	const label = "Branch: "
	if width <= len(label) {
		return []string{label + branch}
	}
	runes := []rune(branch)
	firstWidth := width - len(label)
	firstEnd := minInt(firstWidth, len(runes))
	lines := []string{label + string(runes[:firstEnd])}
	for start := firstEnd; start < len(runes); start += width {
		end := minInt(start+width, len(runes))
		lines = append(lines, string(runes[start:end]))
	}
	return lines
}

func inspectorPopupWidth(termWidth int) int {
	w := termWidth * 9 / 10
	if w > 118 {
		w = 118
	}
	if w < 60 {
		w = 60
	}
	if w > termWidth {
		w = termWidth
	}
	return w
}

func inspectorDescriptionLines(termHeight int) int {
	lines := termHeight - 15
	if lines < 10 {
		return 10
	}
	if lines > 24 {
		return 24
	}
	return lines
}

func (m Model) editDirty(t storage.Ticket) bool {
	harness := strings.TrimSpace(m.editInputs[2].Value())
	if harness == "" {
		harness = "pi"
	}
	return m.editInputs[0].Value() != t.Title || m.bodyTA.Value() != t.Body || harness != strings.TrimSpace(t.Harness)
}

func (m Model) editFooter(dirty bool) string {
	prefix := ""
	if dirty {
		prefix = "Unsaved changes · "
	}
	switch m.editField {
	case 0:
		return prefix + "editing title · Tab body · Ctrl+S save · Esc cancel"
	case 1:
		if dirty {
			return "Unsaved · body · Ctrl+V paste/attach · Ctrl+S save"
		}
		return "body · Ctrl+V paste/attach · Ctrl+E editor · Ctrl+S save"
	case 2:
		return prefix + "editing harness · Tab notes · Ctrl+S save · Esc cancel"
	case 3:
		return prefix + "notes · a add · e edit · d delete · Ctrl+S save"
	default:
		return prefix + "Tab/Shift+Tab focus · Ctrl+S save · Esc cancel"
	}
}

func ticketInspectorTitle(displayID string, title InputBuffer, focused bool, dirty bool, maxWidth int) string {
	prefix := ""
	if dirty {
		prefix = "* "
	}
	if strings.TrimSpace(displayID) != "" {
		prefix += strings.TrimSpace(displayID) + " "
	}
	if focused {
		available := maxWidth - lipgloss.Width(prefix)
		if available < 1 {
			available = 1
		}
		return prefix + renderInputWindow(title, available)
	}
	value := strings.TrimSpace(title.Value())
	if value == "" {
		value = "Untitled ticket"
	}
	full := prefix + value
	if lipgloss.Width(full) > maxWidth {
		full = trimToWidth(full, maxWidth)
	}
	return full
}

func renderInputWindow(input InputBuffer, width int) string {
	if width <= 0 {
		return ""
	}
	value := []rune(input.Value())
	cursor := input.Cursor()
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	if len(value) == 0 {
		return cursorStyle.Render(" ")
	}
	if cursor == len(value) {
		textWidth := width - 1
		if textWidth < 0 {
			textWidth = 0
		}
		start := len(value) - textWidth
		if start < 0 {
			start = 0
		}
		return string(value[start:]) + cursorStyle.Render(" ")
	}
	start := 0
	if cursor >= width {
		start = cursor - width + 1
	}
	end := start + width
	if end > len(value) {
		end = len(value)
	}
	var out strings.Builder
	out.WriteString(string(value[start:cursor]))
	out.WriteString(cursorStyle.Render(string(value[cursor : cursor+1])))
	out.WriteString(string(value[cursor+1 : end]))
	return out.String()
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Local().Format("Jan 2")
	}
}

func ticketInspectorBox(content, title string, width int) string {
	if width < 20 {
		width = 20
	}
	border := lipgloss.NewStyle().Foreground(palette.accent)
	innerWidth := width - 2
	bodyWidth := innerWidth - 4
	if bodyWidth < 1 {
		bodyWidth = 1
	}
	title = strings.ReplaceAll(strings.TrimSpace(title), "\n", " ")
	if title != "" && lipgloss.Width(title) > innerWidth-4 {
		title = trimToWidth(title, innerWidth-4)
	}
	titlePart := " " + title + " "
	rightRule := innerWidth - lipgloss.Width(titlePart) - 1
	if rightRule < 0 {
		rightRule = 0
	}
	top := border.Render("╭─" + titlePart + strings.Repeat("─", rightRule) + "╮")
	bottom := border.Render("╰" + strings.Repeat("─", innerWidth) + "╯")

	var lines []string
	lines = append(lines, top)
	lines = append(lines, border.Render("│")+"  "+strings.Repeat(" ", bodyWidth)+"  "+border.Render("│"))
	for _, line := range strings.Split(content, "\n") {
		lines = append(lines, border.Render("│")+"  "+padLine(line, bodyWidth)+"  "+border.Render("│"))
	}
	lines = append(lines, border.Render("│")+"  "+strings.Repeat(" ", bodyWidth)+"  "+border.Render("│"))
	lines = append(lines, bottom)
	return strings.Join(lines, "\n")
}

func inspectorStatusLabel(ticket storage.Ticket) string {
	if ticket.SessionRef.Valid && strings.TrimSpace(ticket.SessionRef.String) != "" && !ticket.SessionActive {
		return "resumable"
	}
	switch ticket.Runtime {
	case "", kanban.StateNotStarted:
		return "not started"
	case kanban.StateNeedsPermission:
		return "needs permission"
	case kanban.StateWaitingForUser:
		return "waiting for user"
	case kanban.StateIdleUnknown:
		return "idle unknown"
	case kanban.StateRepairNeeded:
		return "repair needed"
	default:
		return strings.ReplaceAll(ticket.Runtime, "_", " ")
	}
}

func statusChipForTicket(ticket storage.Ticket, label string) string {
	style := lipgloss.NewStyle().Foreground(palette.muted).Bold(true)
	if label == "resumable" {
		return style.Foreground(palette.success).Render(label)
	}
	switch ticket.Runtime {
	case kanban.StateWaitingForUser:
		style = style.Foreground(palette.warning)
	case kanban.StateNeedsPermission, kanban.StateError, kanban.StateRepairNeeded:
		style = style.Foreground(palette.error_)
	case kanban.StateRunning:
		style = style.Foreground(palette.success)
	}
	return style.Render(label)
}

func renderMarkdownForInspector(body string, width int, maxLines int) string {
	if width < 20 {
		width = 20
	}
	if maxLines <= 0 {
		return ""
	}

	var lines []string
	truncated := false
	for _, raw := range strings.Split(strings.TrimSpace(body), "\n") {
		if len(lines) >= maxLines {
			truncated = true
			break
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			if len(lines) > 0 && strings.TrimSpace(ansiStrip(lines[len(lines)-1])) != "" {
				lines = append(lines, "")
			}
			continue
		}

		if imageLines := renderMarkdownImagesInline(trimmed, width, maxLines-len(lines)); len(imageLines) > 0 {
			for _, line := range imageLines {
				if len(lines) >= maxLines {
					truncated = true
					break
				}
				lines = append(lines, line)
			}
			continue
		}

		text, style := markdownLineStyle(trimmed)
		wrapped := wrapText(text, width, maxLines-len(lines))
		if len(wrapped) == 0 {
			continue
		}
		if len(wrapped) < len(wrapText(text, width, 10_000)) {
			truncated = true
		}
		for _, line := range wrapped {
			lines = append(lines, style.Render(renderInlineMarkdown(line)))
		}
	}
	for len(lines) > 0 && strings.TrimSpace(ansiStrip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if truncated && len(lines) < maxLines+1 {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.muted).Render("…"))
	}
	return strings.Join(lines, "\n")
}

func markdownLineStyle(line string) (string, lipgloss.Style) {
	heading := lipgloss.NewStyle().Bold(true)
	switch {
	case strings.HasPrefix(line, "### "):
		return strings.TrimSpace(strings.TrimPrefix(line, "### ")), heading
	case strings.HasPrefix(line, "## "):
		return strings.TrimSpace(strings.TrimPrefix(line, "## ")), heading
	case strings.HasPrefix(line, "# "):
		return strings.TrimSpace(strings.TrimPrefix(line, "# ")), heading
	case strings.HasPrefix(line, "- "):
		return "• " + strings.TrimSpace(strings.TrimPrefix(line, "- ")), lipgloss.NewStyle()
	case strings.HasPrefix(line, "* "):
		return "• " + strings.TrimSpace(strings.TrimPrefix(line, "* ")), lipgloss.NewStyle()
	case strings.HasPrefix(line, "> "):
		return "│ " + strings.TrimSpace(strings.TrimPrefix(line, "> ")), lipgloss.NewStyle().Foreground(palette.muted)
	default:
		return line, lipgloss.NewStyle()
	}
}

func renderInlineMarkdown(s string) string {
	s = renderDelimitedInline(s, "**", lipgloss.NewStyle().Bold(true))
	s = renderDelimitedInline(s, "__", lipgloss.NewStyle().Bold(true))
	s = renderDelimitedInline(s, "`", lipgloss.NewStyle().Foreground(palette.accent))
	return s
}

func renderDelimitedInline(s string, delim string, style lipgloss.Style) string {
	var out strings.Builder
	for {
		start := strings.Index(s, delim)
		if start < 0 {
			out.WriteString(s)
			return out.String()
		}
		end := strings.Index(s[start+len(delim):], delim)
		if end < 0 {
			out.WriteString(strings.ReplaceAll(s, delim, ""))
			return out.String()
		}
		end += start + len(delim)
		out.WriteString(s[:start])
		out.WriteString(style.Render(s[start+len(delim) : end]))
		s = s[end+len(delim):]
	}
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
			t := m.editTicket
			if t.ID == 0 {
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
	t := m.editTicket
	if t.ID == 0 {
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
		return faint.Render("No notes yet.")
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
