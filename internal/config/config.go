package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/harness"
	"github.com/carlotran4/kanbi/internal/statusbar"

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

type Herdr struct {
	Binary            string `yaml:"binary"`
	Session           string `yaml:"session"`
	WorkspaceStrategy string `yaml:"workspace_strategy"`
	TabStrategy       string `yaml:"tab_strategy"`
	FocusOnOpen       bool   `yaml:"focus_on_open"`
}

type Multiplexer struct {
	Default string `yaml:"default"`
	Tmux    Tmux   `yaml:"tmux"`
	Herdr   Herdr  `yaml:"herdr"`
}

type Timeouts struct {
	IdleUnknownAfterSeconds    int `yaml:"idle_unknown_after_seconds"`
	GracefulExitTimeoutSeconds int `yaml:"graceful_exit_timeout_seconds"`
	PromptReadyTimeoutSeconds  int `yaml:"prompt_ready_timeout_seconds"`
}

// Diagnostics configures opt-in bounded private file logging under StateDir.
// Logging is disabled (level off) unless set via config or KANBI_LOG_LEVEL.
type Integration struct {
	Harness           string `yaml:"harness"`
	ValidationCommand string `yaml:"validation_command"`
}

// Focus is a global, cross-board commitment policy. WorkflowKeys use stable
// column workflow_key values rather than display names.
type Focus struct {
	Enabled      bool     `yaml:"enabled"`
	Limit        int      `yaml:"limit"`
	WorkflowKeys []string `yaml:"workflow_keys"`
}

type Diagnostics struct {
	// Level is off|error|warn|info|debug. Default off.
	Level string `yaml:"level"`
	// MaxBytes is the soft size of the active log before rotation (default 1048576).
	MaxBytes int64 `yaml:"max_bytes"`
	// MaxFiles is how many rotated files to retain (default 3).
	MaxFiles int `yaml:"max_files"`
}

type Config struct {
	Paths               Paths
	DBPath              string             `yaml:"db_path"`
	DefaultHarness      string             `yaml:"default_harness"`
	TmuxSession         string             `yaml:"tmux_session"`
	Tmux                Tmux               `yaml:"tmux"`
	Multiplexer         Multiplexer        `yaml:"multiplexer"`
	Diagnostics         Diagnostics        `yaml:"diagnostics"`
	Integration         Integration        `yaml:"integration"`
	Focus               Focus              `yaml:"focus"`
	StatusBar           *statusbar.Config  `yaml:"status_bar"`
	PromptReadyTimeout  time.Duration      `yaml:"-"`
	PromptReadyRaw      string             `yaml:"prompt_ready_timeout"`
	IdleUnknownAfter    time.Duration      `yaml:"-"`
	GracefulExitTimeout time.Duration      `yaml:"-"`
	Timeouts            Timeouts           `yaml:"timeouts"`
	Harnesses           map[string]Harness `yaml:"harnesses"`
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
	DBPath             string
	TmuxSession        string
	DefaultMultiplexer string
	LogLevel           string
}

type NormalizeOptions struct {
	Env                         Env
	NestedPromptReadyTimeoutSet bool
}

func Defaults(paths Paths) Config {
	return defaultConfig(paths, readEnv())
}

func Load() (Config, error) {
	paths := ResolvePaths()
	raw, nestedPromptReadyTimeoutSet, err := LoadRaw(paths.ConfigFile)
	if err != nil {
		return Config{}, err
	}
	return Normalize(raw, paths, NormalizeOptions{Env: readEnv(), NestedPromptReadyTimeoutSet: nestedPromptReadyTimeoutSet})
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
	var presence struct {
		Timeouts *struct {
			PromptReadyTimeoutSeconds *int `yaml:"prompt_ready_timeout_seconds"`
		} `yaml:"timeouts"`
	}
	if err := yaml.Unmarshal(b, &presence); err != nil {
		return Config{}, false, fmt.Errorf("load config %s: %w", path, err)
	}
	return raw, presence.Timeouts != nil && presence.Timeouts.PromptReadyTimeoutSeconds != nil, nil
}

