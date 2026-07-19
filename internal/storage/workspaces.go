package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const workspaceSelectSQL = `select id,ticket_id,board_id,kind,state,is_current,owns_worktree,repository_root,common_dir,worktree_path,launch_subdir,launch_cwd,branch_name,source_branch,source_commit_sha,base_commit_sha,last_status_json,last_error,integrated_at,retired_at,created_at,updated_at from ticket_workspaces`

type workspaceScanner interface {
	Scan(dest ...any) error
}

func scanWorkspace(row workspaceScanner, w *Workspace) error {
	var isCurrent, owns int
	if err := row.Scan(
		&w.ID, &w.TicketID, &w.BoardID, &w.Kind, &w.State, &isCurrent, &owns,
		&w.RepositoryRoot, &w.CommonDir, &w.WorktreePath, &w.LaunchSubdir, &w.LaunchCWD,
		&w.BranchName, &w.SourceBranch, &w.SourceCommitSHA, &w.BaseCommitSHA,
		&w.LastStatusJSON, &w.LastError, &w.IntegratedAt, &w.RetiredAt, &w.CreatedAt, &w.UpdatedAt,
	); err != nil {
		return err
	}
	w.IsCurrent = isCurrent == 1
	w.OwnsWorktree = owns == 1
	return nil
}

// CurrentWorkspace returns the current workspace for a ticket. Integrated
// workspaces remain current while their filesystem checkout is retired so they
// can be rehydrated at the same launch path.
func (s *Store) CurrentWorkspace(ctx context.Context, ticketID int64) (Workspace, bool, error) {
	var w Workspace
	err := scanWorkspace(s.db.QueryRowContext(ctx, workspaceSelectSQL+` where ticket_id=? and is_current=1 order by id desc limit 1`, ticketID), &w)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, false, nil
	}
	if err != nil {
		return Workspace{}, false, err
	}
	return w, true, nil
}

// WorkspaceByID returns a workspace by primary key.
func (s *Store) WorkspaceByID(ctx context.Context, id int64) (Workspace, bool, error) {
	var w Workspace
	err := scanWorkspace(s.db.QueryRowContext(ctx, workspaceSelectSQL+` where id=?`, id), &w)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, false, nil
	}
	if err != nil {
		return Workspace{}, false, err
	}
	return w, true, nil
}

// ListCurrentWorkspaces returns all current workspaces, optionally filtered by board.
func (s *Store) ListCurrentWorkspaces(ctx context.Context, boardID int64) ([]Workspace, error) {
	query := workspaceSelectSQL + ` where is_current=1`
	var args []any
	if boardID > 0 {
		query += ` and board_id=?`
		args = append(args, boardID)
	}
	query += ` order by id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		if err := scanWorkspace(rows, &w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CreateWorkspaceClaims inserts a new current workspace and retires any previous current claim.
// The caller is responsible for provisioning the filesystem/Git resource and advancing state.
func (s *Store) CreateWorkspaceClaim(ctx context.Context, w Workspace) (int64, error) {
	if w.TicketID == 0 || w.BoardID == 0 {
		return 0, errors.New("workspace requires ticket and board")
	}
	if strings.TrimSpace(w.Kind) == "" {
		w.Kind = WorkspaceKindGitWorktree
	}
	if strings.TrimSpace(w.State) == "" {
		w.State = WorkspaceStateProvisioning
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Serialize with board lifecycle through the board row.
	if _, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=?`, w.BoardID); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	var current int
	if err := tx.QueryRowContext(ctx, `select count(*) from ticket_workspaces where ticket_id=? and is_current=1`, w.TicketID).Scan(&current); err != nil {
		return 0, err
	}
	if current > 0 {
		return 0, errors.New("ticket already has a current workspace")
	}
	owns := 0
	if w.OwnsWorktree {
		owns = 1
	}
	res, err := tx.ExecContext(ctx, `insert into ticket_workspaces(
ticket_id,board_id,kind,state,is_current,owns_worktree,repository_root,common_dir,worktree_path,launch_subdir,launch_cwd,branch_name,source_branch,source_commit_sha,base_commit_sha,last_status_json,last_error,created_at,updated_at
) values(?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		w.TicketID, w.BoardID, w.Kind, w.State, owns,
		w.RepositoryRoot, w.CommonDir, w.WorktreePath, w.LaunchSubdir, w.LaunchCWD,
		w.BranchName, w.SourceBranch, w.SourceCommitSHA, w.BaseCommitSHA,
		nullStringValue(w.LastStatusJSON), nullStringValue(w.LastError), now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			return 0, errors.New("ticket already has a current workspace")
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateWorkspace replaces mutable workspace fields for an existing row.
func (s *Store) UpdateWorkspace(ctx context.Context, w Workspace) error {
	if w.ID == 0 {
		return errors.New("workspace id is required")
	}
	owns := 0
	if w.OwnsWorktree {
		owns = 1
	}
	current := 0
	if w.IsCurrent {
		current = 1
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update ticket_workspaces set
kind=?,state=?,is_current=?,owns_worktree=?,repository_root=?,common_dir=?,worktree_path=?,launch_subdir=?,launch_cwd=?,
branch_name=?,source_branch=?,source_commit_sha=?,base_commit_sha=?,last_status_json=?,last_error=?,
integrated_at=?,retired_at=?,updated_at=?
where id=?`,
		w.Kind, w.State, current, owns, w.RepositoryRoot, w.CommonDir, w.WorktreePath, w.LaunchSubdir, w.LaunchCWD,
		w.BranchName, w.SourceBranch, w.SourceCommitSHA, w.BaseCommitSHA, nullStringValue(w.LastStatusJSON), nullStringValue(w.LastError),
		nullTimeValue(w.IntegratedAt), nullTimeValue(w.RetiredAt), now, w.ID)
	return requireAffected(res, err)
}

