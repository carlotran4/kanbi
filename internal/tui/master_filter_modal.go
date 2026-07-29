package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
)

func (m *Model) startMasterFilter() {
	if !m.masterBoard {
		m.status = "filters apply to Master only"
		return
	}
	m.reloadBoards()
	m.reloadMasterFilterOptions()
	m.masterFilterDraft = cloneMasterFilter(m.masterFilter)
	m.masterFilterSearch = NewInputBuffer(m.masterFilterDraft.Search)
	m.masterFilterOpen = true
	m.masterFilterField = 0
}

func masterRuntimeOptions() []string {
	return kanban.FilterableRuntimeStates()
}

func (m *Model) reloadMasterFilterOptions() {
	m.masterFilterRuntimes = masterRuntimeOptions()
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
	if m.filterPresetMode == "save" {
		switch key.String() {
		case "esc":
			m.filterPresetMode = ""
		case "enter":
			durable, err := m.actions.DurableFromMasterFilter(m.ctx, m.masterFilterDraft)
			if err != nil {
				m.status = err.Error()
				return m
			}
			p, err := m.actions.SaveFilterPreset(m.ctx, strings.TrimSpace(m.filterPresetName.Value()), durable)
			if err != nil {
				m.status = err.Error()
				return m
			}
			m.filterPresetMode = ""
			m.status = "saved filter preset " + p.Name
		default:
			m.filterPresetName.HandleKey(key.String(), key.Runes)
		}
		return m
	}
	if m.filterPresetMode == "list" {
		switch key.String() {
		case "esc":
			m.filterPresetMode = ""
		case "j", "down":
			if len(m.filterPresets) > 0 {
				m.filterPresetIndex = (m.filterPresetIndex + 1) % len(m.filterPresets)
			}
		case "k", "up":
			if len(m.filterPresets) > 0 {
				m.filterPresetIndex--
				if m.filterPresetIndex < 0 {
					m.filterPresetIndex = len(m.filterPresets) - 1
				}
			}
		case "enter":
			if len(m.filterPresets) == 0 {
				return m
			}
			p := m.filterPresets[m.filterPresetIndex]
			resolved, missing, err := m.actions.ResolveMasterFilter(m.ctx, p.Filter)
			if err != nil {
				m.status = err.Error()
				return m
			}
			m.masterFilter = resolved
			m.activePresetName = p.Name
			m.filterPresetMode = ""
			m.masterFilterOpen = false
			if len(missing) > 0 {
				m.status = "applied preset " + p.Name + "; missing boards: " + strings.Join(missing, ", ")
			} else {
				m.status = "applied preset " + p.Name
			}
			m.reload()
		case "d", "x":
			if len(m.filterPresets) == 0 {
				return m
			}
			p := m.filterPresets[m.filterPresetIndex]
			if err := m.actions.DeleteFilterPreset(m.ctx, p.ID); err != nil {
				m.status = err.Error()
				return m
			}
			if m.activePresetName == p.Name {
				m.activePresetName = ""
			}
			m.filterPresets, _ = m.actions.ListFilterPresets(m.ctx)
			if m.filterPresetIndex >= len(m.filterPresets) {
				m.filterPresetIndex = max(0, len(m.filterPresets)-1)
			}
			m.status = "deleted preset " + p.Name
		}
		return m
	}
	if m.masterFilterField == 0 {
		switch key.String() {
		case "esc", "enter", "up", "down", "ctrl+l", "ctrl+s", "ctrl+p":
			// Modal controls remain available while search is focused.
		default:
			m.masterFilterSearch.HandleKey(key.String(), key.Runes)
			m.masterFilterDraft.Search = m.masterFilterSearch.Value()
			return m
		}
	}
	max := 2 + len(m.boards) + len(m.masterFilterRuntimes) + len(m.masterFilterHarnesses) - 1
	if max < 1 {
		max = 1
	}
	switch key.String() {
	case "esc":
		m.masterFilterOpen = false
	case "enter":
		m.masterFilter = cloneMasterFilter(m.masterFilterDraft)
		m.masterFilterOpen = false
		m.activePresetName = ""
		m.status = "applied Master filters"
		m.reload()
	case "C", "ctrl+l":
		m.masterFilter = storage.MasterFilter{}
		m.masterFilterDraft = storage.MasterFilter{}
		m.masterFilterSearch = NewInputBuffer("")
		m.activePresetName = ""
		m.masterFilterOpen = false
		m.status = "cleared Master filters"
		m.reload()
	case "S", "ctrl+s":
		m.filterPresetMode = "save"
		m.filterPresetName = NewInputBuffer("")
		return m
	case "P", "ctrl+p":
		presets, err := m.actions.ListFilterPresets(m.ctx)
		if err != nil {
			m.status = err.Error()
			return m
		}
		m.filterPresets = presets
		m.filterPresetIndex = 0
		m.filterPresetMode = "list"
		return m
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
	}
	return m
}

