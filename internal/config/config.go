package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"kanbi/internal/harness"

	"gopkg.in/yaml.v3"
)

const (
	AppName        = "kanbi"
	DefaultSession = "kanbi"
)

type Paths struct {
	ConfigFile string
	DataDir    string
	StateDir   string
	DBFile     string
}

type Harness = harness.Config

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
	configFile := os.Getenv("KANBI_CONFIG")
	if configFile == "" {
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		configFile = filepath.Join(configHome, AppName, "config.yaml")
	}

	dataDir := os.Getenv("KANBI_DATA_DIR")
	if dataDir == "" {
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		dataDir = filepath.Join(dataHome, AppName)
	}

	stateDir := os.Getenv("KANBI_STATE_DIR")
	if stateDir == "" {
		stateHome := os.Getenv("XDG_STATE_HOME")
		if stateHome == "" {
			stateHome = filepath.Join(home, ".local", "state")
		}
		stateDir = filepath.Join(stateHome, AppName)
	}

	dbFile := os.Getenv("KANBI_DB")
	if dbFile == "" {
		dbFile = filepath.Join(dataDir, "kanbi.db")
	}

	if runtime.GOOS == "windows" {
		configFile = filepath.FromSlash(configFile)
		dataDir = filepath.FromSlash(dataDir)
		stateDir = filepath.FromSlash(stateDir)
		dbFile = filepath.FromSlash(dbFile)
	}

	return Paths{ConfigFile: configFile, DataDir: dataDir, StateDir: stateDir, DBFile: dbFile}
}

type Env struct {
	DBPath      string
	TmuxSession string
}

type NormalizeOptions struct {
	Env                  Env
	LoadedNestedTimeouts bool
}

func Defaults(paths Paths) Config {
	return defaultConfig(paths, readEnv())
}

func Load() (Config, error) {
	paths := ResolvePaths()
	raw, loadedNestedTimeouts, err := LoadRaw(paths.ConfigFile)
	if err != nil {
		return Config{}, err
	}
	return Normalize(raw, paths, NormalizeOptions{Env: readEnv(), LoadedNestedTimeouts: loadedNestedTimeouts})
}

func LoadRaw(path string) (Config, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("read config %s: %w", path, err)
	}
	var raw Config
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return Config{}, false, fmt.Errorf("load config %s: %w", path, err)
	}
	return raw, byteContains(b, []byte("timeouts:")), nil
}

func Normalize(raw Config, paths Paths, opts NormalizeOptions) (Config, error) {
	cfg := defaultConfig(paths, opts.Env)
	overlayRawConfig(&cfg, raw)
	if opts.Env.DBPath != "" {
		cfg.DBPath = opts.Env.DBPath
	}
	if opts.Env.TmuxSession != "" {
		cfg.TmuxSession = opts.Env.TmuxSession
	}
	cfg.Tmux.SessionName = cfg.TmuxSession
	if cfg.PromptReadyRaw == "" {
		cfg.PromptReadyRaw = "5s"
	}
	timeout, err := time.ParseDuration(cfg.PromptReadyRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid prompt_ready_timeout %q: %w", cfg.PromptReadyRaw, err)
	}
	cfg.PromptReadyTimeout = timeout
	applyTimeoutDefaults(&cfg, opts.LoadedNestedTimeouts)
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

func defaultConfig(paths Paths, env Env) Config {
	tmuxSession := env.TmuxSession
	if tmuxSession == "" {
		tmuxSession = DefaultSession
	}
	return Config{
		Paths:                 paths,
		DBPath:                paths.DBFile,
		DefaultHarness:        "pi",
		TmuxSession:           tmuxSession,
		Tmux:                  Tmux{SessionName: tmuxSession, BoardWindowName: "board"},
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
		Harnesses: harness.DefaultConfigs(),
	}
}

func overlayRawConfig(cfg *Config, raw Config) {
	if raw.DBPath != "" {
		cfg.DBPath = raw.DBPath
	}
	if raw.DefaultHarness != "" {
		cfg.DefaultHarness = raw.DefaultHarness
	}
	if raw.TmuxSession != "" {
		cfg.TmuxSession = raw.TmuxSession
	}
	if raw.Tmux.SessionName != "" {
		if raw.TmuxSession == "" {
			cfg.TmuxSession = raw.Tmux.SessionName
		}
		cfg.Tmux.SessionName = raw.Tmux.SessionName
	}
	if raw.Tmux.BoardWindowName != "" {
		cfg.Tmux.BoardWindowName = raw.Tmux.BoardWindowName
	}
	if raw.PromptReadyRaw != "" {
		cfg.PromptReadyRaw = raw.PromptReadyRaw
	}
	if raw.Timeouts.IdleUnknownAfterSeconds > 0 {
		cfg.Timeouts.IdleUnknownAfterSeconds = raw.Timeouts.IdleUnknownAfterSeconds
	}
	if raw.Timeouts.AutoCloseWaitingAfterMinutes > 0 {
		cfg.Timeouts.AutoCloseWaitingAfterMinutes = raw.Timeouts.AutoCloseWaitingAfterMinutes
	}
	if raw.Timeouts.GracefulExitTimeoutSeconds > 0 {
		cfg.Timeouts.GracefulExitTimeoutSeconds = raw.Timeouts.GracefulExitTimeoutSeconds
	}
	if raw.Timeouts.PromptReadyTimeoutSeconds > 0 {
		cfg.Timeouts.PromptReadyTimeoutSeconds = raw.Timeouts.PromptReadyTimeoutSeconds
	}
	if raw.Harnesses != nil {
		cfg.Harnesses = raw.Harnesses
	}
}

func mergeHarnessDefaults(cfg *Config) {
	defaults := defaultConfig(cfg.Paths, Env{}).Harnesses
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

func readEnv() Env {
	return Env{
		DBPath:      os.Getenv("KANBI_DB"),
		TmuxSession: os.Getenv("KANBI_TMUX_SESSION"),
	}
}