// MarkWorkspaceState sets lifecycle state and optional diagnostic error text.
func (s *Store) MarkWorkspaceState(ctx context.Context, id int64, state, lastError string) error {
	if strings.TrimSpace(state) == "" {
		return errors.New("workspace state is required")
	}
	now := time.Now().UTC()
	var errText any
	if strings.TrimSpace(lastError) != "" {
		errText = lastError
	} else {
		errText = nil
	}
	res, err := s.db.ExecContext(ctx, `update ticket_workspaces set state=?, last_error=?, updated_at=? where id=?`, state, errText, now, id)
	return requireAffected(res, err)
}

// SaveWorkspaceStatusJSON stores the latest observed git status projection.
func (s *Store) SaveWorkspaceStatusJSON(ctx context.Context, id int64, statusJSON string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update ticket_workspaces set last_status_json=?, last_error=null, updated_at=? where id=?`, nullableString(statusJSON), now, id)
	return requireAffected(res, err)
}

// RecordWorkspaceObservationError preserves workspace lifecycle state while
// surfacing a stale/unavailable Git observation separately from session state.
func (s *Store) RecordWorkspaceObservationError(ctx context.Context, id int64, reason string) error {
	res, err := s.db.ExecContext(ctx, `update ticket_workspaces set last_error=?, updated_at=? where id=?`, nullableString(reason), time.Now().UTC(), id)
	return requireAffected(res, err)
}

// MarkWorkspaceIntegrated records a successful integration while retaining the
// workspace as current. retired_at means only the linked checkout was retired;
// its branch and durable identity remain available for rehydration.
func (s *Store) MarkWorkspaceIntegrated(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update ticket_workspaces set state=?, is_current=1, integrated_at=?, retired_at=?, last_error=null, updated_at=? where id=?`,
		WorkspaceStateIntegrated, now, now, now, id)
	return requireAffected(res, err)
}

// MarkWorkspaceRehydrated records that the retained branch is checked out again.
func (s *Store) MarkWorkspaceRehydrated(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `update ticket_workspaces set state=?, is_current=1, retired_at=null, last_error=null, updated_at=? where id=?`,
		WorkspaceStateReady, time.Now().UTC(), id)
	return requireAffected(res, err)
}

// MarkWorkspaceCleanupRequired records that integration succeeded but retiring
// the filesystem checkout still needs repair. The branch is never deleted.
func (s *Store) MarkWorkspaceCleanupRequired(ctx context.Context, id int64, reason string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update ticket_workspaces set state=?, is_current=1, integrated_at=coalesce(integrated_at,?), last_error=?, updated_at=? where id=?`,
		WorkspaceStateCleanupReq, now, nullableString(reason), now, id)
	return requireAffected(res, err)
}

// ClearCurrentWorkspace unsets the current flag without deleting history.
func (s *Store) ClearCurrentWorkspace(ctx context.Context, ticketID int64) error {
	_, err := s.db.ExecContext(ctx, `update ticket_workspaces set is_current=0, updated_at=? where ticket_id=? and is_current=1`, time.Now().UTC(), ticketID)
	return err
}

// AcquireRepoIntegrationLock uses the board/repo board row as a serialize point.
// Callers should that keep a short transaction around mutation while holding the lock.
func (s *Store) WithBoardMutationLock(ctx context.Context, boardID int64, fn func(tx *sql.Tx) error) error {
	if boardID == 0 {
		return errors.New("board id is required")
	}
	if fn == nil {
		return errors.New("mutation function is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=?`, boardID); err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `select count(*) from boards where id=?`, boardID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("board %d not found", boardID)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
