package config

import (
	"os"
	"path/filepath"
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

func TestNormalizeDefaultsFromInMemoryRawConfig(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
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
	if cfg.Multiplexer.Default != "tmux" || cfg.Multiplexer.Tmux != cfg.Tmux || cfg.Multiplexer.Herdr.Binary != "herdr" {
		t.Fatalf("multiplexer defaults not applied: %+v", cfg.Multiplexer)
	}
	if cfg.PromptReadyTimeout != 5*time.Second || cfg.IdleUnknownAfter != 120*time.Second || cfg.AutoCloseWaitingAfter != 10*time.Minute || cfg.GracefulExitTimeout != 15*time.Second {
		t.Fatalf("timeout defaults not applied: %+v", cfg)
	}
	if len(cfg.Harnesses["pi"].Start) == 0 || len(cfg.Harnesses["codex"].Start) == 0 || len(cfg.Harnesses["copilot"].Start) == 0 {
		t.Fatalf("default harnesses not applied: %+v", cfg.Harnesses)
	}
}

func TestNormalizeEnvOverridesInMemoryRawConfig(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	raw := Config{DBPath: "/from/config.db", TmuxSession: "from-config"}
	cfg, err := Normalize(raw, paths, NormalizeOptions{Env: Env{DBPath: "/from/env.db", TmuxSession: "from-env"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPath != "/from/env.db" || cfg.TmuxSession != "from-env" || cfg.Tmux.SessionName != "from-env" {
		t.Fatalf("env overrides not applied during normalize: %+v", cfg)
	}
}

func TestNormalizeMultiplexerConfigAndEnvOverride(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	raw := Config{Multiplexer: Multiplexer{
		Default: "tmux",
		Tmux:    Tmux{SessionName: "mux-session", BoardWindowName: "mux-board"},
		Herdr:   Herdr{Binary: "/bin/herdr", Session: "work", WorkspaceStrategy: "board", TabStrategy: "tickets", FocusOnOpen: true},
	}}
	cfg, err := Normalize(raw, paths, NormalizeOptions{Env: Env{DefaultMultiplexer: "herdr"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Multiplexer.Default != "herdr" {
		t.Fatalf("env multiplexer override not applied: %+v", cfg.Multiplexer)
	}
	if cfg.TmuxSession != "mux-session" || cfg.Tmux.SessionName != "mux-session" || cfg.Tmux.BoardWindowName != "mux-board" || cfg.Multiplexer.Tmux != cfg.Tmux {
		t.Fatalf("multiplexer tmux config did not sync legacy fields: %+v", cfg)
	}
	if cfg.Multiplexer.Herdr.Binary != "/bin/herdr" || cfg.Multiplexer.Herdr.Session != "work" || !cfg.Multiplexer.Herdr.FocusOnOpen {
		t.Fatalf("herdr config not applied: %+v", cfg.Multiplexer.Herdr)
	}
}

func TestNormalizeLegacyTmuxConfigSyncsMultiplexer(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	cfg, err := Normalize(Config{TmuxSession: "legacy", Tmux: Tmux{BoardWindowName: "legacy-board"}}, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Multiplexer.Default != "tmux" || cfg.Multiplexer.Tmux.SessionName != "legacy" || cfg.Multiplexer.Tmux.BoardWindowName != "legacy-board" {
		t.Fatalf("legacy tmux did not sync multiplexer config: %+v", cfg)
	}
}

func TestNormalizeNestedTimeoutsAndLegacyPromptTimeout(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
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
	t.Setenv("KANBI_CONFIG", cfgFile)
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
	t.Setenv("KANBI_CONFIG", cfgFile)
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
	t.Setenv("KANBI_CONFIG", cfgFile)
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
