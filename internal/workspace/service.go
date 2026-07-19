package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
	"golang.org/x/sys/unix"
)

var stableComponentRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Service coordinates durable workspace records with observed Git/filesystem
// state. Git mutations are serialized per common directory across processes.
type Service struct {
	Store    *storage.Store
	Git      Git
	StateDir string
}

// ProvisionOptions selects a stable ticket worktree. ExistingBranch must be
// explicitly true to attach an already-created local branch.
type ProvisionOptions struct {
	BoardID        int64
	BoardUUID      string
	BoardCWD       string
	TicketID       int64
	Branch         string
	ExistingBranch bool
}

// IntegrateOptions controls the conservative source-branch merge flow.
type IntegrateOptions struct {
	ValidationCommand string
	Close             func(context.Context) error
}

// StablePath returns a title-independent location for a ticket checkout.
func (s *Service) StablePath(boardUUID string, ticketID int64) (string, error) {
	if strings.TrimSpace(s.StateDir) == "" {
		return "", errors.New("state directory is required for Git worktree mode")
	}
	component := strings.Trim(stableComponentRE.ReplaceAllString(strings.TrimSpace(boardUUID), "-"), "-.")
	if component == "" || ticketID <= 0 {
		return "", errors.New("board UUID and ticket ID are required for a stable workspace path")
	}
	return filepath.Join(s.StateDir, "worktrees", component, fmt.Sprintf("%d", ticketID)), nil
}

// Preflight performs only read-only checks. It is safe to call while editing
// the branch modal and never creates a durable workspace, branch, or path.
func (s *Service) Preflight(ctx context.Context, opts ProvisionOptions) (storage.WorkspacePreflight, error) {
	if s == nil || s.Store == nil {
		return storage.WorkspacePreflight{}, errors.New("workspace service requires storage")
	}
	branch := strings.TrimSpace(opts.Branch)
	if err := s.Git.ValidateBranch(ctx, branch); err != nil {
		return storage.WorkspacePreflight{}, err
	}
	source, err := s.Git.InspectSource(ctx, opts.BoardCWD)
	if err != nil {
		return storage.WorkspacePreflight{}, err
	}
	exists, err := s.Git.branchExists(ctx, source.RepositoryRoot, branch)
	if err != nil {
		return storage.WorkspacePreflight{}, err
	}
	if exists {
		if path, checkedOut, err := s.Git.branchWorktree(ctx, source.RepositoryRoot, branch); err != nil {
			return storage.WorkspacePreflight{}, err
		} else if checkedOut {
			return storage.WorkspacePreflight{}, fmt.Errorf("branch %q is already checked out by worktree %s", branch, path)
		}
	}
	path, err := s.StablePath(opts.BoardUUID, opts.TicketID)
	if err != nil {
		return storage.WorkspacePreflight{}, err
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		return storage.WorkspacePreflight{}, fmt.Errorf("stable workspace path already exists: %s", path)
	} else if !os.IsNotExist(statErr) {
		return storage.WorkspacePreflight{}, statErr
	}
	return storage.WorkspacePreflight{
		SourceBranch: source.SourceBranch,
		SourceCommit: source.SourceCommitSHA,
		SourceDirty:  source.Dirty,
		Branch:       branch,
		BranchExists: exists,
	}, nil
}

