package tui

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/tmux"
)

// Uses actual Git processes and optionally a private real tmux server. Both
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
		manager := tmux.NewManager(config.Defaults(config.Paths{}), store)
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
		if os.Getenv("KANBI_PERFORMANCE_TMUX") != "1" {
			t.Skip("requires private real tmux server")
		}
		store, ctx := newTestStore(t)
		view := defaultBoardView(t, ctx, store)
		socket := filepath.Join(t.TempDir(), "tmux.sock")
		run := func(args ...string) string {
			t.Helper()
			out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("tmux %v: %v: %s", args, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		run("new-session", "-d", "-s", "perf", "-n", "placeholder", "sleep 120")
		defer exec.Command("tmux", "-S", socket, "kill-server").Run()
		// Let the unmodified runtime adapter select this isolated server.
		t.Setenv("TMUX", socket+",0,0")
		t.Setenv("TMUX_PANE", "")
		for i := 0; i < 10; i++ {
			ticket := createTicket(t, ctx, store, view.Columns[0].ID, fmt.Sprintf("session %d", i), "body", "pi")
			name := fmt.Sprintf("perf-ticket-%d", i)
			id := run("new-window", "-d", "-P", "-F", "#{window_id}", "-t", "perf:", "-n", name, "printf 'working on fixture\\n'; sleep 120")
			_, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "perf", TmuxWindowName: name, TmuxWindowID: sql.NullString{String: id, Valid: true}, Status: "running"})
			if err != nil {
				t.Fatal(err)
			}
		}
		manager := tmux.NewManager(config.Defaults(config.Paths{}), store)
		defer manager.Close()
		measurePerformance(t, "real-10-tmux-sessions-refresh", 30, func() {
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
