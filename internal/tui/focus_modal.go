package tui

import (
	"fmt"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func focusModalWidth(termWidth int) int {
	// Keep a visible margin at 80 columns while retaining a sensible compact
	// layout on smaller terminals.
	return minInt(76, maxInt(2, termWidth-4))
}

func focusTextarea(value string, width, height int) textarea.Model {
	ta := textarea.New()
	ta.SetValue(value)
	ta.SetWidth(maxInt(1, modalContentWidth(width)))
	ta.SetHeight(maxInt(2, height))
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	return ta
}

func (m *Model) resizePauseInputs() {
	if !m.pauseOpen {
		return
	}
	width := focusModalWidth(m.width)
	for i := range m.pauseInputs {
		m.pauseInputs[i].SetWidth(maxInt(1, modalContentWidth(width)))
	}
}

func (m *Model) startPause(ticket storage.Ticket) {
	m.pauseOpen = true
	m.pauseSubmitting = false
	m.pauseTicket = ticket
	m.pauseField = 0
	m.status = ""
	m.errOperation = ""
	m.errNext = ""
	m.pauseWarnNoRef = ticket.SessionActive && (!ticket.SessionRef.Valid || strings.TrimSpace(ticket.SessionRef.String) == "")
	for i := range m.pauseInputs {
		m.pauseInputs[i] = focusTextarea("", focusModalWidth(m.width), 3)
	}
	m.pauseInputs[0].Focus()
}

func (m *Model) updatePause(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pauseSubmitting {
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.pauseOpen = false
		m.status = ""
		m.errOperation = ""
		m.errNext = ""
		m.clearFocusReplacement()
		return m, nil
	case "tab", "down":
		m.status = ""
		m.pauseInputs[m.pauseField].Blur()
		m.pauseField = (m.pauseField + 1) % 3
		return m, m.pauseInputs[m.pauseField].Focus()
	case "shift+tab", "up":
		m.status = ""
		m.pauseInputs[m.pauseField].Blur()
		m.pauseField = (m.pauseField + 2) % 3
		return m, m.pauseInputs[m.pauseField].Focus()
	case "ctrl+s":
		values := [3]string{m.pauseInputs[0].Value(), m.pauseInputs[1].Value(), m.pauseInputs[2].Value()}
		labels := []string{"Why it was paused", "What has already been completed", "Exact next action"}
		for i, value := range values {
			if strings.TrimSpace(value) == "" {
				m.status = labels[i] + " is required"
				m.pauseInputs[m.pauseField].Blur()
				m.pauseField = i
				return m, m.pauseInputs[i].Focus()
			}
		}
		if m.pauseWarnNoRef {
			m.pauseWarnNoRef = false
			m.status = "No verified resume reference: Ctrl+S again confirms close may require repair/start-fresh"
			return m, nil
		}
		m.pauseSubmitting = true
		m.status = "Pausing…"
		replacement := m.pauseTicket
		moveTarget, moveColumn := m.focusReplaceMoveTicket, m.focusReplaceMoveColumn
		resumeTarget, resumeSend := m.focusReplaceResumeTicket, m.focusReplaceResumeSend
		resolveOnly := m.focusReplaceResolveOnly
		return m, func() tea.Msg {
			switch {
			case moveTarget.ID != 0 && resolveOnly:
				if err := m.actions.PauseTicket(m.ctx, replacement.ID, values[0], values[1], values[2]); err != nil {
					return pauseTicketMsg{displayID: replacement.DisplayID, err: err}
				}
				return focusMoveTicketMsg{ticket: moveTarget, columnID: moveColumn, err: m.actions.MoveTicket(m.ctx, moveTarget.ID, moveColumn)}
			case moveTarget.ID != 0:
				return pauseTicketMsg{displayID: replacement.DisplayID, err: m.actions.PauseAndMove(m.ctx, replacement.ID, moveTarget.ID, moveColumn, values[0], values[1], values[2])}
			case resumeTarget.ID != 0 && resolveOnly:
				if err := m.actions.PauseTicket(m.ctx, replacement.ID, values[0], values[1], values[2]); err != nil {
					return pauseTicketMsg{displayID: replacement.DisplayID, err: err}
				}
				return resumeTicketMsg{
					ticket: resumeTarget, displayID: resumeTarget.DisplayID, sendHandoff: resumeSend,
					err: m.actions.ResumePausedTicket(m.ctx, resumeTarget.ID, resumeSend),
				}
			case resumeTarget.ID != 0:
				return resumeTicketMsg{
					ticket: resumeTarget, displayID: resumeTarget.DisplayID, sendHandoff: resumeSend,
					err: m.actions.PauseAndResume(m.ctx, replacement.ID, resumeTarget.ID, values[0], values[1], values[2], resumeSend),
				}
			default:
				return pauseTicketMsg{displayID: replacement.DisplayID, err: m.actions.PauseTicket(m.ctx, replacement.ID, values[0], values[1], values[2])}
			}
		}
	}
	m.status = ""
	m.errOperation = ""
	m.errNext = ""
	var cmd tea.Cmd
	m.pauseInputs[m.pauseField], cmd = m.pauseInputs[m.pauseField].Update(key)
	return m, cmd
}

func (m *Model) startResume(ticket storage.Ticket) {
	m.resumeOpen = true
	m.resumeTicket = ticket
	m.resumeSending = false
	m.resumeSubmitting = false
	m.modalScroll = 0
}
func (m *Model) updateResume(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.resumeSubmitting {
		return m, nil
	}
	m.clampResumeScroll()
	switch key.String() {
	case "esc":
		m.resumeOpen = false
		return m, nil
	case "j", "down":
		if limit := m.resumeScrollLimit(); m.modalScroll < limit {
			m.modalScroll++
		}
		return m, nil
	case "k", "up":
		if m.modalScroll > 0 {
			m.modalScroll--
		}
		return m, nil
	case "r", "s":
		m.resumeSending = key.String() == "s"
		m.resumeSubmitting = true
		m.status = "Resuming…"
		ticket := m.resumeTicket
		send := m.resumeSending
		return m, func() tea.Msg {
			return resumeTicketMsg{ticket: ticket, displayID: ticket.DisplayID, sendHandoff: send, err: m.actions.ResumePausedTicket(m.ctx, ticket.ID, send)}
		}
	}
	return m, nil
}

func (m Model) pauseView() string {
	width := focusModalWidth(m.width)
	contentWidth := modalContentWidth(width)
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render("Pause " + m.pauseTicket.DisplayID)}
	labels := []string{"Why is this being paused?", "What has already been completed?", "Exact next action"}
	for i, label := range labels {
		prefix := "  "
		if i == m.pauseField {
			prefix = "> "
		}
		lines = append(lines, prefix+label)
		for _, line := range strings.Split(textareaOverlayView(m.pauseInputs[i]), "\n") {
			lines = append(lines, trimToWidth(line, contentWidth))
		}
	}
	if m.pauseWarnNoRef {
		lines = append(lines, wrapText("WARNING: no verified resume reference; closing may require repair/start-fresh.", contentWidth, 3)...)
	}
	if m.status != "" {
		lines = append(lines, wrapText("Status: "+m.status, contentWidth, 3)...)
	}
	controls := "Ctrl+S pause   Esc cancel"
	if m.pauseSubmitting {
		controls = "Pausing…"
	}
	lines = append(lines, controls)
	return modalFrame(lines, width, palette.warning)
}
func (m *Model) startFocusReplacement(moveTicket storage.Ticket, columnID int64, resumeTicket storage.Ticket, send bool) {
	candidates, err := m.actions.FocusedTickets(m.ctx)
	if err != nil || len(candidates) == 0 {
		m.status = "focus limit reached; no focused ticket is available to pause"
		return
	}
	m.focusReplaceOpen = true
	m.focusReplaceTickets = candidates
	m.focusReplaceIndex = 0
	m.focusReplaceMoveTicket = moveTicket
	m.focusReplaceMoveColumn = columnID
	m.focusReplaceResumeTicket = resumeTicket
	m.focusReplaceResumeSend = send
	m.focusReplaceResolveOnly = m.focus.OverCapacity && (moveTicket.ID != 0 || resumeTicket.ID != 0)
	if m.focusReplaceResolveOnly {
		needed := m.focus.Used - m.focus.Limit + 1
		m.status = fmt.Sprintf("Overflow resolution: pause %d focused ticket(s) before admission can continue", needed)
	} else {
		m.status = "Choose focused work to pause or Esc to cancel"
	}
}

