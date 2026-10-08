package tui

import (
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/tui/textarea"
	"github.com/charmbracelet/lipgloss"
)

func TestTextareaOverlaySanitizerOnlyRemovesEraseControls(t *testing.T) {
	styled := "before\x1b[2K\n\x1b[7mcursor\x1b[0m\x1b[?0Jafter\x1b[1J"
	got := sanitizeTextareaOverlayView(styled)
	if strings.Contains(got, "\x1b[2K") || strings.Contains(got, "\x1b[?0J") || strings.Contains(got, "\x1b[1J") {
		t.Fatalf("erase controls remain: %q", got)
	}
	if !strings.Contains(got, "\x1b[7mcursor\x1b[0m") || ansiStrip(got) != "before\ncursorafter" {
		t.Fatalf("style/text changed: %q", got)
	}
}

func TestTextareaOverlayViewRetainsFocusedCursorStyle(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("handoff")
	ta.SetWidth(30)
	ta.Focus()
	raw := ta.View()
	view := textareaOverlayView(ta)
	if !strings.Contains(ansiStrip(view), "handoff") || strings.Contains(view, "\x1b[K") || strings.Contains(view, "\x1b[J") {
		t.Fatalf("focused textarea presentation changed unexpectedly: %q", view)
	}
	if strings.Contains(raw, "\x1b[") && !strings.Contains(view, "\x1b[") {
		t.Fatalf("textarea styling was stripped: raw=%q view=%q", raw, view)
	}
	if lipgloss.Width(view) > 30 {
		t.Fatalf("textarea width=%d", lipgloss.Width(view))
	}
}
