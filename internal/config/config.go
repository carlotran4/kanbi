package config

import (
	"bytes"
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
	AppName = "kanbi"
)

type Paths struct {
	ConfigFile string
	DataDir    string
	StateDir   string
	DBFile     string
}

type Harness = harness.Config

type Herdr struct {
	Binary            string `yaml:"binary"`
	Session           string `yaml:"session"`
	WorkspaceStrategy string `yaml:"workspace_strategy"`
	TabStrategy       string `yaml:"tab_strategy"`
	FocusOnOpen       bool   `yaml:"focus_on_open"`
}

type Multiplexer struct {
	Default string `yaml:"default"`
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

func SaveFocus(path string, focus Focus) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("config path is required")
	}
	if focus.Limit <= 0 {
		focus.Limit = 3
	}
	seen := make(map[string]bool)
	keys := make([]string, 0, len(focus.WorkflowKeys))
	for _, key := range focus.WorkflowKeys {
		key = strings.TrimSpace(key)
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	focus.WorkflowKeys = keys

	var document yaml.Node
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if len(bytes.TrimSpace(contents)) == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	} else if err := yaml.Unmarshal(contents, &document); err != nil {
		return fmt.Errorf("load config %s: %w", path, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("load config %s: root must be a YAML mapping", path)
	}
	root := document.Content[0]
	var encodedFocus yaml.Node
	if err := encodedFocus.Encode(focus); err != nil {
		return fmt.Errorf("encode focus config: %w", err)
	}
	focusNode := mappingValue(root, "focus")
	if focusNode == nil {
		focusNode = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "focus"}, focusNode)
	}
	if focusNode.Kind != yaml.MappingNode {
		return fmt.Errorf("load config %s: focus must be a YAML mapping", path)
	}
	// Update only Kanbi-owned fields. Unknown extension keys and comments under
	// focus remain intact.
	for i := 0; i+1 < len(encodedFocus.Content); i += 2 {
		setMappingValue(focusNode, encodedFocus.Content[i].Value, encodedFocus.Content[i+1])
	}

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("encode config %s: %w", path, err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("encode config %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".kanbi-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(output.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open config directory for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync config directory: %w", err)
	}
	return directory.Close()
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		old := mapping.Content[i+1]
		value.HeadComment = old.HeadComment
		value.LineComment = old.LineComment
		value.FootComment = old.FootComment
		mapping.Content[i+1] = value
		return
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
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
	if opts.Env.DefaultMultiplexer != "" {
		cfg.Multiplexer.Default = opts.Env.DefaultMultiplexer
	}
	syncMultiplexerConfig(&cfg)
	if cfg.Multiplexer.Default != "herdr" {
		return Config{}, fmt.Errorf("multiplexer %q is no longer supported; set multiplexer.default: herdr", cfg.Multiplexer.Default)
	}
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
	statusBar := statusbar.DefaultConfig()
	return Config{
		Paths:               paths,
		DBPath:              paths.DBFile,
		DefaultHarness:      "pi",
		Multiplexer:         Multiplexer{Default: "herdr", Herdr: Herdr{Binary: "herdr", Session: "default", WorkspaceStrategy: "board", TabStrategy: "tickets", FocusOnOpen: false}},
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
	if raw.Multiplexer.Default != "" {
		cfg.Multiplexer.Default = raw.Multiplexer.Default
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
		cfg.Multiplexer.Default = "herdr"
	}
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
		DefaultMultiplexer: os.Getenv("KANBI_MULTIPLEXER"),
		LogLevel:           os.Getenv("KANBI_LOG_LEVEL"),
	}
}
