package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/carlotran4/kanbi/internal/kanban"
	"strings"
	"time"
)

func (s *Store) enforceSessionInvariant(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `update sessions set is_active=0 where is_active=1 and id not in (select max(id) from sessions where is_active=1 group by ticket_id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create unique index if not exists sessions_one_active_per_ticket on sessions(ticket_id) where is_active=1`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertActiveSession(ctx context.Context, ticketID int64, session Session) (int64, error) {
	return s.claimSession(ctx, ticketID, session, true)
}

// ClaimSession durably reserves the ticket before a runtime container is
// launched. A second caller cannot claim the same ticket while this attempt is
// active, even when it is using another Store/process.
func (s *Store) ClaimSession(ctx context.Context, ticketID int64, session Session, replaceActive bool) (int64, error) {
	session.Status = kanban.StateStarting
	return s.claimSession(ctx, ticketID, session, replaceActive)
}

func (s *Store) claimSession(ctx context.Context, ticketID int64, session Session, replaceActive bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Refuse active claims on archived tickets or boards so archive cannot race a launch.
	var ticketArchived, boardArchived sql.NullTime
	var worktreeMode string
	if err := tx.QueryRowContext(ctx, `select t.archived_at,b.archived_at,coalesce(b.worktree_mode,'off') from tickets t join boards b on b.id=t.board_id where t.id=?`, ticketID).Scan(&ticketArchived, &boardArchived, &worktreeMode); err != nil {
		return 0, err
	}
	if ticketArchived.Valid {
		return 0, ErrTicketArchived
	}
	if boardArchived.Valid {
		return 0, errors.New("cannot start a session on an archived board")
	}
	// Touch the parent board row so archive and session claims serialize.
	if _, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=(select board_id from tickets where id=?)`, ticketID); err != nil {
		return 0, err
	}
	if worktreeMode == WorktreeModeGit {
		if !session.WorkspaceID.Valid || strings.TrimSpace(session.LaunchCWD.String) == "" {
			return 0, errors.New("Git-worktree board sessions require a prepared workspace and launch directory")
		}
		var validWorkspace int
		if err := tx.QueryRowContext(ctx, `select count(*) from ticket_workspaces where id=? and ticket_id=? and is_current=1 and state in (?,?) and launch_cwd=?`, session.WorkspaceID.Int64, ticketID, WorkspaceStateReady, WorkspaceStateResolving, session.LaunchCWD.String).Scan(&validWorkspace); err != nil {
			return 0, err
		}
		if validWorkspace != 1 {
			return 0, errors.New("Git-worktree session workspace is not current, launchable, or owned by the ticket")
		}
	}
	now := time.Now().UTC()
	if replaceActive {
		if _, err := tx.ExecContext(ctx, `update sessions set is_active=0, updated_at=? where ticket_id=? and is_active=1`, now, ticketID); err != nil {
			return 0, err
		}
	} else {
		var active int
		if err := tx.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=? and is_active=1`, ticketID).Scan(&active); err != nil {
			return 0, err
		}
		if active > 0 {
			return 0, ErrActiveSessionExists
		}
	}
	status := session.Status
	if status == "" {
		status = kanban.StateRunning
	}
	applySessionMuxDefaults(&session)
	res, err := tx.ExecContext(ctx, `insert into sessions(ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,multiplexer,mux_namespace,mux_container_id,mux_container_name,mux_metadata,workspace_id,launch_cwd,status,is_active,started_at,last_seen_tmux_at,last_state_change_at,last_detected_state,last_detection_source,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ticketID, session.Harness, nullableString(session.HarnessSessionRef.String), nullableString(session.HarnessSessionName.String), session.TmuxSessionName, nullableString(session.TmuxWindowID.String), session.TmuxWindowName, session.Multiplexer, nullableString(session.MuxNamespace.String), nullableString(session.MuxContainerID.String), nullableString(session.MuxContainerName.String), nullableString(session.MuxMetadata.String), nullInt64Value(session.WorkspaceID), nullableString(session.LaunchCWD.String), status, 1, now, now, now, status, "system", now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			return 0, ErrActiveSessionExists
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// CompleteSessionLaunch attaches the newly-created runtime container to the
// durable claim and transitions it from starting to running.
func (s *Store) CompleteSessionLaunch(ctx context.Context, sessionID int64, session Session) error {
	applySessionMuxDefaults(&session)
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update sessions set harness_session_ref=?,harness_session_name=?,tmux_session_name=?,tmux_window_id=?,tmux_window_name=?,multiplexer=?,mux_namespace=?,mux_container_id=?,mux_container_name=?,mux_metadata=?,status=?,last_seen_tmux_at=?,last_state_change_at=?,last_detected_state=?,last_detection_source='system',updated_at=? where id=? and is_active=1 and status=?`,
		nullableString(session.HarnessSessionRef.String), nullableString(session.HarnessSessionName.String), session.TmuxSessionName, nullableString(session.TmuxWindowID.String), session.TmuxWindowName, session.Multiplexer, nullableString(session.MuxNamespace.String), nullableString(session.MuxContainerID.String), nullableString(session.MuxContainerName.String), nullableString(session.MuxMetadata.String), kanban.StateRunning, now, now, kanban.StateRunning, now, sessionID, kanban.StateStarting)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return errors.New("session launch claim is no longer active")
	}
	return nil
}

