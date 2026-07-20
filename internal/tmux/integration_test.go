package tmux

import (
	"context"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
)

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
