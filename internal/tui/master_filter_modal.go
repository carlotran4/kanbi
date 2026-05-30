package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"agent-kanban/internal/kanban"
	"agent-kanban/internal/storage"
)

func (m *Model) startMasterFilter() {
	if !m.masterBoard {
		m.status = "filters apply to Master only"
		return
	}
	m.reloadBoards()
	m.reloadMasterFilterOptions()
	m.masterFilterOpen = true
	m.masterFilterField = 0
}

var masterRuntimeOptions = []string{kanban.StateNotStarted, kanban.StateRunning, kanban.StateWaitingForUser, kanban.StateNeedsPermission, kanban.StateError, kanban.StateClosed}

func (m *Model) reloadMasterFilterOptions() {
	m.masterFilterRuntimes = append([]string(nil), masterRuntimeOptions...)
	m.masterFilterHarnesses = nil
	harnessSet := map[string]bool{}
	tickets, err := m.actions.ListTickets(m.ctx, true)
	if err == nil {
		for _, ticket := range tickets {
			if strings.TrimSpace(ticket.Harness) != "" {
				harnessSet[ticket.Harness] = true
			}
		}
	}
	for _, h := range []string{"pi", "codex", "copilot"} {
		if harnessSet[h] {
			m.masterFilterHarnesses = append(m.masterFilterHarnesses, h)
			delete(harnessSet, h)
		}
	}
	for h := range harnessSet {
		m.masterFilterHarnesses = append(m.masterFilterHarnesses, h)
	}
	if len(m.masterFilterHarnesses) == 0 {
		m.masterFilterHarnesses = []string{"pi", "codex", "copilot"}
	}
}

func (m Model) updateMasterFilter(key tea.KeyMsg) Model {
	max := 2 + len(m.boards) + len(m.masterFilterRuntimes) + len(m.masterFilterHarnesses) - 1
	if max < 1 {
		max = 1
	}
	switch key.String() {
	case "esc":
		m.masterFilterOpen = false
	case "enter":
		m.masterFilterOpen = false
		m.status = "applied Master filters"
		m.reload()
	case "C":
		m.masterFilter = storage.MasterFilter{}
		m.masterFilterOpen = false
		m.status = "cleared Master filters"
		m.reload()
	case "j", "down":
		m.masterFilterField++
		if m.masterFilterField > max {
			m.masterFilterField = 0
		}
	case "k", "up":
		m.masterFilterField--
		if m.masterFilterField < 0 {
			m.masterFilterField = max
		}
	case " ":
		m.toggleMasterFilterField()
		m.reload()
	case "backspace":
		if m.masterFilterField == 0 {
			m.masterFilter.Search = popRune(m.masterFilter.Search)
			m.reload()
		}
	default:
		if m.masterFilterField == 0 && len(key.Runes) > 0 {
			m.masterFilter.Search += string(key.Runes)
			m.reload()
		}
	}
	return m
}

func (m *Model) toggleMasterFilterField() {
	idx := m.masterFilterField
	if idx == 1 {
		m.masterFilter.IncludeArchived = !m.masterFilter.IncludeArchived
		return
	}
	idx -= 2
	if idx >= 0 && idx < len(m.boards) {
		m.masterFilter.BoardIDs = toggleInt64(m.masterFilter.BoardIDs, m.boards[idx].ID)
		return
	}
	idx -= len(m.boards)
	if idx >= 0 && idx < len(m.masterFilterRuntimes) {
		m.masterFilter.Runtimes = toggleString(m.masterFilter.Runtimes, m.masterFilterRuntimes[idx])
		return
	}
	idx -= len(m.masterFilterRuntimes)
	if idx >= 0 && idx < len(m.masterFilterHarnesses) {
		m.masterFilter.Harnesses = toggleString(m.masterFilter.Harnesses, m.masterFilterHarnesses[idx])
	}
}

func (m Model) masterFilterView() string {
	var lines []string
	header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Master filters")
	lines = append(lines, header, "")
	row := func(i int, text string) string {
		if i == m.masterFilterField {
			return lipgloss.NewStyle().Foreground(palette.accent).Render(">") + " " + text
		}
		return "  " + text
	}
	lines = append(lines, row(0, "search: "+renderWithCursor(m.masterFilter.Search, len([]rune(m.masterFilter.Search)))))
	archived := "[ ] show archived"
	if m.masterFilter.IncludeArchived {
		archived = "[x] show archived"
	}
	lines = append(lines, row(1, archived))
	idx := 2
	rowText := func(checked bool, label string) string {
		if checked {
			return "[x] " + label
		}
		return "[ ] " + label
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Boards (none = All Boards)"))
	for _, board := range m.boards {
		lines = append(lines, row(idx, rowText(hasInt64(m.masterFilter.BoardIDs, board.ID), board.Name)))
		idx++
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Runtime/state"))
	for _, runtime := range m.masterFilterRuntimes {
		lines = append(lines, row(idx, rowText(hasString(m.masterFilter.Runtimes, runtime), runtime)))
		idx++
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Harness"))
	for _, harness := range m.masterFilterHarnesses {
		lines = append(lines, row(idx, rowText(hasString(m.masterFilter.Harnesses, harness), harness)))
		idx++
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Type to search · Space toggle · Enter apply · C clear · Esc close"))
	if summary := m.masterFilterSummary(); summary != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.warning).Render("Active: "+summary))
	}
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}

func (m Model) masterFilterSummary() string {
	if m.masterFilter.Empty() {
		return ""
	}
	var parts []string
	if len(m.masterFilter.BoardIDs) > 0 {
		var names []string
		for _, id := range m.masterFilter.BoardIDs {
			for _, board := range m.boards {
				if board.ID == id {
					names = append(names, board.Name)
				}
			}
		}
		parts = append(parts, "boards="+strings.Join(names, ","))
	}
	if len(m.masterFilter.Runtimes) > 0 {
		parts = append(parts, "runtime="+strings.Join(m.masterFilter.Runtimes, ","))
	}
	if len(m.masterFilter.Harnesses) > 0 {
		parts = append(parts, "harness="+strings.Join(m.masterFilter.Harnesses, ","))
	}
	if strings.TrimSpace(m.masterFilter.Search) != "" {
		parts = append(parts, "search="+strings.TrimSpace(m.masterFilter.Search))
	}
	if m.masterFilter.IncludeArchived {
		parts = append(parts, "archived")
	}
	return strings.Join(parts, " · ")
}

func hasInt64(values []int64, value int64) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func toggleInt64(values []int64, value int64) []int64 {
	for i, v := range values {
		if v == value {
			return append(values[:i], values[i+1:]...)
		}
	}
	return append(values, value)
}

func hasString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func toggleString(values []string, value string) []string {
	for i, v := range values {
		if v == value {
			return append(values[:i], values[i+1:]...)
		}
	}
	return append(values, value)
}
