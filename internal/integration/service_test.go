package integration

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/workspace"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
func setup(t *testing.T) (context.Context, *storage.Store, string, storage.Board, []storage.Workspace) {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(repo, 0755)
	git(t, repo, "init", "-b", "develop")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(repo, "base"), []byte("base\n"), 0644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "base")
	s, _ := storage.OpenMemory()
	t.Cleanup(func() { s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := s.DefaultBoard(ctx)
	if err := s.SetBoardWorkdir(ctx, b.ID, repo); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBoardWorktreeMode(ctx, b.ID, storage.WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	b, _ = s.BoardByID(ctx, b.ID)
	view, _ := s.BoardViewByID(ctx, b.ID)
	wsvc := workspace.Service{Store: s, StateDir: t.TempDir()}
	var ws []storage.Workspace
	for i, name := range []string{"one", "two"} {
		ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, name, "intent "+name, "pi")
		if err != nil {
			t.Fatal(err)
		}
		w, err := wsvc.Provision(ctx, workspace.ProvisionOptions{BoardID: b.ID, BoardUUID: b.UUID, BoardCWD: repo, TicketID: ticket.ID, Branch: "feat/" + name})
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(w.WorktreePath, name), []byte(name+"\n"), 0644)
		git(t, w.WorktreePath, "add", ".")
		git(t, w.WorktreePath, "commit", "-m", name)
		_ = i
		ws = append(ws, w)
	}
	return ctx, s, repo, b, ws
}

func TestCreateReportPromoteIntegrationRun(t *testing.T) {
	ctx, s, repo, b, ws := setup(t)
	state := t.TempDir()
	svc := Service{Store: s, StateDir: state}
	result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID, ws[1].ID}, Harness: "pi"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Items) != 2 || run.TokenHash == result.Token {
		t.Fatalf("run=%+v", run)
	}
	git(t, run.WorktreePath, "merge", "--no-edit", run.Items[0].HeadSHA)
	git(t, run.WorktreePath, "merge", "--no-edit", run.Items[1].HeadSHA)
	git(t, run.WorktreePath, "config", "user.email", "test@example.invalid")
	git(t, run.WorktreePath, "config", "user.name", "Test")
	head := strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateIntegrationRuntime(ctx, run.PublicID, storage.IntegrationStateRunning, storage.IntegrationRun{}); err != nil {
		t.Fatal(err)
	}
	if reported, _ := s.IntegrationRunByPublicID(ctx, run.PublicID); reported.State != storage.IntegrationStateReady {
		t.Fatalf("stale runtime refresh changed ready state to %s", reported.State)
	}
	if err := svc.Promote(ctx, run.PublicID, PromoteOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if _, err := os.Stat(filepath.Join(repo, name)); err != nil {
			t.Fatal(err)
		}
	}
	run, _ = s.IntegrationRunByPublicID(ctx, run.PublicID)
	if run.State != storage.IntegrationStatePromoted {
		t.Fatalf("state=%s", run.State)
	}
	for _, w := range ws {
		got, ok, err := s.WorkspaceByID(ctx, w.ID)
		if err != nil || !ok || got.State != storage.WorkspaceStateIntegrated {
			t.Fatalf("workspace=%+v err=%v", got, err)
		}
	}
}

func TestEligibleShowsDirtyWorkspaceAsDisabled(t *testing.T) {
	ctx, s, _, b, ws := setup(t)
	if err := os.WriteFile(filepath.Join(ws[0].WorktreePath, "uncommitted"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: s, StateDir: t.TempDir()}
	candidates, err := svc.Eligible(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.Workspace.ID == ws[0].ID {
			if candidate.Eligible || !strings.Contains(candidate.Reason, "dirty") {
				t.Fatalf("candidate=%+v", candidate)
			}
			return
		}
	}
	t.Fatal("dirty candidate missing from selection projection")
}

func TestCancelRemovesOnlyTemporaryIntegrationResources(t *testing.T) {
	ctx, s, repo, b, ws := setup(t)
	svc := Service{Store: s, StateDir: t.TempDir()}
	result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	if err := svc.Cancel(ctx, run.PublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("integration path remains: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+run.BranchName).CombinedOutput(); err == nil {
		t.Fatalf("integration branch remains: %s", out)
	}
	for _, w := range ws {
		if _, err := os.Stat(w.WorktreePath); err != nil {
			t.Fatalf("ticket workspace was removed: %v", err)
		}
	}
	run, _ = s.IntegrationRunByPublicID(ctx, run.PublicID)
	if run.State != storage.IntegrationStateCancelled {
		t.Fatalf("state=%s", run.State)
	}
}

func TestPromoteReconcilesCrashAfterSourceFastForward(t *testing.T) {
	ctx, s, repo, b, ws := setup(t)
	svc := Service{Store: s, StateDir: t.TempDir()}
	result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	git(t, run.WorktreePath, "merge", "--no-edit", run.Items[0].HeadSHA)
	head := strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkIntegrationPromoting(ctx, run.PublicID); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "merge", "--ff-only", head)
	if err := svc.Promote(ctx, run.PublicID, PromoteOptions{}); err != nil {
		t.Fatal(err)
	}
	run, _ = s.IntegrationRunByPublicID(ctx, run.PublicID)
	if run.State != storage.IntegrationStatePromoted {
		t.Fatalf("state=%s", run.State)
	}
	w, _, _ := s.WorkspaceByID(ctx, ws[0].ID)
	if w.State != storage.WorkspaceStateIntegrated {
		t.Fatalf("workspace state=%s", w.State)
	}
}

func TestPromoteRejectsUncommittedWritesMadeWhileClosingAgent(t *testing.T) {
	ctx, s, repo, b, ws := setup(t)
	svc := Service{Store: s, StateDir: t.TempDir()}
	result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	git(t, run.WorktreePath, "merge", "--no-edit", run.Items[0].HeadSHA)
	head := strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err != nil {
		t.Fatal(err)
	}
	ticket, _ := s.TicketByID(ctx, ws[0].TicketID)
	sessionID, err := s.ClaimSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "s", TmuxWindowName: "w", WorkspaceID: sql.NullInt64{Int64: ws[0].ID, Valid: true}, LaunchCWD: sql.NullString{String: ws[0].LaunchCWD, Valid: true}}, false)
	if err != nil {
		t.Fatal(err)
	}
	err = svc.Promote(ctx, run.PublicID, PromoteOptions{CloseTicket: func(context.Context, storage.Ticket) error {
		if err := os.WriteFile(filepath.Join(ws[0].WorktreePath, "late-uncommitted"), []byte("late"), 0644); err != nil {
			return err
		}
		return s.MarkSessionClosed(ctx, sessionID, "closed", "test", "closed")
	}})
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("promote error=%v", err)
	}
	if got := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")); got != run.SourceSHA {
		t.Fatalf("source mutated to %s", got)
	}
}

