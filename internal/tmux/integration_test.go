package tmux

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
)

func TestLaunchIntegrationWithHerdrUsesPaneFirstAgentStart(t *testing.T) {
	ctx := context.Background()
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board"}`,
		"agent start help": `--kind <KIND> --pane <ID>`,
		"pane list":        `{"result":{"panes":[{"pane_id":"ws-board:p1","workspace_id":"ws-board"}]}}`,
		"pane split":       `{"result":{"pane":{"pane_id":"ws-board:p2","workspace_id":"ws-board"}}}`,
		"pane move":        `{"result":{"move_result":{"pane":{"pane_id":"ws-board:p2"}}}}`,
		"agent start":      `{"result":{"agent":{"name":"integration-agent","pane_id":"ws-board:p2"}}}`,
		"agent focus":      `{"ok":true}`,
	})
	cfg := config.Defaults(config.Paths{DBFile: "/tmp/kanbi-integration-test.db"})
	cfg.DBPath = cfg.Paths.DBFile
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Runner: &failIfTmuxRunner{t: t}}

	runtime, err := manager.LaunchIntegration(ctx, integrationpkg.LaunchSpec{PublicID: "run-123", Name: "integration-run", CWD: "/tmp/integration-worktree", Harness: "pi", Prompt: "integrate exact commits", Token: "secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.MuxContainerID.String != "integration-agent" || !strings.Contains(runtime.MuxMetadata.String, "ws-board:p2") {
		t.Fatalf("runtime=%+v", runtime)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	for _, want := range []string{
		"pane split ws-board:p1 --direction right --cwd /tmp/integration-worktree",
		"--env KANBI_INTEGRATION_RUN_ID=run-123",
		"--env KANBI_INTEGRATION_TOKEN=secret-token",
		"agent start integration-run --kind pi --pane ws-board:p2 --",
		"integrate exact commits",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("Herdr log %q missing %q", log, want)
		}
	}
	if strings.Contains(log, "agent start integration-run --cwd") || strings.Contains(log, "agent start integration-run --workspace") {
		t.Fatalf("Herdr launch used legacy flags despite pane-first support: %s", log)
	}
}

func TestLaunchIntegrationUsesManagedCWDPromptAndToken(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	cfg := config.Defaults(config.Paths{DBFile: "/tmp/kanbi-integration-test.db"})
	cfg.DBPath = cfg.Paths.DBFile
	manager := &Manager{Config: cfg, Runner: runner}
	runtime, err := manager.LaunchIntegration(ctx, integrationpkg.LaunchSpec{PublicID: "run-123", Name: "integration-run", CWD: "/tmp/integration-worktree", Harness: "pi", Prompt: "integrate exact commits", Token: "secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.MuxContainerID.String == "" {
		t.Fatalf("runtime=%+v", runtime)
	}
	for _, call := range runner.calls {
		if len(call.args) == 0 || call.args[0] != "new-window" {
			continue
		}
		joined := strings.Join(call.args, " ")
		for _, want := range []string{"-c /tmp/integration-worktree", "KANBI_INTEGRATION_RUN_ID=run-123", "KANBI_INTEGRATION_TOKEN=secret-token", "integrate exact commits"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("launch %q missing %q", joined, want)
			}
		}
		return
	}
	t.Fatal("integration window was not launched")
}
