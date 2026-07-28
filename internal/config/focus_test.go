package config

import "testing"

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