func (m *Model) toggleMasterFilterField() {
	idx := m.masterFilterField
	if idx == 1 {
		m.masterFilterDraft.IncludeArchived = !m.masterFilterDraft.IncludeArchived
		return
	}
	idx -= 2
	if idx >= 0 && idx < len(m.boards) {
		m.masterFilterDraft.BoardIDs = toggleInt64(m.masterFilterDraft.BoardIDs, m.boards[idx].ID)
		return
	}
	idx -= len(m.boards)
	if idx >= 0 && idx < len(m.masterFilterRuntimes) {
		m.masterFilterDraft.Runtimes = toggleString(m.masterFilterDraft.Runtimes, m.masterFilterRuntimes[idx])
		return
	}
	idx -= len(m.masterFilterRuntimes)
	if idx >= 0 && idx < len(m.masterFilterHarnesses) {
		m.masterFilterDraft.Harnesses = toggleString(m.masterFilterDraft.Harnesses, m.masterFilterHarnesses[idx])
	}
}

func (m Model) masterFilterView() string {
	var lines []string
	header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Master filters")
	lines = append(lines, header, "")
	rowPrefix := func(i int) string {
		if i == m.masterFilterField {
			return lipgloss.NewStyle().Foreground(palette.accent).Render(">") + " "
		}
		return "  "
	}
	row := func(i int, text string) string { return rowPrefix(i) + text }
	search := m.masterFilterDraft.Search
	if m.masterFilterField == 0 {
		search = modalLabeledInput(rowPrefix(0)+"search: ", m.masterFilterSearch, true, modalContentWidth(popupWidth(m.width)))
		lines = append(lines, search)
	} else {
		lines = append(lines, row(0, "search: "+search))
	}
	archived := "[ ] show archived"
	if m.masterFilterDraft.IncludeArchived {
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
		lines = append(lines, row(idx, rowText(hasInt64(m.masterFilterDraft.BoardIDs, board.ID), board.Name)))
		idx++
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Runtime/state"))
	for _, runtime := range m.masterFilterRuntimes {
		lines = append(lines, row(idx, rowText(hasString(m.masterFilterDraft.Runtimes, runtime), runtime)))
		idx++
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Harness"))
	for _, harness := range m.masterFilterHarnesses {
		lines = append(lines, row(idx, rowText(hasString(m.masterFilterDraft.Harnesses, harness), harness)))
		idx++
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Type to search · Space toggle · Enter apply · Ctrl+L clear · Ctrl+S save preset · Ctrl+P presets · Esc cancel"))
	if m.filterPresetMode == "save" {
		lines = append(lines, "", "Save preset name: "+modalInput(m.filterPresetName, true, maxInt(1, modalContentWidth(popupWidth(m.width))-18)))
	}
	if m.filterPresetMode == "list" {
		lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Presets (Enter apply · d delete · Esc back)"))
		if len(m.filterPresets) == 0 {
			lines = append(lines, "  (none)")
		}
		for i, p := range m.filterPresets {
			mark := "  "
			if i == m.filterPresetIndex {
				mark = "> "
			}
			lines = append(lines, mark+p.Name)
		}
	}
	if summary := m.masterFilterSummaryFor(m.masterFilterDraft); summary != "" {
		label := "Draft: " + summary
		if m.activePresetName != "" {
			label = "Draft from preset " + m.activePresetName + ": " + summary
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.warning).Render(label))
	}
	popupW := popupWidth(m.width)
	return modalFrame(lines, popupW, palette.accent)
}

func (m Model) masterFilterSummary() string {
	return m.masterFilterSummaryFor(m.masterFilter)
}

func (m Model) masterFilterSummaryFor(filter storage.MasterFilter) string {
	if filter.Empty() {
		return ""
	}
	var parts []string
	if len(filter.BoardIDs) > 0 {
		var names []string
		for _, id := range filter.BoardIDs {
			for _, board := range m.boards {
				if board.ID == id {
					names = append(names, board.Name)
				}
			}
		}
		parts = append(parts, "boards="+strings.Join(names, ","))
	}
	if len(filter.Runtimes) > 0 {
		parts = append(parts, "runtime="+strings.Join(filter.Runtimes, ","))
	}
	if len(filter.Harnesses) > 0 {
		parts = append(parts, "harness="+strings.Join(filter.Harnesses, ","))
	}
	if strings.TrimSpace(filter.Search) != "" {
		parts = append(parts, "search="+strings.TrimSpace(filter.Search))
	}
	if filter.IncludeArchived {
		parts = append(parts, "archived")
	}
	return strings.Join(parts, " · ")
}

func cloneMasterFilter(filter storage.MasterFilter) storage.MasterFilter {
	filter.BoardIDs = append([]int64(nil), filter.BoardIDs...)
	filter.Runtimes = append([]string(nil), filter.Runtimes...)
	filter.Harnesses = append([]string(nil), filter.Harnesses...)
	return filter
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
