// Package workspace provides ticket-scoped execution workspaces. Git is the
// first adapter; durable intent remains owned by storage.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Git runs operation-scoped Git commands. It does not keep background state.
type Git struct {
	Binary string
}

// Source describes the checkout containing a board working directory.
type Source struct {
	RepositoryRoot  string
	CommonDir       string
	LaunchSubdir    string
	SourceBranch    string
	SourceCommitSHA string
	Dirty           bool
}

// Observation is the current review/integration state of a ticket worktree.
type Observation struct {
	Ahead             int      `json:"ahead"`
	Behind            int      `json:"behind"`
	Dirty             bool     `json:"dirty"`
	ChangedFiles      int      `json:"changed_files"`
	Conflicts         []string `json:"conflicts,omitempty"`
	Mergeable         bool     `json:"mergeable"`
	MergeabilityKnown bool     `json:"mergeability_known"`
}

func (g Git) binary() string {
	if strings.TrimSpace(g.Binary) == "" {
		return "git"
	}
	return g.Binary
}

func (g Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	cmdArgs := append([]string(nil), args...)
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, cmdArgs...)
	}
	cmd := exec.CommandContext(ctx, g.binary(), cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), message, err)
	}
	return stdout.String(), nil
}

// ResolveRevision resolves a revision in an explicit Git checkout.
func (g Git) ResolveRevision(ctx context.Context, dir, revision string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", revision)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CurrentBranch returns the checked-out branch and rejects detached HEAD.
func (g Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsClean reports whether porcelain-v1 status has no tracked or normal
// untracked changes.
func (g Git) IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := g.run(ctx, dir, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// IsAncestor reports whether ancestor is reachable from descendant. Git's
// ordinary non-ancestor exit code is a false result, not an execution error.
func (g Git) IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	_, err := g.run(ctx, dir, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if isExitCode(err, 1) {
		return false, nil
	}
	return false, err
}

// InspectSource resolves repository identity and the launch subdirectory while
// preserving the board checkout's exact branch and commit as integration data.
func (g Git) InspectSource(ctx context.Context, boardCWD string) (Source, error) {
	if strings.TrimSpace(boardCWD) == "" {
		return Source{}, errors.New("board working directory is required for Git worktree mode")
	}
	cwd, err := filepath.Abs(boardCWD)
	if err != nil {
		return Source{}, fmt.Errorf("resolve board working directory: %w", err)
	}
	rootOut, err := g.run(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Source{}, fmt.Errorf("inspect board Git repository: %w", err)
	}
	root, err := filepath.Abs(strings.TrimSpace(rootOut))
	if err != nil {
		return Source{}, err
	}
	commonOut, err := g.run(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Source{}, fmt.Errorf("inspect Git common directory: %w", err)
	}
	common, err := filepath.Abs(strings.TrimSpace(commonOut))
	if err != nil {
		return Source{}, err
	}
	branch, err := g.CurrentBranch(ctx, cwd)
	if err != nil {
		return Source{}, errors.New("board Git checkout must be on a branch (detached HEAD is unsupported)")
	}
	sha, err := g.ResolveRevision(ctx, cwd, "HEAD")
	if err != nil {
		return Source{}, err
	}
	clean, err := g.IsClean(ctx, cwd)
	if err != nil {
		return Source{}, err
	}
	subdir, err := filepath.Rel(root, cwd)
	if err != nil || subdir == ".." || strings.HasPrefix(subdir, ".."+string(filepath.Separator)) {
		return Source{}, errors.New("board working directory is outside its Git repository root")
	}
	if subdir == "." {
		subdir = ""
	}
	return Source{
		RepositoryRoot:  filepath.Clean(root),
		CommonDir:       filepath.Clean(common),
		LaunchSubdir:    subdir,
		SourceBranch:    branch,
		SourceCommitSHA: sha,
		Dirty:           !clean,
	}, nil
}

// ValidateBranch delegates branch grammar to the installed Git version.
func (g Git) ValidateBranch(ctx context.Context, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("workspace branch is required")
	}
	if _, err := g.run(ctx, "", "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid workspace branch %q: %w", branch, err)
	}
	return nil
}

func (g Git) branchExists(ctx context.Context, repo, branch string) (bool, error) {
	_, err := g.run(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (g Git) branchWorktree(ctx context.Context, repo, branch string) (string, bool, error) {
	out, err := g.run(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.TrimSpace(line) == "branch refs/heads/"+branch:
			return path, true, nil
		case strings.TrimSpace(line) == "":
			path = ""
		}
	}
	return "", false, nil
}

func (g Git) worktreeAdd(ctx context.Context, repo, path, branch, start string, existing bool) error {
	args := []string{"worktree", "add"}
	if existing {
		args = append(args, path, branch)
	} else {
		args = append(args, "-b", branch, path, start)
	}
	_, err := g.run(ctx, repo, args...)
	return err
}

// Observe reports branch divergence, dirtiness, conflicts, and whether the
// recorded source branch can currently merge into the ticket branch.
func (g Git) Observe(ctx context.Context, worktreePath, sourceBranch string) (Observation, error) {
	counts, err := g.run(ctx, worktreePath, "rev-list", "--left-right", "--count", sourceBranch+"...HEAD")
	if err != nil {
		return Observation{}, fmt.Errorf("compare workspace with source branch: %w", err)
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return Observation{}, fmt.Errorf("unexpected Git divergence output %q", strings.TrimSpace(counts))
	}
	behind, err := strconv.Atoi(fields[0])
	if err != nil {
		return Observation{}, err
	}
	ahead, err := strconv.Atoi(fields[1])
	if err != nil {
		return Observation{}, err
	}
	status, err := g.run(ctx, worktreePath, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return Observation{}, err
	}
	conflictOut, err := g.run(ctx, worktreePath, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return Observation{}, err
	}
	conflicts := nonEmptyLines(conflictOut)
	changed := nonEmptyLines(status)
	obs := Observation{Ahead: ahead, Behind: behind, Dirty: len(changed) > 0, ChangedFiles: len(changed), Conflicts: conflicts}
	_, mergeErr := g.run(ctx, worktreePath, "merge-tree", "--write-tree", "--no-messages", "HEAD", sourceBranch)
	if mergeErr == nil {
		obs.Mergeable, obs.MergeabilityKnown = true, true
	} else if isExitCode(mergeErr, 1) {
		obs.Mergeable, obs.MergeabilityKnown = false, true
	}
	return obs, nil
}

func nonEmptyLines(value string) []string {
	var out []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

func samePath(a, b string) bool {
	aa, errA := filepath.EvalSymlinks(a)
	bb, errB := filepath.EvalSymlinks(b)
	if errA == nil {
		a = aa
	}
	if errB == nil {
		b = bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func pathIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
