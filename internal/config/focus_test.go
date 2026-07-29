package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFocusConfigDefaultsAndWorkflowKeys(t *testing.T) {
	paths := Paths{ConfigFile: "/cfg/config.yaml", DataDir: "/data", StateDir: "/state", DBFile: "/data/kanbi.db"}
	cfg, err := Normalize(Config{Focus: Focus{Enabled: true, Limit: 2, WorkflowKeys: []string{"doing", "review", "doing", " "}}}, paths, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Focus.Enabled || cfg.Focus.Limit != 2 || len(cfg.Focus.WorkflowKeys) != 2 || cfg.Focus.WorkflowKeys[0] != "doing" || cfg.Focus.WorkflowKeys[1] != "review" {
		t.Fatalf("focus=%+v", cfg.Focus)
	}
	defaults := Defaults(paths)
	if defaults.Focus.Enabled || defaults.Focus.Limit != 3 {
		t.Fatalf("default focus=%+v", defaults.Focus)
	}
}

func TestSaveFocusPreservesOtherConfigAndCanDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kanbi", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "# keep this comment\ndefault_harness: codex\nunknown_extension:\n  value: keep\nfocus:\n  enabled: false # keep enabled comment\n  limit: 3\n  workflow_keys: [Review]\n  extension_key: keep-focus-extension # keep nested comment\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveFocus(path, Focus{Enabled: true, Limit: 4, WorkflowKeys: []string{" In Progress ", "Review", "Review"}}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, want := range []string{"# keep this comment", "unknown_extension:", "enabled: true", "# keep enabled comment", "limit: 4", "- In Progress", "- Review", "extension_key: keep-focus-extension", "# keep nested comment"} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q:\n%s", want, text)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode=%o, want 600", info.Mode().Perm())
	}
	if err := SaveFocus(path, Focus{Enabled: false, Limit: 4, WorkflowKeys: []string{"In Progress", "Review"}}); err != nil {
		t.Fatal(err)
	}
	raw, _, err := LoadRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Focus.Enabled {
		t.Fatal("focus remained enabled after durable disable")
	}
}
