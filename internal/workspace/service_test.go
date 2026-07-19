package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func testRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "init", "-b", "develop")
	gitCmd(t, repo, "config", "user.email", "kanbi-test@example.invalid")
	gitCmd(t, repo, "config", "user.name", "Kanbi Test")
	if err := os.WriteFile(filepath.Join(repo, "pkg", "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", ".")
	gitCmd(t, repo, "commit", "-m", "base")
	return repo
}

func testStore(t *testing.T) (*storage.Store, storage.Board, storage.Ticket) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := store.DefaultBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "feat/isolated", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	return store, board, ticket
}

func TestInspectSourcePreservesBranchAndSubdirectory(t *testing.T) {
	repo := testRepo(t)
	source, err := (Git{}).InspectSource(context.Background(), filepath.Join(repo, "pkg"))
	if err != nil {
		t.Fatal(err)
	}
	if source.RepositoryRoot != repo || source.LaunchSubdir != "pkg" || source.SourceBranch != "develop" || source.SourceCommitSHA == "" {
		t.Fatalf("unexpected source: %+v", source)
	}
}

func TestProvisionCreatesDistinctStableWorktreesAndReusesCurrent(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	store, board, first := testStore(t)
	if err := store.SetBoardWorkdir(ctx, board.ID, filepath.Join(repo, "pkg")); err != nil {
		t.Fatal(err)
	}
	view, _ := store.BoardViewByID(ctx, board.ID)
	second, err := store.CreateTicket(ctx, view.Columns[0].ID, "second", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: store, StateDir: t.TempDir()}
	one, err := svc.Provision(ctx, ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: filepath.Join(repo, "pkg"), TicketID: first.ID, Branch: "feat/one"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := svc.Provision(ctx, ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: filepath.Join(repo, "pkg"), TicketID: second.ID, Branch: "feat/two"})
	if err != nil {
		t.Fatal(err)
	}
	if one.WorktreePath == two.WorktreePath || one.LaunchCWD == two.LaunchCWD {
		t.Fatal("ticket worktrees are not isolated")
	}
	if filepath.Base(one.LaunchCWD) != "pkg" {
		t.Fatalf("launch cwd = %s", one.LaunchCWD)
	}
	reused, err := svc.Provision(ctx, ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: repo, TicketID: first.ID})
	if err != nil || reused.ID != one.ID {
		t.Fatalf("reuse = %+v, %v", reused, err)
	}
}

func TestIntegrateRetainsBranchAndRehydratesForRepeatedIntegration(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	store, board, ticket := testStore(t)
	svc := Service{Store: store, StateDir: t.TempDir()}
	w, err := svc.Provision(ctx, ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: repo, TicketID: ticket.ID, Branch: "feat/integrate"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.WorktreePath, "feature.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, w.WorktreePath, "add", ".")
	gitCmd(t, w.WorktreePath, "commit", "-m", "feature")
	if err := svc.Integrate(ctx, w, IntegrateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("integrated file: %v", err)
	}
	if _, err := os.Stat(w.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	if out := gitCmd(t, repo, "show-ref", "--verify", "refs/heads/feat/integrate"); out == "" {
		t.Fatal("retained branch missing")
	}
	current, ok, err := store.CurrentWorkspace(ctx, ticket.ID)
	if err != nil || !ok || current.State != storage.WorkspaceStateIntegrated || !current.RetiredAt.Valid {
		t.Fatalf("integrated current workspace = %+v ok=%v err=%v", current, ok, err)
	}

	rehydrated, err := svc.Rehydrate(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if rehydrated.WorktreePath != w.WorktreePath || !pathIsDir(rehydrated.LaunchCWD) {
		t.Fatalf("rehydrated at wrong path: %+v", rehydrated)
	}
	if err := os.WriteFile(filepath.Join(rehydrated.WorktreePath, "feature.txt"), []byte("done\ntweak\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, rehydrated.WorktreePath, "add", ".")
	gitCmd(t, rehydrated.WorktreePath, "commit", "-m", "tweak")
	if err := svc.Integrate(ctx, rehydrated, IntegrateOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "feature.txt"))
	if err != nil || string(data) != "done\ntweak\n" {
		t.Fatalf("repeated integration data=%q err=%v", data, err)
	}
}

func TestPreflightRequiresExplicitExistingBranchAndRejectsCheckedOutBranch(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	store, board, ticket := testStore(t)
	svc := Service{Store: store, StateDir: t.TempDir()}
	pre, err := svc.Preflight(ctx, ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: repo, TicketID: ticket.ID, Branch: "develop"})
	if err == nil || pre.BranchExists {
		t.Fatalf("expected checked-out branch rejection, got %+v, %v", pre, err)
	}
	gitCmd(t, repo, "branch", "feat/existing")
	pre, err = svc.Preflight(ctx, ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: repo, TicketID: ticket.ID, Branch: "feat/existing"})
	if err != nil || !pre.BranchExists {
		t.Fatalf("preflight = %+v, %v", pre, err)
	}
	if _, err := svc.Provision(ctx, ProvisionOptions{BoardID: board.ID, BoardUUID: board.UUID, BoardCWD: repo, TicketID: ticket.ID, Branch: "feat/existing"}); err == nil {
		t.Fatal("existing branch attached without explicit choice")
	}
}
