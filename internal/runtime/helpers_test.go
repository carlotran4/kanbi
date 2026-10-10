package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestSlugEdgeCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World!", "hello-world"},
		{"", "ticket"},
		{"---", "ticket"},
		{strings.Repeat("a", 50), strings.Repeat("a", 40)},
		{"Fix OAuth/Redirect", "fix-oauth-redirect"},
	}
	for _, tc := range cases {
		if got := slug(tc.in); got != tc.want {
			t.Errorf("slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellQuoteEdgeCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"simple", "simple"},
		{"has space", "'has space'"},
		{"it's", `'it'\''s'`},
		{"/path/to/file", "/path/to/file"},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWindowNameAndShellCommand(t *testing.T) {
	if got := WindowName("T-001", "Fix OAuth Redirect!"); got != "T-001-fix-oauth-redirect" {
		t.Fatalf("window name = %s", got)
	}
	if got := ShellCommand([]string{"/tmp/fake harness", "resume", "abc"}); got != "'/tmp/fake harness' resume abc" {
		t.Fatalf("shell command = %s", got)
	}
}

func newRuntimeTestStore(t *testing.T) (*storage.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return store, ctx
}

func defaultBoardView(t *testing.T, ctx context.Context, store *storage.Store) storage.BoardView {
	t.Helper()
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func createTicket(t *testing.T, ctx context.Context, store *storage.Store, columnID int64, title, body, harness string) storage.Ticket {
	t.Helper()
	ticket, err := store.CreateTicket(ctx, columnID, title, body, harness)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func sqlString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestReadPiSessionRefFileRejectsMissingMalformedOrEmptyRefs(t *testing.T) {
	dir := t.TempDir()
	if _, ok := readPiSessionRefFile(filepath.Join(dir, "missing.json"), ""); ok {
		t.Fatal("missing file should not yield a ref")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`not-json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPiSessionRefFile(bad, ""); ok {
		t.Fatal("malformed JSON should not yield a ref")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"sessionId":"   "}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPiSessionRefFile(empty, ""); ok {
		t.Fatal("blank session id should not yield a ref")
	}
	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"sessionId":"  019e-good  "}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, ok := readPiSessionRefFile(valid, "")
	if !ok || ref != "019e-good" {
		t.Fatalf("valid ref = %q ok=%v", ref, ok)
	}
}

func TestCodexCaptureLockSerializesManagers(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	first := &Manager{Config: config.Defaults(config.Paths{StateDir: t.TempDir()})}
	second := &Manager{Config: config.Defaults(config.Paths{StateDir: t.TempDir()})}

	unlockFirst, err := first.acquireCodexCaptureLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlockFirst()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		unlock, err := second.acquireCodexCaptureLock(ctx)
		if err != nil {
			errs <- err
			return
		}
		acquired <- unlock
	}()

	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("second manager acquired Codex capture lock before release")
	case err := <-errs:
		t.Fatalf("second manager failed while waiting for lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	unlockFirst()
	select {
	case unlock := <-acquired:
		unlock()
	case err := <-errs:
		t.Fatalf("second manager failed to acquire released lock: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
