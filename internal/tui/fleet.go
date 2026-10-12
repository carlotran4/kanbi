package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type FleetActions interface {
	PreviewFleet(context.Context, int64, int, int) (multiplexer.FleetSummary, error)
	EnterFleet(context.Context, int64, int, int, string) error
	RestoreFleet(context.Context) error
	ResizeFleet(context.Context) error
	FleetStatus(context.Context) (multiplexer.FleetStatus, error)
}
type fleetView struct {
	Board                                                  int64
	Master                                                 bool
	Filter                                                 storage.MasterFilter
	Selected                                               int64
	Column, Offset, Width, Height                          int
	Scroll                                                 []int
	Tops                                                   map[string]int64
	InspectorOpen, InspectorFocused                        bool
	InspectorSection, InspectorScroll, InspectorHorizontal int
	Picker                                                 bool
}
type fleetUI struct {
	active, confirm, busy, quit bool
	pendingReturn               bool
	summary                     multiplexer.FleetSummary
	status                      multiplexer.FleetStatus
	saved                       fleetView
	err                         string
}
type fleetResult struct {
	operation string
	summary   multiplexer.FleetSummary
	status    multiplexer.FleetStatus
	err       error
}

func (m Model) fleetCmd(operation string) tea.Cmd {
	actions, ok := m.actions.(FleetActions)
	if !ok {
		return func() tea.Msg { return fleetResult{operation: operation, err: fmt.Errorf("Fleet is unavailable")} }
	}
	board := m.boardID
	if m.masterBoard {
		board = 0
	} else if board == 0 {
		board = m.view.Board.ID
	}
	raw, _ := json.Marshal(m.fleet.saved)
	return func() tea.Msg {
		w := m.refreshWorker
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return nil
		}
		w.wg.Add(1)
		w.mu.Unlock()
		defer w.wg.Done()
		ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
		defer cancel()
		result := fleetResult{operation: operation}
		switch operation {
		case "preview":
			result.summary, result.err = actions.PreviewFleet(ctx, board, m.width, m.height)
		case "enter":
			result.err = actions.EnterFleet(ctx, board, m.fleet.saved.Width, m.fleet.saved.Height, string(raw))
			result.status, _ = actions.FleetStatus(ctx)
		case "return":
			result.err = actions.RestoreFleet(ctx)
		case "resize":
			result.err = actions.ResizeFleet(ctx)
			result.status, _ = actions.FleetStatus(ctx)
		case "startup", "status":
			result.status, result.err = actions.FleetStatus(ctx)
			if operation == "startup" && result.status.Present && result.status.Recovery {
				result.summary, result.err = actions.PreviewFleet(ctx, board, m.width, m.height)
			}
		}
		return result
	}
}
func (m *Model) saveFleetView() {
	v := fleetView{Board: m.boardID, Master: m.masterBoard, Filter: m.masterFilter, Column: m.col, Offset: m.colOffset, Width: m.width, Height: m.height, Scroll: append([]int(nil), m.colScroll...), Tops: map[string]int64{}, InspectorOpen: m.inspector.open, InspectorFocused: m.inspector.focused, InspectorSection: m.inspector.section, InspectorScroll: m.inspector.scroll, InspectorHorizontal: m.inspector.horizontal, Picker: m.boardPicker}
	if t, ok := m.selectedTicket(); ok {
		v.Selected = t.ID
	}
	for i, c := range m.view.Columns {
		if i < len(m.colScroll) && m.colScroll[i] >= 0 && m.colScroll[i] < len(c.Tickets) {
			v.Tops[m.searchColumnKey(c)] = c.Tickets[m.colScroll[i]].ID
		}
	}
	m.fleet.saved = v
}
func (m *Model) restoreFleetView() {
	v := m.fleet.saved
	if v.Width == 0 {
		v.Width = m.width
	}
	if v.Height == 0 {
		v.Height = defaultTermHeight
	}
	m.width, m.height = v.Width, v.Height
	m.boardID, m.masterBoard, m.masterFilter = v.Board, v.Master, v.Filter
	m.boardPicker = v.Picker
	m.col, m.card, m.colOffset = v.Column, 0, v.Offset
	m.colScroll = append([]int(nil), v.Scroll...)
	m.inspector.open, m.inspector.focused = v.InspectorOpen, v.InspectorFocused
	m.reload()
	m.selectSearchTicket(v.Selected)
	for i, c := range m.view.Columns {
		for index, t := range c.Tickets {
			if t.ID == v.Tops[m.searchColumnKey(c)] && i < len(m.colScroll) {
				m.colScroll[i] = index
				break
			}
		}
	}
	m.inspector.section, m.inspector.scroll, m.inspector.horizontal = v.InspectorSection, v.InspectorScroll, v.InspectorHorizontal
	m.inspector.target = ""
	m.inspector.busy = false
	m.inspector.generation++
	m.renderVersion++
}
func (m *Model) startFleet() tea.Cmd {
	m.fleet.pendingReturn = false
	m.fleet.quit = false
	m.saveFleetView()
	m.fleet.busy = true
	m.fleet.err = ""
	return m.fleetCmd("preview")
}
func (m *Model) fleetReturn(quit bool) tea.Cmd {
	m.fleet.busy = true
	m.fleet.pendingReturn = false
	m.fleet.quit = quit
	m.fleet.err = ""
	return m.fleetCmd("return")
}

