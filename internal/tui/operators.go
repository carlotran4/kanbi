package tui

import (
	"context"
	"fmt"
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
	open                 bool
	focused              bool
	editing              bool
	confirm              string
	menu                 bool
	index                int
	field                int
	scheduleEditing      bool
	editorGeneration     uint64
	instructionsOriginal string
	items                []storage.Operator
	selected             storage.Operator
	inputs               [5]InputBuffer
	instructions         textarea.Model
	scheduledPrompt      textarea.Model
	busy                 bool
	error                string
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
	m.operators.menu = false
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
	u.menu = false
	u.scheduleEditing = false
	u.editorGeneration++
	u.field = 0
	u.error = ""
	values := []string{o.Name, o.Harness, o.CWD, o.Cron, o.Timezone}
	for i, v := range values {
		u.inputs[i] = NewInputBuffer(v)
	}
	ta := newBodyTextarea(text, m.width)
	ta.Placeholder = "Standing instructions"
	ta.CharLimit = 0
	u.instructions = ta
	u.instructionsOriginal = text
	u.scheduledPrompt = newBodyTextarea(o.ScheduledPrompt, m.width)
	u.scheduledPrompt.Placeholder = "What should the scheduled check do?"
	u.scheduledPrompt.CharLimit = 0
	m.resizeOperatorEditors()
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
		return m.updateOperatorEditor(key, a)
	}
	if u.menu {
		switch key.String() {
		case "esc", "?":
			u.menu = false
			return m, nil
		case "n", "e", "enter", "r", "p", "s", "x", "f", "d":
			u.menu = false
		default:
			return m, nil
		}
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
	case "?":
		u.menu = true
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
	muted := lipgloss.NewStyle().Foreground(palette.muted)
	heading := lipgloss.NewStyle().Foreground(palette.headerText).Bold(true)
	if u.focused {
		heading = heading.Foreground(palette.accent)
	}
	label, hide := fmt.Sprintf("OPERATORS  %d", len(u.items)), "O hide"
	gap := maxInt(1, width-lipgloss.Width(label)-lipgloss.Width(hide))
	lines := []string{heading.Render(label) + strings.Repeat(" ", gap) + muted.Render(hide), muted.Render(strings.Repeat("─", width))}
	runLabel := "r run"
	if len(u.items) > 0 && (u.items[u.index].State == "failed" || u.items[u.index].State == "error" || u.items[u.index].State == "repair_needed") {
		runLabel = "r retry"
	}
	footer := []string{muted.Render(strings.Repeat("─", width)), muted.Render("Enter open · " + runLabel + " · e edit"), muted.Render("n new · ? actions · Esc board")}
	if u.busy {
		footer = append([]string{muted.Render("Working…")}, footer...)
	}
	if u.error != "" && !u.menu {
		var errors []string
		for _, line := range wrapText(u.error, width, 2) {
			errors = append(errors, lipgloss.NewStyle().Foreground(palette.error_).Render(line))
		}
		footer = append(errors, footer...)
	}
	if u.confirm != "" {
		text := u.confirm + " " + u.selected.Name + "? y / n"
		if u.confirm == "delete" {
			text = "Delete triggers/address; retain history/session? y / n"
		}
		footer = append(wrapText(text, width, 3), footer...)
	}
	if u.menu {
		pause := "p  Pause schedule"
		if len(u.items) > 0 && u.items[u.index].SchedulePaused {
			pause = "p  Resume schedule"
		}
		lines = append(lines, "", heading.Render("Operator actions"), "", "Enter  Open session", "r  Run now / retry", "e  Edit configuration", pause, "s  Stop turn", "x  Close session…", "f  Start fresh…", "d  Delete Operator…", "", muted.Render("Esc / ? back"))
	} else if len(u.items) == 0 {
		lines = append(lines, "", muted.Render("No Operators yet."), muted.Render("Press n to create one."))
	} else {
		// Three content rows plus the same one-row gap used by board cards.
		// Reserve a row for overflow and keep the action footer fixed in place.
		available := maxInt(1, (height-len(lines)-len(footer)-1)/4)
		start := maxInt(0, u.index-available+1)
		end := minInt(len(u.items), start+available)
		for i := start; i < end; i++ {
			lines = append(lines, operatorCardLines(u.items[i], width, i == u.index, u.focused)...)
		}
		more := []string{}
		if start > 0 {
			more = append(more, fmt.Sprintf("%d more ↑", start))
		}
		if end < len(u.items) {
			more = append(more, fmt.Sprintf("%d more ↓", len(u.items)-end))
		}
		lines = append(lines, muted.Render(strings.Join(more, " · ")))
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	for i, line := range lines {
		lines[i] = padLine(trimToWidth(line, width), width)
	}
	return strings.Join(lines, "\n")
}

func operatorCardLines(o storage.Operator, width int, selected, focused bool) []string {
	inner := maxInt(1, width-4)
	harness := trimToWidth(o.Harness, maxInt(1, inner/3))
	name := trimToWidth(o.Name, maxInt(1, inner-lipgloss.Width(harness)-1))
	top := lipgloss.NewStyle().Foreground(palette.headerText).Bold(true).Render(name) + strings.Repeat(" ", maxInt(1, inner-lipgloss.Width(name)-lipgloss.Width(harness))) + lipgloss.NewStyle().Foreground(palette.cardMuted).Render(harness)
	state := strings.ReplaceAll(o.State, "_", " ")
	if state == "" {
		state = "not started"
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
	tone := palette.cardMuted
	var rail lipgloss.TerminalColor
	if selected {
		rail = palette.rule
		if focused {
			rail = palette.accent
		}
	}
	switch {
	case o.State == "failed" || o.State == "error" || o.State == "repair_needed":
		tone, rail = palette.error_, palette.error_
	case isAttention(o.State):
		tone, rail = palette.warning, palette.warning
	}
	style := lipgloss.NewStyle().Foreground(tone)
	return filledCardLines([]string{top, style.Render(trimToWidth(state, inner)), style.Render(trimToWidth(detail, inner))}, width, rail, selected)
}

func (m Model) operatorsOverlay() string {
	width := maxInt(2, minInt(40, m.width))
	height := maxInt(3, m.height-2)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(palette.accent).Padding(0, 1).Render(m.operatorsView(maxInt(1, width-4), height-2))
}

// Main focus follows the ticket editor: title, metadata, directory, body.
// Scheduling has its own local editor and stays in the draft until the main save.
func (m Model) updateOperatorEditor(key tea.KeyMsg, a operatorActions) (Model, tea.Cmd) {
	u := &m.operators
	switch key.String() {
	case "esc":
		if u.scheduleEditing {
			u.scheduleEditing = false
			u.field = 7
			u.scheduledPrompt.Blur()
		} else {
			u.editing = false
			u.instructions.Blur()
		}
	case "tab", "shift+tab":
		fields := []int{0, 1, 2, 6, 7}
		if u.selected.ID != 0 {
			fields = []int{0, 2, 6, 7}
		}
		if u.scheduleEditing {
			fields = []int{3, 4, 5}
		}
		index := 0
		for i, f := range fields {
			if f == u.field {
				index = i
				break
			}
		}
		delta := 1
		if key.String() == "shift+tab" {
			delta = len(fields) - 1
		}
		u.field = fields[(index+delta)%len(fields)]
		u.instructions.Blur()
		u.scheduledPrompt.Blur()
		if u.field == 6 {
			return m, u.instructions.Focus()
		}
		if u.field == 5 {
			return m, u.scheduledPrompt.Focus()
		}
	case "enter":
		if u.field == 7 {
			u.scheduleEditing = true
			u.field = 3
			return m, nil
		}
		if u.field != 6 && u.field != 5 {
			return m.updateOperatorEditor(tea.KeyMsg{Type: tea.KeyTab}, a)
		}
		return m.updateOperatorTextarea(key)
	case "ctrl+e":
		if u.field == 6 || u.field == 5 {
			generation, field := u.editorGeneration, u.field
			text := u.instructions.Value()
			if field == 5 {
				text = u.scheduledPrompt.Value()
			}
			return m, editTextExternally(text, func(text string, err error) tea.Msg {
				return operatorEditorFinished{generation: generation, field: field, text: text, err: err}
			})
		}
		if u.field < 5 {
			u.inputs[u.field].HandleKey(key.String(), key.Runes)
		}
	case "ctrl+s":
		if u.scheduleEditing {
			u.scheduleEditing = false
			u.field = 7
			u.scheduledPrompt.Blur()
			return m, nil
		}
		o := u.selected
		o.Name, o.Harness, o.CWD = u.inputs[0].Value(), u.inputs[1].Value(), u.inputs[2].Value()
		o.Cron, o.Timezone, o.ScheduledPrompt = u.inputs[3].Value(), u.inputs[4].Value(), u.scheduledPrompt.Value()
		if _, err := a.SaveOperator(m.ctx, o, u.instructions.Value()); err != nil {
			u.error = err.Error()
			return m, nil
		}
		u.editing, u.error = false, ""
		u.instructions.Blur()
		m.reloadOperators()
	default:
		if u.field == 6 || u.field == 5 {
			return m.updateOperatorTextarea(key)
		}
		if u.field < 5 {
			u.inputs[u.field].HandleKey(key.String(), key.Runes)
		}
	}
	return m, nil
}

func (m Model) updateOperatorTextarea(key tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.operators.field == 5 {
		m.operators.scheduledPrompt, cmd = m.operators.scheduledPrompt.Update(key)
	} else {
		m.operators.instructions, cmd = m.operators.instructions.Update(key)
	}
	return m, cmd
}

type operatorEditorFinished struct {
	generation uint64
	field      int
	text       string
	err        error
}

func (m *Model) applyOperatorEditorResult(msg operatorEditorFinished) {
	u := &m.operators
	if !u.editing || msg.generation != u.editorGeneration {
		return
	}
	if msg.err != nil {
		u.error = msg.err.Error()
		return
	}
	if msg.field == 5 {
		u.scheduledPrompt.SetValue(msg.text)
	} else {
		u.instructions.SetValue(msg.text)
	}
	u.error = ""
}

func (m *Model) resizeOperatorEditors() {
	width := maxInt(1, inspectorPopupWidth(m.width)-6)
	m.operators.instructions.SetWidth(width)
	m.operators.instructions.SetHeight(maxInt(2, minInt(24, m.height-16)))
	m.operators.scheduledPrompt.SetWidth(width)
	m.operators.scheduledPrompt.SetHeight(maxInt(2, minInt(10, m.height-17)))
}

func (m Model) operatorEditorDirty() bool {
	u := m.operators
	o := u.selected
	return o.ID == 0 || u.inputs[0].Value() != o.Name || u.inputs[1].Value() != o.Harness || u.inputs[2].Value() != o.CWD || u.inputs[3].Value() != o.Cron || u.inputs[4].Value() != o.Timezone || u.scheduledPrompt.Value() != o.ScheduledPrompt || u.instructions.Value() != u.instructionsOriginal
}

func (m Model) operatorEditorView() string {
	u := m.operators
	outerWidth := inspectorPopupWidth(m.width)
	width := outerWidth - 6
	muted := lipgloss.NewStyle().Foreground(palette.muted)
	accent := lipgloss.NewStyle().Foreground(palette.accent)
	prefix := "Operator ·"
	if u.selected.ID == 0 {
		prefix = "New Operator ·"
	}
	title := textEditorTitle(prefix, u.inputs[0], u.field == 0, m.operatorEditorDirty(), width, "Unnamed Operator")
	var lines []string
	footer := "Tab fields · Ctrl+E editor · Ctrl+S save · Esc cancel"
	if u.scheduleEditing {
		title = "Schedule · " + u.inputs[0].Value()
		lines = append(lines, muted.Render("Repeat: cron / 15m / hourly / daily; empty = none"), "")
		for i, label := range []string{"Repeat", "Timezone"} {
			field := i + 3
			prefix := "  "
			if u.field == field {
				prefix = "▸ "
			}
			lines = append(lines, modalLabeledInput(prefix+label+": ", u.inputs[field], u.field == field, width))
		}
		lines = append(lines, "", accent.Render("Scheduled prompt"), textareaOverlayView(u.scheduledPrompt), "", muted.Render("Fixed clock · while Kanbi is running"))
		footer = "Tab fields · Ctrl+E editor · Ctrl+S done · Esc back"
	} else {
		meta := []string{muted.Render("workspace")}
		if u.field == 1 {
			meta = append(meta, accent.Render("harness ")+u.inputs[1].Viewport(maxInt(1, width-25)))
		} else {
			harness := u.inputs[1].Value()
			if u.selected.ID != 0 {
				harness += " (locked)"
			}
			meta = append(meta, muted.Render(harness))
		}
		if m.operatorEditorDirty() {
			meta = append(meta, lipgloss.NewStyle().Foreground(palette.warning).Render("unsaved"))
		}
		lines = append(lines, inspectorMetadataLines(width, muted.Render("  ·  "), meta...)...)
		lines = append(lines, modalLabeledInput("Directory ", u.inputs[2], u.field == 2, width), "", accent.Render("Standing instructions"), textareaOverlayView(u.instructions), "")
		schedule := u.inputs[3].Value()
		switch schedule {
		case "":
			schedule = "No recurring schedule"
		case "15m":
			schedule = "Every 15 minutes"
		case "hourly":
			schedule = "Hourly"
		case "daily":
			schedule = "Daily at 09:00"
		}
		if u.inputs[3].Value() != "" {
			schedule += " · " + u.inputs[4].Value()
		}
		label := "Schedule  " + schedule
		if u.field == 7 {
			label = accent.Underline(true).Render("▸ " + label + " · Enter edit")
		} else {
			label = muted.Render(label)
		}
		lines = append(lines, trimToWidth(label, width))
		if u.field == 7 {
			footer = "Enter edit schedule · Tab fields · Ctrl+S save · Esc cancel"
		}
	}
	if u.error != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.error_).Render(trimToWidth(u.error, width)))
	}
	lines = append(lines, muted.Render(strings.Repeat("─", width)), muted.Render(trimToWidth(footer, width)))
	return ticketInspectorBox(strings.Join(lines, "\n"), title, outerWidth)
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
