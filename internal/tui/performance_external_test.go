package tui

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	kanbiruntime "github.com/carlotran4/kanbi/internal/runtime"
	"github.com/carlotran4/kanbi/internal/storage"
)

// Uses actual Git processes and optionally a private real Herdr server. Both
// fixtures are disposable; no harness, provider, or user database is involved.
func TestPerformanceExternal(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("opt-in external-process measurements")
	}
	t.Run("worktrees=10", func(t *testing.T) {
		store, ctx := newTestStore(t)
		view := defaultBoardView(t, ctx, store)
		root := t.TempDir()
		repo := filepath.Join(root, "repo")
		if err := os.Mkdir(repo, 0700); err != nil {
			t.Fatal(err)
		}
		git := func(args ...string) string {
			t.Helper()
			out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		git("init", "-b", "main")
		git("-c", "user.name=Kanbi Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
		sha := git("rev-parse", "HEAD")
		for i := 0; i < 10; i++ {
			ticket := createTicket(t, ctx, store, view.Columns[0].ID, fmt.Sprintf("worktree %d", i), "body", "pi")
			path, branch := filepath.Join(root, fmt.Sprintf("wt-%d", i)), fmt.Sprintf("ticket-%d", i)
			git("worktree", "add", "-b", branch, path)
			_, err := store.CreateWorkspaceClaim(ctx, storage.Workspace{TicketID: ticket.ID, BoardID: view.Board.ID, Kind: storage.WorkspaceKindGitWorktree, State: storage.WorkspaceStateReady, OwnsWorktree: true, RepositoryRoot: repo, CommonDir: filepath.Join(repo, ".git"), WorktreePath: path, LaunchCWD: path, BranchName: branch, SourceBranch: "main", SourceCommitSHA: sha, BaseCommitSHA: sha})
			if err != nil {
				t.Fatal(err)
			}
		}
		manager := kanbiruntime.NewManager(config.Defaults(config.Paths{}), store)
		defer manager.Close()
		// Report the uncached pass separately from steady polling; the cache has a
		// five-second lifetime, so this measures one warm polling burst.
		start := time.Now()
		if err := manager.RefreshRuntime(ctx); err != nil {
			t.Fatal(err)
		}
		t.Logf("PERF real-10-worktrees-refresh-cold n=1 elapsed=%.3fms", float64(time.Since(start).Nanoseconds())/1e6)
		measurePerformance(t, "real-10-worktrees-refresh-warm", 30, func() {
			if err := manager.RefreshRuntime(ctx); err != nil {
				t.Fatal(err)
			}
		})
		workspaces, err := store.ListCurrentWorkspaces(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range workspaces {
			if !w.LastStatusJSON.Valid || w.LastError.Valid {
				t.Fatalf("workspace not successfully observed: %+v", w)
			}
		}
	})
	t.Run("sessions=10", func(t *testing.T) {
		if os.Getenv("KANBI_PERFORMANCE_HERDR") != "1" {
			t.Skip("requires private real Herdr server")
		}
		store, ctx := newTestStore(t)
		view := defaultBoardView(t, ctx, store)
		root, err := filepath.Abs("../..")
		if err != nil {
			t.Fatal(err)
		}
		work := filepath.Join(t.TempDir(), "herdr")
		fixture := filepath.Join(root, "scripts/herdr-fixture.py")
		if out, err := exec.Command("python3", fixture, "start", work, "160", "45").CombinedOutput(); err != nil {
			t.Fatalf("start Herdr: %v: %s", err, out)
		}
		t.Cleanup(func() {
			if out, err := exec.Command("python3", fixture, "stop", work).CombinedOutput(); err != nil {
				t.Errorf("stop Herdr: %v: %s", err, out)
			}
		})
		for _, name := range []string{"HERDR_SESSION", "HERDR_CLIENT_SOCKET_PATH", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "HERDR_ENV"} {
			t.Setenv(name, "")
		}
		t.Setenv("HERDR_SOCKET_PATH", filepath.Join(work, "herdr.sock"))
		run := func(args ...string) map[string]any {
			t.Helper()
			out, err := exec.Command("herdr", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("herdr %v: %v: %s", args, err, out)
			}
			if len(strings.TrimSpace(string(out))) == 0 {
				return nil
			}
			var obj map[string]any
			if err := json.Unmarshal(out, &obj); err != nil {
				t.Fatal(err)
			}
			return obj["result"].(map[string]any)
		}
		space := run("workspace", "create", "--cwd", work, "--label", "performance", "--no-focus")["workspace"].(map[string]any)["workspace_id"].(string)
		for i := 0; i < 10; i++ {
			ticket := createTicket(t, ctx, store, view.Columns[0].ID, fmt.Sprintf("session %d", i), "body", "pi")
			name := fmt.Sprintf("perf-ticket-%d", i)
			pane := run("tab", "create", "--workspace", space, "--cwd", work, "--label", name, "--no-focus")["root_pane"].(map[string]any)["pane_id"].(string)
			run("pane", "run", pane, "printf 'working on fixture\\n'; sleep 120")
			metadata, _ := json.Marshal(map[string]string{"pane_id": pane})
			_, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", Multiplexer: "herdr", MuxNamespace: sql.NullString{String: space, Valid: true}, MuxContainerID: sql.NullString{String: pane, Valid: true}, MuxContainerName: sql.NullString{String: name, Valid: true}, MuxMetadata: sql.NullString{String: string(metadata), Valid: true}, Status: "running"})
			if err != nil {
				t.Fatal(err)
			}
		}
		manager := kanbiruntime.NewManager(config.Defaults(config.Paths{}), store)
		defer manager.Close()
		measurePerformance(t, "real-10-herdr-sessions-refresh", 30, func() {
			if err := manager.RefreshRuntime(ctx); err != nil {
				t.Fatal(err)
			}
		})
		tickets, err := store.ListTickets(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, ticket := range tickets {
			if !ticket.SessionActive {
				t.Fatalf("observer lost real session: %+v", ticket)
			}
		}
	})
}