func Normalize(raw Config, paths Paths, opts NormalizeOptions) (Config, error) {
	cfg := defaultConfig(paths, opts.Env)
	overlayRawConfig(&cfg, raw)
	if opts.Env.DBPath != "" {
		cfg.DBPath = opts.Env.DBPath
	}
	if opts.Env.TmuxSession != "" {
		cfg.TmuxSession = opts.Env.TmuxSession
		cfg.Tmux.SessionName = opts.Env.TmuxSession
		cfg.Multiplexer.Tmux.SessionName = opts.Env.TmuxSession
	}
	if opts.Env.DefaultMultiplexer != "" {
		cfg.Multiplexer.Default = opts.Env.DefaultMultiplexer
	}
	syncMultiplexerConfig(&cfg)
	applyDiagnosticsDefaults(&cfg, opts.Env)
	if cfg.PromptReadyRaw == "" {
		cfg.PromptReadyRaw = "5s"
	}
	timeout, err := time.ParseDuration(cfg.PromptReadyRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid prompt_ready_timeout %q: %w", cfg.PromptReadyRaw, err)
	}
	cfg.PromptReadyTimeout = timeout
	applyTimeoutDefaults(&cfg, raw.Timeouts.PromptReadyTimeoutSeconds, opts.NestedPromptReadyTimeoutSet)
	mergeHarnessDefaults(&cfg)
	if cfg.Focus.Limit <= 0 {
		cfg.Focus.Limit = 3
	}
	seenFocusKeys := make(map[string]bool)
	focusKeys := make([]string, 0, len(cfg.Focus.WorkflowKeys))
	for _, key := range cfg.Focus.WorkflowKeys {
		key = strings.TrimSpace(key)
		if key != "" && !seenFocusKeys[key] {
			seenFocusKeys[key] = true
			focusKeys = append(focusKeys, key)
		}
	}
	cfg.Focus.WorkflowKeys = focusKeys
	if cfg.StatusBar == nil {
		statusBar := statusbar.DefaultConfig()
		cfg.StatusBar = &statusBar
	}
	if err := statusbar.Normalize(cfg.StatusBar); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) EnsureDirs() error {
	dirs := []string{filepath.Dir(c.Paths.ConfigFile), c.Paths.DataDir, c.Paths.StateDir, filepath.Dir(c.DBPath)}
	for _, dir := range dirs {
		_, statErr := os.Stat(dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		// Paths may be explicit overrides such as /tmp or /etc; never change an
		// existing directory's permissions merely because Kanbi stores a file
		// there. Newly-created application directories are private by default.
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Chmod(dir, 0o700); err != nil {
				return fmt.Errorf("secure %s: %w", dir, err)
			}
		}
	}
	if _, err := os.Stat(c.Paths.ConfigFile); err == nil {
		if err := os.Chmod(c.Paths.ConfigFile, 0o600); err != nil {
			return fmt.Errorf("secure config file: %w", err)
		}
	}
	return nil
}

