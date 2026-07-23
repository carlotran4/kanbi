package diagnostics

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/carlotran4/kanbi/internal/buildinfo"
	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

// BundleFormatVersion is the support-bundle archive format.
const BundleFormatVersion = 1

// BundleFormat is the official identifier stored in the manifest.
const BundleFormat = "kanbi-support-bundle"

// DoctorResult is a serializable doctor probe row used by the support bundle.
type DoctorResult struct {
	Severity string `json:"severity"`
	Name     string `json:"name"`
	Detail   string `json:"detail,omitempty"`
	Error    string `json:"error,omitempty"`
}

// HarnessPresence is existence-only harness binary presence.
type HarnessPresence struct {
	Name      string `json:"name"`
	Command   string `json:"command,omitempty"`
	Present   bool   `json:"present"`
	LookupErr string `json:"lookup_error,omitempty"`
}

// MigrationRow is one applied schema migration.
type MigrationRow struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	AppliedAt time.Time `json:"applied_at"`
}

// RuntimeDiagnosticSummary is a redacted durable diagnostic row snapshot.
type RuntimeDiagnosticSummary struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Kind      string    `json:"kind"`
	Operation string    `json:"operation"`
	BoardID   *int64    `json:"board_id,omitempty"`
	TicketID  *int64    `json:"ticket_id,omitempty"`
	SessionID *int64    `json:"session_id,omitempty"`
	Attempt   int       `json:"attempt"`
	Message   string    `json:"message"`
	Cause     string    `json:"cause"`
}

// Bundle is the versioned support report payload.
// Default bundles never include ticket bodies, notes, tokens, prompts,
// session refs, attachment contents, or terminal excerpts.
type Bundle struct {
	Schema          string                     `json:"schema"`
	Format          string                     `json:"format"`
	FormatVersion   int                        `json:"format_version"`
	GeneratedAt     time.Time                  `json:"generated_at"`
	Build           buildinfo.Info             `json:"build"`
	Platform        PlatformInfo               `json:"platform"`
	Paths           PathsInfo                  `json:"paths"`
	Config          map[string]any             `json:"config_redacted"`
	Multiplexer     MultiplexerInfo            `json:"multiplexer"`
	Migrations      []MigrationRow             `json:"migrations"`
	SchemaVersion   int                        `json:"schema_version"`
	Doctor          []DoctorResult             `json:"doctor"`
	Diagnostics     []RuntimeDiagnosticSummary `json:"runtime_diagnostics"`
	Harnesses       []HarnessPresence          `json:"harnesses"`
	RecentLog       string                     `json:"recent_diagnostics_log,omitempty"`
	Degraded        []string                   `json:"degraded,omitempty"`
	CollectionNotes []string                   `json:"collection_notes"`
	FieldPolicies   []FieldPolicy              `json:"field_policies"`
}

// PlatformInfo is host/runtime metadata.
type PlatformInfo struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	GoVersion  string `json:"go_version"`
	NumCPU     int    `json:"num_cpu"`
	Term       string `json:"term,omitempty"`
	ColorTerm  string `json:"colorterm,omitempty"`
	Shell      string `json:"shell,omitempty"`
	InsideTmux bool   `json:"inside_tmux"`
	// Env subset is presence/redacted only.
	Env map[string]string `json:"env_redacted,omitempty"`
}

// PathsInfo lists sanitized canonical paths.
type PathsInfo struct {
	ConfigFile string `json:"config_file"`
	DataDir    string `json:"data_dir"`
	StateDir   string `json:"state_dir"`
	DBPath     string `json:"db_path"`
}

// MultiplexerInfo summarizes configured runtime substrate without secrets.
type MultiplexerInfo struct {
	Default           string `json:"default"`
	TmuxSession       string `json:"tmux_session,omitempty"`
	HerdrBinary       string `json:"herdr_binary,omitempty"`
	HerdrSession      string `json:"herdr_session,omitempty"`
	WorkspaceStrategy string `json:"workspace_strategy,omitempty"`
	FocusOnOpen       bool   `json:"focus_on_open,omitempty"`
}

