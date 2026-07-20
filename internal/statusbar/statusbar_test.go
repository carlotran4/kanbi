package statusbar

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestNormalizeDefaultsAndRejectsUnknownModules(t *testing.T) {
	cfg := Config{
		Left:  "$kanbi $board",
		Right: "${custom.usage}",
		Custom: map[string]CustomModule{
			"usage": {Command: []string{"usage-left"}},
		},
	}
	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	module := cfg.Custom["usage"]
	if cfg.CommandTimeoutDuration != 500*time.Millisecond || module.TimeoutDuration != 500*time.Millisecond || module.RefreshDuration != time.Minute || module.Format != "$output" {
		t.Fatalf("defaults not applied: cfg=%+v module=%+v", cfg, module)
	}

	cfg.Right = "$missing"
	if err := Normalize(&cfg); err == nil || !strings.Contains(err.Error(), "unknown module") {
		t.Fatalf("expected unknown module error, got %v", err)
	}
}

func TestRenderExpandsBuiltinsAndCachedCustomModules(t *testing.T) {
	cfg := Config{
		Left:   "$kanbi $board",
		Center: "${custom.usage}",
		Right:  "$time $$5",
		Time:   TimeModule{Format: "15:04"},
		Custom: map[string]CustomModule{
			"usage": {Command: []string{"usage-left"}, Format: "weekly $output"},
		},
	}
	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	left, center, right := Render(cfg, RenderContext{
		BoardName: "Client A",
		Now:       time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC),
		Results:   map[string]ModuleResult{"usage": {Value: "42% left"}},
	})
	if left != "Kanbi Client A" || center != "weekly 42% left" || right != "15:04 $5" {
		t.Fatalf("rendered left=%q center=%q right=%q", left, center, right)
	}

	_, center, _ = Render(cfg, RenderContext{Results: map[string]ModuleResult{"usage": {Value: "42% left", Err: "timeout"}}})
	if center != "weekly 42% left !" {
		t.Fatalf("stale failure indicator missing: %q", center)
	}
	_, center, _ = Render(cfg, RenderContext{Results: map[string]ModuleResult{"usage": {Err: "timeout"}}})
	if center != "!usage" {
		t.Fatalf("initial failure indicator missing: %q", center)
	}
}

func TestLayoutAnchorsZonesAndTruncatesCenterFirst(t *testing.T) {
	line := Layout("Kanbi Board", "weekly usage", "15:04", 40)
	if ansi.StringWidth(line) != 40 || !strings.HasPrefix(line, "Kanbi Board") || !strings.HasSuffix(line, "15:04") {
		t.Fatalf("bad anchored layout width=%d line=%q", ansi.StringWidth(line), line)
	}
	centerStart := strings.Index(line, "weekly usage")
	if centerStart != (40-len("weekly usage"))/2 {
		t.Fatalf("center start=%d line=%q", centerStart, line)
	}

	narrow := Layout("Kanbi Very Long Board", "weekly usage left", "15:04", 28)
	if ansi.StringWidth(narrow) != 28 || !strings.HasPrefix(narrow, "Kanbi Very Long Board") || !strings.HasSuffix(narrow, "15:04") || strings.Contains(narrow, "weekly usage left") {
		t.Fatalf("collision policy failed: %q width=%d", narrow, ansi.StringWidth(narrow))
	}

	unicodeLine := Layout("Kanbi 開発", "50%", "🕒", 24)
	if ansi.StringWidth(unicodeLine) != 24 {
		t.Fatalf("unicode width=%d line=%q", ansi.StringWidth(unicodeLine), unicodeLine)
	}
}

func TestCustomNamesOnlyReturnsReferencedModules(t *testing.T) {
	cfg := Config{
		Left:  "${custom.used}",
		Right: "$$${custom.also_used}",
		Custom: map[string]CustomModule{
			"used":      {Command: []string{"used"}},
			"also_used": {Command: []string{"also-used"}},
			"unused":    {Command: []string{"unused"}},
		},
	}
	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(CustomNames(cfg), ","); got != "also_used,used" {
		t.Fatalf("active custom modules=%q", got)
	}
}

func TestRunUsesDirectCommandContextAndSanitizesOutput(t *testing.T) {
	module := CustomModule{
		Command:         []string{"sh", "-c", `printf '\033[31m%s\033[0m\nignored' "$KANBI_BOARD_NAME"`},
		TimeoutDuration: time.Second,
	}
	output, err := Run(context.Background(), module, CommandContext{BoardName: "Client A", Master: true})
	if err != nil {
		t.Fatal(err)
	}
	if output != "Client A" {
		t.Fatalf("output=%q", output)
	}
}

func TestRunTimesOut(t *testing.T) {
	module := CustomModule{
		Command:         []string{"sh", "-c", "sleep 1"},
		TimeoutDuration: 20 * time.Millisecond,
	}
	_, err := Run(context.Background(), module, CommandContext{})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
}