func (s *Store) FailSessionLaunch(ctx context.Context, sessionID int64, reason string) error {
	return s.MarkSessionClosed(ctx, sessionID, kanban.StateError, "system", reason)
}

func (s *Store) ActiveSession(ctx context.Context, ticketID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, sessionSelectSQL+` where ticket_id=? and is_active=1 order by id desc limit 1`, ticketID)
}

func (s *Store) LatestSession(ctx context.Context, ticketID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, sessionSelectSQL+` where ticket_id=? order by id desc limit 1`, ticketID)
}

func (s *Store) SessionByID(ctx context.Context, sessionID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, sessionSelectSQL+` where id=?`, sessionID)
}

// BindSessionLaunch records the workspace and launch directory while a durable
// session claim is still starting. Once set, these audit fields are immutable.
func (s *Store) BindSessionLaunch(ctx context.Context, sessionID int64, workspaceID sql.NullInt64, launchCWD string) error {
	launchCWD = strings.TrimSpace(launchCWD)
	if launchCWD == "" {
		return errors.New("session launch cwd is required")
	}
	res, err := s.db.ExecContext(ctx, `update sessions set workspace_id=?,launch_cwd=?,updated_at=? where id=? and is_active=1 and status=? and workspace_id is null and launch_cwd is null`,
		nullInt64Value(workspaceID), launchCWD, time.Now().UTC(), sessionID, kanban.StateStarting)
	return requireAffected(res, err)
}

const sessionSelectSQL = `select id,ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,coalesce(multiplexer,'tmux'),coalesce(mux_namespace,tmux_session_name),coalesce(mux_container_id,tmux_window_id),coalesce(mux_container_name,tmux_window_name),mux_metadata,workspace_id,launch_cwd,status,is_active,started_at,closed_at,last_seen_tmux_at,last_output_at,last_state_change_at,last_detected_state,last_attention_reason,last_detection_source,last_observed_excerpt,cast(updated_at as text) from sessions`

