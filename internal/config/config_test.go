package config

import (
	"os"
	"path/filepath"
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
	if paths.DBFile != filepath.Join(home, "data", AppName, "agent-kanban.db") {
		t.Fatalf("db path = %s", paths.DBFile)
	}
	t.Setenv("AGENT_KANBAN_DB", filepath.Join(home, "override.db"))
	t.Setenv("AGENT_KANBAN_CONFIG", filepath.Join(home, "override.yaml"))
	paths = ResolvePaths()
	if paths.DBFile != filepath.Join(home, "override.db") || paths.ConfigFile != filepath.Join(home, "override.yaml") {
		t.Fatalf("env overrides not applied: %+v", paths)
	}
}

func TestNormalizeDefaultsFromInMemoryRawConfig(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/agent-kanban.db"}
	cfg, err := Normalize(Config{}, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths != paths || cfg.DBPath != paths.DBFile || cfg.DefaultHarness != "pi" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.TmuxSession != DefaultSession || cfg.Tmux.SessionName != DefaultSession || cfg.Tmux.BoardWindowName != "board" {
		t.Fatalf("tmux defaults not applied: %+v", cfg)
	}
	if cfg.PromptReadyTimeout != 5*time.Second || cfg.IdleUnknownAfter != 120*time.Second || cfg.AutoCloseWaitingAfter != 10*time.Minute || cfg.GracefulExitTimeout != 15*time.Second {
		t.Fatalf("timeout defaults not applied: %+v", cfg)
	}
	if len(cfg.Harnesses["pi"].Start) == 0 || len(cfg.Harnesses["codex"].Start) == 0 || len(cfg.Harnesses["copilot"].Start) == 0 {
		t.Fatalf("default harnesses not applied: %+v", cfg.Harnesses)
	}
}

func TestNormalizeEnvOverridesInMemoryRawConfig(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/agent-kanban.db"}
	raw := Config{DBPath: "/from/config.db", TmuxSession: "from-config"}
	cfg, err := Normalize(raw, paths, NormalizeOptions{Env: Env{DBPath: "/from/env.db", TmuxSession: "from-env"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != "/from/env.db" || cfg.TmuxSession != "from-env" || cfg.Tmux.SessionName != "from-env" {
		t.Fatalf("env overrides not applied during normalize: %+v", cfg)
	}
}

func TestNormalizeNestedTimeoutsAndLegacyPromptTimeout(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/agent-kanban.db"}
	raw := Config{
		PromptReadyRaw: "2s",
		Timeouts: Timeouts{
			IdleUnknownAfterSeconds:      5,
			AutoCloseWaitingAfterMinutes: 1,
			GracefulExitTimeoutSeconds:   2,
			PromptReadyTimeoutSeconds:    3,
		},
	}
	cfg, err := Normalize(raw, paths, NormalizeOptions{LoadedNestedTimeouts: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PromptReadyTimeout != 3*time.Second || cfg.IdleUnknownAfter != 5*time.Second || cfg.AutoCloseWaitingAfter != time.Minute || cfg.GracefulExitTimeout != 2*time.Second {
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
	t.Setenv("AGENT_KANBAN_CONFIG", cfgFile)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != "/tmp/custom.db" || cfg.TmuxSession != "custom-session" {
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
	t.Setenv("AGENT_KANBAN_CONFIG", cfgFile)
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid yaml error")
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
  auto_close_waiting_after_minutes: 1
  graceful_exit_timeout_seconds: 2
  prompt_ready_timeout_seconds: 3
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_KANBAN_CONFIG", cfgFile)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "codex" || cfg.TmuxSession != "spec-session" || cfg.Tmux.BoardWindowName != "board-main" {
		t.Fatalf("nested config not applied: %+v", cfg)
	}
	if cfg.PromptReadyTimeout != 3*time.Second || cfg.IdleUnknownAfter != 5*time.Second || cfg.AutoCloseWaitingAfter != time.Minute || cfg.GracefulExitTimeout != 2*time.Second {
		t.Fatalf("timeouts not applied: %+v", cfg)
	}
}

func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AGENT_KANBAN_CONFIG",
		"AGENT_KANBAN_DB",
		"AGENT_KANBAN_DATA_DIR",
		"AGENT_KANBAN_STATE_DIR",
		"AGENT_KANBAN_TMUX_SESSION",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_STATE_HOME",
	} {
		t.Setenv(key, "")
	}
}