func defaultConfig(paths Paths, env Env) Config {
	tmuxSession := env.TmuxSession
	if tmuxSession == "" {
		tmuxSession = DefaultSession
	}
	statusBar := statusbar.DefaultConfig()
	return Config{
		Paths:               paths,
		DBPath:              paths.DBFile,
		DefaultHarness:      "pi",
		TmuxSession:         tmuxSession,
		Tmux:                Tmux{SessionName: tmuxSession, BoardWindowName: "board"},
		Multiplexer:         Multiplexer{Default: "tmux", Tmux: Tmux{SessionName: tmuxSession, BoardWindowName: "board"}, Herdr: Herdr{Binary: "herdr", Session: "default", WorkspaceStrategy: "board", TabStrategy: "tickets", FocusOnOpen: false}},
		Diagnostics:         Diagnostics{Level: "off", MaxBytes: 1 << 20, MaxFiles: 3},
		Integration:         Integration{Harness: "pi"},
		Focus:               Focus{Enabled: false, Limit: 3},
		StatusBar:           &statusBar,
		PromptReadyTimeout:  5 * time.Second,
		PromptReadyRaw:      "5s",
		IdleUnknownAfter:    120 * time.Second,
		GracefulExitTimeout: 15 * time.Second,
		Timeouts: Timeouts{
			IdleUnknownAfterSeconds:    120,
			GracefulExitTimeoutSeconds: 15,
			PromptReadyTimeoutSeconds:  5,
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
		cfg.Tmux.SessionName = raw.TmuxSession
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
	if raw.Multiplexer.Default != "" {
		cfg.Multiplexer.Default = raw.Multiplexer.Default
	}
	if raw.Multiplexer.Tmux.SessionName != "" {
		cfg.TmuxSession = raw.Multiplexer.Tmux.SessionName
		cfg.Tmux.SessionName = raw.Multiplexer.Tmux.SessionName
	}
	if raw.Multiplexer.Tmux.BoardWindowName != "" {
		cfg.Tmux.BoardWindowName = raw.Multiplexer.Tmux.BoardWindowName
	}
	if raw.Multiplexer.Herdr.Binary != "" {
		cfg.Multiplexer.Herdr.Binary = raw.Multiplexer.Herdr.Binary
	}
	if raw.Multiplexer.Herdr.Session != "" {
		cfg.Multiplexer.Herdr.Session = raw.Multiplexer.Herdr.Session
	}
	if raw.Multiplexer.Herdr.WorkspaceStrategy != "" {
		cfg.Multiplexer.Herdr.WorkspaceStrategy = raw.Multiplexer.Herdr.WorkspaceStrategy
	}
	if raw.Multiplexer.Herdr.TabStrategy != "" {
		cfg.Multiplexer.Herdr.TabStrategy = raw.Multiplexer.Herdr.TabStrategy
	}
	if raw.Multiplexer.Herdr.FocusOnOpen {
		cfg.Multiplexer.Herdr.FocusOnOpen = raw.Multiplexer.Herdr.FocusOnOpen
	}
	if raw.PromptReadyRaw != "" {
		cfg.PromptReadyRaw = raw.PromptReadyRaw
	}
	if raw.Timeouts.IdleUnknownAfterSeconds > 0 {
		cfg.Timeouts.IdleUnknownAfterSeconds = raw.Timeouts.IdleUnknownAfterSeconds
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
	if raw.Integration.Harness != "" {
		cfg.Integration.Harness = raw.Integration.Harness
	}
	if raw.Integration.ValidationCommand != "" {
		cfg.Integration.ValidationCommand = raw.Integration.ValidationCommand
	}
	if raw.Focus.Enabled {
		cfg.Focus.Enabled = true
	}
	if raw.Focus.Limit > 0 {
		cfg.Focus.Limit = raw.Focus.Limit
	}
	if raw.Focus.WorkflowKeys != nil {
		cfg.Focus.WorkflowKeys = append([]string(nil), raw.Focus.WorkflowKeys...)
	}
	if raw.Diagnostics.Level != "" {
		cfg.Diagnostics.Level = raw.Diagnostics.Level
	}
	if raw.Diagnostics.MaxBytes > 0 {
		cfg.Diagnostics.MaxBytes = raw.Diagnostics.MaxBytes
	}
	if raw.Diagnostics.MaxFiles > 0 {
		cfg.Diagnostics.MaxFiles = raw.Diagnostics.MaxFiles
	}
	if raw.StatusBar != nil {
		cfg.StatusBar = raw.StatusBar
	}
}

func syncMultiplexerConfig(cfg *Config) {
	if cfg.Multiplexer.Default == "" {
		cfg.Multiplexer.Default = "tmux"
	}
	if cfg.Tmux.SessionName == "" {
		cfg.Tmux.SessionName = cfg.TmuxSession
	}
	if cfg.TmuxSession == "" {
		cfg.TmuxSession = cfg.Tmux.SessionName
	}
	if cfg.TmuxSession == "" {
		cfg.TmuxSession = DefaultSession
		cfg.Tmux.SessionName = DefaultSession
	}
	if cfg.Tmux.BoardWindowName == "" {
		cfg.Tmux.BoardWindowName = "board"
	}
	cfg.TmuxSession = cfg.Tmux.SessionName
	cfg.Multiplexer.Tmux = cfg.Tmux
	if cfg.Multiplexer.Herdr.Binary == "" {
		cfg.Multiplexer.Herdr.Binary = "herdr"
	}
	if cfg.Multiplexer.Herdr.Session == "" {
		cfg.Multiplexer.Herdr.Session = "default"
	}
	if cfg.Multiplexer.Herdr.WorkspaceStrategy == "" {
		cfg.Multiplexer.Herdr.WorkspaceStrategy = "board"
	}
	if cfg.Multiplexer.Herdr.TabStrategy == "" {
		cfg.Multiplexer.Herdr.TabStrategy = "tickets"
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

func applyTimeoutDefaults(cfg *Config, nestedPromptReadyTimeoutSeconds int, nestedPromptReadyTimeoutSet bool) {
	if nestedPromptReadyTimeoutSet && nestedPromptReadyTimeoutSeconds > 0 {
		cfg.PromptReadyTimeout = time.Duration(nestedPromptReadyTimeoutSeconds) * time.Second
	}
	if cfg.Timeouts.IdleUnknownAfterSeconds <= 0 {
		cfg.Timeouts.IdleUnknownAfterSeconds = 120
	}
	if cfg.Timeouts.GracefulExitTimeoutSeconds <= 0 {
		cfg.Timeouts.GracefulExitTimeoutSeconds = 15
	}
	if cfg.Timeouts.PromptReadyTimeoutSeconds <= 0 {
		cfg.Timeouts.PromptReadyTimeoutSeconds = int(cfg.PromptReadyTimeout / time.Second)
	}
	cfg.IdleUnknownAfter = time.Duration(cfg.Timeouts.IdleUnknownAfterSeconds) * time.Second
	cfg.GracefulExitTimeout = time.Duration(cfg.Timeouts.GracefulExitTimeoutSeconds) * time.Second
}

func applyDiagnosticsDefaults(cfg *Config, env Env) {
	if cfg.Diagnostics.Level == "" {
		cfg.Diagnostics.Level = "off"
	}
	if cfg.Diagnostics.MaxBytes <= 0 {
		cfg.Diagnostics.MaxBytes = 1 << 20
	}
	if cfg.Diagnostics.MaxFiles <= 0 {
		cfg.Diagnostics.MaxFiles = 3
	}
	// Environment overrides config for temporary debug sessions.
	if env.LogLevel != "" {
		cfg.Diagnostics.Level = env.LogLevel
	}
}

func readEnv() Env {
	return Env{
		DBPath:             os.Getenv("KANBI_DB"),
		TmuxSession:        os.Getenv("KANBI_TMUX_SESSION"),
		DefaultMultiplexer: os.Getenv("KANBI_MULTIPLEXER"),
		LogLevel:           os.Getenv("KANBI_LOG_LEVEL"),
	}
}
