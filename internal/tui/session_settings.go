package tui

import (
	"fmt"

	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) refreshSessionSettings() {
	m.sessionSettingsTicket, m.sessionSettingsError = m.actions.TicketByID(m.ctx, m.sessionSettingsTicketID)
	if m.sessionSettingsError == nil {
		m.sessionSettingsLatest, m.sessionSettingsHasPrior, m.sessionSettingsError = m.actions.LatestSession(m.ctx, m.sessionSettingsTicketID)
	}
}

func (m Model) updateSessionSettings(key tea.KeyMsg) (Model, tea.Cmd) {
	m.refreshSessionSettings()
	switch key.String() {
	case "esc", "c":
		if m.sessionFreshConfirm {
			m.sessionFreshConfirm = false
		} else {
			m.sessionSettings = false
		}
	case "e":
		if m.sessionFreshConfirm {
			return m, nil
		}
		t, err := m.actions.TicketByID(m.ctx, m.sessionSettingsTicketID)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.startEditTicket(t)
		m.editField = 2
	case "f":
		m.sessionFreshConfirm = true
	case "enter":
		if !m.sessionFreshConfirm {
			return m, nil
		}
		t, err := m.actions.TicketByID(m.ctx, m.sessionSettingsTicketID)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		if t.ArchivedAt.Valid || t.FocusPaused {
			m.status = "Unarchive or resume the paused ticket before starting fresh."
			return m, nil
		}
		if t.BoardWorktreeMode == storage.WorktreeModeGit && !t.WorkspaceID.Valid && !t.SessionActive {
			branch := normalizeBranchName(t.Title)
			preflight, err := m.actions.PreflightTicketWorkspace(m.ctx, t, branch)
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.branchNaming = true
			m.branchTicket = t
			m.branchName = NewInputBuffer(branch)
			m.branchPreflight = preflight
			m.branchConfirmed = false
			m.branchSendPrompt = true
			m.sessionFreshConfirm = false
			return m, nil
		}
		m.sessionFreshConfirm = false
		m.status = "starting fresh " + t.DisplayID + "…"
		return m, func() tea.Msg {
			return openTicketMsg{ticket: t, sendPrompt: true, err: m.actions.StartFreshTicket(m.ctx, t, true)}
		}
	}
	return m, nil
}

func (m Model) sessionSettingsView() string {
	w := popupWidth(m.width)
	cw := modalContentWidth(w)
	t := m.sessionSettingsTicket
	if m.sessionSettingsError != nil {
		return modalFrame([]string{"Session settings", m.sessionSettingsError.Error(), "Esc close"}, w, palette.accent)
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render("Session settings: " + t.DisplayID), ""}
	ses, ok := m.sessionSettingsLatest, m.sessionSettingsHasPrior
	if !ok {
		lines = append(lines, "Current: no prior session")
	} else {
		activity := "inactive"
		if ses.IsActive {
			activity = "active"
		}
		lines = append(lines, fmt.Sprintf("Current: %s · %s · %s", ses.Harness, activity, t.Runtime))
		lines = append(lines, repairWrapped("Ref: "+ses.HarnessSessionRef.String, cw, 2)...)
		if !ses.HarnessSessionRef.Valid {
			lines = append(lines, "No resume ref captured.")
		}
	}
	lines = append(lines, "Fresh launch harness: "+t.Harness, "")
	if m.sessionFreshConfirm {
		lines = append(lines, repairWrapped("Start a new conversation with the ticket prompt? Any active agent will be closed first. If closing fails, no new agent is launched.", cw, 4)...)
		lines = append(lines, repairWrapped("History stays intact. The previous conversation ref is NOT reused. A failed launch remains in history.", cw, 3)...)
		lines = append(lines, "", "Enter start fresh · Esc back")
	} else {
		lines = append(lines, repairWrapped("Harness edits apply to the next fresh launch. Enter on the board still focuses/resumes the current conversation with its original harness; editing does not stop it.", cw, 4)...)
		lines = append(lines, "", "e  edit harness / captured session ref", "f  start fresh…", "Esc close")
	}
	return modalFrame(lines, w, palette.accent)
}