// FieldPolicy documents what a field contains and its redaction policy.
type FieldPolicy struct {
	Field   string `json:"field"`
	Policy  string `json:"policy"`
	Default string `json:"default_inclusion"`
}

// CollectOptions configures support-bundle generation.
type CollectOptions struct {
	Config          config.Config
	Doctor          []DoctorResult
	LookPath        func(string) (string, error)
	Getenv          func(string) string
	InsideTmux      func() bool
	OpenStore       func(ctx context.Context, dbPath string) (*storage.Store, error)
	Now             func() time.Time
	IncludeLogBytes int64
}

// DefaultFieldPolicies is the documented field inventory for support bundles.
func DefaultFieldPolicies() []FieldPolicy {
	return []FieldPolicy{
		{Field: "build.*", Policy: "version/commit/build date/go/os/arch/schema; no secrets", Default: "included"},
		{Field: "platform.*", Policy: "OS/arch/term/shell; home paths redacted; secrets absent", Default: "included"},
		{Field: "paths.*", Policy: "canonical Kanbi paths with home redacted to ~", Default: "included"},
		{Field: "config_redacted", Policy: "YAML config with tokens/secrets/passwords fully redacted; URLs sanitized", Default: "included"},
		{Field: "multiplexer.*", Policy: "runtime type and non-secret multiplexer settings only", Default: "included"},
		{Field: "migrations", Policy: "schema_migrations version/name/applied_at only", Default: "included when DB openable"},
		{Field: "doctor", Policy: "doctor probe severity/name/detail (redacted)", Default: "included"},
		{Field: "runtime_diagnostics", Policy: "durable redacted failure rows (no bodies/prompts/session refs)", Default: "included when DB openable"},
		{Field: "harnesses", Policy: "binary presence and configured command; filesystem paths redacted; no auth state", Default: "included"},
		{Field: "recent_diagnostics_log", Policy: "tail of opt-in log file after redaction; may be empty", Default: "included if present"},
		{Field: "ticket bodies / notes / prompts", Policy: "never collected", Default: "excluded"},
		{Field: "session refs / terminal excerpts / attachments", Policy: "never collected", Default: "excluded"},
		{Field: "provider tokens / auth headers", Policy: "never collected; any accidental match auto-redacted", Default: "excluded"},
	}
}

