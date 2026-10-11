package tui

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// activeTerminalTheme uses Omarchy's active terminal palette when it is
// available. On other systems, the ANSI/adaptive defaults remain in place.
func activeTerminalTheme(base theme) theme {
	home, err := os.UserHomeDir()
	if err != nil {
		return base
	}
	return loadOmarchyPalette(filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "colors.toml"), base)
}

func loadOmarchyPalette(path string, base theme) theme {
	file, err := os.Open(path)
	if err != nil {
		return base
	}
	defer file.Close()

	colors := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if len(value) == 7 && value[0] == '#' {
			if _, err := strconv.ParseUint(value[1:], 16, 24); err == nil {
				colors[key] = value
			}
		}
	}
	if scanner.Err() != nil {
		return base
	}
	for _, key := range []string{"accent", "selection", "muted", "lighter_background", "foreground", "bright_foreground", "red", "yellow", "green"} {
		if colors[key] == "" {
			return base
		}
	}
	adaptive := func(value string) lipgloss.TerminalColor {
		return lipgloss.AdaptiveColor{Light: value, Dark: value}
	}
	base.muted = adaptive(colors["foreground"])
	base.rule = adaptive(colors["muted"])
	base.backdrop = adaptive(colors["muted"])
	base.accent = adaptive(colors["accent"])
	base.warning = adaptive(colors["yellow"])
	base.error_ = adaptive(colors["red"])
	base.success = adaptive(colors["green"])
	base.card = adaptive(colors["lighter_background"])
	base.cardSelected = adaptive(colors["selection"])
	base.cardMuted = adaptive(colors["foreground"])
	base.header = adaptive(colors["lighter_background"])
	base.headerText = adaptive(colors["bright_foreground"])
	base.headerSubtleText = adaptive(colors["foreground"])
	return base
}