func (m *Model) clearFocusReplacement() {
	m.focusReplaceOpen = false
	m.focusReplaceTickets = nil
	m.focusReplaceIndex = 0
	m.focusReplaceMoveTicket = storage.Ticket{}
	m.focusReplaceMoveColumn = 0
	m.focusReplaceResumeTicket = storage.Ticket{}
	m.focusReplaceResumeSend = false
	m.focusReplaceResolveOnly = false
}

func (m *Model) updateFocusReplace(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.clearFocusReplacement()
		m.status = ""
		return m, nil
	case "j", "down":
		if m.focusReplaceIndex < len(m.focusReplaceTickets)-1 {
			m.focusReplaceIndex++
		}
		return m, nil
	case "k", "up":
		if m.focusReplaceIndex > 0 {
			m.focusReplaceIndex--
		}
		return m, nil
	case "enter":
		m.focusReplaceOpen = false
		m.startPause(m.focusReplaceTickets[m.focusReplaceIndex])
		return m, nil
	}
	return m, nil
}

func (m Model) focusReplaceView() string {
	width := focusModalWidth(m.width)
	contentWidth := modalContentWidth(width)
	title := "Focus limit reached — choose work to pause"
	if m.focusReplaceResolveOnly {
		title = fmt.Sprintf("Focus overflow %d/%d — pause work before admission", m.focus.Used, m.focus.Limit)
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render(trimToWidth(title, contentWidth)), "Enter opens the required handoff form; Esc cancels"}
	for i, t := range m.focusReplaceTickets {
		prefix := "  "
		if i == m.focusReplaceIndex {
			prefix = "> "
		}
		for j, line := range wrapText(fmt.Sprintf("%s%s [%s] %s", prefix, t.DisplayID, t.BoardName, t.Title), contentWidth, 3) {
			if j > 0 {
				line = "  " + line
			}
			lines = append(lines, line)
		}
	}
	return modalFrame(lines, width, palette.warning)
}
func (m *Model) clampResumeScroll() {
	if !m.resumeOpen {
		return
	}
	m.modalScroll = minInt(maxInt(0, m.modalScroll), m.resumeScrollLimit())
}