// Collect builds a support Bundle without mutating application state.
// It remains useful when the DB, multiplexer, or providers are unavailable.
func Collect(ctx context.Context, opts CollectOptions) Bundle {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.LookPath == nil {
		opts.LookPath = lookPathDefault
	}
	if opts.InsideTmux == nil {
		opts.InsideTmux = func() bool { return opts.Getenv("TMUX") != "" }
	}
	if opts.IncludeLogBytes <= 0 {
		opts.IncludeLogBytes = 64 << 10
	}
	cfg := opts.Config
	now := opts.Now().UTC()
	build := buildinfo.Current(storage.CurrentSchemaVersion())

	notes := []string{
		"Support bundles never include ticket bodies, notes, prompts, harness session refs, attachment contents, or terminal excerpts by default.",
		"Inspect the generated archive before sharing. Config is redacted but still review paths and doctor details.",
		"Creating a support bundle does not mutate SQLite state or require a working multiplexer/provider.",
	}
	var degraded []string

	platform := PlatformInfo{
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		GoVersion:  runtime.Version(),
		NumCPU:     runtime.NumCPU(),
		Term:       opts.Getenv("TERM"),
		ColorTerm:  opts.Getenv("COLORTERM"),
		Shell:      RedactPath(opts.Getenv("SHELL")),
		InsideTmux: opts.InsideTmux(),
		Env: SanitizeEnvMap(map[string]string{
			"TERM":                 opts.Getenv("TERM"),
			"COLORTERM":            opts.Getenv("COLORTERM"),
			"SHELL":                opts.Getenv("SHELL"),
			"KANBI_CONFIG":         opts.Getenv("KANBI_CONFIG"),
			"KANBI_DB":             opts.Getenv("KANBI_DB"),
			"KANBI_DATA_DIR":       opts.Getenv("KANBI_DATA_DIR"),
			"KANBI_STATE_DIR":      opts.Getenv("KANBI_STATE_DIR"),
			"KANBI_MULTIPLEXER":    opts.Getenv("KANBI_MULTIPLEXER"),
			"KANBI_LOG_LEVEL":      opts.Getenv("KANBI_LOG_LEVEL"),
			"GITHUB_TOKEN":         opts.Getenv("GITHUB_TOKEN"),
			"KANBI_GITHUB_TOKEN":   opts.Getenv("KANBI_GITHUB_TOKEN"),
			"KANBI_JIRA_API_TOKEN": opts.Getenv("KANBI_JIRA_API_TOKEN"),
		}),
	}

	paths := PathsInfo{
		ConfigFile: RedactPath(cfg.Paths.ConfigFile),
		DataDir:    RedactPath(cfg.Paths.DataDir),
		StateDir:   RedactPath(cfg.Paths.StateDir),
		DBPath:     RedactPath(cfg.DBPath),
	}

	redactedCfg, cfgNote := loadRedactedConfig(cfg.Paths.ConfigFile)
	if cfgNote != "" {
		degraded = append(degraded, cfgNote)
	}

	muxDefault := strings.ToLower(strings.TrimSpace(cfg.Multiplexer.Default))
	if muxDefault == "" {
		muxDefault = "tmux"
	}
	mux := MultiplexerInfo{
		Default:           muxDefault,
		TmuxSession:       cfg.TmuxSession,
		HerdrBinary:       cfg.Multiplexer.Herdr.Binary,
		HerdrSession:      cfg.Multiplexer.Herdr.Session,
		WorkspaceStrategy: cfg.Multiplexer.Herdr.WorkspaceStrategy,
		FocusOnOpen:       cfg.Multiplexer.Herdr.FocusOnOpen,
	}

	doctor := make([]DoctorResult, 0, len(opts.Doctor))
	for _, d := range opts.Doctor {
		doctor = append(doctor, DoctorResult{
			Severity: string(d.Severity),
			Name:     d.Name,
			Detail:   RedactText(d.Detail),
			Error:    RedactText(d.Error),
		})
	}

	var migrations []MigrationRow
	var diags []RuntimeDiagnosticSummary
	schemaVersion := 0
	if open := opts.OpenStore; open != nil && strings.TrimSpace(cfg.DBPath) != "" {
		store, err := open(ctx, cfg.DBPath)
		if err != nil {
			degraded = append(degraded, "database unavailable: "+RedactText(err.Error()))
		} else {
			defer store.Close()
			// Prefer read-only collection; Init mutates but is needed to ensure schema for older empty files.
			// Support-bundle must not mutate; so only query when already openable/initialized.
			rows, mErr := store.ListSchemaMigrations(ctx)
			if mErr != nil {
				degraded = append(degraded, "schema migrations unavailable: "+RedactText(mErr.Error()))
			} else {
				for _, r := range rows {
					migrations = append(migrations, MigrationRow{Version: r.Version, Name: r.Name, AppliedAt: r.AppliedAt})
					if r.Version > schemaVersion {
						schemaVersion = r.Version
					}
				}
			}
			list, dErr := store.ListRuntimeDiagnostics(ctx, 50)
			if dErr != nil {
				degraded = append(degraded, "runtime diagnostics unavailable: "+RedactText(dErr.Error()))
			} else {
				for _, d := range list {
					item := RuntimeDiagnosticSummary{
						ID:        d.ID,
						CreatedAt: d.CreatedAt,
						Kind:      d.Kind,
						Operation: d.Operation,
						Attempt:   d.Attempt,
						Message:   RedactText(d.Message),
						Cause:     RedactText(d.Cause),
					}
					if d.BoardID.Valid {
						v := d.BoardID.Int64
						item.BoardID = &v
					}
					if d.TicketID.Valid {
						v := d.TicketID.Int64
						item.TicketID = &v
					}
					if d.SessionID.Valid {
						v := d.SessionID.Int64
						item.SessionID = &v
					}
					diags = append(diags, item)
				}
			}
		}
	} else {
		degraded = append(degraded, "database not opened for support bundle")
	}
	if schemaVersion == 0 {
		schemaVersion = build.Schema
	}

	harnesses := collectHarnessPresence(cfg, opts.LookPath)

	logTail := ""
	if raw, err := ReadRecent(cfg.Paths.StateDir, opts.IncludeLogBytes); err != nil {
		degraded = append(degraded, "diagnostics log unread: "+RedactText(err.Error()))
	} else if len(raw) > 0 {
		logTail = RedactText(string(raw))
	}

	return Bundle{
		Schema:          "kanbi.v1.support_bundle",
		Format:          BundleFormat,
		FormatVersion:   BundleFormatVersion,
		GeneratedAt:     now,
		Build:           build,
		Platform:        platform,
		Paths:           paths,
		Config:          redactedCfg,
		Multiplexer:     mux,
		Migrations:      migrations,
		SchemaVersion:   schemaVersion,
		Doctor:          doctor,
		Diagnostics:     diags,
		Harnesses:       harnesses,
		RecentLog:       logTail,
		Degraded:        degraded,
		CollectionNotes: notes,
		FieldPolicies:   DefaultFieldPolicies(),
	}
}

