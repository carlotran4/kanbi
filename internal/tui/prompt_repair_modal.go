package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/session"
	"github.com/carlotran4/kanbi/internal/storage"
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
	m.repairRef = NewInputBuffer("")
	m.repairReason = err.Error()
	m.repairSendHandoff = false
	m.repairCheckpoint = nil
	m.repairRuntimeReady = false
	m.status = err.Error()
}

func (m *Model) startResumeRepair(ticket storage.Ticket, err error, sendHandoff bool) {
	checkpoint := ticket.LatestCheckpoint
	m.startRepair(ticket, err)
	m.repairSendHandoff = sendHandoff && checkpoint != nil
	m.repairCheckpoint = checkpoint
}

func (m *Model) startHandoffRetry(ticket storage.Ticket, checkpoint *storage.PauseCheckpoint, err error) {
	m.repairing = true
	m.repairEditingRef = false
	m.repairTicket = ticket
	m.repairReason = err.Error()
	m.repairSendHandoff = checkpoint != nil
	m.repairCheckpoint = checkpoint
	m.repairRuntimeReady = true
	m.status = "session opened; handoff not sent: " + err.Error() + " · press s to retry"
}

func (m *Model) finishRepairHandoff(ticket storage.Ticket) error {
	if !m.repairSendHandoff {
		return nil
	}
	return m.actions.SendPauseHandoff(m.ctx, ticket, m.repairCheckpoint)
}

func (m *Model) clearRepair() {
	m.repairing = false
	m.repairSendHandoff = false
	m.repairCheckpoint = nil
	m.repairRuntimeReady = false
}

func (m Model) updateRepair(key tea.KeyMsg) Model {
	if m.repairRuntimeReady {
		switch key.String() {
		case "esc", "c":
			m.clearRepair()
			m.status = "cancelled saved handoff"
		case "s":
			if err := m.finishRepairHandoff(m.repairTicket); err != nil {
				m.status = "handoff not sent: " + err.Error() + " · press s to retry"
				return m
			}
			m.clearRepair()
			m.status = "sent saved handoff " + m.repairTicket.DisplayID
			m.reload()
		}
		return m
	}
	if m.repairEditingRef {
		switch key.String() {
		case "esc":
			m.repairEditingRef = false
		case "enter":
			if err := m.actions.UpdateSessionRef(m.ctx, m.repairTicket, strings.TrimSpace(m.repairRef.Value())); err != nil {
				m.status = err.Error()
				return m
			}
			updated := m.repairTicket
			updated.SessionRef.Valid = strings.TrimSpace(m.repairRef.Value()) != ""
			updated.SessionRef.String = strings.TrimSpace(m.repairRef.Value())
			if updated.SessionRef.Valid {
				if err := m.actions.OpenTicket(m.ctx, updated, false); err != nil {
					m.status = err.Error()
					return m
				}
				m.repairRuntimeReady = true
				if err := m.finishRepairHandoff(updated); err != nil {
					m.repairEditingRef = false
					m.status = "session opened; handoff not sent: " + err.Error() + " · press s to retry"
					return m
				}
			}
			m.clearRepair()
			m.status = "updated session ref " + m.repairTicket.DisplayID
			m.reload()
		default:
			m.repairRef.HandleKey(key.String(), key.Runes)
		}
		return m
	}
	switch key.String() {
	case "esc", "c":
		m.clearRepair()
		m.status = "cancelled repair"
	case "e":
		m.repairEditingRef = true
		m.repairRef = NewInputBuffer("")
	case "f":
		if err := m.actions.StartFreshTicket(m.ctx, m.repairTicket, true); err != nil {
			m.status = err.Error()
			return m
		}
		m.repairRuntimeReady = true
		if err := m.finishRepairHandoff(m.repairTicket); err != nil {
			m.status = "session started; handoff not sent: " + err.Error() + " · press s to retry"
			return m
		}
		m.clearRepair()
		m.status = "started fresh " + m.repairTicket.DisplayID
		m.reload()
	case "r":
		if err := m.actions.OpenTicket(m.ctx, m.repairTicket, false); err != nil {
			m.status = err.Error()
			return m
		}
		m.repairRuntimeReady = true
		if err := m.finishRepairHandoff(m.repairTicket); err != nil {
			m.status = "session opened; handoff not sent: " + err.Error() + " · press s to retry"
			return m
		}
		m.clearRepair()
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
			fmt.Sprintf("%s ref: %s", lipgloss.NewStyle().Foreground(palette.accent).Render(">"), m.repairRef.Render()) +
			"\n\n" + lipgloss.NewStyle().Faint(true).Render("Enter save · Esc back")
		return lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.accent).
			Padding(1, 2).
			Width(popupW - 4).
			Render(content)
	}
	var lines []string
	if m.repairRuntimeReady {
		lines = append(lines,
			lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render("Saved handoff not sent: "+m.repairTicket.DisplayID),
			"", "The session is open. Retry only the structured pause handoff.", "",
			"s  retry saved handoff", "c  cancel",
		)
		return lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.warning).
			Padding(1, 2).
			Width(popupW - 4).
			Render(strings.Join(lines, "\n"))
	}
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render("Session repair needed: "+m.repairTicket.DisplayID))
	lines = append(lines, "", "Cause: "+m.repairReason, "")
	lines = append(lines, "Next: retry after checking the runtime, edit a verified session ref, or start fresh.", "")
	lines = append(lines, "r  retry")
	lines = append(lines, "e  edit session ref")
	lines = append(lines, "f  start fresh")
	lines = append(lines, "c  cancel")
	if m.repairSendHandoff {
		lines = append(lines, "", "After repair, Kanbi will send the saved pause handoff.")
		if m.repairRuntimeReady {
			lines = append(lines, "s  retry saved handoff")
		}
	}
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
