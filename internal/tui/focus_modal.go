package tui

import (
	"fmt"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func focusTextarea(value string, width, height int) textarea.Model {
	ta := textarea.New()
	ta.SetValue(value)
	ta.SetWidth(maxInt(20, width-12))
	ta.SetHeight(maxInt(2, height))
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	return ta
}

func (m *Model) startPause(ticket storage.Ticket) {
	m.pauseOpen = true
	m.pauseTicket = ticket
	m.pauseField = 0
	m.pauseWarnNoRef = ticket.SessionActive && (!ticket.SessionRef.Valid || strings.TrimSpace(ticket.SessionRef.String) == "")
	for i := range m.pauseInputs {
		m.pauseInputs[i] = focusTextarea("", m.width, 3)
	}
	m.pauseInputs[0].Focus()
}

func (m *Model) updatePause(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.pauseOpen = false
		m.clearFocusReplacement()
		return m, nil
	case "tab", "down":
		m.pauseInputs[m.pauseField].Blur()
		m.pauseField = (m.pauseField + 1) % 3
		return m, m.pauseInputs[m.pauseField].Focus()
	case "shift+tab", "up":
		m.pauseInputs[m.pauseField].Blur()
		m.pauseField = (m.pauseField + 2) % 3
		return m, m.pauseInputs[m.pauseField].Focus()
	case "ctrl+s":
		values := [3]string{m.pauseInputs[0].Value(), m.pauseInputs[1].Value(), m.pauseInputs[2].Value()}
		labels := []string{"Why it was paused", "What has already been completed", "Exact next action"}
		for i, value := range values {
			if strings.TrimSpace(value) == "" {
				m.status = labels[i] + " is required"
				m.pauseField = i
				return m, nil
			}
		}
		if m.pauseWarnNoRef {
			m.pauseWarnNoRef = false
			m.status = "No verified resume reference: Ctrl+S again confirms close may require repair/start-fresh"
			return m, nil
		}
		replacement := m.pauseTicket
		moveTarget, moveColumn := m.focusReplaceMoveTicket, m.focusReplaceMoveColumn
		resumeTarget, resumeSend := m.focusReplaceResumeTicket, m.focusReplaceResumeSend
		return m, func() tea.Msg {
			switch {
			case moveTarget.ID != 0:
				return pauseTicketMsg{displayID: replacement.DisplayID, err: m.actions.PauseAndMove(m.ctx, replacement.ID, moveTarget.ID, moveColumn, values[0], values[1], values[2])}
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
	var cmd tea.Cmd
	m.pauseInputs[m.pauseField], cmd = m.pauseInputs[m.pauseField].Update(key)
	return m, cmd
}

func (m *Model) startResume(ticket storage.Ticket) {
	m.resumeOpen = true
	m.resumeTicket = ticket
	m.resumeSending = false
	m.modalScroll = 0
}
func (m *Model) updateResume(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.resumeOpen = false
		return m, nil
	case "j", "down":
		m.modalScroll++
		return m, nil
	case "k", "up":
		if m.modalScroll > 0 {
			m.modalScroll--
		}
		return m, nil
	case "r", "s":
		m.resumeSending = key.String() == "s"
		ticket := m.resumeTicket
		send := m.resumeSending
		return m, func() tea.Msg {
			return resumeTicketMsg{ticket: ticket, displayID: ticket.DisplayID, sendHandoff: send, err: m.actions.ResumePausedTicket(m.ctx, ticket.ID, send)}
		}
	}
	return m, nil
}

func (m Model) pauseView() string {
	lines := []string{"Pause " + m.pauseTicket.DisplayID}
	labels := []string{"Why is this being paused?", "What has already been completed?", "Exact next action"}
	for i, label := range labels {
		lines = append(lines, label, m.pauseInputs[i].View())
	}
	if m.pauseWarnNoRef {
		lines = append(lines, "WARNING: no verified resume reference; closing may require repair/start-fresh.")
	}
	if m.status != "" {
		lines = append(lines, "Status: "+m.status)
	}
	lines = append(lines, "Ctrl+S pause   Esc cancel")
	return boxLines(strings.Split(strings.Join(lines, "\n"), "\n"), maxInt(30, minInt(m.width-2, 76)))
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
}

func (m *Model) clearFocusReplacement() {
	m.focusReplaceOpen = false
	m.focusReplaceTickets = nil
	m.focusReplaceIndex = 0
	m.focusReplaceMoveTicket = storage.Ticket{}
	m.focusReplaceMoveColumn = 0
	m.focusReplaceResumeTicket = storage.Ticket{}
	m.focusReplaceResumeSend = false
}

func (m *Model) updateFocusReplace(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.clearFocusReplacement()
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
	lines := []string{"Focus limit reached — choose work to pause", "Enter opens the required handoff form; Esc cancels"}
	for i, t := range m.focusReplaceTickets {
		prefix := " "
		if i == m.focusReplaceIndex {
			prefix = ">"
		}
		lines = append(lines, fmt.Sprintf("%s %s [%s] %s", prefix, t.DisplayID, t.BoardName, t.Title))
	}
	return boxLines(lines, maxInt(30, minInt(m.width-2, 76)))
}

func (m Model) resumeView() string {
	checkpoint := m.resumeTicket.LatestCheckpoint
	width := maxInt(30, minInt(m.width-2, 76))
	if checkpoint == nil {
		return boxLines([]string{"Resume unavailable: no pause checkpoint"}, width)
	}
	contentWidth := maxInt(20, width-4)
	body := []string{"Description"}
	appendWrapped := func(prefix, value string) {
		value = strings.TrimSpace(value)
		wrapped := wrapText(prefix+value, contentWidth, 1000)
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

	bodyRows := maxInt(4, m.height-6)
	maxScroll := maxInt(0, len(body)-bodyRows)
	scroll := minInt(maxInt(0, m.modalScroll), maxScroll)
	end := minInt(len(body), scroll+bodyRows)
	visibleBody := body[scroll:end]
	if scroll > 0 && len(visibleBody) > 0 {
		visibleBody[0] = "↑ " + trimToWidth(visibleBody[0], maxInt(1, contentWidth-2))
	}
	if end < len(body) && len(visibleBody) > 0 {
		last := len(visibleBody) - 1
		visibleBody[last] = trimToWidth(visibleBody[last], maxInt(1, contentWidth-2)) + " ↓"
	}

	title := trimToWidth(fmt.Sprintf("Resume %s: %s", m.resumeTicket.DisplayID, m.resumeTicket.Title), contentWidth)
	lines := append([]string{title}, visibleBody...)
	lines = append(lines, "r resume   s resume + send handoff   Esc cancel")
	return boxLines(lines, width)
}
