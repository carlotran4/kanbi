package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	IntegrationStatePlanning        = "planning"
	IntegrationStateRunning         = "running"
	IntegrationStateWaitingForUser  = "waiting_for_user"
	IntegrationStateNeedsPermission = "needs_permission"
	IntegrationStateReady           = "ready"
	IntegrationStateBlocked         = "blocked"
	IntegrationStatePromoting       = "promoting"
	IntegrationStateCleanupRequired = "cleanup_required"
	IntegrationStateFailed          = "failed"
	IntegrationStatePromoted        = "promoted"
	IntegrationStateCancelled       = "cancelled"
)

type IntegrationRun struct {
	ID                int64
	PublicID          string
	BoardID           int64
	State             string
	RepositoryRoot    string
	CommonDir         string
	WorktreePath      string
	BranchName        string
	SourceBranch      string
	SourceSHA         string
	CandidateSHA      sql.NullString
	Harness           string
	TokenHash         string
	Prompt            string
	ValidationCommand sql.NullString
	LastError         sql.NullString
	Multiplexer       sql.NullString
	MuxNamespace      sql.NullString
	MuxContainerID    sql.NullString
	MuxContainerName  sql.NullString
	MuxMetadata       sql.NullString
	HarnessSessionRef sql.NullString
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CompletedAt       sql.NullTime
	PromotedAt        sql.NullTime
	Items             []IntegrationRunItem
}

type IntegrationRunItem struct {
	ID          int64
	RunID       int64
	WorkspaceID int64
	TicketID    int64
	DisplayID   string
	Title       string
	BranchName  string
	HeadSHA     string
	Position    int
}

type CreateIntegrationRunInput struct {
	Run   IntegrationRun
	Items []IntegrationRunItem
}

