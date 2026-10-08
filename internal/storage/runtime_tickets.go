package storage

import "context"

// ActiveRuntimeTickets is an observation read model. It intentionally excludes
// bodies, notes, checkpoints, and workspace projections from the global poll.
// ActiveSession revalidates each attempt before the manager observes it.
func (s *Store) ActiveRuntimeTickets(ctx context.Context) ([]Ticket, error) {
	rows, err := s.db.QueryContext(ctx, `select t.id, t.board_id, t.display_id, t.title,
s.id, s.tmux_window_id, s.tmux_window_name
from sessions s join tickets t on t.id=s.ticket_id join boards b on b.id=t.board_id
where s.is_active=1 and t.archived_at is null and b.archived_at is null
order by t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tickets []Ticket
	for rows.Next() {
		var ticket Ticket
		if err := rows.Scan(&ticket.ID, &ticket.BoardID, &ticket.DisplayID, &ticket.Title, &ticket.SessionID, &ticket.WindowID, &ticket.WindowName); err != nil {
			return nil, err
		}
		ticket.SessionActive = true
		tickets = append(tickets, ticket)
	}
	return tickets, rows.Err()
}
