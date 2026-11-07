package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestOperatorsCLIConfigurationAndFollowups(t *testing.T) {
	run, store := setupCLI(t)
	cwd := t.TempDir()
	raw := captureStdout(t, func() error {
		return run("operators", "create", "--name", "Intake", "--harness", "pi", "--cwd", cwd, "--instructions", "first", "--schedule", "15m", "--timezone", "UTC")
	})
	var o storage.Operator
	if e := json.Unmarshal([]byte(raw), &o); e != nil {
		t.Fatal(e)
	}
	id := strconv.FormatInt(o.ID, 10)
	path := filepath.Join(filepath.Dir(os.Getenv("KANBI_CONFIG")), "operators", id, "instructions.md")
	if e := os.WriteFile(path, []byte("direct edit"), 0600); e != nil {
		t.Fatal(e)
	}
	captureStdout(t, func() error { return run("operators", "edit", id, "--name", "Intake revised") })
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "direct edit" {
		t.Fatal(string(b), e)
	}
	if e = run("operators", "edit", id, "--harness", "codex"); e == nil {
		t.Fatal("immutable harness changed")
	}
	raw = captureStdout(t, func() error {
		return run("operators", "wakeups", "create", id, "--at", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "--prompt", "follow up exactly")
	})
	var result map[string]int64
	if e = json.Unmarshal([]byte(raw), &result); e != nil {
		t.Fatal(e)
	}
	if e = run("operators", "pause", id); e != nil {
		t.Fatal(e)
	}
	raw = captureStdout(t, func() error { return run("operators", "wakeups", "list", id) })
	if !strings.Contains(raw, "follow up exactly") {
		t.Fatal(raw)
	}
	if e = run("operators", "wakeups", "cancel", strconv.FormatInt(result["id"], 10)); e != nil {
		t.Fatal(e)
	}
	if e = run("operators", "resume", id); e != nil {
		t.Fatal(e)
	}
	if e = run("operators", "delete", id); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("instructions deleted", e)
	}
	ops, e := store().Operators(context.Background())
	if e != nil || len(ops) != 0 {
		t.Fatal(ops, e)
	}
}
