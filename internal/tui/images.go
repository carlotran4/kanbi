package tui

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	markdownImageRE = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	bareImagePathRE = regexp.MustCompile(`(?:~|/|\.)[^\s)\]]+\.(?:png|jpg|jpeg|gif|webp)\b`)
)

type imageProtocol int

const (
	imageProtocolNone imageProtocol = iota
	imageProtocolKitty
	imageProtocolSixel
)

func terminalImageProtocol() imageProtocol {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_KANBAN_IMAGE_PROTOCOL"))) {
	case "kitty":
		return imageProtocolKitty
	case "sixel":
		return imageProtocolSixel
	case "none", "off", "placeholder":
		return imageProtocolNone
	}
	if supportsKittyGraphics() {
		return imageProtocolKitty
	}
	if supportsSixelGraphics() {
		return imageProtocolSixel
	}
	return imageProtocolNone
}

func supportsKittyGraphics() bool {
	if strings.TrimSpace(terminalEnv("KITTY_WINDOW_ID")) != "" {
		return true
	}
	if strings.TrimSpace(terminalEnv("GHOSTTY_BIN_DIR")) != "" || strings.TrimSpace(terminalEnv("WEZTERM_EXECUTABLE")) != "" {
		return true
	}
	termProgram := strings.ToLower(terminalEnv("TERM_PROGRAM"))
	if termProgram == "kitty" || termProgram == "ghostty" || termProgram == "wezterm" {
		return true
	}
	term := strings.ToLower(terminalEnv("TERM"))
	return strings.Contains(term, "kitty") || strings.Contains(term, "xterm-kitty")
}

func supportsSixelGraphics() bool {
	termProgram := strings.ToLower(terminalEnv("TERM_PROGRAM"))
	if termProgram == "wezterm" || termProgram == "iterm.app" || termProgram == "mlterm" {
		return true
	}
	term := strings.ToLower(terminalEnv("TERM"))
	return strings.Contains(term, "sixel") || strings.Contains(term, "mlterm")
}

func terminalEnv(name string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return ""
	}
	out, err := exec.Command("tmux", "show-environment", "-g", name).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if strings.HasPrefix(line, name+"=") {
		return strings.TrimPrefix(line, name+"=")
	}
	return ""
}

func renderMarkdownImagesInline(line string, cols int, maxRows int) []string {
	return renderMarkdownImagesInlineWithGraphics(line, cols, maxRows, true)
}

func renderMarkdownImagesPlaceholder(line string, cols int, maxRows int) []string {
	return renderMarkdownImagesInlineWithGraphics(line, cols, maxRows, false)
}

