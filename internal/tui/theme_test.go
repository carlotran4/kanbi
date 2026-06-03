package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTUIColorLiteralsUseAdaptivePalette(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := file.Name()
		if file.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "lipgloss.Color(") {
			t.Fatalf("%s uses lipgloss.Color directly; add an AdaptiveColor token to palette instead", name)
		}
	}
}