func (s *Store) CreateIntegrationRun(ctx context.Context, in CreateIntegrationRunInput) (IntegrationRun, error) {
	if strings.TrimSpace(in.Run.PublicID) == "" || in.Run.BoardID == 0 || len(in.Items) == 0 {
		return IntegrationRun{}, errors.New("integration run requires public id, board, and items")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IntegrationRun{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	state := in.Run.State
	if state == "" {
		state = IntegrationStatePlanning
	}
	res, err := tx.ExecContext(ctx, `insert into integration_runs(public_id,board_id,state,repository_root,common_dir,worktree_path,branch_name,source_branch,source_sha,harness,token_hash,prompt,validation_command,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.Run.PublicID, in.Run.BoardID, state, in.Run.RepositoryRoot, in.Run.CommonDir, in.Run.WorktreePath, in.Run.BranchName, in.Run.SourceBranch, in.Run.SourceSHA, in.Run.Harness, in.Run.TokenHash, in.Run.Prompt, nullableString(in.Run.ValidationCommand.String), now, now)
	if err != nil {
		return IntegrationRun{}, fmt.Errorf("create integration run: %w", err)
	}
	id, _ := res.LastInsertId()
	for i, item := range in.Items {
		var valid int
		if err := tx.QueryRowContext(ctx, `select count(*) from ticket_workspaces w join tickets t on t.id=w.ticket_id where w.id=? and w.ticket_id=? and w.board_id=? and w.is_current=1 and w.common_dir=? and w.source_branch=? and w.branch_name=?`, item.WorkspaceID, item.TicketID, in.Run.BoardID, in.Run.CommonDir, in.Run.SourceBranch, item.BranchName).Scan(&valid); err != nil {
			return IntegrationRun{}, err
		}
		if valid != 1 {
			return IntegrationRun{}, fmt.Errorf("workspace %d is not an eligible current workspace", item.WorkspaceID)
		}
		if _, err := tx.ExecContext(ctx, `insert into integration_run_items(run_id,workspace_id,ticket_id,branch_name,head_sha,position) values(?,?,?,?,?,?)`, id, item.WorkspaceID, item.TicketID, item.BranchName, item.HeadSHA, i); err != nil {
			return IntegrationRun{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return IntegrationRun{}, err
	}
	return s.IntegrationRunByPublicID(ctx, in.Run.PublicID)
}

const integrationRunSelect = `select id,public_id,board_id,state,repository_root,common_dir,worktree_path,branch_name,source_branch,source_sha,candidate_sha,harness,token_hash,prompt,validation_command,last_error,multiplexer,mux_namespace,mux_container_id,mux_container_name,mux_metadata,harness_session_ref,created_at,updated_at,completed_at,promoted_at from integration_runs`

func scanIntegrationRun(row interface{ Scan(...any) error }) (IntegrationRun, error) {
	var r IntegrationRun
	err := row.Scan(&r.ID, &r.PublicID, &r.BoardID, &r.State, &r.RepositoryRoot, &r.CommonDir, &r.WorktreePath, &r.BranchName, &r.SourceBranch, &r.SourceSHA, &r.CandidateSHA, &r.Harness, &r.TokenHash, &r.Prompt, &r.ValidationCommand, &r.LastError, &r.Multiplexer, &r.MuxNamespace, &r.MuxContainerID, &r.MuxContainerName, &r.MuxMetadata, &r.HarnessSessionRef, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt, &r.PromotedAt)
	return r, err
}

func (s *Store) IntegrationRunByPublicID(ctx context.Context, publicID string) (IntegrationRun, error) {
	r, err := scanIntegrationRun(s.db.QueryRowContext(ctx, integrationRunSelect+` where public_id=?`, publicID))
	if err != nil {
		return IntegrationRun{}, err
	}
	rows, err := s.db.QueryContext(ctx, `select i.id,i.run_id,i.workspace_id,i.ticket_id,t.display_id,t.title,i.branch_name,i.head_sha,i.position from integration_run_items i join tickets t on t.id=i.ticket_id where i.run_id=? order by i.position`, r.ID)
	if err != nil {
		return IntegrationRun{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item IntegrationRunItem
		if err := rows.Scan(&item.ID, &item.RunID, &item.WorkspaceID, &item.TicketID, &item.DisplayID, &item.Title, &item.BranchName, &item.HeadSHA, &item.Position); err != nil {
			return IntegrationRun{}, err
		}
		r.Items = append(r.Items, item)
	}
	return r, rows.Err()
}

func (s *Store) ListIntegrationRuns(ctx context.Context, boardID int64) ([]IntegrationRun, error) {
	rows, err := s.db.QueryContext(ctx, integrationRunSelect+` where (?=0 or board_id=?) order by id desc`, boardID, boardID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		r, err := scanIntegrationRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, r.PublicID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]IntegrationRun, 0, len(ids))
	for _, id := range ids {
		r, err := s.IntegrationRunByPublicID(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) UpdateIntegrationRuntime(ctx context.Context, publicID, state string, ref IntegrationRun) error {
	res, err := s.db.ExecContext(ctx, `update integration_runs set state=?,multiplexer=?,mux_namespace=?,mux_container_id=?,mux_container_name=?,mux_metadata=?,harness_session_ref=?,updated_at=? where public_id=? and state in (?,?,?,?)`, state, nullStringValue(ref.Multiplexer), nullStringValue(ref.MuxNamespace), nullStringValue(ref.MuxContainerID), nullStringValue(ref.MuxContainerName), nullStringValue(ref.MuxMetadata), nullStringValue(ref.HarnessSessionRef), time.Now().UTC(), publicID, IntegrationStatePlanning, IntegrationStateRunning, IntegrationStateWaitingForUser, IntegrationStateNeedsPermission)
	if err != nil {
		return err
	}
	_, err = res.RowsAffected()
	return err
}

func (s *Store) ReportIntegrationRun(ctx context.Context, publicID, state, candidate, lastError string) error {
	if state != IntegrationStateReady && state != IntegrationStateBlocked && state != IntegrationStateFailed {
		return errors.New("invalid integration report state")
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update integration_runs set state=?,candidate_sha=?,last_error=?,completed_at=?,updated_at=? where public_id=? and state in ('planning','running','waiting_for_user','needs_permission','blocked')`, state, nullableString(candidate), nullableString(lastError), now, now, publicID)
	return requireAffected(res, err)
}

func (s *Store) CancelIntegrationRun(ctx context.Context, publicID string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update integration_runs set state=?,completed_at=?,updated_at=?,last_error=null where public_id=? and state not in (?,?,?)`, IntegrationStateCancelled, now, now, publicID, IntegrationStatePromoted, IntegrationStateCancelled, IntegrationStatePromoting)
	return requireAffected(res, err)
}

func (s *Store) MarkIntegrationPromoting(ctx context.Context, publicID string) error {
	res, err := s.db.ExecContext(ctx, `update integration_runs set state=?,updated_at=? where public_id=? and state=?`, IntegrationStatePromoting, time.Now().UTC(), publicID, IntegrationStateReady)
	return requireAffected(res, err)
}

func (s *Store) MarkIntegrationCleanupRequired(ctx context.Context, publicID, reason string) error {
	res, err := s.db.ExecContext(ctx, `update integration_runs set state=?,last_error=?,updated_at=? where public_id=? and state in (?,?)`, IntegrationStateCleanupRequired, nullableString(reason), time.Now().UTC(), publicID, IntegrationStatePromoting, IntegrationStateCleanupRequired)
	return requireAffected(res, err)
}

func (s *Store) MarkIntegrationPromoted(ctx context.Context, publicID string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update integration_runs set state=?,promoted_at=?,updated_at=?,last_error=null where public_id=? and state in (?,?)`, IntegrationStatePromoted, now, now, publicID, IntegrationStatePromoting, IntegrationStateCleanupRequired)
	return requireAffected(res, err)
}
