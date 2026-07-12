package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	l := New(Options{Dir: dir, Level: LevelOff})
	if l.Enabled() {
		t.Fatal("expected disabled logger")
	}
	l.Debug("cli", "x", "c1", "should not write", nil)
	if _, err := os.Stat(filepath.Join(dir, logFileName)); !os.IsNotExist(err) {
		t.Fatalf("log file should not exist, err=%v", err)
	}
}

func TestLoggerWrites0600WithStructuredFields(t *testing.T) {
	dir := t.TempDir()
	l := New(Options{Dir: dir, Level: LevelDebug, MaxBytes: 1 << 20, MaxFiles: 2})
	if !l.Enabled() {
		t.Fatal("expected enabled logger")
	}
	l.Info("sync", "pull", "op-1", "hello", map[string]string{"board_id": "3"})
	path := l.LogPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec Record
	if err := json.Unmarshal(bytesTrimLine(raw), &rec); err != nil {
		t.Fatalf("json: %v raw=%q", err, raw)
	}
	if rec.Severity != "info" || rec.Subsystem != "sync" || rec.Operation != "pull" || rec.Correlation != "op-1" || rec.Message != "hello" {
		t.Fatalf("record=%+v", rec)
	}
	if rec.Time.IsZero() {
		t.Fatal("missing timestamp")
	}
	if rec.Fields["board_id"] != "3" {
		t.Fatalf("fields=%v", rec.Fields)
	}
}

func TestLoggerRotationBounded(t *testing.T) {
	dir := t.TempDir()
	l := New(Options{Dir: dir, Level: LevelDebug, MaxBytes: 200, MaxFiles: 2})
	payload := strings.Repeat("x", 80)
	for i := 0; i < 20; i++ {
		l.Debug("cli", "rotate", "", payload, nil)
	}
	// Active log plus at most MaxFiles rotated logs.
	matches, err := filepath.Glob(filepath.Join(dir, logFileName+"*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 || len(matches) > 3 {
		t.Fatalf("unexpected rotation files: %v", matches)
	}
}

func bytesTrimLine(b []byte) []byte {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return []byte(s)
}