func TestCreateRejectsSecondActiveRunForRepositorySource(t *testing.T) {
	ctx, s, _, b, ws := setup(t)
	svc := Service{Store: s, StateDir: t.TempDir()}
	if _, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[1].ID}}); err == nil {
		t.Fatal("expected active repository integration rejection")
	}
}

func TestReportRejectsDirtyCandidate(t *testing.T) {
	ctx, s, _, b, ws := setup(t)
	svc := Service{Store: s, StateDir: t.TempDir()}
	result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	git(t, run.WorktreePath, "merge", "--no-edit", run.Items[0].HeadSHA)
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "untracked"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("dirty report error=%v", err)
	}
}

func TestPromoteRejectsDirtySourceAndCandidate(t *testing.T) {
	for _, dirty := range []string{"source", "candidate"} {
		t.Run(dirty, func(t *testing.T) {
			ctx, s, repo, b, ws := setup(t)
			svc := Service{Store: s, StateDir: t.TempDir()}
			result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}})
			if err != nil {
				t.Fatal(err)
			}
			run, _ := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
			git(t, run.WorktreePath, "merge", "--no-edit", run.Items[0].HeadSHA)
			head := strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
			if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err != nil {
				t.Fatal(err)
			}
			path := repo
			if dirty == "candidate" {
				path = run.WorktreePath
			}
			if err := os.WriteFile(filepath.Join(path, "late-dirty"), []byte("dirty"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := svc.Promote(ctx, run.PublicID, PromoteOptions{}); err == nil || !strings.Contains(err.Error(), "clean") {
				t.Fatalf("dirty %s promotion error=%v", dirty, err)
			}
			if got := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")); got != run.SourceSHA {
				t.Fatalf("source mutated to %s", got)
			}
		})
	}
}

func TestPromoteValidationUsesCandidateCWDAndFailureLeavesRunUnpromoted(t *testing.T) {
	ctx, s, repo, b, ws := setup(t)
	output := filepath.Join(t.TempDir(), "validation-cwd")
	svc := Service{Store: s, StateDir: t.TempDir()}
	result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}, ValidationCommand: "pwd > " + output})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	git(t, run.WorktreePath, "merge", "--no-edit", run.Items[0].HeadSHA)
	head := strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Promote(ctx, run.PublicID, PromoteOptions{}); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.ReadFile(output)
	if err != nil || strings.TrimSpace(string(cwd)) != run.WorktreePath {
		t.Fatalf("validation cwd=%q err=%v, want %s", cwd, err, run.WorktreePath)
	}
	// A failed validation happens before promoting intent or source mutation.
	ctx, s, repo, b, ws = setup(t)
	svc = Service{Store: s, StateDir: t.TempDir()}
	result, err = svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}, ValidationCommand: "echo validation failed; exit 7"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	git(t, run.WorktreePath, "merge", "--no-edit", run.Items[0].HeadSHA)
	head = strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Promote(ctx, run.PublicID, PromoteOptions{}); err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("validation promotion error=%v", err)
	}
	got, _ := s.IntegrationRunByPublicID(ctx, run.PublicID)
	if got.State != storage.IntegrationStateReady || strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")) != run.SourceSHA {
		t.Fatalf("failed validation promoted run=%+v", got)
	}
}

func TestReportRejectsWrongTokenAndMissingTicketCommit(t *testing.T) {
	ctx, s, _, b, ws := setup(t)
	svc := Service{Store: s, StateDir: t.TempDir()}
	result, err := svc.Create(ctx, CreateOptions{BoardID: b.ID, WorkspaceIDs: []int64{ws[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.IntegrationRunByPublicID(ctx, result.Run.PublicID)
	head := strings.TrimSpace(git(t, run.WorktreePath, "rev-parse", "HEAD"))
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, "wrong", run.WorktreePath, ""); err == nil {
		t.Fatal("accepted wrong token")
	}
	if err := svc.Report(ctx, run.PublicID, storage.IntegrationStateReady, head, result.Token, run.WorktreePath, ""); err == nil {
		t.Fatal("accepted candidate missing selected commit")
	}
}
