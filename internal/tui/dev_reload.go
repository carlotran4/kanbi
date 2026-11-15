package tui

import (
	"encoding/json"
	"time"

	"github.com/carlotran4/kanbi/internal/devreload"
	tea "github.com/charmbracelet/bubbletea"
)

type devTickMsg struct{}

// EnableDevReload enables only the development process's restart protocol.
func (m *Model) EnableDevReload(saved string) {
	m.devEnabled = true
	if saved == "" {
		return
	}
	var view fleetView
	if json.Unmarshal([]byte(saved), &view) == nil {
		m.fleet.saved = view
		m.restoreFleetView()
		m.status = "development build reloaded"
	}
}

func (m Model) devTick() tea.Cmd {
	if !m.devEnabled {
		return nil
	}
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return devTickMsg{} })
}

func (m Model) devSafe() bool {
	modal := m.activeModalKind()
	return (modal == modalNone || (modal == modalBoardPicker && m.boardPickerMode == "switch")) &&
		!m.fleet.active && !m.fleet.busy && !m.fleet.confirm && !m.fleet.status.Present &&
		!m.search.active && !m.operators.open && !m.operators.busy &&
		m.devActions == 0 && !m.pauseSubmitting && !m.resumeSubmitting && !m.focusSettingsSubmitting && !m.devEditor
}

func (m *Model) updateDevReload(msg tea.Msg) (bool, tea.Cmd) {
	if !m.devEnabled {
		return false, nil
	}
	switch msg := msg.(type) {
	case devreload.Result:
		if msg.Err != nil {
			m.devPending = ""
			if msg.Log == "" {
				m.status = "development reload unavailable: " + msg.Err.Error()
			} else {
				m.status = "development build failed; see " + msg.Log
			}
		} else {
			m.devPending = msg.Path
			m.status = "development build ready; reload waits for board navigation"
		}
		return true, nil
	case devTickMsg:
		if m.devPending != "" && m.devSafe() {
			m.devRestart = true
			return true, tea.Quit
		}
		return true, m.devTick()
	}
	return false, nil
}

// DevRestart returns transient navigation state only after a safe reload quit.
func (m Model) DevRestart() (string, string) {
	if !m.devRestart {
		return "", ""
	}
	m.saveFleetView()
	raw, _ := json.Marshal(m.fleet.saved)
	return m.devPending, string(raw)
}