func renderMarkdownImagesInlineWithGraphics(line string, cols int, maxRows int, graphics bool) []string {
	if matches := markdownImageRE.FindAllStringSubmatchIndex(line, -1); len(matches) > 0 {
		var out []string
		last := 0
		for _, match := range matches {
			fullStart, fullEnd := match[0], match[1]
			pathStart, pathEnd := match[2], match[3]
			before := strings.TrimSpace(line[last:fullStart])
			if before != "" {
				out = append(out, before)
			}
			path := cleanMarkdownImagePath(line[pathStart:pathEnd])
			out = append(out, renderImageBlock(path, cols, maxRows, graphics)...)
			last = fullEnd
		}
		if after := strings.TrimSpace(line[last:]); after != "" {
			out = append(out, after)
		}
		return out
	}
	matches := bareImagePathRE.FindAllStringIndex(line, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []string
	last := 0
	for _, match := range matches {
		fullStart, fullEnd := match[0], match[1]
		before := strings.TrimSpace(line[last:fullStart])
		if before != "" {
			out = append(out, before)
		}
		out = append(out, renderImageBlock(line[fullStart:fullEnd], cols, maxRows, graphics)...)
		last = fullEnd
	}
	if after := strings.TrimSpace(line[last:]); after != "" {
		out = append(out, after)
	}
	return out
}

func renderImageBlock(path string, cols int, maxRows int, graphics bool) []string {
	if maxRows <= 0 {
		return nil
	}
	if cols < 8 {
		cols = 8
	}
	if maxRows > 4 {
		maxRows = 4
	}
	if !graphics {
		return []string{imagePlaceholderForWidth(path, cols)}
	}
	resolved := expandImagePath(path)
	switch terminalImageProtocol() {
	case imageProtocolKitty:
		return renderKittyImage(resolved, cols, maxRows)
	case imageProtocolSixel:
		return []string{imagePlaceholderForWidth(path, cols) + " (sixel preview unavailable)"}
	default:
		return []string{imagePlaceholderForWidth(path, cols)}
	}
}

func renderKittyImage(path string, cols int, rows int) []string {
	id := kittyImageID(path)
	format, pxW, pxH, ok := kittyImageFileMetadata(path)
	if !ok {
		return []string{imagePlaceholder(path)}
	}
	payload := base64.StdEncoding.EncodeToString([]byte(path))
	// a=T transmits/displays, t=f means payload is a file path. Kitty-compatible
	// terminals such as Ghostty require image format and pixel dimensions for
	// file payloads; otherwise they report EINVAL: dimensions required.
	// C=1 keeps cursor movement predictable for TUI layouts, c/r bound the image
	// to terminal cells.
	esc := tmuxPassthrough(fmt.Sprintf("\x1b_Ga=T,t=f,f=%d,s=%d,v=%d,i=%d,C=1,c=%d,r=%d;%s\x1b\\", format, pxW, pxH, id, cols, rows, payload))
	lines := make([]string, rows)
	lines[0] = esc
	for i := 1; i < rows-1; i++ {
		lines[i] = " "
	}
	if rows > 1 {
		lines[rows-1] = imagePlaceholder(path)
	}
	return lines
}

func kittyImageFileMetadata(path string) (format int, width int, height int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	cfg, name, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, 0, false
	}
	switch name {
	case "png":
		format = 100
	case "jpeg":
		format = 100
	case "gif":
		format = 100
	default:
		return 0, 0, 0, false
	}
	return format, cfg.Width, cfg.Height, true
}

func kittyImageID(path string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return h.Sum32()
}

func tmuxPassthrough(seq string) string {
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return seq
	}
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

func containsImageEscape(s string) bool {
	return strings.Contains(s, "\x1b_G") || strings.Contains(s, "\x1bPtmux;")
}

func clearKittyImagesCmd() tea.Cmd {
	seq := clearKittyImagesSeq()
	if seq == "" {
		return nil
	}
	return func() tea.Msg {
		fmt.Print(seq)
		return nil
	}
}

func withClearKittyImages(cmd tea.Cmd) tea.Cmd {
	seq := clearKittyImagesSeq()
	if seq == "" || cmd == nil {
		return cmd
	}
	return func() tea.Msg {
		fmt.Print(seq)
		return cmd()
	}
}

func clearKittyImagesSeq() string {
	if terminalImageProtocol() != imageProtocolKitty {
		return ""
	}
	return tmuxPassthrough("\x1b_Ga=d,d=A\x1b\\")
}

func imagePlaceholder(path string) string {
	return imagePlaceholderForWidth(path, 0)
}

func imagePlaceholderForWidth(path string, width int) string {
	base := filepath.Base(expandImagePath(path))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = path
	}
	label := "[image: " + base + "]"
	if width > 0 && lipgloss.Width(label) > width {
		label = trimImageLabel(label, width)
	}
	return lipgloss.NewStyle().Foreground(palette.muted).Render(label)
}

func trimImageLabel(label string, width int) string {
	if width <= 1 {
		return "…"
	}
	runes := []rune(label)
	if len(runes) <= width {
		return label
	}
	if width <= 12 {
		return string(runes[:width-1]) + "…"
	}
	prefix := width / 2
	suffix := width - prefix - 1
	return string(runes[:prefix]) + "…" + string(runes[len(runes)-suffix:])
}

func cleanMarkdownImagePath(path string) string {
	path = strings.TrimSpace(path)
	if unquoted, err := strconv.Unquote(path); err == nil {
		path = unquoted
	}
	if idx := strings.IndexAny(path, " \t"); idx >= 0 {
		path = path[:idx]
	}
	return path
}

func expandImagePath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
