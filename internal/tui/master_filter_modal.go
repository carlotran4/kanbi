package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
	var b strings.Builder
	fmt.Fprintf(&b, "Master filters\n\n")
	row := func(i int, text string) {
		cursor := " "
		if i == m.masterFilterField {
			cursor = ">"
		}
		fmt.Fprintf(&b, "%s %s\n", cursor, text)
	}
	row(0, "search: "+renderWithCursor(m.masterFilter.Search, len([]rune(m.masterFilter.Search))))
	archived := "[ ] show archived"
	if m.masterFilter.IncludeArchived {
		archived = "[x] show archived"
	}
	row(1, archived)
	idx := 2
	rowText := func(checked bool, label string) string {
		if checked {
			return "[x] " + label
		}
		return "[ ] " + label
	}
	b.WriteString("\nBoards (none = All Boards)\n")
	for _, board := range m.boards {
		row(idx, rowText(hasInt64(m.masterFilter.BoardIDs, board.ID), board.Name))
		idx++
	}
	b.WriteString("\nRuntime/state\n")
	for _, runtime := range m.masterFilterRuntimes {
		row(idx, rowText(hasString(m.masterFilter.Runtimes, runtime), runtime))
		idx++
	}
	b.WriteString("\nHarness\n")
	for _, harness := range m.masterFilterHarnesses {
		row(idx, rowText(hasString(m.masterFilter.Harnesses, harness), harness))
		idx++
	}
	b.WriteString("\nType to search · Space toggle · Enter apply · C clear · Esc close\n")
	if summary := m.masterFilterSummary(); summary != "" {
		b.WriteString("Active: " + summary + "\n")
	}
	return b.String()
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
