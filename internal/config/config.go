package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	AppName        = "agent-kanban"
	DefaultSession = "agent-kanban"
)

type Paths struct {
	ConfigFile string
	DataDir    string
	StateDir   string
	DBFile     string
}

type Harness struct {
	Start       []string `yaml:"start"`
	Resume      []string `yaml:"resume"`
	Exit        []string `yaml:"exit"`
	PromptReady string   `yaml:"prompt_ready"`
	PromptMode  string   `yaml:"prompt_mode"`
	SessionRef  string   `yaml:"session_ref"`
}

type Tmux struct {
	SessionName     string `yaml:"session_name"`
	BoardWindowName string `yaml:"board_window_name"`
}

type Timeouts struct {
	IdleUnknownAfterSeconds      int `yaml:"idle_unknown_after_seconds"`
	AutoCloseWaitingAfterMinutes int `yaml:"auto_close_waiting_after_minutes"`
	GracefulExitTimeoutSeconds   int `yaml:"graceful_exit_timeout_seconds"`
	PromptReadyTimeoutSeconds    int `yaml:"prompt_ready_timeout_seconds"`
}

type Config struct {
	Paths                 Paths
	DBPath                string             `yaml:"db_path"`
	DefaultHarness        string             `yaml:"default_harness"`
	TmuxSession           string             `yaml:"tmux_session"`
	Tmux                  Tmux               `yaml:"tmux"`
	PromptReadyTimeout    time.Duration      `yaml:"-"`
	PromptReadyRaw        string             `yaml:"prompt_ready_timeout"`
	IdleUnknownAfter      time.Duration      `yaml:"-"`
	AutoCloseWaitingAfter time.Duration      `yaml:"-"`
	GracefulExitTimeout   time.Duration      `yaml:"-"`
	Timeouts              Timeouts           `yaml:"timeouts"`
	Harnesses             map[string]Harness `yaml:"harnesses"`
}

func ResolvePaths() Paths {
	home, _ := os.UserHomeDir()
	configFile := os.Getenv("AGENT_KANBAN_CONFIG")
	if configFile == "" {
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		configFile = filepath.Join(configHome, AppName, "config.yaml")
	}

	dataDir := os.Getenv("AGENT_KANBAN_DATA_DIR")
	if dataDir == "" {
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		dataDir = filepath.Join(dataHome, AppName)
	}

	stateDir := os.Getenv("AGENT_KANBAN_STATE_DIR")
	if stateDir == "" {
		stateHome := os.Getenv("XDG_STATE_HOME")
		if stateHome == "" {
			stateHome = filepath.Join(home, ".local", "state")
		}
		stateDir = filepath.Join(stateHome, AppName)
	}

	dbFile := os.Getenv("AGENT_KANBAN_DB")
	if dbFile == "" {
		dbFile = filepath.Join(dataDir, "agent-kanban.db")
	}

	if runtime.GOOS == "windows" {
		configFile = filepath.FromSlash(configFile)
		dataDir = filepath.FromSlash(dataDir)
		stateDir = filepath.FromSlash(stateDir)
		dbFile = filepath.FromSlash(dbFile)
	}

	return Paths{ConfigFile: configFile, DataDir: dataDir, StateDir: stateDir, DBFile: dbFile}
}

func Defaults(paths Paths) Config {
	return Config{
		Paths:                 paths,
		DBPath:                paths.DBFile,
		DefaultHarness:        "pi",
		TmuxSession:           envDefault("AGENT_KANBAN_TMUX_SESSION", DefaultSession),
		Tmux:                  Tmux{SessionName: envDefault("AGENT_KANBAN_TMUX_SESSION", DefaultSession), BoardWindowName: "board"},
		PromptReadyTimeout:    5 * time.Second,
		PromptReadyRaw:        "5s",
		IdleUnknownAfter:      120 * time.Second,
		AutoCloseWaitingAfter: 10 * time.Minute,
		GracefulExitTimeout:   15 * time.Second,
		Timeouts: Timeouts{
			IdleUnknownAfterSeconds:      120,
			AutoCloseWaitingAfterMinutes: 10,
			GracefulExitTimeoutSeconds:   15,
			PromptReadyTimeoutSeconds:    5,
		},
		Harnesses: map[string]Harness{
			"pi": {
				Start:       []string{"pi"},
				Resume:      []string{"pi", "--session", "{session_ref}"},
				Exit:        []string{"C-c", "exit", "Enter"},
				PromptReady: "",
				PromptMode:  "arg",
				SessionRef:  "",
			},
			"codex": {
				Start:       []string{"codex", "--no-alt-screen"},
				Resume:      []string{"codex", "resume", "--no-alt-screen", "{session_ref}"},
				Exit:        []string{"C-c", "exit", "Enter"},
				PromptReady: "",
				PromptMode:  "arg",
				SessionRef:  "",
			},
			"copilot": {
				Start:       []string{"gh", "copilot", "--", "-i"},
				Resume:      []string{"gh", "copilot", "--", "--resume={session_ref}"},
				Exit:        []string{"C-c", "exit", "Enter"},
				PromptReady: "",
				PromptMode:  "arg",
				SessionRef:  "",
			},
		},
	}
}

