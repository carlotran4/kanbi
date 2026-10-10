package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestResolvePathsUsesXDGAndEnvOverrides(t *testing.T) {
	clearAgentEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths := ResolvePaths()
	if paths.ConfigFile != filepath.Join(home, "cfg", AppName, "config.yaml") {
		t.Fatalf("config path = %s", paths.ConfigFile)
	}
	if paths.DBFile != filepath.Join(home, "data", AppName, "kanbi.db") {
		t.Fatalf("db path = %s", paths.DBFile)
	}
	t.Setenv("KANBI_DB", filepath.Join(home, "override.db"))
	t.Setenv("KANBI_CONFIG", filepath.Join(home, "override.yaml"))
	paths = ResolvePaths()
	if paths.DBFile != filepath.Join(home, "override.db") || paths.ConfigFile != filepath.Join(home, "override.yaml") {
		t.Fatalf("env overrides not applied: %+v", paths)
	}
}

func TestEnsureDirsCreatesPrivateDirectoriesWithoutChangingSharedParent(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	root := t.TempDir()
	paths := Paths{ConfigFile: filepath.Join(root, "config", "config.yaml"), DataDir: filepath.Join(root, "data"), StateDir: filepath.Join(root, "state"), DBFile: filepath.Join(root, "db", "kanbi.db")}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("{}"), 0o666); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Paths: paths, DBPath: paths.DBFile}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{paths.DataDir, paths.StateDir, filepath.Dir(paths.DBFile)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode=%o", p, info.Mode().Perm())
		}
	}
	configParent, _ := os.Stat(filepath.Dir(paths.ConfigFile))
	if configParent.Mode().Perm() != 0o777 {
		t.Fatalf("explicit existing config parent mode changed to %o", configParent.Mode().Perm())
	}
	info, _ := os.Stat(paths.ConfigFile)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode=%o", info.Mode().Perm())
	}
}

