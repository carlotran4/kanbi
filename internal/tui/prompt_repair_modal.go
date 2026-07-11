package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kanbi/internal/session"
	"kanbi/internal/storage"
)

func (m Model) updatePromptFallback(key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "p":
		if err := m.actions.PastePromptNow(m.ctx, m.promptWindow, m.promptText); err != nil {
			m.status = err.Error()
		} else {
			m.status = "pasted prompt " + m.promptTicket.DisplayID
		}
		m.promptFallback = false
		m.reload()
	case "c", "esc":
		m.status = "cancelled prompt send"
		m.promptFallback = false
	}
	return m, nil
}

func isRepairError(err error) bool {
	var repair session.RepairNeededError
	var resume session.ResumeFailedError
	return errors.As(err, &repair) || errors.As(err, &resume)
}

func (m *Model) startRepair(ticket storage.Ticket, err error) {
	m.repairing = true
	m.repairEditingRef = false
	m.repairTicket = ticket
	m.repairRef = ""
	m.repairReason = err.Error()
	m.status = err.Error()
}

func (m Model) updateRepair(key tea.KeyMsg) Model {
	if m.repairEditingRef {
		switch key.String() {
		case "esc":
			m.repairEditingRef = false
		case "enter":
			if err := m.actions.UpdateSessionRef(m.ctx, m.repairTicket, strings.TrimSpace(m.repairRef)); err != nil {
				m.status = err.Error()
				return m
			}
			updated := m.repairTicket
			updated.SessionRef.Valid = strings.TrimSpace(m.repairRef) != ""
			updated.SessionRef.String = strings.TrimSpace(m.repairRef)
			if updated.SessionRef.Valid {
				if err := m.actions.OpenTicket(m.ctx, updated, false); err != nil {
					m.status = err.Error()
					return m
				}
			}
			m.repairing = false
			m.status = "updated session ref " + m.repairTicket.DisplayID
			m.reload()
		case "backspace":
			m.repairRef = popRune(m.repairRef)
		default:
			if len(key.Runes) > 0 {
				m.repairRef += string(key.Runes)
			}
		}
		return m
	}
	switch key.String() {
	case "esc", "c":
		m.repairing = false
		m.status = "cancelled repair"
	case "e":
		m.repairEditingRef = true
		m.repairRef = ""
	case "f":
		if err := m.actions.StartFreshTicket(m.ctx, m.repairTicket, true); err != nil {
			m.status = err.Error()
			return m
		}
		m.repairing = false
		m.status = "started fresh " + m.repairTicket.DisplayID
		m.reload()
	case "r":
		if err := m.actions.OpenTicket(m.ctx, m.repairTicket, false); err != nil {
			m.status = err.Error()
			return m
		}
		m.repairing = false
		m.status = "retried " + m.repairTicket.DisplayID
		m.reload()
	}
	return m
}

func (m Model) promptFallbackView() string {
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render("Prompt readiness not detected"),
		"",
		lipgloss.NewStyle().Faint(true).Render("Ticket: ") + m.promptTicket.DisplayID,
		"",
		"p  paste now",
		"c  cancel",
	}
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.warning).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}

func (m Model) repairView() string {
	popupW := popupWidth(m.width)
	if m.repairEditingRef {
		content := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Edit session ref for "+m.repairTicket.DisplayID) +
			"\n\n" +
			fmt.Sprintf("%s ref: %s", lipgloss.NewStyle().Foreground(palette.accent).Render(">"), renderWithCursor(m.repairRef, len([]rune(m.repairRef)))) +
			"\n\n" + lipgloss.NewStyle().Faint(true).Render("Enter save · Esc back")
		return lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.accent).
			Padding(1, 2).
			Width(popupW - 4).
			Render(content)
	}
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render("Session repair needed: "+m.repairTicket.DisplayID))
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render(m.repairReason), "")
	lines = append(lines, "r  retry")
	lines = append(lines, "e  edit session ref")
	lines = append(lines, "f  start fresh")
	lines = append(lines, "c  cancel")
	if m.repairTicket.Harness == "copilot" && (!m.repairTicket.SessionRef.Valid || m.repairTicket.SessionRef.String == "") {
		lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Note: Copilot does not expose a session ref; start fresh or edit ref manually."))
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.warning).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}
