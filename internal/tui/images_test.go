package tui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBareImagePathRendersAsTruncatedPlaceholder(t *testing.T) {
	stubNoGraphicsTerminal(t)

	long := "/tmp/pi-clipboard-9d536cd7-a40c-42e3-9307-5e1282184ee2.png"
	rendered := ansiStrip(renderMarkdownForInspector("see "+long+" please", 34, 4))
	if strings.Contains(rendered, long) {
		t.Fatalf("bare long path should be replaced: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if len([]rune(line)) > 34 {
			t.Fatalf("line too wide (%d): %q", len([]rune(line)), line)
		}
	}
	if !strings.Contains(rendered, "[image:") || !strings.Contains(rendered, "…") {
		t.Fatalf("rendered = %q", rendered)
	}
}

func TestImagePlaceholderTruncatesLongNamesToWidth(t *testing.T) {
	stubNoGraphicsTerminal(t)

	long := "/tmp/pi-clipboard-885c5b19-b68b-43b4-892d-d5a79361ad5d.png"
	preview := ansiStrip(renderBodyPreview("![]("+long+")", 30))
	for _, line := range strings.Split(preview, "\n") {
		if len([]rune(line)) > 30 {
			t.Fatalf("line too wide (%d): %q", len([]rune(line)), line)
		}
	}
	if !strings.Contains(preview, "…") {
		t.Fatalf("preview should be truncated: %q", preview)
	}
}

func TestRenderBodyPreviewShowsImagePlaceholderWithoutGraphics(t *testing.T) {
	stubNoGraphicsTerminal(t)

	preview := ansiStrip(renderBodyPreview("![](/tmp/example.png)", 40))
	if !strings.Contains(preview, "[image: example.png]") {
		t.Fatalf("preview = %q", preview)
	}
}

func TestRenderBodyPreviewFindsImageAfterIntroParagraph(t *testing.T) {
	stubNoGraphicsTerminal(t)

	body := "This ticket was created by the agent so you can verify attachment rendering.\n\n![](~/.local/share/agent-kanban/attachments/59/verification-001.png)\n\nExpected result: placeholder appears."
	preview := ansiStrip(renderBodyPreview(body, 60))
	if !strings.Contains(preview, "[image: verification-001.png]") {
		t.Fatalf("preview = %q", preview)
	}
}

func stubNoGraphicsTerminal(t *testing.T) {
	t.Helper()
	t.Setenv("AGENT_KANBAN_IMAGE_PROTOCOL", "")
	t.Setenv("TMUX", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("GHOSTTY_BIN_DIR", "")
	t.Setenv("WEZTERM_EXECUTABLE", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
}

func TestRenderMarkdownForInspectorShowsImagePlaceholderWithoutGraphics(t *testing.T) {
	stubNoGraphicsTerminal(t)

	body := "This ticket was created by the agent so you can verify attachment rendering.\n\n![](~/.local/share/agent-kanban/attachments/59/verification-001.png)"
	rendered := ansiStrip(renderMarkdownForInspector(body, 60, 8))
	if !strings.Contains(rendered, "[image: verification-001.png]") {
		t.Fatalf("rendered = %q", rendered)
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

	imagePath := writeTestPNG(t)
	rendered := renderMarkdownForInspector("before\n![]("+imagePath+")\nafter", 20, 8)
	if !strings.Contains(rendered, "\x1b_Ga=T,t=f,f=100,s=1,v=1") {
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

func writeTestPNG(t *testing.T) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "example.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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
