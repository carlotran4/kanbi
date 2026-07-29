package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
	"github.com/carlotran4/kanbi/internal/storage"
)

func integrationRunActive(state string) bool {
	switch state {
	case storage.IntegrationStatePlanning, storage.IntegrationStateRunning, storage.IntegrationStateWaitingForUser, storage.IntegrationStateNeedsPermission, storage.IntegrationStateReady, storage.IntegrationStateBlocked, storage.IntegrationStatePromoting, storage.IntegrationStateCleanupRequired:
		return true
	default:
		return false
	}
}

func (m *Model) openIntegration() tea.Cmd {
	if m.masterBoard || m.view.Board.ID == 0 {
		m.status = "integration runs are available from a named board"
		return nil
	}
	if m.view.Board.WorktreeMode != storage.WorktreeModeGit {
		m.status = "enable Git worktrees for this board before starting an integration run"
		return nil
	}
	runs, err := m.actions.ListIntegrationRuns(m.ctx, m.view.Board.ID)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	for _, run := range runs {
		if integrationRunActive(run.State) {
			m.integrationRun = run
			m.integrationCandidates = nil
			m.integrationOpen = true
			m.integrationPromoting = false
			m.integrationCancelling = false
			return nil
		}
	}
	candidates, err := m.actions.IntegrationCandidates(m.ctx, m.view.Board.ID)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	if len(candidates) == 0 {
		m.status = "no clean, ready ticket worktrees are available to integrate"
		return nil
	}
	m.integrationCandidates = candidates
	m.integrationSelected = map[int64]bool{}
	m.integrationIndex = 0
	m.integrationRun = storage.IntegrationRun{}
	m.integrationOpen = true
	m.integrationPromoting = false
	m.integrationCancelling = false
	return nil
}

func (m Model) updateIntegration(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.integrationRun.PublicID == "" {
		switch key.String() {
		case "esc":
			m.integrationOpen = false
		case "j", "down":
			if len(m.integrationCandidates) > 0 {
				m.integrationIndex = (m.integrationIndex + 1) % len(m.integrationCandidates)
			}
		case "k", "up":
			if len(m.integrationCandidates) > 0 {
				m.integrationIndex--
				if m.integrationIndex < 0 {
					m.integrationIndex = len(m.integrationCandidates) - 1
				}
			}
		case " ":
			if m.integrationIndex >= 0 && m.integrationIndex < len(m.integrationCandidates) {
				candidate := m.integrationCandidates[m.integrationIndex]
				if !candidate.Eligible {
					m.status = candidate.Reason
					return m, nil
				}
				id := candidate.Workspace.ID
				m.integrationSelected[id] = !m.integrationSelected[id]
			}
		case "enter":
			var ids []int64
			for _, candidate := range m.integrationCandidates {
				if m.integrationSelected[candidate.Workspace.ID] {
					ids = append(ids, candidate.Workspace.ID)
				}
			}
			if len(ids) == 0 {
				m.status = "select at least one ticket workspace"
				return m, nil
			}
			m.integrationOpen = false
			boardID := m.view.Board.ID
			return m, func() tea.Msg {
				result, err := m.actions.CreateIntegration(m.ctx, integrationpkg.CreateOptions{BoardID: boardID, WorkspaceIDs: ids})
				if err != nil {
					return integrationActionMsg{action: "start", err: err}
				}
				runs, listErr := m.actions.ListIntegrationRuns(m.ctx, boardID)
				if listErr == nil {
					for _, run := range runs {
						if run.PublicID == result.Run.PublicID {
							result.Run = run
							break
						}
					}
				}
				return integrationActionMsg{action: "started", run: result.Run, err: listErr}
			}
		}
		return m, nil
	}

	run := m.integrationRun
	if m.integrationCancelling {
		switch key.String() {
		case "esc":
			m.integrationCancelling = false
		case "enter":
			m.integrationOpen = false
			return m, func() tea.Msg {
				return integrationActionMsg{action: "cancel", run: run, err: m.actions.CancelIntegration(m.ctx, run.PublicID)}
			}
		}
		return m, nil
	}
	if m.integrationPromoting {
		switch key.String() {
		case "esc":
			m.integrationPromoting = false
		case "enter":
			m.integrationOpen = false
			return m, func() tea.Msg {
				err := m.actions.PromoteIntegration(m.ctx, run.PublicID)
				if err != nil {
					return integrationActionMsg{action: "promote", run: run, err: err}
				}
				runs, listErr := m.actions.ListIntegrationRuns(m.ctx, run.BoardID)
				if listErr == nil {
					for _, fresh := range runs {
						if fresh.PublicID == run.PublicID {
							run = fresh
							break
						}
					}
				}
				return integrationActionMsg{action: "promoted", run: run, err: listErr}
			}
		}
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.integrationOpen = false
	case "enter":
		return m, func() tea.Msg {
			return integrationActionMsg{action: "opened", run: run, err: m.actions.FocusIntegration(m.ctx, run)}
		}
	case "p":
		if run.State != storage.IntegrationStateReady && run.State != storage.IntegrationStateCleanupRequired {
			m.status = "integration agent has not reported a ready candidate"
		} else {
			m.integrationPromoting = true
		}
	case "x":
		if run.State == storage.IntegrationStatePromoting || run.State == storage.IntegrationStateCleanupRequired {
			m.status = "promotion has started; use p to reconcile/finish cleanup"
		} else {
			m.integrationCancelling = true
		}
	}
	return m, nil
}