// Provision creates or validates the current durable workspace. A durable
// provisioning claim is written before the filesystem is mutated.
func (s *Service) Provision(ctx context.Context, opts ProvisionOptions) (storage.Workspace, error) {
	if s == nil || s.Store == nil {
		return storage.Workspace{}, errors.New("workspace service requires storage")
	}
	if opts.BoardID <= 0 || opts.TicketID <= 0 {
		return storage.Workspace{}, errors.New("workspace requires board and ticket IDs")
	}
	if current, ok, err := s.Store.CurrentWorkspace(ctx, opts.TicketID); err != nil {
		return storage.Workspace{}, err
	} else if ok {
		if current.BoardID != opts.BoardID {
			return storage.Workspace{}, errors.New("current workspace belongs to a different board")
		}
		if current.State == storage.WorkspaceStateIntegrated {
			return s.Rehydrate(ctx, current)
		}
		if err := s.Validate(ctx, current); err != nil {
			_ = s.Store.MarkWorkspaceState(ctx, current.ID, storage.WorkspaceStateRepairNeeded, err.Error())
			return storage.Workspace{}, err
		}
		return current, nil
	}

	preflight, err := s.Preflight(ctx, opts)
	if err != nil {
		return storage.Workspace{}, err
	}
	if opts.ExistingBranch != preflight.BranchExists {
		if preflight.BranchExists {
			return storage.Workspace{}, fmt.Errorf("branch %q already exists; explicit existing-branch selection is required", preflight.Branch)
		}
		return storage.Workspace{}, fmt.Errorf("existing branch %q was not found", preflight.Branch)
	}
	source, err := s.Git.InspectSource(ctx, opts.BoardCWD)
	if err != nil {
		return storage.Workspace{}, err
	}
	branch := preflight.Branch
	path, err := s.StablePath(opts.BoardUUID, opts.TicketID)
	if err != nil {
		return storage.Workspace{}, err
	}
	launchCWD := filepath.Join(path, source.LaunchSubdir)
	claim := storage.Workspace{
		TicketID: opts.TicketID, BoardID: opts.BoardID, Kind: storage.WorkspaceKindGitWorktree,
		State: storage.WorkspaceStateProvisioning, IsCurrent: true, OwnsWorktree: true,
		RepositoryRoot: source.RepositoryRoot, CommonDir: source.CommonDir, WorktreePath: path,
		LaunchSubdir: source.LaunchSubdir, LaunchCWD: launchCWD, BranchName: branch,
		SourceBranch: source.SourceBranch, SourceCommitSHA: source.SourceCommitSHA, BaseCommitSHA: source.SourceCommitSHA,
	}
	claim.ID, err = s.Store.CreateWorkspaceClaim(ctx, claim)
	if err != nil {
		return storage.Workspace{}, err
	}
	fail := func(cause error) (storage.Workspace, error) {
		_ = s.Store.MarkWorkspaceState(ctx, claim.ID, storage.WorkspaceStateRepairNeeded, cause.Error())
		return storage.Workspace{}, cause
	}

	err = s.withRepoLock(ctx, source.CommonDir, func() error {
		exists, err := s.Git.branchExists(ctx, source.RepositoryRoot, branch)
		if err != nil {
			return err
		}
		if opts.ExistingBranch != exists {
			return errors.New("workspace branch changed during provisioning; retry")
		}
		if exists {
			if path, checkedOut, err := s.Git.branchWorktree(ctx, source.RepositoryRoot, branch); err != nil {
				return err
			} else if checkedOut {
				return fmt.Errorf("branch %q is already checked out by worktree %s", branch, path)
			}
		}
		if _, statErr := os.Lstat(path); statErr == nil {
			return fmt.Errorf("stable workspace path already exists: %s", path)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return s.Git.worktreeAdd(ctx, source.RepositoryRoot, path, branch, source.SourceCommitSHA, opts.ExistingBranch)
	})
	if err != nil {
		return fail(fmt.Errorf("provision Git worktree: %w", err))
	}
	claim.State = storage.WorkspaceStateReady
	if err := s.Validate(ctx, claim); err != nil {
		return fail(fmt.Errorf("validate provisioned workspace: %w", err))
	}
	if err := s.Store.UpdateWorkspace(ctx, claim); err != nil {
		return storage.Workspace{}, err
	}
	obs, observeErr := s.Observe(ctx, claim)
	if observeErr != nil {
		_ = s.Store.RecordWorkspaceObservationError(ctx, claim.ID, observeErr.Error())
		claim.LastError = sql.NullString{String: observeErr.Error(), Valid: true}
		return claim, nil
	}
	claim.LastStatusJSON = sql.NullString{String: mustJSON(obs), Valid: true}
	return claim, nil
}

// Validate verifies path, repository identity, branch, and launch subdirectory.
func (s *Service) Validate(ctx context.Context, w storage.Workspace) error {
	if w.Kind != storage.WorkspaceKindGitWorktree {
		return fmt.Errorf("unsupported workspace kind %q", w.Kind)
	}
	if !pathIsDir(w.WorktreePath) {
		return fmt.Errorf("workspace path is missing: %s", w.WorktreePath)
	}
	actual, err := s.Git.InspectSource(ctx, w.WorktreePath)
	if err != nil {
		return fmt.Errorf("inspect workspace: %w", err)
	}
	if !samePath(actual.RepositoryRoot, w.WorktreePath) {
		return fmt.Errorf("workspace repository root mismatch: got %s", actual.RepositoryRoot)
	}
	if !samePath(actual.CommonDir, w.CommonDir) {
		return errors.New("workspace belongs to a different Git repository")
	}
	if actual.SourceBranch != w.BranchName {
		return fmt.Errorf("workspace branch mismatch: got %q, want %q", actual.SourceBranch, w.BranchName)
	}
	wantLaunch := filepath.Join(w.WorktreePath, w.LaunchSubdir)
	if !samePath(wantLaunch, w.LaunchCWD) || !pathIsDir(w.LaunchCWD) {
		return fmt.Errorf("workspace launch directory is missing or changed: %s", w.LaunchCWD)
	}
	return nil
}

// Rehydrate recreates an intentionally retired integrated checkout at its exact
// recorded path from the retained local branch.
func (s *Service) Rehydrate(ctx context.Context, w storage.Workspace) (storage.Workspace, error) {
	if w.State != storage.WorkspaceStateIntegrated {
		return storage.Workspace{}, fmt.Errorf("workspace is not intentionally retired: %s", w.State)
	}
	if pathIsDir(w.WorktreePath) {
		if err := s.Validate(ctx, w); err != nil {
			return storage.Workspace{}, err
		}
		if err := s.Store.MarkWorkspaceRehydrated(ctx, w.ID); err != nil {
			return storage.Workspace{}, err
		}
		w.State, w.RetiredAt = storage.WorkspaceStateReady, sql.NullTime{}
		return w, nil
	}
	err := s.withRepoLock(ctx, w.CommonDir, func() error {
		exists, err := s.Git.branchExists(ctx, w.RepositoryRoot, w.BranchName)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("retained workspace branch %q is missing", w.BranchName)
		}
		if path, checkedOut, err := s.Git.branchWorktree(ctx, w.RepositoryRoot, w.BranchName); err != nil {
			return err
		} else if checkedOut {
			if samePath(path, w.WorktreePath) && pathIsDir(w.WorktreePath) {
				return s.Validate(ctx, w)
			}
			return fmt.Errorf("retained branch %q is checked out by another worktree %s", w.BranchName, path)
		}
		if _, statErr := os.Lstat(w.WorktreePath); statErr == nil {
			return fmt.Errorf("retired workspace path is occupied: %s", w.WorktreePath)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if err := os.MkdirAll(filepath.Dir(w.WorktreePath), 0o700); err != nil {
			return err
		}
		return s.Git.worktreeAdd(ctx, w.RepositoryRoot, w.WorktreePath, w.BranchName, "", true)
	})
	if err != nil {
		_ = s.Store.RecordWorkspaceObservationError(context.Background(), w.ID, err.Error())
		return storage.Workspace{}, fmt.Errorf("rehydrate workspace: %w", err)
	}
	if err := s.Validate(ctx, w); err != nil {
		_ = s.Store.MarkWorkspaceState(ctx, w.ID, storage.WorkspaceStateRepairNeeded, err.Error())
		return storage.Workspace{}, err
	}
	if err := s.Store.MarkWorkspaceRehydrated(ctx, w.ID); err != nil {
		return storage.Workspace{}, err
	}
	w.State, w.RetiredAt = storage.WorkspaceStateReady, sql.NullTime{}
	if _, err := s.Observe(ctx, w); err != nil {
		_ = s.Store.RecordWorkspaceObservationError(ctx, w.ID, err.Error())
	}
	return w, nil
}

// Observe validates and persists the latest Git status projection.
func (s *Service) Observe(ctx context.Context, w storage.Workspace) (Observation, error) {
	if err := s.Validate(ctx, w); err != nil {
		return Observation{}, err
	}
	obs, err := s.Git.Observe(ctx, w.WorktreePath, w.SourceBranch)
	if err != nil {
		return Observation{}, err
	}
	if s.Store != nil && w.ID != 0 {
		if err := s.Store.SaveWorkspaceStatusJSON(ctx, w.ID, mustJSON(obs)); err != nil {
			return Observation{}, err
		}
	}
	return obs, nil
}

// Resolve merges the recorded source branch into the ticket worktree. Conflicts
// are intentionally retained for the agent to resolve in that workspace.
func (s *Service) Resolve(ctx context.Context, w storage.Workspace) (Observation, error) {
	if err := s.Validate(ctx, w); err != nil {
		return Observation{}, err
	}
	if err := s.Store.MarkWorkspaceState(ctx, w.ID, storage.WorkspaceStateResolving, ""); err != nil {
		return Observation{}, err
	}
	_, mergeErr := s.Git.run(ctx, w.WorktreePath, "merge", "--no-edit", w.SourceBranch)
	obs, observeErr := s.Git.Observe(ctx, w.WorktreePath, w.SourceBranch)
	if observeErr == nil {
		_ = s.Store.SaveWorkspaceStatusJSON(ctx, w.ID, mustJSON(obs))
	}
	if mergeErr != nil {
		_ = s.Store.MarkWorkspaceState(ctx, w.ID, storage.WorkspaceStateResolving, mergeErr.Error())
		if observeErr != nil {
			return Observation{}, errors.Join(mergeErr, observeErr)
		}
		return obs, mergeErr
	}
	if observeErr != nil {
		return Observation{}, observeErr
	}
	if err := s.Store.MarkWorkspaceState(ctx, w.ID, storage.WorkspaceStateReady, ""); err != nil {
		return Observation{}, err
	}
	return obs, nil
}

// Integrate merges the clean ticket branch into the exact recorded source
// checkout/branch, validates, and then retires only the linked checkout. The
// local branch and current durable workspace remain for exact-path rehydration.
// Failures roll the source checkout back to its pre-merge commit.
func (s *Service) Integrate(ctx context.Context, w storage.Workspace, opts IntegrateOptions) error {
	if err := s.Validate(ctx, w); err != nil {
		return err
	}
	obs, err := s.Observe(ctx, w)
	if err != nil {
		return err
	}
	if obs.Dirty || len(obs.Conflicts) != 0 {
		return errors.New("workspace must be committed and conflict-free before integration")
	}
	if obs.MergeabilityKnown && !obs.Mergeable {
		return errors.New("workspace is not mergeable into its recorded source branch; resolve conflicts first")
	}
	source, err := s.Git.InspectSource(ctx, w.RepositoryRoot)
	if err != nil {
		return err
	}
	if !samePath(source.CommonDir, w.CommonDir) || source.SourceBranch != w.SourceBranch {
		return fmt.Errorf("source checkout must remain on recorded branch %q", w.SourceBranch)
	}
	if source.Dirty {
		return errors.New("source checkout must be clean before integration")
	}
	if opts.Close != nil {
		if err := opts.Close(ctx); err != nil {
			return fmt.Errorf("close active ticket session: %w", err)
		}
	}
	return s.withRepoLock(ctx, w.CommonDir, func() error {
		// Re-check after acquiring the cross-process repository lock.
		fresh, err := s.Git.InspectSource(ctx, w.RepositoryRoot)
		if err != nil {
			return err
		}
		if fresh.SourceBranch != w.SourceBranch || fresh.Dirty {
			return errors.New("source checkout changed while waiting for integration lock")
		}
		freshWorkspace, ok, err := s.Store.WorkspaceByID(ctx, w.ID)
		if err != nil || !ok {
			if err != nil {
				return err
			}
			return errors.New("workspace record disappeared before integration")
		}
		if err := s.Validate(ctx, freshWorkspace); err != nil {
			return err
		}
		currentObs, err := s.Git.Observe(ctx, freshWorkspace.WorktreePath, freshWorkspace.SourceBranch)
		if err != nil {
			return err
		}
		if currentObs.Dirty || len(currentObs.Conflicts) > 0 {
			return errors.New("workspace changed while preparing integration; commit and retry")
		}
		preMerge := fresh.SourceCommitSHA
		rollback := func(cause error) error {
			_, _ = s.Git.run(context.Background(), w.RepositoryRoot, "merge", "--abort")
			_, resetErr := s.Git.run(context.Background(), w.RepositoryRoot, "reset", "--hard", preMerge)
			_, cleanErr := s.Git.run(context.Background(), w.RepositoryRoot, "clean", "-fd")
			return errors.Join(cause, resetErr, cleanErr)
		}
		if _, err := s.Git.run(ctx, w.RepositoryRoot, "merge", "--no-edit", w.BranchName); err != nil {
			return rollback(fmt.Errorf("merge ticket branch: %w", err))
		}
		if command := strings.TrimSpace(opts.ValidationCommand); command != "" {
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
			cmd.Dir = w.RepositoryRoot
			if out, err := cmd.CombinedOutput(); err != nil {
				return rollback(fmt.Errorf("integration validation failed: %s: %w", strings.TrimSpace(string(out)), err))
			}
		}
		if err := s.Store.MarkWorkspaceIntegrated(ctx, w.ID); err != nil {
			return fmt.Errorf("record successful integration before cleanup: %w", err)
		}
		if _, err := s.Git.run(ctx, w.RepositoryRoot, "worktree", "remove", w.WorktreePath); err != nil {
			_ = s.Store.MarkWorkspaceCleanupRequired(ctx, w.ID, err.Error())
			return fmt.Errorf("remove integrated worktree: %w", err)
		}
		return nil
	})
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func (s *Service) withRepoLock(ctx context.Context, commonDir string, fn func() error) error {
	if strings.TrimSpace(s.StateDir) == "" {
		return errors.New("state directory is required for repository locking")
	}
	digest := sha256.Sum256([]byte(filepath.Clean(commonDir)))
	lockRoot := filepath.Join(s.StateDir, "worktree-locks")
	if err := os.MkdirAll(lockRoot, 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(lockRoot, hex.EncodeToString(digest[:])+".flock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	for {
		err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
			return fn()
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
