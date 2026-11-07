package tui

import (
	"context"
	"os"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/tui/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type operatorActions interface {
	ListOperators(context.Context) ([]storage.Operator, error)
	ReadOperatorInstructions(int64) (string, error)
	SaveOperator(context.Context, storage.Operator, string) (storage.Operator, error)
	OperatorAction(context.Context, int64, string) error
}
type operatorUI struct {
	open         bool
	focused      bool
	editing      bool
	confirm      string
	index        int
	field        int
	items        []storage.Operator
	selected     storage.Operator
	inputs       [6]InputBuffer
	instructions textarea.Model
	busy         bool
	error        string
}
type operatorResult struct{ err error }

func (m *Model) reloadOperators() {
	a, ok := m.actions.(operatorActions)
	if !ok {
		return
	}
	items, e := a.ListOperators(m.ctx)
	if e != nil {
		m.operators.error = e.Error()
		return
	}
	m.operators.items = items
	if m.operators.index >= len(items) {
		m.operators.index = maxInt(0, len(items)-1)
	}
}
func (m *Model) toggleOperators() {
	m.operators.open = !m.operators.open
	m.operators.focused = m.operators.open
	m.inspector.open = false
	if m.operators.open {
		m.reloadOperators()
	}
	m.renderVersion++
}
func (m *Model) editOperator(o storage.Operator) tea.Cmd {
	a, ok := m.actions.(operatorActions)
	if !ok {
		return nil
	}
	text := ""
	if o.ID != 0 {
		var e error
		text, e = a.ReadOperatorInstructions(o.ID)
		if e != nil {
			m.operators.error = e.Error()
			return nil
		}
	} else {
		o.Harness = m.defaultHarness
		if o.Harness == "" {
			o.Harness = "pi"
		}
		o.Timezone = "UTC"
		o.CWD, _ = os.Getwd()
	}
	u := &m.operators
	u.selected = o
	u.editing = true
	u.field = 0
	u.error = ""
	values := []string{o.Name, o.Harness, o.CWD, o.Cron, o.Timezone, o.ScheduledPrompt}
	for i, v := range values {
		u.inputs[i] = NewInputBuffer(v)
	}
	ta := textarea.New()
	ta.SetValue(text)
	ta.SetWidth(modalContentWidth(focusModalWidth(m.width)))
	ta.SetHeight(maxInt(2, minInt(6, m.height-18)))
	ta.CharLimit = 0
	u.instructions = ta
	return nil
}
func (m Model) updateOperators(key tea.KeyMsg) (Model, tea.Cmd) {
	m.renderVersion++
	a, ok := m.actions.(operatorActions)
	if !ok {
		return m, nil
	}
	u := &m.operators
	if u.busy {
		if key.String() == "esc" {
			u.focused = false
		}
		return m, nil
	}
	if u.editing {
		switch key.String() {
		case "esc":
			u.editing = false
			u.instructions.Blur()
		case "tab", "shift+tab":
			u.instructions.Blur()
			delta := 1
			if key.String() == "shift+tab" {
				delta = 6
			}
			u.field = (u.field + delta) % 7
			if u.selected.ID != 0 && u.field == 1 {
				u.field = (u.field + delta) % 7
			}
			if u.field == 6 {
				return m, u.instructions.Focus()
			}
		case "ctrl+s":
			o := u.selected
			o.Name = u.inputs[0].Value()
			o.Harness = u.inputs[1].Value()
			o.CWD = u.inputs[2].Value()
			o.Cron = u.inputs[3].Value()
			o.Timezone = u.inputs[4].Value()
			o.ScheduledPrompt = u.inputs[5].Value()
			_, e := a.SaveOperator(m.ctx, o, u.instructions.Value())
			if e != nil {
				u.error = e.Error()
				return m, nil
			}
			u.editing = false
			u.error = ""
			m.reloadOperators()
		default:
			if u.field == 6 {
				var cmd tea.Cmd
				u.instructions, cmd = u.instructions.Update(key)
				return m, cmd
			}
			u.inputs[u.field].HandleKey(key.String(), key.Runes)
		}
		return m, nil
	}
	if u.confirm != "" {
		switch key.String() {
		case "esc", "n":
			u.confirm = ""
		case "y":
			action := u.confirm
			u.confirm = ""
			return m, m.operatorActionCmd(a, u.selected.ID, action)
		}
		return m, nil
	}
	switch key.String() {
	case "O":
		m.toggleOperators()
	case "esc", "tab":
		u.focused = false
	case "j", "down":
		if len(u.items) > 0 {
			u.index = (u.index + 1) % len(u.items)
		}
	case "k", "up":
		if len(u.items) > 0 {
			u.index = (u.index + len(u.items) - 1) % len(u.items)
		}
	case "n":
		return m, m.editOperator(storage.Operator{})
	default:
		if len(u.items) == 0 {
			return m, nil
		}
		o := u.items[u.index]
		switch key.String() {
		case "e":
			return m, m.editOperator(o)
		case "enter":
			return m, m.operatorActionCmd(a, o.ID, "open")
		case "r":
			return m, m.operatorActionCmd(a, o.ID, "run")
		case "p":
			action := "pause"
			if o.SchedulePaused {
				action = "resume"
			}
			return m, m.operatorActionCmd(a, o.ID, action)
		case "s":
			return m, m.operatorActionCmd(a, o.ID, "stop")
		case "x", "f", "d":
			u.selected = o
			u.confirm = map[string]string{"x": "close", "f": "fresh", "d": "delete"}[key.String()]
		}
	}
	return m, nil
}
func (m *Model) operatorActionCmd(a operatorActions, id int64, action string) tea.Cmd {
	m.operators.busy = true
	return func() tea.Msg { return operatorResult{a.OperatorAction(m.ctx, id, action)} }
}
func (m Model) operatorsView(width, height int) string {
	u := m.operators
	lines := []string{"Operators  [O] collapse"}
	if u.focused {
		lines[0] = "Operators • focused  [O] close"
	}
	if len(u.items) == 0 {
		lines = append(lines, "No Operators. Press n to create.")
	}
	available := maxInt(1, (height-5)/2)
	start := maxInt(0, u.index-available+1)
	for i := start; i < len(u.items) && i < start+available; i++ {
		o := u.items[i]
		prefix := "  "
		if i == u.index {
			prefix = "> "
		}
		detail := "No scheduled wake"
		if o.SchedulePaused {
			detail = "Schedule paused"
		} else if o.NextWake != nil {
			detail = "Next: " + o.NextWake.Local().Format("Jan 2 15:04")
		}
		if o.Attention != "" {
			detail = o.Attention
		}
		row := trimToWidth(prefix+o.Name+" · "+strings.ReplaceAll(o.State, "_", " "), width)
		sub := trimToWidth("  "+detail, width)
		if o.State == "failed" || o.State == "error" || o.State == "repair_needed" {
			style := lipgloss.NewStyle().Foreground(palette.error_)
			row = style.Render(row)
			sub = style.Render(sub)
		} else if isAttention(o.State) {
			style := lipgloss.NewStyle().Foreground(palette.warning)
			row = style.Render(row)
			sub = style.Render(sub)
		}
		lines = append(lines, row, sub)
	}
	lines = append(lines, "n new  e edit  Enter open  r run", "p schedule  s stop  x close", "f fresh  d delete  Esc board")
	if u.busy {
		lines = append(lines, "Working…")
	}
	if u.error != "" {
		lines = append(lines, trimToWidth(u.error, width))
	}
	if u.confirm != "" {
		text := u.confirm + " " + u.selected.Name + "? y / n"
		if u.confirm == "delete" {
			text = "Delete triggers/address; retain session? y / n"
		}
		lines = append(lines, trimToWidth(text, width))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}
func (m Model) operatorEditorView() string {
	u := m.operators
	labels := []string{"Name", "Harness (immutable)", "Working directory", "Schedule (cron / 15m / hourly / daily)", "Timezone", "Scheduled prompt"}
	lines := []string{"Operator configuration", ""}
	outerWidth := focusModalWidth(m.width)
	width := modalContentWidth(outerWidth)
	for i, label := range labels {
		prefix := "  "
		if u.field == i {
			prefix = "> "
		}
		lines = append(lines, modalLabeledInput(prefix+label+": ", u.inputs[i], u.field == i, width))
	}
	lines = append(lines, "Standing instructions (read again on every wake):", u.instructions.View(), "Tab fields · Ctrl+S save · Esc cancel")
	if u.error != "" {
		lines = append(lines, trimToWidth(u.error, width))
	}
	return modalFrame(strings.Split(strings.Join(lines, "\n"), "\n"), outerWidth, palette.accent)
}

// Keep a small summary visible while keyboard focus returns to the board.
func (m Model) operatorsNarrow() bool { return m.width < 120 }
func (m Model) operatorSummary() string {
	if len(m.operators.items) == 0 {
		return "none · n to create"
	}
	o := m.operators.items[minInt(m.operators.index, len(m.operators.items)-1)]
	return o.Name + " · " + o.State
}