func (m Model) integrationView() string {
	width := popupWidth(m.width)
	var lines []string
	if m.integrationRun.PublicID == "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Select worktrees to integrate"), "")
		maxRows := maxInt(3, m.height-12)
		start := 0
		if m.integrationIndex >= maxRows {
			start = m.integrationIndex - maxRows + 1
		}
		end := minInt(len(m.integrationCandidates), start+maxRows)
		if start > 0 {
			lines = append(lines, fmt.Sprintf("… %d above", start))
		}
		for i := start; i < end; i++ {
			candidate := m.integrationCandidates[i]
			cursor := " "
			if i == m.integrationIndex {
				cursor = ">"
			}
			mark := "[ ]"
			if !candidate.Eligible {
				mark = "[-]"
			} else if m.integrationSelected[candidate.Workspace.ID] {
				mark = "[x]"
			}
			line := fmt.Sprintf("%s %s %s  %s  %s", cursor, mark, candidate.Ticket.DisplayID, candidate.Workspace.BranchName, shortSHA(candidate.HeadSHA))
			if candidate.Reason != "" {
				line += " · " + candidate.Reason
			}
			lines = append(lines, trimToWidth(line, maxInt(20, width-8)))
		}
		if end < len(m.integrationCandidates) {
			lines = append(lines, fmt.Sprintf("… %d below", len(m.integrationCandidates)-end))
		}
		lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Space select · j/k move · Enter launch integration agent · Esc cancel"))
	} else {
		run := m.integrationRun
		title := "Integration run " + run.PublicID[:minInt(8, len(run.PublicID))]
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render(title), "", "State: "+run.State, "Source: "+run.SourceBranch+" @ "+shortSHA(run.SourceSHA), "Candidate: "+shortSHA(run.CandidateSHA.String), "")
		maxItems := maxInt(3, m.height-16)
		for i, item := range run.Items {
			if i >= maxItems {
				lines = append(lines, fmt.Sprintf("… %d more", len(run.Items)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("• %s %s @ %s", item.DisplayID, item.BranchName, shortSHA(item.HeadSHA)))
		}
		if run.LastError.Valid && strings.TrimSpace(run.LastError.String) != "" {
			lines = append(lines, "", lipgloss.NewStyle().Foreground(palette.warning).Render("Agent report: "+run.LastError.String))
		}
		if m.integrationCancelling {
			lines = append(lines, "", lipgloss.NewStyle().Foreground(palette.warning).Render("Cancel this integration run and remove its temporary candidate checkout?"), "Ticket worktrees and source remain untouched.", "", lipgloss.NewStyle().Faint(true).Render("Enter cancel run · Esc back"))
		} else if m.integrationPromoting {
			lines = append(lines, "", lipgloss.NewStyle().Foreground(palette.warning).Render("Promote this verified candidate into the recorded source branch?"), "Kanbi will revalidate source, ticket heads, candidate ancestry, cleanliness, and configured validation.", "", lipgloss.NewStyle().Faint(true).Render("Enter promote · Esc back"))
		} else {
			hint := "Enter open agent · x cancel run · Esc close"
			if run.State == storage.IntegrationStateReady {
				hint = "Enter open agent · p review/promote · x cancel run · Esc close"
			} else if run.State == storage.IntegrationStatePromoting || run.State == storage.IntegrationStateCleanupRequired {
				hint = "p reconcile/finish cleanup · Esc close"
			}
			lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render(hint))
		}
	}
	return modalFrame(lines, width, palette.accent)
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "-"
	}
	return sha
}
