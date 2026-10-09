package storage

import (
	"context"
	"database/sql"
	"strings"

	"github.com/carlotran4/kanbi/internal/kanban"
)

const ticketProjectionRuntimeSQL = `case
when s.id is null then '` + kanban.StateNotStarted + `'
when s.is_active=0
 and s.status='` + kanban.StateError + `'
 and s.last_detection_source='tmux'
 and s.last_attention_reason='tmux window missing'
then case when trim(coalesce(s.harness_session_ref,''))<>''
          then '` + kanban.StateExited + `'
          else '` + kanban.StateRepairNeeded + `' end
else coalesce(s.status,'` + kanban.StateNotStarted + `') end`

var ticketProjectionColumns = []string{
	"t.id",
	"t.board_id",
	"pb.name",
	"coalesce(pb.uuid,'')",
	"coalesce(pb.workdir,'')",
	"coalesce(pb.worktree_mode,'off')",
	"t.column_id",
	"t.external_id",
	"t.external_url",
	"t.external_updated_at",
	"t.sync_version",
	"t.display_id",
	"t.display_number",
	"t.title",
	"t.body",
	"t.harness",
	"t.position",
	"t.archived_at",
	"coalesce(t.focus_paused,0)",
	"pc.id",
	"pc.why",
	"pc.completed",
	"pc.next_action",
	"pc.paused_at",
	"pc.resumed_at",
	"t.remote_push_state",
	"t.remote_push_token",
	"t.remote_push_attempted_at",
	ticketProjectionRuntimeSQL + " runtime",
	"coalesce(s.is_active,0)",
	"s.tmux_session_name",
	"s.tmux_window_id",
	"s.tmux_window_name",
	"coalesce(s.multiplexer,'tmux')",
	"coalesce(s.mux_namespace,s.tmux_session_name)",
	"coalesce(s.mux_container_id,s.tmux_window_id)",
	"coalesce(s.mux_container_name,s.tmux_window_name)",
	"s.mux_metadata",
	"s.id",
	"s.harness_session_ref",
	"s.workspace_id",
	"s.launch_cwd",
	"s.last_output_at",
	"s.last_state_change_at",
	"s.last_detected_state",
	"s.last_attention_reason",
	"s.last_detection_source",
	"s.last_observed_excerpt",
	"w.id",
	"w.state",
	"w.branch_name",
	"w.source_branch",
	"w.launch_cwd",
	"w.last_status_json",
	"w.last_error",
	"t.created_at",
	"t.updated_at",
	"(select count(*) from ticket_notes where ticket_id=t.id and deleted_at is null)",
}

const latestSessionProjectionJoin = `
from tickets t
left join boards pb on pb.id=t.board_id
left join sessions s on s.id=(
  select id from sessions where ticket_id=t.id order by id desc limit 1
)
left join ticket_workspaces w on w.id=(
  select id from ticket_workspaces where ticket_id=t.id and is_current=1 order by id desc limit 1
)
left join pause_checkpoints pc on pc.id=(
  select id from pause_checkpoints where ticket_id=t.id order by paused_at desc, id desc limit 1
) `

var ticketProjectionPrefix = "select " + strings.Join(ticketProjectionColumns, ",") + latestSessionProjectionJoin

func ticketProjectionSQL(suffix string) string {
	return ticketProjectionPrefix + suffix
}

func (s *Store) queryProjectedTickets(ctx context.Context, suffix string, args ...any) ([]Ticket, error) {
	return queryProjectedTicketsFrom(ctx, s.reader(), suffix, args...)
}

// projectionReader lets a projection use one physical connection throughout.
type projectionReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryProjectedTicketsFrom(ctx context.Context, reader projectionReader, suffix string, args ...any) ([]Ticket, error) {
	rows, err := reader.QueryContext(ctx, ticketProjectionSQL(suffix), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tickets []Ticket
	for rows.Next() {
		t, err := scanProjectedTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, t)
	}
	return tickets, rows.Err()
}

func scanProjectedTicket(rows *sql.Rows) (Ticket, error) {
	var t Ticket
	var active, paused int
	var checkpointID sql.NullInt64
	var checkpointWhy, checkpointCompleted, checkpointNext sql.NullString
	var checkpointPausedAt, checkpointResumedAt sql.NullTime
	if err := rows.Scan(&t.ID, &t.BoardID, &t.BoardName, &t.BoardUUID, &t.BoardWorkdir, &t.BoardWorktreeMode, &t.ColumnID, &t.ExternalID, &t.ExternalURL, &t.ExternalUpdatedAt, &t.SyncVersion, &t.DisplayID, &t.DisplayNum, &t.Title, &t.Body, &t.Harness, &t.Position, &t.ArchivedAt, &paused, &checkpointID, &checkpointWhy, &checkpointCompleted, &checkpointNext, &checkpointPausedAt, &checkpointResumedAt, &t.RemotePushState, &t.RemotePushToken, &t.RemotePushAttemptedAt, &t.Runtime, &active, &t.TmuxSessionName, &t.WindowID, &t.WindowName, &t.Multiplexer, &t.MuxNamespace, &t.MuxContainerID, &t.MuxContainerName, &t.MuxMetadata, &t.SessionID, &t.SessionRef, &t.SessionWorkspaceID, &t.SessionLaunchCWD, &t.LastOutputAt, &t.LastStateChangeAt, &t.LastDetectedState, &t.LastAttentionReason, &t.LastDetectionSource, &t.LastObservedExcerpt, &t.WorkspaceID, &t.WorkspaceState, &t.WorkspaceBranch, &t.WorkspaceSourceBranch, &t.WorkspaceLaunchCWD, &t.WorkspaceStatusJSON, &t.WorkspaceLastError, &t.CreatedAt, &t.UpdatedAt, &t.NoteCount); err != nil {
		return Ticket{}, err
	}
	t.SessionActive = active == 1
	t.FocusPaused = paused == 1
	if checkpointID.Valid {
		t.LatestCheckpoint = &PauseCheckpoint{ID: checkpointID.Int64, TicketID: t.ID, Why: checkpointWhy.String, Completed: checkpointCompleted.String, NextAction: checkpointNext.String, PausedAt: checkpointPausedAt.Time, ResumedAt: checkpointResumedAt}
	}
	return t, nil
}
