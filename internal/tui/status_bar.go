package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/statusbar"
)

type statusBarResultMsg struct {
	name       string
	generation uint64
	value      string
	err        error
}

type statusBarRefreshMsg struct {
	name       string
	generation uint64
}

func (m Model) initialStatusBarCmd() tea.Cmd {
	commands := make([]tea.Cmd, 0, len(m.statusBar.Custom))
	for _, name := range statusbar.CustomNames(m.statusBar) {
		commands = append(commands, m.runStatusBarModule(name))
	}
	return tea.Batch(commands...)
}

func (m Model) runStatusBarModule(name string) tea.Cmd {
	module, ok := m.statusBar.Custom[name]
	if !ok {
		return nil
	}
	generation := m.statusBarGeneration
	values := statusbar.CommandContext{
		BoardName: m.view.Board.Name,
		Workdir:   m.view.Board.Workdir,
		Master:    m.masterBoard,
	}
	return func() tea.Msg {
		value, err := statusbar.Run(m.statusBarCtx, module, values)
		return statusBarResultMsg{name: name, generation: generation, value: value, err: err}
	}
}

func (m *Model) applyStatusBarResult(msg statusBarResultMsg) bool {
	if msg.generation != m.statusBarGeneration {
		return false
	}
	result := m.statusBarResults[msg.name]
	if msg.err != nil {
		result.Err = msg.err.Error()
	} else {
		result.Value = msg.value
		result.Err = ""
	}
	m.statusBarResults[msg.name] = result
	return true
}

func (m Model) scheduleStatusBarRefresh(name string, generation uint64) tea.Cmd {
	module, ok := m.statusBar.Custom[name]
	if !ok {
		return nil
	}
	return tea.Tick(module.RefreshDuration, func(time.Time) tea.Msg {
		return statusBarRefreshMsg{name: name, generation: generation}
	})
}

func (m Model) statusBarZones(now time.Time) (left, center, right string) {
	boardName := m.view.Board.Name
	if m.masterBoard {
		if summary := m.masterFilterSummary(); summary != "" {
			boardName += "  filter: " + summary
		}
	}
	return statusbar.Render(m.statusBar, statusbar.RenderContext{
		BoardName: boardName,
		Now:       now,
		Results:   m.statusBarResults,
	})
}
