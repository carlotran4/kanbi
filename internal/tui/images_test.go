package tui

import (
	"strings"
	"testing"
)

func TestRenderBodyPreviewShowsImagePlaceholderWithoutGraphics(t *testing.T) {
	t.Setenv("AGENT_KANBAN_IMAGE_PROTOCOL", "")
	t.Setenv("TMUX", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("GHOSTTY_BIN_DIR", "")
	t.Setenv("WEZTERM_EXECUTABLE", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")

	preview := ansiStrip(renderBodyPreview("![](/tmp/example.png)", 40))
	if !strings.Contains(preview, "[image: example.png]") {
		t.Fatalf("preview = %q", preview)
	}
}

func TestRenderMarkdownForInspectorEmitsKittyGraphics(t *testing.T) {
	t.Setenv("AGENT_KANBAN_IMAGE_PROTOCOL", "")
	t.Setenv("TMUX", "")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("GHOSTTY_BIN_DIR", "")
	t.Setenv("WEZTERM_EXECUTABLE", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")

	rendered := renderMarkdownForInspector("before\n![](/tmp/example.png)\nafter", 20, 8)
	if !strings.Contains(rendered, "\x1b_Ga=T,t=f") {
		t.Fatalf("kitty escape missing: %q", rendered)
	}
	if !strings.Contains(rendered, "before") || !strings.Contains(rendered, "after") {
		t.Fatalf("surrounding text missing: %q", rendered)
	}
	if !strings.Contains(rendered, "[image: example.png]") {
		t.Fatalf("caption fallback missing: %q", rendered)
	}
	if got := strings.Count(rendered, "\n"); got < 4 {
		t.Fatalf("expected image rows to be reserved, got %d newlines in %q", got, rendered)
	}
}

func TestTerminalImageProtocolDetectsSixelFallback(t *testing.T) {
	t.Setenv("AGENT_KANBAN_IMAGE_PROTOCOL", "")
	t.Setenv("TMUX", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("GHOSTTY_BIN_DIR", "")
	t.Setenv("WEZTERM_EXECUTABLE", "")
	t.Setenv("TERM_PROGRAM", "WezTerm")
	t.Setenv("TERM", "xterm-256color")

	if terminalImageProtocol() != imageProtocolKitty {
		t.Fatalf("wezterm should use kitty protocol when available")
	}

	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if terminalImageProtocol() != imageProtocolSixel {
		t.Fatalf("iterm should be detected as sixel-capable")
	}
}
