package tui

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var markdownImageRE = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)

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
	matches := markdownImageRE.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return nil
	}
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
		out = append(out, renderImageBlock(path, cols, maxRows)...)
		last = fullEnd
	}
	if after := strings.TrimSpace(line[last:]); after != "" {
		out = append(out, after)
	}
	return out
}

func renderImageBlock(path string, cols int, maxRows int) []string {
	if maxRows <= 0 {
		return nil
	}
	if cols < 8 {
		cols = 8
	}
	if maxRows > 4 {
		maxRows = 4
	}
	resolved := expandImagePath(path)
	switch terminalImageProtocol() {
	case imageProtocolKitty:
		return renderKittyImage(resolved, cols, maxRows)
	case imageProtocolSixel:
		return []string{imagePlaceholder(path) + " (sixel preview unavailable)"}
	default:
		return []string{imagePlaceholder(path)}
	}
}

func renderKittyImage(path string, cols int, rows int) []string {
	payload := base64.StdEncoding.EncodeToString([]byte(path))
	id := kittyImageID(path)
	// a=T transmits/displays, t=f means payload is a file path, C=1 keeps cursor
	// movement predictable for TUI layouts, c/r bound the image to terminal cells.
	esc := tmuxPassthrough(fmt.Sprintf("\x1b_Ga=T,t=f,i=%d,C=1,c=%d,r=%d;%s\x1b\\", id, cols, rows, payload))
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

func imagePlaceholder(path string) string {
	base := filepath.Base(expandImagePath(path))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = path
	}
	return lipgloss.NewStyle().Foreground(palette.muted).Render("[image: " + base + "]")
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
