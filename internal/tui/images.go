package tui

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"os"
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
	if supportsKittyGraphics() {
		return imageProtocolKitty
	}
	if supportsSixelGraphics() {
		return imageProtocolSixel
	}
	return imageProtocolNone
}

func supportsKittyGraphics() bool {
	if strings.TrimSpace(os.Getenv("KITTY_WINDOW_ID")) != "" {
		return true
	}
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if termProgram == "kitty" || termProgram == "ghostty" || termProgram == "wezterm" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	return strings.Contains(term, "kitty") || strings.Contains(term, "xterm-kitty")
}

func supportsSixelGraphics() bool {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if termProgram == "wezterm" || termProgram == "iterm.app" || termProgram == "mlterm" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	return strings.Contains(term, "sixel") || strings.Contains(term, "mlterm")
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
	esc := fmt.Sprintf("\x1b_Ga=T,t=f,i=%d,C=1,c=%d,r=%d;%s\x1b\\", id, cols, rows, payload)
	blank := strings.Repeat(" ", cols)
	lines := make([]string, rows)
	lines[0] = esc + blank
	for i := 1; i < rows; i++ {
		lines[i] = "\x1b[0m" + blank
	}
	return lines
}

func kittyImageID(path string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return h.Sum32()
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