func (s *Store) sessionByQuery(ctx context.Context, query string, ticketID int64) (Session, bool, error) {
	var ses Session
	var active int
	err := s.db.QueryRowContext(ctx, query, ticketID).
		Scan(&ses.ID, &ses.TicketID, &ses.Harness, &ses.HarnessSessionRef, &ses.HarnessSessionName, &ses.TmuxSessionName, &ses.TmuxWindowID, &ses.TmuxWindowName, &ses.Multiplexer, &ses.MuxNamespace, &ses.MuxContainerID, &ses.MuxContainerName, &ses.MuxMetadata, &ses.WorkspaceID, &ses.LaunchCWD, &ses.Status, &active, &ses.StartedAt, &ses.ClosedAt, &ses.LastSeenTmuxAt, &ses.LastOutputAt, &ses.LastStateChangeAt, &ses.LastDetectedState, &ses.LastAttentionReason, &ses.LastDetectionSource, &ses.LastObservedExcerpt, &ses.observationVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	ses.IsActive = active == 1
	return ses, true, nil
}

func applySessionMuxDefaults(session *Session) {
	if session.Multiplexer == "" {
		session.Multiplexer = "tmux"
	}
	if !session.MuxNamespace.Valid && session.TmuxSessionName != "" {
		session.MuxNamespace = sql.NullString{String: session.TmuxSessionName, Valid: true}
	}
	if !session.MuxContainerID.Valid && session.TmuxWindowID.Valid {
		session.MuxContainerID = session.TmuxWindowID
	}
	if !session.MuxContainerName.Valid && session.TmuxWindowName != "" {
		session.MuxContainerName = sql.NullString{String: session.TmuxWindowName, Valid: true}
	}
}

func nullInt64Value(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func (s *Store) RenameSessionWindow(ctx context.Context, ticketID int64, name string) error {
	_, err := s.db.ExecContext(ctx, `update sessions set tmux_window_name=?, mux_container_name=?, updated_at=? where ticket_id=? and is_active=1`, name, name, time.Now().UTC(), ticketID)
	return err
}

func (s *Store) UpdateSessionRef(ctx context.Context, sessionID int64, ref string) error {
	res, err := s.db.ExecContext(ctx, `update sessions set harness_session_ref=?, updated_at=? where id=?`, nullableString(ref), time.Now().UTC(), sessionID)
	return requireAffected(res, err)
}

func (s *Store) MarkSessionMissing(ctx context.Context, sessionID int64) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update sessions set
status=case when trim(coalesce(harness_session_ref,''))<>'' then ? else ? end,
is_active=0,
closed_at=?,
last_state_change_at=?,
last_detected_state=case when trim(coalesce(harness_session_ref,''))<>'' then ? else ? end,
last_attention_reason='terminal container missing',
last_detection_source=coalesce(nullif(multiplexer,''),'tmux'),
updated_at=?
where id=?`, kanban.StateExited, kanban.StateRepairNeeded, now, now, kanban.StateExited, kanban.StateRepairNeeded, now, sessionID)
	return requireAffected(res, err)
}

// RecordSessionObservationFailure preserves lifecycle state when the runtime
// watcher cannot read a live container. An observation failure is not evidence
// that the harness session itself failed.
func (s *Store) RecordSessionObservationFailure(ctx context.Context, sessionID int64, source, reason string) error {
	res, err := s.db.ExecContext(ctx, `update sessions set last_attention_reason=?,last_detection_source=?,updated_at=? where id=? and is_active=1`, nullableString(reason), nullableString(source), time.Now().UTC(), sessionID)
	return requireAffected(res, err)
}

func (s *Store) MarkSessionClosed(ctx context.Context, sessionID int64, status, source, reason string) error {
	if status == "" {
		status = kanban.StateClosed
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `update sessions set status=?, is_active=0, closed_at=?, last_state_change_at=?, last_detected_state=?, last_attention_reason=?, last_detection_source=?, updated_at=? where id=?`,
		status, now, now, status, nullableString(reason), nullableString(source), now, sessionID)
	return err
}

func (s *Store) MarkTicketRuntime(ctx context.Context, ticketID int64, status, source, reason string) error {
	ses, ok, err := s.ActiveSession(ctx, ticketID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("ticket has no active session")
	}
	return s.UpdateSessionRuntime(ctx, ses.ID, status, source, reason, "", false)
}

func (s *Store) UpdateSessionRuntime(ctx context.Context, sessionID int64, status, source, reason, excerpt string, outputChanged bool) error {
	if status == "" {
		return errors.New("runtime status is required")
	}
	now := time.Now().UTC()
	var currentStatus string
	err := s.db.QueryRowContext(ctx, `select status from sessions where id=?`, sessionID).Scan(&currentStatus)
	if err != nil {
		return err
	}
	stateChanged := currentStatus != status
	lastOutput := any(nil)
	if outputChanged {
		lastOutput = now
	}
	lastStateChange := any(nil)
	if stateChanged {
		lastStateChange = now
	}
	_, err = s.db.ExecContext(ctx, `update sessions set
status=?,
last_seen_tmux_at=?,
last_output_at=coalesce(?, last_output_at),
last_state_change_at=coalesce(?, last_state_change_at),
last_detected_state=?,
last_attention_reason=?,
last_detection_source=?,
last_observed_excerpt=coalesce(?, last_observed_excerpt),
updated_at=?
where id=?`,
		status, now, lastOutput, lastStateChange, status, nullableString(reason), nullableString(source), nullableString(excerpt), now, sessionID)
	return err
}
