package statusbar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const (
	defaultCommandTimeout  = 500 * time.Millisecond
	defaultRefreshInterval = time.Minute
	maxCommandOutputBytes  = 1024
	maxCommandOutputWidth  = 256
)

var (
	modulePattern     = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_.-]*)\}|([A-Za-z_][A-Za-z0-9_.-]*))`)
	customNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
)

// Config describes the three status-bar zones and their modules.
type Config struct {
	Left           string                  `yaml:"left"`
	Center         string                  `yaml:"center"`
	Right          string                  `yaml:"right"`
	CommandTimeout string                  `yaml:"command_timeout"`
	Time           TimeModule              `yaml:"time"`
	Custom         map[string]CustomModule `yaml:"custom"`

	CommandTimeoutDuration time.Duration `yaml:"-"`
}

type TimeModule struct {
	Format string `yaml:"format"`
}

type CustomModule struct {
	Command         []string `yaml:"command"`
	RefreshInterval string   `yaml:"refresh_interval"`
	Timeout         string   `yaml:"timeout"`
	Format          string   `yaml:"format"`

	RefreshDuration time.Duration `yaml:"-"`
	TimeoutDuration time.Duration `yaml:"-"`
}

// ModuleResult is the cached state of a custom module. A failed refresh keeps
// the last successful value while making the failure visible in the bar.
type ModuleResult struct {
	Value string
	Err   string
}

type RenderContext struct {
	BoardName string
	Now       time.Time
	Results   map[string]ModuleResult
}

type CommandContext struct {
	BoardName string
	Workdir   string
	Master    bool
}

func DefaultConfig() Config {
	cfg := Config{
		Left:           "$kanbi $board",
		CommandTimeout: defaultCommandTimeout.String(),
		Time:           TimeModule{Format: "15:04"},
	}
	_ = Normalize(&cfg)
	return cfg
}

// Normalize applies module defaults and rejects invalid formats eagerly so a
// typo in config does not fail repeatedly during rendering.
func Normalize(cfg *Config) error {
	if cfg == nil {
		return errors.New("status bar config is nil")
	}
	if cfg.CommandTimeout == "" {
		cfg.CommandTimeout = defaultCommandTimeout.String()
	}
	commandTimeout, err := positiveDuration("status_bar.command_timeout", cfg.CommandTimeout)
	if err != nil {
		return err
	}
	cfg.CommandTimeoutDuration = commandTimeout
	if cfg.Time.Format == "" {
		cfg.Time.Format = "15:04"
	}

	for name, module := range cfg.Custom {
		if !customNamePattern.MatchString(name) {
			return fmt.Errorf("invalid status_bar custom module name %q", name)
		}
		if len(module.Command) == 0 || strings.TrimSpace(module.Command[0]) == "" {
			return fmt.Errorf("status_bar.custom.%s.command must not be empty", name)
		}
		if module.RefreshInterval == "" {
			module.RefreshInterval = defaultRefreshInterval.String()
		}
		module.RefreshDuration, err = positiveDuration("status_bar.custom."+name+".refresh_interval", module.RefreshInterval)
		if err != nil {
			return err
		}
		if module.Timeout == "" {
			module.TimeoutDuration = commandTimeout
			module.Timeout = commandTimeout.String()
		} else {
			module.TimeoutDuration, err = positiveDuration("status_bar.custom."+name+".timeout", module.Timeout)
			if err != nil {
				return err
			}
		}
		if module.Format == "" {
			module.Format = "$output"
		}
		cfg.Custom[name] = module
	}

	for zone, format := range map[string]string{"left": cfg.Left, "center": cfg.Center, "right": cfg.Right} {
		if err := validateFormat(format, cfg.Custom); err != nil {
			return fmt.Errorf("invalid status_bar.%s: %w", zone, err)
		}
	}
	return nil
}

func positiveDuration(field, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", field, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", field)
	}
	return d, nil
}

func validateFormat(format string, custom map[string]CustomModule) error {
	for _, match := range modulePattern.FindAllStringSubmatch(strings.ReplaceAll(format, "$$", ""), -1) {
		name := match[1]
		if name == "" {
			name = match[2]
		}
		switch name {
		case "kanbi", "board", "time":
			continue
		}
		if strings.HasPrefix(name, "custom.") {
			if _, ok := custom[strings.TrimPrefix(name, "custom.")]; ok {
				continue
			}
		}
		return fmt.Errorf("unknown module $%s", name)
	}
	return nil
}

// Render expands each configured zone. Command output is always treated as
// literal text and cannot inject another module or terminal escape sequence.
func Render(cfg Config, ctx RenderContext) (left, center, right string) {
	expand := func(format string) string {
		const dollarSentinel = "\x00KANBI_DOLLAR\x00"
		format = strings.ReplaceAll(format, "$$", dollarSentinel)
		result := modulePattern.ReplaceAllStringFunc(format, func(token string) string {
			match := modulePattern.FindStringSubmatch(token)
			name := match[1]
			if name == "" {
				name = match[2]
			}
			switch name {
			case "kanbi":
				return "Kanbi"
			case "board":
				return ctx.BoardName
			case "time":
				return ctx.Now.Format(cfg.Time.Format)
			}
			if strings.HasPrefix(name, "custom.") {
				customName := strings.TrimPrefix(name, "custom.")
				module, ok := cfg.Custom[customName]
				if !ok {
					return ""
				}
				cached := ctx.Results[customName]
				if cached.Value == "" {
					if cached.Err != "" {
						return "!" + customName
					}
					return ""
				}
				value := strings.ReplaceAll(module.Format, "$output", cached.Value)
				if cached.Err != "" {
					value += " !"
				}
				return value
			}
			return ""
		})
		result = strings.ReplaceAll(result, dollarSentinel, "$")
		return strings.TrimSpace(sanitize(result))
	}
	return expand(cfg.Left), expand(cfg.Center), expand(cfg.Right)
}

// Layout anchors the three zones. Center is truncated first on collisions,
// followed by right; left is retained for board identity as long as possible.
func Layout(left, center, right string, width int) string {
	if width <= 0 {
		return ""
	}
	left = sanitize(left)
	center = sanitize(center)
	right = sanitize(right)

	leftWidth := ansi.StringWidth(left)
	rightWidth := ansi.StringWidth(right)
	gap := 0
	if left != "" && right != "" {
		gap = 1
	}
	if leftWidth+gap+rightWidth > width {
		rightRoom := width - leftWidth - gap
		if rightRoom < 0 {
			rightRoom = 0
		}
		right = ansi.Truncate(right, rightRoom, "…")
		rightWidth = ansi.StringWidth(right)
	}
	if leftWidth+gap+rightWidth > width {
		leftRoom := width - rightWidth - gap
		if leftRoom < 0 {
			leftRoom = 0
		}
		left = ansi.Truncate(left, leftRoom, "…")
		leftWidth = ansi.StringWidth(left)
	}

	rightStart := width - rightWidth
	centerLeft := leftWidth
	if left != "" {
		centerLeft++
	}
	centerRight := rightStart
	if right != "" {
		centerRight--
	}
	centerRoom := centerRight - centerLeft + 1
	if centerRoom < 0 {
		centerRoom = 0
	}
	center = ansi.Truncate(center, centerRoom, "…")
	centerWidth := ansi.StringWidth(center)
	centerStart := (width - centerWidth) / 2
	if centerStart < centerLeft {
		centerStart = centerLeft
	}
	if centerStart+centerWidth > rightStart {
		centerStart = rightStart - centerWidth
	}
	if centerStart < leftWidth {
		center = ""
		centerWidth = 0
		centerStart = leftWidth
	}

	var out strings.Builder
	out.WriteString(left)
	position := leftWidth
	if center != "" {
		out.WriteString(strings.Repeat(" ", max(0, centerStart-position)))
		out.WriteString(center)
		position = centerStart + centerWidth
	}
	if right != "" {
		out.WriteString(strings.Repeat(" ", max(0, rightStart-position)))
		out.WriteString(right)
		position = rightStart + rightWidth
	}
	out.WriteString(strings.Repeat(" ", max(0, width-position)))
	return out.String()
}

// CustomNames returns referenced custom modules in stable scheduling order.
// Merely defining a module does not execute it.
func CustomNames(cfg Config) []string {
	active := make(map[string]struct{})
	formats := []string{cfg.Left, cfg.Center, cfg.Right}
	for _, format := range formats {
		for _, match := range modulePattern.FindAllStringSubmatch(strings.ReplaceAll(format, "$$", ""), -1) {
			name := match[1]
			if name == "" {
				name = match[2]
			}
			if strings.HasPrefix(name, "custom.") {
				active[strings.TrimPrefix(name, "custom.")] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(active))
	for name := range active {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Run executes one custom module without an implicit shell.
func Run(parent context.Context, module CustomModule, values CommandContext) (string, error) {
	ctx, cancel := context.WithTimeout(parent, module.TimeoutDuration)
	defer cancel()

	cmd := exec.CommandContext(ctx, module.Command[0], module.Command[1:]...)
	if values.Workdir != "" {
		cmd.Dir = values.Workdir
	}
	cmd.Env = append(os.Environ(),
		"KANBI_BOARD_NAME="+values.BoardName,
		"KANBI_BOARD_WORKDIR="+values.Workdir,
		fmt.Sprintf("KANBI_MASTER=%t", values.Master),
	)
	var stdout, stderr limitedBuffer
	stdout.limit = maxCommandOutputBytes
	stderr.limit = maxCommandOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("command timed out after %s", module.TimeoutDuration)
		}
		detail := strings.TrimSpace(sanitize(stderr.String()))
		if detail != "" {
			return "", fmt.Errorf("command failed: %w: %s", err, detail)
		}
		return "", fmt.Errorf("command failed: %w", err)
	}
	return sanitizeCommandOutput(stdout.Bytes()), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return original, nil
}

func sanitizeCommandOutput(output []byte) string {
	line := string(output)
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(sanitize(line))
	return ansi.Truncate(line, maxCommandOutputWidth, "…")
}

func sanitize(value string) string {
	value = strings.ToValidUTF8(ansi.Strip(value), "�")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