// This runs before ordinary board keys so tiny header PTYs cannot mutate the
// hidden board. Input in agent panes never reaches this Bubble Tea program.
func (m *Model) updateFleet(msg tea.Msg) (bool, tea.Cmd) {
	if result, ok := msg.(fleetResult); ok {
		switch result.operation {
		case "startup":
			if result.status.Present && result.status.Recovery {
				m.saveFleetView()
				m.fleet.summary = result.summary
				if result.summary.View != "" {
					_ = json.Unmarshal([]byte(result.summary.View), &m.fleet.saved)
				}
				m.fleet.saved.Width, m.fleet.saved.Height = m.width, m.height
				m.fleet.confirm = true
			} else if result.status.Present {
				m.status = "Fleet is active in another Kanbi tab; return from its header first"
			}
		case "preview":
			m.fleet.busy = false
			if result.err != nil {
				m.status = storage.RedactSecretText(result.err.Error())
				return true, nil
			}
			m.fleet.summary = result.summary
			if result.summary.Recovery && result.summary.View != "" {
				_ = json.Unmarshal([]byte(result.summary.View), &m.fleet.saved)
				m.fleet.saved.Width, m.fleet.saved.Height = m.width, m.height
			}
			m.fleet.confirm = true
		case "enter":
			m.fleet.busy = false
			m.fleet.status = result.status
			if result.err == nil {
				m.fleet.active = true
				if m.fleet.pendingReturn {
					return true, m.fleetReturn(m.fleet.quit)
				}
				return true, tea.Batch(tea.EnableMouseCellMotion, tea.ClearScreen)
			}
			if result.status.Present {
				m.fleet.active = true
			} else {
				m.fleet.active = false
				m.restoreFleetView()
			}
			m.fleet.pendingReturn = false
			m.fleet.err = storage.RedactSecretText(result.err.Error())
			m.status = m.fleet.err
		case "return":
			m.fleet.pendingReturn = false
			m.fleet.busy = false
			if result.err != nil {
				m.fleet.err = storage.RedactSecretText(result.err.Error())
				return true, nil
			}
			quit := m.fleet.quit
			m.fleet.active = false
			m.fleet.confirm = false
			m.restoreFleetView()
			m.fleet.err = ""
			m.status = "returned from Fleet"
			if quit {
				m.refreshWorker.stop()
				m.statusBarCancel()
				return true, tea.Batch(tea.DisableMouse, tea.Quit)
			}
			return true, tea.Batch(tea.DisableMouse, tea.ClearScreen, m.initialStatusBarCmd())
		case "resize":
			if result.status.Width > 0 {
				m.fleet.saved.Width = result.status.Width
			}
			if result.status.Height > 0 {
				m.fleet.saved.Height = result.status.Height
			}
			if result.err != nil {
				m.fleet.err = storage.RedactSecretText(result.err.Error())
			}
		case "status":
			if result.err != nil {
				m.fleet.err = "stale: " + storage.RedactSecretText(result.err.Error())
			} else {
				m.fleet.status = result.status
				if strings.HasPrefix(m.fleet.err, "stale:") {
					m.fleet.err = ""
				}
			}
			m.refreshBusy = false
			return true, runtimeTickCmd()
		}
		if result.err != nil {
			m.fleet.err = storage.RedactSecretText(result.err.Error())
		}
		return true, nil
	}
	if m.fleet.active {
		switch msg := msg.(type) {
		case tea.WindowSizeMsg:
			m.width, m.height = msg.Width, msg.Height
			if !m.fleet.busy {
				return true, tea.Batch(tea.EnableMouseCellMotion, tea.ClearScreen, m.fleetCmd("resize"))
			}
			return true, nil
		case runtimeTickMsg:
			if m.refreshBusy || m.fleet.busy {
				return true, runtimeTickCmd()
			}
			m.refreshBusy = true
			return true, m.fleetCmd("status")
		case runtimeRefreshedMsg:
			m.refreshBusy = false
			return true, runtimeTickCmd()
		case tea.MouseMsg:
			if (msg.Action == tea.MouseActionRelease || msg.Action == tea.MouseActionPress) && msg.Button == tea.MouseButtonLeft && msg.Y == 0 && msg.X >= m.fleetReturnStart() {
				if m.fleet.busy {
					m.fleet.pendingReturn = true
					return true, nil
				}
				return true, m.fleetReturn(false)
			}
			return true, nil
		case tea.KeyMsg:
			if m.fleet.busy {
				if msg.Type == tea.KeyCtrlC {
					m.refreshWorker.stop()
					m.statusBarCancel()
					return true, tea.Quit
				}
				switch msg.String() {
				case "v", "enter", "esc":
					m.fleet.pendingReturn = true
				case "q":
					m.fleet.pendingReturn = true
					m.fleet.quit = true
				}
				return true, nil
			}
			switch msg.String() {
			case "v", "enter", "esc":
				return true, m.fleetReturn(false)
			case "q", "ctrl+c":
				return true, m.fleetReturn(true)
			}
			return true, nil
		}
	}
	if m.fleet.confirm {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc", "v":
				m.fleet.confirm = false
			case "enter":
				if !m.fleet.summary.Recovery {
					m.fleet.saved.Width, m.fleet.saved.Height = m.width, m.height
				}
				m.fleet.confirm = false
				m.fleet.active = true
				m.fleet.busy = true
				if m.fleet.summary.Recovery {
					return true, m.fleetReturn(false)
				}
				return true, m.fleetCmd("enter")
			case "ctrl+c":
				return false, nil
			}
			return true, nil
		}
	}
	if m.fleet.busy {
		if key, ok := msg.(tea.KeyMsg); ok && key.Type != tea.KeyCtrlC {
			return true, nil
		}
	}
	return false, nil
}
func (m Model) fleetControl() string {
	if m.fleet.err != "" {
		return "[Retry return · v]"
	}
	return "[Return to board · v]"
}
func (m Model) fleetReturnStart() int { return maxInt(0, m.width-ansi.StringWidth(m.fleetControl())) }
func (m Model) fleetHeader() string {
	prefix := "FLEET / " + m.fleet.summary.Scope
	if m.fleet.busy {
		prefix += " · moving panes…"
	} else if m.fleet.err != "" {
		prefix += " · " + strings.ReplaceAll(m.fleet.err, "\n", " ")
	} else {
		if m.fleetSmall() {
			prefix += " · small panes: Herdr zoom"
		} else {
			prefix += fmt.Sprintf(" · %d/%d live · %d attention", m.fleet.status.Live, m.fleet.status.Count, m.fleet.status.Attention)
		}
		if m.fleet.status.New > 0 {
			prefix += fmt.Sprintf(" · +%d new: re-enter", m.fleet.status.New)
		}
	}
	start := m.fleetReturnStart()
	prefix = ansi.Truncate(prefix, maxInt(0, start-1), "…")
	padding := maxInt(0, start-ansi.StringWidth(prefix))
	return prefix + strings.Repeat(" ", padding) + lipgloss.NewStyle().Bold(true).Underline(true).Foreground(palette.accent).Render(ansi.Truncate(m.fleetControl(), m.width, ""))
}
func (m Model) fleetConfirmView() string {
	text := m.fleetConfirmText()
	width := popupWidth(m.width)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, wrapText(line, maxInt(20, width-8), 10)...)
	}
	return lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(palette.accent).Padding(1, 2).Width(width - 2).Render(strings.Join(lines, "\n"))
}
func (m Model) fleetConfirmText() string {
	s := m.fleet.summary
	if s.Recovery {
		return fmt.Sprintf("RESTORE FLEET PANES\n\n%s · %d recorded agents\nRestore the existing terminals to their source tabs.\n\nEnter: restore · Esc: cancel", s.Scope, s.Count)
	}
	return fmt.Sprintf("GATHER FLEET\n\nScope: %s (all active agents; filters do not limit Fleet)\n%d agents · %d starting/closing, other-server or unsupported excluded\nLayout: %s\n\nHerdr focus: %s · Zoom/unzoom: %s\nAgent pane input goes directly to its harness.\nReturn: focus the top header, then v / Enter / Esc, or click Return.\nNew agents stay in their own tabs until you re-enter.\n\nEnter: gather · Esc: cancel", s.Scope, s.Count, s.Excluded, s.Arrangement, s.FocusHint, s.ZoomHint)
}

func (m Model) fleetSmall() bool {
	n := m.fleet.status.Count
	cols, rows := 1, 1
	if n > 1 {
		cols = 2
	}
	if n == 3 || n >= 5 {
		cols = 3
	}
	if n >= 4 {
		rows = 2
	}
	h := m.fleet.status.Height
	if h == 0 {
		h = m.fleet.saved.Height
	}
	return m.width/cols-2 < 36 || (h-4)/rows-4 < 8
}