func TestNormalizeDiagnosticsEnvOverridesConfig(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	cfg, err := Normalize(Config{Diagnostics: Diagnostics{Level: "warn"}}, paths, NormalizeOptions{Env: Env{LogLevel: "debug"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Diagnostics.Level != "debug" || cfg.Diagnostics.MaxBytes != 1<<20 || cfg.Diagnostics.MaxFiles != 3 {
		t.Fatalf("diagnostics=%+v", cfg.Diagnostics)
	}
}

func TestNormalizeDefaultsFromInMemoryRawConfig(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	cfg, err := Normalize(Config{}, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths != paths || cfg.DBPath != paths.DBFile || cfg.DefaultHarness != "pi" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.Diagnostics.Level != "off" {
		t.Fatalf("diagnostics default=%+v", cfg.Diagnostics)
	}
	if cfg.Multiplexer.Default != "herdr" || cfg.Multiplexer.Herdr.Binary != "herdr" {
		t.Fatalf("multiplexer defaults not applied: %+v", cfg.Multiplexer)
	}
	if cfg.PromptReadyTimeout != 5*time.Second || cfg.IdleUnknownAfter != 120*time.Second || cfg.GracefulExitTimeout != 15*time.Second {
		t.Fatalf("timeout defaults not applied: %+v", cfg)
	}
	if len(cfg.Harnesses["pi"].Start) == 0 || len(cfg.Harnesses["codex"].Start) == 0 || len(cfg.Harnesses["copilot"].Start) == 0 {
		t.Fatalf("default harnesses not applied: %+v", cfg.Harnesses)
	}
}

func TestNormalizeStatusBarDefaultsAndOverrides(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	cfg, err := Normalize(Config{}, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusBar == nil || cfg.StatusBar.Left != "$kanbi $board" || cfg.StatusBar.CommandTimeoutDuration != 500*time.Millisecond {
		t.Fatalf("status bar defaults=%+v", cfg.StatusBar)
	}

	cfgFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(`
status_bar:
  left: "$kanbi"
  center: "${custom.usage}"
  right: "$time"
  command_timeout: 750ms
  time:
    format: "15:04:05"
  custom:
    usage:
      command: ["usage-left", "--weekly"]
      refresh_interval: 5m
      format: "weekly $output"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _, err := LoadRaw(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = Normalize(raw, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	usage := cfg.StatusBar.Custom["usage"]
	if cfg.StatusBar.Left != "$kanbi" || cfg.StatusBar.Center != "${custom.usage}" || cfg.StatusBar.Right != "$time" || cfg.StatusBar.CommandTimeoutDuration != 750*time.Millisecond || usage.RefreshDuration != 5*time.Minute || usage.TimeoutDuration != 750*time.Millisecond {
		t.Fatalf("status bar override=%+v usage=%+v", cfg.StatusBar, usage)
	}
}

func TestNormalizeRejectsInvalidStatusBar(t *testing.T) {
	paths := Paths{DBFile: "/data/kanbi.db"}
	cfgFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("status_bar:\n  left: '$missing'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _, err := LoadRaw(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(raw, paths, NormalizeOptions{}); err == nil || !strings.Contains(err.Error(), "unknown module") {
		t.Fatalf("expected status bar validation error, got %v", err)
	}
}

func TestNormalizeEnvOverridesInMemoryRawConfig(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	raw := Config{DBPath: "/from/config.db"}
	cfg, err := Normalize(raw, paths, NormalizeOptions{Env: Env{DBPath: "/from/env.db"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != "/from/env.db" {
		t.Fatalf("env overrides not applied during normalize: %+v", cfg)
	}
}

func TestNormalizeMultiplexerConfigAndEnvOverride(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	raw := Config{Multiplexer: Multiplexer{
		Default: "tmux",
		Herdr:   Herdr{Binary: "/bin/herdr", Session: "work", WorkspaceStrategy: "board", TabStrategy: "tickets", FocusOnOpen: true},
	}}
	cfg, err := Normalize(raw, paths, NormalizeOptions{Env: Env{DefaultMultiplexer: "herdr"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Multiplexer.Default != "herdr" {
		t.Fatalf("env multiplexer override not applied: %+v", cfg.Multiplexer)
	}
	if cfg.Multiplexer.Herdr.Binary != "/bin/herdr" || cfg.Multiplexer.Herdr.Session != "work" || !cfg.Multiplexer.Herdr.FocusOnOpen {
		t.Fatalf("herdr config not applied: %+v", cfg.Multiplexer.Herdr)
	}
}

func TestNormalizeRejectsRetiredRuntime(t *testing.T) {
	_, err := Normalize(Config{Multiplexer: Multiplexer{Default: "tmux"}}, Paths{}, NormalizeOptions{})
	if err == nil || !strings.Contains(err.Error(), "set multiplexer.default: herdr") {
		t.Fatalf("expected migration guidance: %v", err)
	}
}

func TestNormalizeNestedTimeoutsAndLegacyPromptTimeout(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	raw := Config{
		PromptReadyRaw: "2s",
		Timeouts: Timeouts{
			IdleUnknownAfterSeconds:    5,
			GracefulExitTimeoutSeconds: 2,
			PromptReadyTimeoutSeconds:  3,
		},
	}
	cfg, err := Normalize(raw, paths, NormalizeOptions{NestedPromptReadyTimeoutSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PromptReadyTimeout != 3*time.Second || cfg.IdleUnknownAfter != 5*time.Second || cfg.GracefulExitTimeout != 2*time.Second {
		t.Fatalf("nested timeouts not applied: %+v", cfg)
	}

	raw.Timeouts.PromptReadyTimeoutSeconds = 0
	cfg, err = Normalize(raw, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PromptReadyTimeout != 2*time.Second {
		t.Fatalf("legacy prompt timeout not applied: %+v", cfg)
	}
}

func TestNormalizeInvalidPromptReadyTimeout(t *testing.T) {
	_, err := Normalize(Config{PromptReadyRaw: "not-a-duration"}, Paths{DBFile: "/tmp/db"}, NormalizeOptions{})
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
	if got := err.Error(); got != `invalid prompt_ready_timeout "not-a-duration": time: invalid duration "not-a-duration"` {
		t.Fatalf("error = %q", got)
	}
}

func TestLoadDefaultsAndConfigOverrides(t *testing.T) {
	clearAgentEnv(t)
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(`
db_path: /tmp/custom.db
tmux_session: custom-session
prompt_ready_timeout: 2s
harnesses:
  pi:
    start: ["/tmp/fake-pi", "start"]
    resume: ["/tmp/fake-pi", "resume", "{session_ref}"]
    prompt_ready: "READY"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KANBI_CONFIG", cfgFile)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != "/tmp/custom.db" {
		t.Fatalf("override failed: %+v", cfg)
	}
	if cfg.PromptReadyTimeout != 2*time.Second {
		t.Fatalf("timeout = %s", cfg.PromptReadyTimeout)
	}
	if got := cfg.Harnesses["pi"].Start[0]; got != "/tmp/fake-pi" {
		t.Fatalf("pi start = %s", got)
	}
	if len(cfg.Harnesses["codex"].Start) == 0 {
		t.Fatalf("defaults were not merged")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	clearAgentEnv(t)
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("harnesses: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KANBI_CONFIG", cfgFile)
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid yaml error")
	}
}

func TestLoadNestedTimeoutConfigPrecedence(t *testing.T) {
	clearAgentEnv(t)
	tests := []struct {
		name    string
		content string
		want    time.Duration
	}{
		{
			name: "comment mentioning timeouts does not override legacy prompt timeout",
			content: `
prompt_ready_timeout: 30s
# timeouts:
`,
			want: 30 * time.Second,
		},
		{
			name: "partial nested timeouts do not override legacy prompt timeout",
			content: `
prompt_ready_timeout: 30s
timeouts:
  idle_unknown_after_seconds: 10
`,
			want: 30 * time.Second,
		},
		{
			name: "nested prompt timeout overrides legacy prompt timeout",
			content: `
prompt_ready_timeout: 30s
timeouts:
  prompt_ready_timeout_seconds: 3
`,
			want: 3 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgFile := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(cfgFile, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KANBI_CONFIG", cfgFile)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PromptReadyTimeout != tt.want {
				t.Fatalf("prompt-ready timeout = %s, want %s", cfg.PromptReadyTimeout, tt.want)
			}
		})
	}
}

func TestLoadNestedDesignSpecConfig(t *testing.T) {
	clearAgentEnv(t)
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(`
default_harness: codex
tmux:
  session_name: spec-session
  board_window_name: board-main
timeouts:
  idle_unknown_after_seconds: 5
  graceful_exit_timeout_seconds: 2
  prompt_ready_timeout_seconds: 3
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KANBI_CONFIG", cfgFile)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "codex" {
		t.Fatalf("nested config not applied: %+v", cfg)
	}
	if cfg.PromptReadyTimeout != 3*time.Second || cfg.IdleUnknownAfter != 5*time.Second || cfg.GracefulExitTimeout != 2*time.Second {
		t.Fatalf("timeouts not applied: %+v", cfg)
	}
}

func TestLoadIgnoresRemovedAutoCloseTimeout(t *testing.T) {
	clearAgentEnv(t)
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("timeouts:\n  auto_close_waiting_after_minutes: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KANBI_CONFIG", cfgFile)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdleUnknownAfter != 120*time.Second || cfg.GracefulExitTimeout != 15*time.Second {
		t.Fatalf("removed auto-close setting affected active timeout defaults: %+v", cfg)
	}
}

func TestIntegrationDefaultsAndOverrides(t *testing.T) {
	paths := Paths{ConfigFile: filepath.Join(t.TempDir(), "config.yaml"), DataDir: t.TempDir(), StateDir: t.TempDir(), DBFile: filepath.Join(t.TempDir(), "db")}
	cfg, err := Normalize(Config{Integration: Integration{Harness: "codex", ValidationCommand: "go test ./..."}}, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integration.Harness != "codex" || cfg.Integration.ValidationCommand != "go test ./..." {
		t.Fatalf("integration=%+v", cfg.Integration)
	}
}

func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"KANBI_CONFIG",
		"KANBI_DB",
		"KANBI_DATA_DIR",
		"KANBI_STATE_DIR",
		"KANBI_TMUX_SESSION",
		"KANBI_MULTIPLEXER",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_STATE_HOME",
	} {
		t.Setenv(key, "")
	}
}
