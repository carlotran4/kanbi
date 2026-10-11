package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLoadOmarchyPalette(t *testing.T) {
	path := filepath.Join(t.TempDir(), "colors.toml")
	data := `accent = "#7aa2f7"
selection = "#292e42"
muted = "#414868"
lighter_background = "#24283b"
foreground = "#a9b1d6"
bright_foreground = "#c0caf5"
red = "#f7768e"
yellow = "#e0af68"
green = "#9ece6a"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got := loadOmarchyPalette(path, theme{})
	for _, tc := range []struct {
		name  string
		color lipgloss.TerminalColor
		want  string
	}{
		{"card", got.card, "#24283b"},
		{"selected", got.cardSelected, "#292e42"},
		{"warning", got.warning, "#e0af68"},
	} {
		color, ok := tc.color.(lipgloss.AdaptiveColor)
		if !ok || color.Dark != tc.want || color.Light != tc.want {
			t.Errorf("%s = %#v, want %s", tc.name, tc.color, tc.want)
		}
	}

	if err := os.WriteFile(path, []byte(strings.Replace(data, "#e0af68", "invalid", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	fallback := theme{warning: got.warning}
	if actual := loadOmarchyPalette(path, fallback); actual.warning != fallback.warning || actual.card != nil {
		t.Fatal("invalid palette must keep defaults intact")
	}
}