func Load() (Config, error) {
	paths := ResolvePaths()
	cfg := Defaults(paths)
	loadedNestedTimeouts := false

	if b, err := os.ReadFile(paths.ConfigFile); err == nil {
		loadedNestedTimeouts = byteContains(b, []byte("timeouts:"))
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return Config{}, fmt.Errorf("load config %s: %w", paths.ConfigFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read config %s: %w", paths.ConfigFile, err)
	}

	cfg.Paths = paths
	if cfg.DBPath == "" {
		cfg.DBPath = paths.DBFile
	}
	if cfg.DefaultHarness == "" {
		cfg.DefaultHarness = "pi"
	}
	if envDB := os.Getenv("AGENT_KANBAN_DB"); envDB != "" {
		cfg.DBPath = envDB
	}
	defaultSession := envDefault("AGENT_KANBAN_TMUX_SESSION", DefaultSession)
	if cfg.Tmux.SessionName != "" && cfg.TmuxSession == defaultSession {
		cfg.TmuxSession = cfg.Tmux.SessionName
	}
	if cfg.TmuxSession == "" {
		cfg.TmuxSession = defaultSession
	}
	if envSession := os.Getenv("AGENT_KANBAN_TMUX_SESSION"); envSession != "" {
		cfg.TmuxSession = envSession
	}
	cfg.Tmux.SessionName = cfg.TmuxSession
	if cfg.Tmux.BoardWindowName == "" {
		cfg.Tmux.BoardWindowName = "board"
	}
	if cfg.PromptReadyRaw == "" {
		cfg.PromptReadyRaw = "5s"
	}
	timeout, err := time.ParseDuration(cfg.PromptReadyRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid prompt_ready_timeout %q: %w", cfg.PromptReadyRaw, err)
	}
	cfg.PromptReadyTimeout = timeout
	applyTimeoutDefaults(&cfg, loadedNestedTimeouts)
	mergeHarnessDefaults(&cfg)
	return cfg, nil
}

func (c Config) EnsureDirs() error {
	for _, dir := range []string{filepath.Dir(c.Paths.ConfigFile), c.Paths.DataDir, c.Paths.StateDir, filepath.Dir(c.DBPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

func mergeHarnessDefaults(cfg *Config) {
	defaults := Defaults(cfg.Paths).Harnesses
	if cfg.Harnesses == nil {
		cfg.Harnesses = defaults
		return
	}
	for name, def := range defaults {
		h, ok := cfg.Harnesses[name]
		if !ok {
			cfg.Harnesses[name] = def
			continue
		}
		if len(h.Start) == 0 {
			h.Start = def.Start
		}
		if len(h.Resume) == 0 {
			h.Resume = def.Resume
		}
		if len(h.Exit) == 0 {
			h.Exit = def.Exit
		}
		if h.PromptReady == "" {
			h.PromptReady = def.PromptReady
		}
		if h.PromptMode == "" {
			h.PromptMode = def.PromptMode
		}
		if h.SessionRef == "" {
			h.SessionRef = def.SessionRef
		}
		cfg.Harnesses[name] = h
	}
}

func applyTimeoutDefaults(cfg *Config, loadedNestedTimeouts bool) {
	if loadedNestedTimeouts && cfg.Timeouts.PromptReadyTimeoutSeconds > 0 {
		cfg.PromptReadyTimeout = time.Duration(cfg.Timeouts.PromptReadyTimeoutSeconds) * time.Second
	}
	if cfg.Timeouts.IdleUnknownAfterSeconds <= 0 {
		cfg.Timeouts.IdleUnknownAfterSeconds = 120
	}
	if cfg.Timeouts.AutoCloseWaitingAfterMinutes <= 0 {
		cfg.Timeouts.AutoCloseWaitingAfterMinutes = 10
	}
	if cfg.Timeouts.GracefulExitTimeoutSeconds <= 0 {
		cfg.Timeouts.GracefulExitTimeoutSeconds = 15
	}
	if cfg.Timeouts.PromptReadyTimeoutSeconds <= 0 {
		cfg.Timeouts.PromptReadyTimeoutSeconds = int(cfg.PromptReadyTimeout / time.Second)
	}
	cfg.IdleUnknownAfter = time.Duration(cfg.Timeouts.IdleUnknownAfterSeconds) * time.Second
	cfg.AutoCloseWaitingAfter = time.Duration(cfg.Timeouts.AutoCloseWaitingAfterMinutes) * time.Minute
	cfg.GracefulExitTimeout = time.Duration(cfg.Timeouts.GracefulExitTimeoutSeconds) * time.Second
}

func byteContains(b, sub []byte) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		match := true
		for j := range sub {
			if b[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
