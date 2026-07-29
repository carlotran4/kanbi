package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitReadPrimitivesUsePorcelainCleanlinessAndRevisionBranch(t *testing.T) {
	repo := testRepo(t)
	git := Git{}
	branch, err := git.CurrentBranch(context.Background(), repo)
	if err != nil || branch != "develop" {
		t.Fatalf("CurrentBranch = %q, %v", branch, err)
	}
	head, err := git.ResolveRevision(context.Background(), repo, "HEAD")
	if err != nil || head == "" {
		t.Fatalf("ResolveRevision = %q, %v", head, err)
	}
	clean, err := git.IsClean(context.Background(), repo)
	if err != nil || !clean {
		t.Fatalf("clean repository = %v, %v", clean, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err = git.IsClean(context.Background(), repo)
	if err != nil || clean {
		t.Fatalf("tracked dirtiness = %v, %v", clean, err)
	}
	if out := gitCmd(t, repo, "checkout", "--", "."); out != "" {
		t.Fatalf("checkout output = %q", out)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err = git.IsClean(context.Background(), repo)
	if err != nil || clean {
		t.Fatalf("untracked dirtiness = %v, %v", clean, err)
	}
}

func TestGitIsAncestorClassifiesNonAncestorAndErrors(t *testing.T) {
	repo := testRepo(t)
	git := Git{}
	base, err := git.ResolveRevision(context.Background(), repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(repo, "feature"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", ".")
	gitCmd(t, repo, "commit", "-m", "feature")
	feature, err := git.ResolveRevision(context.Background(), repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ancestor, err := git.IsAncestor(context.Background(), repo, base, feature)
	if err != nil || !ancestor {
		t.Fatalf("base ancestor = %v, %v", ancestor, err)
	}
	ancestor, err = git.IsAncestor(context.Background(), repo, feature, base)
	if err != nil || ancestor {
		t.Fatalf("feature non-ancestor = %v, %v", ancestor, err)
	}
	_, err = git.IsAncestor(context.Background(), repo, "missing-revision", feature)
	if err == nil {
		t.Fatal("missing revision unexpectedly classified as non-ancestor")
	}
}

func TestRunValidationCommandHandlesBlankCWDAndFailureOutput(t *testing.T) {
	ctx := context.Background()
	out, err := RunValidationCommand(ctx, t.TempDir(), "   ")
	if err != nil || out != "" {
		t.Fatalf("blank validation = %q, %v", out, err)
	}
	cwd := t.TempDir()
	out, err = RunValidationCommand(ctx, cwd, "pwd")
	if err != nil || out != cwd {
		t.Fatalf("validation cwd = %q, %v, want %q", out, err, cwd)
	}
	out, err = RunValidationCommand(ctx, cwd, "printf 'validation failure'; exit 7")
	if err == nil || !strings.Contains(out, "validation failure") {
		t.Fatalf("failure output = %q, %v", out, err)
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("failure error = %v, want exit 7", err)
	}
}