func loadRedactedConfig(path string) (map[string]any, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, "config file absent (defaults in effect)"
		}
		return map[string]any{}, "config unread: " + RedactText(err.Error())
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return map[string]any{}, "config parse error: " + RedactText(err.Error())
	}
	if m == nil {
		m = map[string]any{}
	}
	return SanitizeConfigMap(m), ""
}

func collectHarnessPresence(cfg config.Config, lookPath func(string) (string, error)) []HarnessPresence {
	names := make([]string, 0, len(cfg.Harnesses))
	for name := range cfg.Harnesses {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]HarnessPresence, 0, len(names))
	for _, name := range names {
		h := cfg.Harnesses[name]
		cmd := ""
		if len(h.Start) > 0 {
			cmd = h.Start[0]
		}
		redactedCmd := RedactCommandPath(cmd)
		p := HarnessPresence{Name: name, Command: redactedCmd}
		if cmd == "" {
			p.LookupErr = "no start command"
			out = append(out, p)
			continue
		}
		if _, err := lookPath(cmd); err != nil {
			p.Present = false
			p.LookupErr = RedactText(strings.ReplaceAll(err.Error(), cmd, redactedCmd))
		} else {
			p.Present = true
		}
		out = append(out, p)
	}
	return out
}

var lookPathDefault = func(file string) (string, error) {
	return execLookPath(file)
}