func (m Model) resumeScrollLimit() int {
	return maxInt(0, len(m.resumeBody())-m.resumeBodyRows())
}

func (m Model) resumeBodyRows() int {
	// Reserve frame, padding, fixed title, and actions before selecting body.
	return maxInt(1, m.height-6-2)
}

func (m Model) resumeBody() []string {
	checkpoint := m.resumeTicket.LatestCheckpoint
	if checkpoint == nil {
		return nil
	}
	contentWidth := modalContentWidth(focusModalWidth(m.width))
	body := []string{"Description"}
	appendWrapped := func(prefix, value string) {
		wrapped := wrapText(prefix+strings.TrimSpace(value), contentWidth, 1000)
		if len(wrapped) == 0 {
			wrapped = []string{prefix}
		}
		body = append(body, wrapped...)
	}
	appendWrapped("", m.resumeTicket.Body)
	body = append(body, "", "Paused "+checkpoint.PausedAt.Local().Format("Jan 2 15:04"))
	appendWrapped("Why: ", checkpoint.Why)
	appendWrapped("Completed: ", checkpoint.Completed)
	appendWrapped("Next: ", checkpoint.NextAction)
	return body
}

func (m Model) resumeView() string {
	checkpoint := m.resumeTicket.LatestCheckpoint
	width := focusModalWidth(m.width)
	contentWidth := modalContentWidth(width)
	if checkpoint == nil {
		return modalFrame([]string{"Resume unavailable: no pause checkpoint"}, width, palette.warning)
	}
	body := m.resumeBody()
	title := trimToWidth(fmt.Sprintf("Resume %s: %s", m.resumeTicket.DisplayID, m.resumeTicket.Title), contentWidth)
	controls := "r resume   s resume + send handoff   Esc cancel"
	maxScroll := m.resumeScrollLimit()
	scroll := minInt(maxInt(0, m.modalScroll), maxScroll)
	end := minInt(len(body), scroll+m.resumeBodyRows())
	visible := append([]string(nil), body[scroll:end]...)
	if scroll > 0 && len(visible) > 0 {
		visible[0] = "↑ " + trimToWidth(visible[0], maxInt(1, contentWidth-2))
	}
	if end < len(body) && len(visible) > 0 {
		last := len(visible) - 1
		visible[last] = trimToWidth(visible[last], maxInt(1, contentWidth-2)) + " ↓"
	}
	return modalFrame(append([]string{lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render(title)}, append(visible, controls)...), width, palette.warning)
}
