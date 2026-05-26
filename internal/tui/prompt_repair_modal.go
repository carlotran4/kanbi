package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
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
	case "o":
		m.status = "opened without prompt " + m.promptTicket.DisplayID
		m.promptFallback = false
		m.reload()
	case "c", "esc":
		m.status = "cancelled prompt send"
		m.promptFallback = false
	}
	return m, nil
}

func isRepairError(err error) bool {
	var repair tmux.RepairNeededError
	var resume tmux.ResumeFailedError
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
		if err := m.actions.StartFreshTicket(m.ctx, m.repairTicket, false); err != nil {
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
	return fmt.Sprintf("Prompt readiness was not detected for %s.\n\np paste now\no open without sending\nc cancel\n", m.promptTicket.DisplayID)
}

func (m Model) repairView() string {
	header := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Faint(true)
	if m.repairEditingRef {
		return fmt.Sprintf("%s\n\n> ref: %s\n\nEnter save, Esc back\n",
			header.Render("Edit session ref for "+m.repairTicket.DisplayID),
			renderWithCursor(m.repairRef, len([]rune(m.repairRef))))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", header.Render("Session repair needed for "+m.repairTicket.DisplayID))
	fmt.Fprintf(&b, "%s\n\n", dim.Render(m.repairReason))
	b.WriteString("r  retry\n")
	b.WriteString("e  edit session ref\n")
	b.WriteString("f  start fresh\n")
	b.WriteString("c  cancel\n")
	if m.repairTicket.Harness == "copilot" && (!m.repairTicket.SessionRef.Valid || m.repairTicket.SessionRef.String == "") {
		fmt.Fprintf(&b, "\n%s\n", dim.Render("Note: Copilot does not expose a session ref; start fresh or edit ref manually."))
	}
	return b.String()
}
