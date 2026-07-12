package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) setActionError(operation string, cause error, next string) {
	m.errOperation = operation
	m.errNext = next
	m.status = cause.Error()
}

func (m Model) updateOnboarding(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc", "q":
		m.firstRun = false
		m.status = "select a board"
	case "enter", "right", "l":
		if m.onboardingPage < 2 {
			m.onboardingPage++
		} else {
			m.firstRun = false
			m.status = "select a board; press c to create one"
		}
	case "left", "h":
		if m.onboardingPage > 0 {
			m.onboardingPage--
		}
	}
	return m
}

func (m Model) onboardingView() string {
	pages := [][]string{
		{
			"Welcome to Kanbi",
			"",
			"Kanbi runs agent CLIs in terminal containers. Before starting:",
			"• Install tmux (the default runtime).",
			"• Install and authenticate at least one harness: pi, codex, copilot, or claude.",
			"• Run `kanbi doctor` if a prerequisite is missing.",
		},
		{
			"Boards and sessions",
			"",
			"Each board needs a working directory. Agent commands start there.",
			"Choose an existing board, or press c in the board picker to create one.",
			"Create a ticket with n; Enter sends its prompt and opens the session.",
			"After this guide, q exits Kanbi without stopping agents. x safely closes the selected session.",
		},
		{
			"Recovery and backup safety",
			"",
			"Closed sessions resume only when Kanbi has a verified harness session ref.",
			"If repair is required, retry, edit the ref, or start fresh; history is preserved.",
			"Before destructive board changes, run `kanbi backup PATH`.",
			"Press ? anytime for controls and the textual state/indicator legend.",
		},
	}
	page := m.onboardingPage
	if page < 0 || page >= len(pages) {
		page = 0
	}
	lines := append([]string(nil), pages[page]...)
	lines = append(lines, "", fmt.Sprintf("Page %d/%d · Enter/→ next · ← back · Esc skip", page+1, len(pages)))
	width := popupWidth(m.width)
	bodyWidth := width - 8
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	var wrapped []string
	for _, line := range lines {
		parts := wrapText(line, bodyWidth, 20)
		if len(parts) == 0 {
			wrapped = append(wrapped, "")
		} else {
			wrapped = append(wrapped, parts...)
		}
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(maxInt(1, width-4)).
		Render(strings.Join(wrapped, "\n"))
}

// fitModal keeps modal content inside short terminals. For list-like modals it
// follows the visible textual focus marker, so controls remain reachable even
// when the popup is taller than the terminal. Help uses an explicit scroll.
func fitModal(popup string, termHeight, scroll int, followFocus bool) string {
	lines := strings.Split(popup, "\n")
	maxHeight := termHeight - 2
	if maxHeight < 3 {
		maxHeight = maxInt(1, termHeight)
	}
	if len(lines) <= maxHeight {
		return popup
	}
	start := scroll
	if followFocus {
		for i, line := range lines {
			plain := ansiStrip(line)
			if strings.Contains(plain, "> ") || strings.Contains(plain, "▸ ") {
				start = i - maxHeight/2
				break
			}
		}
	}
	if start < 0 {
		start = 0
	}
	if start > len(lines)-maxHeight {
		start = len(lines) - maxHeight
	}
	window := append([]string(nil), lines[start:start+maxHeight]...)
	if start > 0 {
		label := "↑ more"
		if !followFocus {
			label += " — k/↑ scroll"
		}
		window[0] = lipgloss.NewStyle().Bold(true).Render(label)
	}
	if start+maxHeight < len(lines) {
		label := "↓ more"
		if !followFocus {
			label += " — j/↓ scroll"
		}
		window[len(window)-1] = lipgloss.NewStyle().Bold(true).Render(label)
	}
	return strings.Join(window, "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
