package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/storage"
)

func (m *Model) startFocusSettings() {
	limit := m.focus.Limit
	if limit <= 0 {
		limit = 3
	}
	m.focusSettingsOpen = true
	m.focusSettingsEnabled = m.focus.Enabled
	m.focusSettingsLimit = NewInputBuffer(strconv.Itoa(limit))
	m.focusSettingsKeys = NewInputBuffer(formatFocusWorkflowKeys(m.focus.WorkflowKeys))
	m.focusSettingsField = 0
	m.focusSettingsSubmitting = false
	m.status = ""
	m.errOperation = ""
	m.errNext = ""
}

func formatFocusWorkflowKeys(keys []string) string {
	encoded := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.ReplaceAll(key, `\`, `\\`)
		key = strings.ReplaceAll(key, `,`, `\,`)
		encoded = append(encoded, key)
	}
	return strings.Join(encoded, ", ")
}

func parseFocusWorkflowKeys(value string) []string {
	seen := make(map[string]bool)
	var keys []string
	var current strings.Builder
	commit := func() {
		key := strings.TrimSpace(current.String())
		current.Reset()
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && i+1 < len(runes) && (runes[i+1] == ',' || runes[i+1] == '\\') {
			current.WriteRune(runes[i+1])
			i++
			continue
		}
		if r == ',' {
			commit()
			continue
		}
		current.WriteRune(r)
	}
	commit()
	return keys
}

func (m *Model) updateFocusSettings(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.focusSettingsSubmitting {
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.focusSettingsOpen = false
		m.status = ""
		m.errOperation = ""
		m.errNext = ""
		return m, nil
	case "tab", "down":
		m.focusSettingsField = (m.focusSettingsField + 1) % 3
		m.status = ""
		return m, nil
	case "shift+tab", "up":
		m.focusSettingsField = (m.focusSettingsField + 2) % 3
		m.status = ""
		return m, nil
	case "ctrl+s":
		limit, err := strconv.Atoi(strings.TrimSpace(m.focusSettingsLimit.Value()))
		if err != nil || limit <= 0 {
			m.focusSettingsField = 1
			m.status = "Focus limit must be a positive whole number"
			return m, nil
		}
		keys := parseFocusWorkflowKeys(m.focusSettingsKeys.Value())
		if m.focusSettingsEnabled && len(keys) == 0 {
			m.focusSettingsField = 2
			m.status = "At least one workflow key is required when Focus Mode is enabled"
			return m, nil
		}
		policy := storage.FocusPolicy{Enabled: m.focusSettingsEnabled, Limit: limit, WorkflowKeys: keys}
		m.focusSettingsSubmitting = true
		m.status = "Saving Focus Mode settings…"
		return m, func() tea.Msg {
			return focusSettingsSavedMsg{policy: policy, err: m.actions.SaveFocusPolicy(m.ctx, policy)}
		}
	case " ", "enter", "left", "right":
		if m.focusSettingsField == 0 {
			m.focusSettingsEnabled = !m.focusSettingsEnabled
			m.status = ""
			return m, nil
		}
	}
	m.status = ""
	switch m.focusSettingsField {
	case 1:
		m.focusSettingsLimit.HandleKey(key.String(), key.Runes)
	case 2:
		m.focusSettingsKeys.HandleKey(key.String(), key.Runes)
	}
	return m, nil
}

func focusSettingsInputView(buffer InputBuffer, width int) string {
	if width <= 1 {
		return ""
	}
	runes := []rune(buffer.Value())
	cursor := buffer.Cursor()
	start := 0
	if cursor >= width {
		start = cursor - width + 1
	}
	end := minInt(len(runes), start+width)
	window := NewInputBuffer(string(runes[start:end]))
	window.SetCursor(cursor - start)
	view := window.Render()
	if start > 0 {
		view = "~" + view
	}
	return trimToWidth(view, width)
}

func (m Model) focusSettingsView() string {
	width := focusModalWidth(m.width)
	contentWidth := maxInt(20, width-4)
	marker := func(field int) string {
		if m.focusSettingsField == field {
			return "> "
		}
		return "  "
	}
	checked := "off"
	if m.focusSettingsEnabled {
		checked = "on"
	}
	lines := []string{
		"Global Focus Mode settings",
		"",
		marker(0) + "Enabled: " + checked + "  (Space/Enter toggle)",
		marker(1) + "Limit: " + focusSettingsInputView(m.focusSettingsLimit, maxInt(8, contentWidth-11)),
		marker(2) + "Workflow keys: " + focusSettingsInputView(m.focusSettingsKeys, maxInt(8, contentWidth-19)),
		"",
	}
	lines = append(lines, wrapText("Use stable workflow_key values separated by commas (escape commas as \\,).", contentWidth, 3)...)
	lines = append(lines, wrapText("Applies here now; other running Kanbi processes update after restart.", contentWidth, 3)...)
	if m.status != "" {
		lines = append(lines, "", "Status: "+trimToWidth(m.status, contentWidth))
	}
	controls := "Ctrl+S save   Esc cancel"
	if m.focusSettingsSubmitting {
		controls = "Saving…"
	}
	lines = append(lines, "", controls)
	return boxLines(lines, width)
}
