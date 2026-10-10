package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/workspace"
)

func TestLaunchIntegrationWithHerdrUsesPaneFirstAgentStart(t *testing.T) {
	ctx := context.Background()
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board"}`,
		"agent start help": `--kind <KIND> --pane <ID>`,
		"tab create":       `{"result":{"root_pane":{"pane_id":"ws-board:p2","workspace_id":"ws-board"}}}`,
		"agent start":      `{"result":{"agent":{"name":"integration-agent","pane_id":"ws-board:p2"}}}`,
		"agent focus":      `{"ok":true}`,
	})
	cfg := config.Defaults(config.Paths{DBFile: "/tmp/kanbi-integration-test.db"})
	cfg.DBPath = cfg.Paths.DBFile
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg}

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
		"tab create --workspace ws-board --label integration-run --cwd /tmp/integration-worktree",
		"--env KANBI_INTEGRATION_RUN_ID=run-123",
		"--env KANBI_INTEGRATION_TOKEN=secret-token",
		"agent start integration-run-",
		"--kind pi --pane ws-board:p2 --",
		"agent prompt integration-agent integrate exact commits",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("Herdr log %q missing %q", log, want)
		}
	}
	if strings.Contains(log, "agent start integration-run --cwd") || strings.Contains(log, "agent start integration-run --workspace") {
		t.Fatalf("Herdr launch used legacy flags despite pane-first support: %s", log)
	}
	if strings.Contains(log, "--pane ws-board:p2 -- integrate exact commits") {
		t.Fatalf("pane-first Herdr must receive the integration prompt through pane input, not agent arguments: %s", log)
	}
}

func integrationObservationRun(t *testing.T, ref multiplexer.ContainerRef) (*storage.Store, context.Context, storage.IntegrationRun) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	store, ctx := newRuntimeTestStore(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "develop"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "base"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "base"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	board, err := store.DefaultBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBoardWorkdir(ctx, board.ID, repo); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBoardWorktreeMode(ctx, board.ID, storage.WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	board, err = store.BoardByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "candidate", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	wsSvc := &workspace.Service{Store: store, StateDir: t.TempDir()}
	w, err := wsSvc.Provision(ctx, workspace.ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: repo, TicketID: ticket.ID, Branch: "feat/candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.WorktreePath, "candidate"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "candidate"}} {
		if out, err := exec.Command("git", append([]string{"-C", w.WorktreePath}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	svc := integrationpkg.Service{Store: store, StateDir: t.TempDir(), Workspace: wsSvc}
	created, err := svc.Create(ctx, integrationpkg.CreateOptions{BoardID: board.ID, WorkspaceIDs: []int64{w.ID}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := storage.IntegrationRun{}
	ApplyContainerRefToIntegrationRun(&runtime, ref)
	if err := store.UpdateIntegrationRuntime(ctx, created.Run.PublicID, storage.IntegrationStateRunning, runtime); err != nil {
		t.Fatal(err)
	}
	run, err := store.IntegrationRunByPublicID(ctx, created.Run.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	return store, ctx, run
}

func TestRefreshIntegrationRunsNativeHerdrStateWinsOverTranscript(t *testing.T) {
	store, ctx, run := integrationObservationRun(t, multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, Namespace: "ws", ID: "agent", Name: "integration", Metadata: `{"pane_id":"pane"}`})
	bin, _ := writeFakeHerdr(t, map[string]string{"agent get": `{"state":"blocked","message":"permission required"}`, "agent read": "waiting for user"})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store}
	if err := manager.RefreshIntegrationRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.IntegrationRunByPublicID(ctx, run.PublicID)
	if got.State != storage.IntegrationStateNeedsPermission {
		t.Fatalf("state=%s, want needs_permission", got.State)
	}
}

func TestRefreshIntegrationRunsFallsBackWhenNativeStateUnknown(t *testing.T) {
	store, ctx, run := integrationObservationRun(t, multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, Namespace: "ws", ID: "agent", Name: "integration", Metadata: `{"pane_id":"pane"}`})
	bin, _ := writeFakeHerdr(t, map[string]string{"agent get": `{"state":"unknown"}`, "agent read": "Approve command? yes/no"})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store}
	if err := manager.RefreshIntegrationRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.IntegrationRunByPublicID(ctx, run.PublicID)
	if got.State != storage.IntegrationStateNeedsPermission {
		t.Fatalf("state=%s, want fallback needs_permission", got.State)
	}
}

func TestRefreshIntegrationRunsDoesNotRewriteTerminalAttentionStates(t *testing.T) {
	for _, state := range []string{storage.IntegrationStateReady, storage.IntegrationStateBlocked} {
		t.Run(state, func(t *testing.T) {
			store, ctx, run := integrationObservationRun(t, multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, Namespace: "runtime", ID: "@7", Name: "integration"})
			if err := store.ReportIntegrationRun(ctx, run.PublicID, state, "", "test"); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults(config.Paths{})
			manager := &Manager{Config: cfg, Store: store}
			if err := manager.RefreshIntegrationRuns(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := store.IntegrationRunByPublicID(ctx, run.PublicID)
			if got.State != state {
				t.Fatalf("state=%s, want %s", got.State, state)
			}
		})
	}
}
