package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"strings"
	"time"
)

func (s *Store) TicketsForColumn(ctx context.Context, columnID int64) ([]Ticket, error) {
	return s.queryProjectedTickets(ctx, `where t.column_id=? and t.archived_at is null order by t.position`, columnID)
}

func (s *Store) ListTickets(ctx context.Context, includeArchived bool) ([]Ticket, error) {
	where := `where t.archived_at is null`
	if includeArchived {
		where = ``
	}
	return s.queryProjectedTickets(ctx, where+` order by t.display_number`)
}

func (s *Store) TicketByDisplayID(ctx context.Context, displayID string) (Ticket, error) {
	tickets, err := s.queryProjectedTickets(ctx, `where t.display_id=?`, strings.ToUpper(displayID))
	if err != nil {
		return Ticket{}, err
	}
	if len(tickets) == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	if len(tickets) > 1 {
		return Ticket{}, errors.New("display id is ambiguous; specify a board")
	}
	return tickets[0], nil
}

func (s *Store) TicketByDisplayIDInBoard(ctx context.Context, displayID string, boardID int64) (Ticket, error) {
	tickets, err := s.queryProjectedTickets(ctx, `where t.display_id=? and t.board_id=?`, strings.ToUpper(displayID), boardID)
	if err != nil {
		return Ticket{}, err
	}
	if len(tickets) == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	return tickets[0], nil
}

func (s *Store) TicketByID(ctx context.Context, id int64) (Ticket, error) {
	tickets, err := s.queryProjectedTickets(ctx, `where t.id=?`, id)
	if err != nil {
		return Ticket{}, err
	}
	if len(tickets) == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	return tickets[0], nil
}

func (s *Store) CreateTicket(ctx context.Context, columnID int64, title, body, harnessName string) (Ticket, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Ticket{}, errors.New("ticket title is required")
	}
	harnessName = strings.ToLower(strings.TrimSpace(harnessName))
	if harnessName == "" {
		harnessName = "pi"
	}
	if !validHarness(harnessName) {
		return Ticket{}, fmt.Errorf("unsupported harness %q", harnessName)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()

	var boardID int64
	if columnID == 0 {
		if err := tx.QueryRowContext(ctx, `select id from columns order by board_id, position limit 1`).Scan(&columnID); err != nil {
			return Ticket{}, err
		}
	}
	if err := tx.QueryRowContext(ctx, `select board_id from columns where id=?`, columnID).Scan(&boardID); err != nil {
		return Ticket{}, err
	}
	var next int
	if err := tx.QueryRowContext(ctx, `select next_ticket_number from boards where id=?`, boardID).Scan(&next); err != nil {
		return Ticket{}, err
	}
	pos, err := visibleTicketOrder.nextPosition(ctx, tx, columnID)
	if err != nil {
		return Ticket{}, err
	}
	displayID := fmt.Sprintf("T-%03d", next)
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `insert into tickets(board_id,column_id,display_id,display_number,title,body,harness,position,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?)`,
		boardID, columnID, displayID, next, title, body, harnessName, pos, now, now)
	if err != nil {
		return Ticket{}, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `update boards set next_ticket_number=next_ticket_number+1 where id=?`, boardID); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}
	return s.TicketByID(ctx, id)
}

func (s *Store) UpdateTicket(ctx context.Context, id int64, title, body, harnessName string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("ticket title is required")
	}
	harnessName = strings.ToLower(strings.TrimSpace(harnessName))
	if !validHarness(harnessName) {
		return fmt.Errorf("unsupported harness %q", harnessName)
	}
	res, err := s.db.ExecContext(ctx, `update tickets set title=?, body=?, harness=?, updated_at=? where id=?`, title, body, harnessName, time.Now().UTC(), id)
	return requireAffected(res, err)
}

func (s *Store) ArchiveTicket(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var columnID int64
	var activeSessions int
	if err := tx.QueryRowContext(ctx, `select t.column_id, count(s.id) from tickets t left join sessions s on s.ticket_id=t.id and s.is_active=1 where t.id=? group by t.id`, id).Scan(&columnID, &activeSessions); err != nil {
		return err
	}
	if activeSessions > 0 {
		return ErrTicketHasActiveSession
	}
	if _, err := tx.ExecContext(ctx, `update tickets set archived_at=?, updated_at=? where id=?`, time.Now().UTC(), time.Now().UTC(), id); err != nil {
		return err
	}
	if err := visibleTicketOrder.compact(ctx, tx, columnID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ColumnIDByBoardAndName(ctx context.Context, boardID int64, name string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `select id from columns where board_id=? and name=? order by position limit 1`, boardID, name).Scan(&id)
	return id, err
}

func (s *Store) ColumnIDByBoardAndWorkflowKey(ctx context.Context, boardID int64, key string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `select id from columns where board_id=? and workflow_key=? order by position limit 1`, boardID, strings.TrimSpace(key)).Scan(&id)
	return id, err
}

func (s *Store) MoveTicket(ctx context.Context, ticketID, toColumnID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ticketBoardID, columnBoardID int64
	if err := tx.QueryRowContext(ctx, `select board_id from tickets where id=?`, ticketID).Scan(&ticketBoardID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `select board_id from columns where id=?`, toColumnID).Scan(&columnBoardID); err != nil {
		return err
	}
	if ticketBoardID != columnBoardID {
		return errors.New("cannot move a ticket to a column on another board")
	}
	if err := visibleTicketOrder.moveToFront(ctx, tx, ticketID, toColumnID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReorderTicket(ctx context.Context, ticketID int64, delta int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := visibleTicketOrder.swapByDelta(ctx, tx, ticketID, delta); err != nil {
		return err
	}
	return tx.Commit()
}