// WriteArchive writes a path-safe zip of the bundle JSON plus a human README.
// Destination must not already exist.
func WriteArchive(destination string, bundle Bundle) (err error) {
	if strings.TrimSpace(destination) == "" {
		return errors.New("support-bundle destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("support-bundle destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	zw := zip.NewWriter(f)
	defer func() {
		if closeErr := zw.Close(); err == nil {
			err = closeErr
		}
	}()

	manifest := map[string]any{
		"format":         BundleFormat,
		"format_version": BundleFormatVersion,
		"generated_at":   bundle.GeneratedAt,
		"kanbi_version":  bundle.Build.Version,
		"commit":         bundle.Build.Commit,
		"schema_version": bundle.SchemaVersion,
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := writeZipBytes(zw, "manifest.json", manifestBytes); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	// Defense-in-depth: re-run redaction on the serialized payload text.
	payload = []byte(RedactText(string(payload)))
	if err := writeZipBytes(zw, "support-bundle.json", payload); err != nil {
		return err
	}
	readme := humanSummary(bundle)
	if err := writeZipBytes(zw, "README.txt", []byte(readme)); err != nil {
		return err
	}
	if err := writeZipBytes(zw, "field-policies.json", mustJSON(bundle.FieldPolicies)); err != nil {
		return err
	}
	return nil
}

func writeZipBytes(zw *zip.Writer, name string, data []byte) error {
	// Reject path traversal names.
	clean := filepath.ToSlash(name)
	if clean != name || strings.Contains(clean, "..") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("unsafe zip entry name %q", name)
	}
	h := &zip.FileHeader{Name: clean, Method: zip.Deflate}
	h.SetMode(0o600)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func mustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

// HumanSummary returns the human-readable support-bundle report.
func HumanSummary(bundle Bundle) string { return humanSummary(bundle) }

func humanSummary(b Bundle) string {
	var sb strings.Builder
	sb.WriteString("Kanbi support bundle\n")
	sb.WriteString("====================\n\n")
	fmt.Fprintf(&sb, "Generated: %s\n", b.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "Version:   %s\n", b.Build.Version)
	fmt.Fprintf(&sb, "Commit:    %s\n", b.Build.Commit)
	fmt.Fprintf(&sb, "Built:     %s\n", b.Build.BuildDate)
	fmt.Fprintf(&sb, "Go:        %s\n", b.Build.GoVersion)
	fmt.Fprintf(&sb, "Platform:  %s/%s\n", b.Platform.GOOS, b.Platform.GOARCH)
	fmt.Fprintf(&sb, "Schema:    %d\n", b.SchemaVersion)
	fmt.Fprintf(&sb, "Runtime:   %s\n\n", b.Multiplexer.Default)

	sb.WriteString("Collection policy\n")
	sb.WriteString("-----------------\n")
	for _, n := range b.CollectionNotes {
		fmt.Fprintf(&sb, "- %s\n", n)
	}
	sb.WriteString("\n")

	if len(b.Degraded) > 0 {
		sb.WriteString("Degraded collection\n")
		sb.WriteString("-------------------\n")
		for _, d := range b.Degraded {
			fmt.Fprintf(&sb, "- %s\n", d)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Doctor\n")
	sb.WriteString("------\n")
	for _, d := range b.Doctor {
		if d.Detail == "" {
			fmt.Fprintf(&sb, "%s %s\n", d.Severity, d.Name)
		} else {
			fmt.Fprintf(&sb, "%s %s %s\n", d.Severity, d.Name, d.Detail)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("Harness presence\n")
	sb.WriteString("----------------\n")
	for _, h := range b.Harnesses {
		status := "missing"
		if h.Present {
			status = "present"
		}
		fmt.Fprintf(&sb, "- %s (%s): %s\n", h.Name, h.Command, status)
	}
	sb.WriteString("\n")

	sb.WriteString("Runtime diagnostics (redacted)\n")
	sb.WriteString("------------------------------\n")
	if len(b.Diagnostics) == 0 {
		sb.WriteString("(none)\n")
	} else {
		for _, d := range b.Diagnostics {
			fmt.Fprintf(&sb, "- [%s] %s/%s attempt=%d: %s\n", d.CreatedAt.Format(time.RFC3339), d.Kind, d.Operation, d.Attempt, d.Message)
			if d.Cause != "" {
				fmt.Fprintf(&sb, "  cause: %s\n", d.Cause)
			}
		}
	}
	sb.WriteString("\n")

	sb.WriteString("Archive contents\n")
	sb.WriteString("----------------\n")
	sb.WriteString("- manifest.json          format/version metadata\n")
	sb.WriteString("- support-bundle.json    full structured payload (machine-readable)\n")
	sb.WriteString("- field-policies.json    per-field inclusion/redaction policy\n")
	sb.WriteString("- README.txt             this human summary\n")
	return sb.String()
}

// WriteHuman writes the human summary to w.
func WriteHuman(w io.Writer, b Bundle) error {
	_, err := io.WriteString(w, HumanSummary(b))
	return err
}

// WriteJSON writes the structured bundle JSON to w.
func WriteJSON(w io.Writer, b Bundle) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}
