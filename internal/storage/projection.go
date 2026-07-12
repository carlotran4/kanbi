package storage

import (
	"context"
	"database/sql"

	"github.com/carlotran4/kanbi/internal/kanban"
)

const ticketProjectionRuntimeSQL = "coalesce(s.status,'" + kanban.StateNotStarted + "')"

var ticketProjectionColumns = []string{
	"t.id",
	"t.board_id",
	"(select name from boards where id=t.board_id)",
	"(select coalesce(workdir,'') from boards where id=t.board_id)",
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
	"s.last_output_at",
	"s.last_state_change_at",
	"s.last_detected_state",
	"s.last_attention_reason",
	"s.last_detection_source",
	"s.last_observed_excerpt",
	"t.created_at",
	"t.updated_at",
	"(select count(*) from ticket_notes where ticket_id=t.id and deleted_at is null)",
}

const latestSessionProjectionJoin = `
from tickets t
left join sessions s on s.id=(
  select id from sessions where ticket_id=t.id order by id desc limit 1
) `

func ticketProjectionSQL(suffix string) string {
	query := "select "
	for i, column := range ticketProjectionColumns {
		if i > 0 {
			query += ","
		}
		query += column
	}
	return query + latestSessionProjectionJoin + suffix
}

func (s *Store) queryProjectedTickets(ctx context.Context, suffix string, args ...any) ([]Ticket, error) {
	rows, err := s.db.QueryContext(ctx, ticketProjectionSQL(suffix), args...)
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
	var active int
	if err := rows.Scan(&t.ID, &t.BoardID, &t.BoardName, &t.BoardWorkdir, &t.ColumnID, &t.ExternalID, &t.ExternalURL, &t.ExternalUpdatedAt, &t.SyncVersion, &t.DisplayID, &t.DisplayNum, &t.Title, &t.Body, &t.Harness, &t.Position, &t.ArchivedAt, &t.Runtime, &active, &t.TmuxSessionName, &t.WindowID, &t.WindowName, &t.Multiplexer, &t.MuxNamespace, &t.MuxContainerID, &t.MuxContainerName, &t.MuxMetadata, &t.SessionID, &t.SessionRef, &t.LastOutputAt, &t.LastStateChangeAt, &t.LastDetectedState, &t.LastAttentionReason, &t.LastDetectionSource, &t.LastObservedExcerpt, &t.CreatedAt, &t.UpdatedAt, &t.NoteCount); err != nil {
		return Ticket{}, err
	}
	t.SessionActive = active == 1
	return t, nil
}
