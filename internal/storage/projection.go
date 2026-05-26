package storage

import (
	"context"
	"database/sql"

	"agent-kanban/internal/kanban"
)

const ticketProjectionRuntimeSQL = "coalesce(s.status,'" + kanban.StateNotStarted + "')"

var ticketProjectionColumns = []string{
	"t.id",
	"t.board_id",
	"(select name from boards where id=t.board_id)",
	"(select coalesce(workdir,'') from boards where id=t.board_id)",
	"t.column_id",
	"t.display_id",
	"t.display_number",
	"t.title",
	"t.body",
	"t.harness",
	"t.position",
	"t.archived_at",
	ticketProjectionRuntimeSQL + " runtime",
	"coalesce(s.is_active,0)",
	"s.tmux_window_id",
	"s.tmux_window_name",
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
	if err := rows.Scan(&t.ID, &t.BoardID, &t.BoardName, &t.BoardWorkdir, &t.ColumnID, &t.DisplayID, &t.DisplayNum, &t.Title, &t.Body, &t.Harness, &t.Position, &t.ArchivedAt, &t.Runtime, &active, &t.WindowID, &t.WindowName, &t.SessionID, &t.SessionRef, &t.LastOutputAt, &t.LastStateChangeAt, &t.LastDetectedState, &t.LastAttentionReason, &t.LastDetectionSource, &t.LastObservedExcerpt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return Ticket{}, err
	}
	t.SessionActive = active == 1
	return t, nil
}
