package diagnostics

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestSupportBundleRedactsSecretsAndExcludesUserContent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kanbi.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgYAML := `
default_harness: pi
diagnostics:
  level: debug
multiplexer:
  default: herdr
github_token: ghp_abcdefghijklmnopqrstuvwxyz012345
backend:
  api_token: "jira-secret-token"
  site_url: "https://user:pass@example.atlassian.net"
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := store.CreateBoard(ctx, "Support")
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "SECRET TITLE SHOULD NOT APPEAR", "ticket body with Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz012345", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddNote(ctx, ticket.ID, "private note body"); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRuntimeDiagnostic(ctx, storage.RuntimeDiagnosticInput{
		Kind:      storage.DiagnosticKindSync,
		Operation: "sync_board",
		BoardID:   board.ID,
		TicketID:  ticket.ID,
		Attempt:   1,
		Message:   "sync failed",
		Cause:     "Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz012345 session_ref=abc-should-redact",
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	// Seed an opt-in log containing secrets that must be redacted in the bundle.
	log := New(Options{Dir: stateDir, Level: LevelDebug})
	log.Error("sync", "pull", "corr-1", "token=supersecret session_ref=xyz", nil)

	paths := config.Paths{ConfigFile: cfgPath, DataDir: dir, StateDir: stateDir, DBFile: dbPath}
	cfg, err := config.Normalize(config.Config{}, paths, config.NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBPath = dbPath

	bundle := Collect(ctx, CollectOptions{
		Config:   cfg,
		Doctor:   []DoctorResult{{Severity: "ok", Name: "build", Detail: "ok token=should-redact ghp_abcdefghijklmnopqrstuvwxyz012345"}},
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Getenv: func(k string) string {
			switch k {
			case "GITHUB_TOKEN":
				return "ghp_abcdefghijklmnopqrstuvwxyz012345"
			case "TERM":
				return "xterm-256color"
			case "HOME":
				return dir
			default:
				return ""
			}
		},
		OpenStore: func(ctx context.Context, p string) (*storage.Store, error) {
			return storage.Open(p)
		},
		Now: func() time.Time { return time.Date(2026, 7, 12, 15, 0, 0, 0, time.UTC) },
	})

	raw, err := jsonMarshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, banned := range []string{
		"ghp_",
		"jira-secret-token",
		"supersecret",
		"pass@",
		"SECRET TITLE",
		"private note body",
		"ticket body",
		"session_ref=abc",
		"session_ref=xyz",
		"token=should-redact",
	} {
		if strings.Contains(text, banned) {
			t.Fatalf("bundle leaked %q in payload", banned)
		}
	}
	if bundle.Build.Schema == 0 {
		t.Fatal("missing schema")
	}
	if bundle.SchemaVersion == 0 {
		t.Fatal("missing schema version")
	}
	if len(bundle.Diagnostics) == 0 {
		t.Fatal("expected runtime diagnostics")
	}
	if !strings.Contains(bundle.Diagnostics[0].Cause, "[REDACTED]") {
		t.Fatalf("cause=%q", bundle.Diagnostics[0].Cause)
	}
	if bundle.Platform.Env["GITHUB_TOKEN"] != "[REDACTED]" {
		t.Fatalf("env token=%v", bundle.Platform.Env["GITHUB_TOKEN"])
	}

	dest := filepath.Join(dir, "support.zip")
	if err := WriteArchive(dest, bundle); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode=%o", info.Mode().Perm())
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Fatalf("unsafe entry %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, f.UncompressedSize64+1)
		n, _ := rc.Read(buf)
		_ = rc.Close()
		content := string(buf[:n])
		for _, banned := range []string{"ghp_", "jira-secret-token", "supersecret", "SECRET TITLE", "private note body"} {
			if strings.Contains(content, banned) {
				t.Fatalf("archive entry %s leaked %q", f.Name, banned)
			}
		}
	}
	for _, required := range []string{"manifest.json", "support-bundle.json", "README.txt", "field-policies.json"} {
		if !names[required] {
			t.Fatalf("missing %s in %v", required, names)
		}
	}
}

func TestSupportBundleRedactsCustomHarnessPathInAllSurfaces(t *testing.T) {
	dir := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	privateCommand := filepath.Join(home, "private-project", "token=value", "bin", "agent")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("harnesses:\n  private:\n    start: ["+privateCommand+"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bundle := Collect(context.Background(), CollectOptions{
		Config: config.Config{
			Paths: config.Paths{ConfigFile: configPath, StateDir: dir},
			Harnesses: map[string]config.Harness{
				"private": {Start: []string{privateCommand}},
			},
		},
		LookPath: func(command string) (string, error) {
			return "", fmt.Errorf("executable %s unavailable", command)
		},
	})
	if got, want := bundle.Harnesses[0].Command, RedactCommandPath(privateCommand); got != want {
		t.Fatalf("harness command = %q, want %q", got, want)
	}

	payload, err := jsonMarshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for surface, content := range map[string]string{
		"structured payload": string(payload),
		"human summary":      HumanSummary(bundle),
	} {
		if strings.Contains(content, privateCommand) || strings.Contains(content, "private-project") {
			t.Fatalf("%s leaked custom harness path %q", surface, privateCommand)
		}
	}

	destination := filepath.Join(dir, "support.zip")
	if err := WriteArchive(destination, bundle); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		content, err := func() ([]byte, error) {
			entry, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer entry.Close()
			return io.ReadAll(entry)
		}()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), privateCommand) || strings.Contains(string(content), "private-project") {
			t.Fatalf("archive entry %s leaked custom harness path %q", file.Name, privateCommand)
		}
	}
}

func TestSupportBundleDegradedWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	paths := config.Paths{
		ConfigFile: filepath.Join(dir, "missing.yaml"),
		DataDir:    dir,
		StateDir:   dir,
		DBFile:     filepath.Join(dir, "nope.db"),
	}
	cfg, err := config.Normalize(config.Config{}, paths, config.NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBPath = paths.DBFile
	bundle := Collect(ctx, CollectOptions{
		Config: cfg,
		Doctor: []DoctorResult{{Severity: "fatal", Name: "sqlite", Detail: "missing"}},
		OpenStore: func(ctx context.Context, dbPath string) (*storage.Store, error) {
			return nil, os.ErrNotExist
		},
	})
	if len(bundle.Degraded) == 0 {
		t.Fatal("expected degraded notes")
	}
	dest := filepath.Join(dir, "degraded.zip")
	if err := WriteArchive(dest, bundle); err != nil {
		t.Fatal(err)
	}
	human := HumanSummary(bundle)
	if !strings.Contains(human, "Degraded collection") {
		t.Fatalf("human missing degraded section: %s", human)
	}
}

func jsonMarshal(v any) ([]byte, error) {
	return bindJSON(v)
}
