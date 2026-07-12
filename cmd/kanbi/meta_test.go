package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/buildinfo"
)

func TestHelpAndVersionDoNotInitializePaths(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "missing", "config.yaml")
	dataPath := filepath.Join(root, "data")
	t.Setenv("KANBI_CONFIG", configPath)
	t.Setenv("KANBI_DATA_DIR", dataPath)
	for _, args := range [][]string{{"--help"}, {"help", "add"}, {"add", "--help"}, {"--version"}, {"version", "--json"}} {
		out := captureStdout(t, func() error { return run(args) })
		if strings.TrimSpace(out) == "" {
			t.Fatalf("%v produced no output", args)
		}
	}
	if _, err := os.Stat(filepath.Dir(configPath)); !os.IsNotExist(err) {
		t.Fatalf("help/version created config directory: %v", err)
	}
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Fatalf("help/version created data directory: %v", err)
	}
}

func TestVersionTextAndJSONContainReleaseMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate = oldVersion, oldCommit, oldDate })
	buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate = "v1.2.3", "deadbeef", "2026-07-11T00:00:00Z"
	text := captureStdout(t, func() error { return run([]string{"version"}) })
	for _, want := range []string{"kanbi 1.2.3", "deadbeef", "database schema:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("version text missing %q:\n%s", want, text)
		}
	}
	raw := captureStdout(t, func() error { return run([]string{"version", "--json"}) })
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got["schema"] != "kanbi.v1.version" || got["version"] != "1.2.3" || got["commit"] != "deadbeef" || got["schema_version"] == nil {
		t.Fatalf("version JSON=%v", got)
	}
}

func TestHelpDiscoveryAndInvalidTopics(t *testing.T) {
	root := captureStdout(t, func() error { return run([]string{"--help"}) })
	for _, want := range []string{"Usage:", "doctor", "backup", "kanbi help COMMAND", "Exit status"} {
		if !strings.Contains(root, want) {
			t.Fatalf("root help missing %q", want)
		}
	}
	if err := run([]string{"help", "missing"}); err == nil || !strings.Contains(err.Error(), "unknown help topic") {
		t.Fatalf("unexpected help error: %v", err)
	}
}
