package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SyncBoardColumns mirrors an external backend's workflow columns for a board
// and returns the local column ID by provider name. Existing columns are reused
// by exact display name or workflow key; missing columns are created; absent
// columns are left in place to avoid destructive ticket/session history changes.
func (s *Store) SyncBoardColumns(ctx context.Context, boardID int64, names []string) (map[string]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	result := map[string]int64{}
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		var displayID, workflowKeyID int64
		displayErr := tx.QueryRowContext(ctx, `select id from columns where board_id=? and name=? order by position limit 1`, boardID, name).Scan(&displayID)
		if displayErr != nil && !errors.Is(displayErr, sql.ErrNoRows) {
			return nil, displayErr
		}
		workflowKeyErr := tx.QueryRowContext(ctx, `select id from columns where board_id=? and workflow_key=? order by position limit 1`, boardID, name).Scan(&workflowKeyID)
		if workflowKeyErr != nil && !errors.Is(workflowKeyErr, sql.ErrNoRows) {
			return nil, workflowKeyErr
		}

		var id int64
		switch {
		case errors.Is(displayErr, sql.ErrNoRows) && errors.Is(workflowKeyErr, sql.ErrNoRows):
			now := time.Now().UTC()
			insertPos, err := columnOrder.nextPosition(ctx, tx, boardID)
			if err != nil {
				return nil, err
			}
			// Default workflow_key to the remote display name; never overwrite user keys later.
			res, err := tx.ExecContext(ctx, `insert into columns(board_id,name,workflow_key,position,created_at,updated_at) values(?,?,?,?,?,?)`, boardID, name, name, insertPos, now, now)
			if err != nil {
				return nil, err
			}
			id, _ = res.LastInsertId()
		case errors.Is(displayErr, sql.ErrNoRows):
			id = workflowKeyID
		case errors.Is(workflowKeyErr, sql.ErrNoRows):
			id = displayID
		case displayID == workflowKeyID:
			id = displayID
		default:
			return nil, fmt.Errorf("provider column %q matches display column %d and workflow key column %d; resolve the column mapping conflict", name, displayID, workflowKeyID)
		}
		result[name] = id
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) SyncTicketsForBoard(ctx context.Context, boardID int64) ([]Ticket, error) {
	return s.queryProjectedTickets(ctx, `where t.board_id=? order by t.position`, boardID)
}

// UpsertRemoteTicketIfUnchanged applies remote metadata only when the ticket
// still has the updated_at value observed by the sync snapshot. If a local edit
// won the race, the method preserves that edit and rebases its timestamp beyond
// the provider response so the next sync pushes it instead of pulling stale
// provider data. SourceTicketID additionally supports race-safe remote-create
// linking without creating a duplicate local ticket.
func (s *Store) UpsertRemoteTicketIfUnchanged(ctx context.Context, rt RemoteTicket, expectedUpdatedAt time.Time) (Ticket, bool, error) {
	if rt.BoardID == 0 || rt.ColumnID == 0 || strings.TrimSpace(rt.ExternalID) == "" {
		return Ticket{}, false, errors.New("remote ticket requires board, column, and external id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, false, err
	}
	defer tx.Rollback()

	var id int64
	var currentUpdatedAt time.Time
	var currentDisplayNumber int
	sourceLink := rt.SourceTicketID != 0
	if sourceLink {
		err = tx.QueryRowContext(ctx, `select id, updated_at, display_number from tickets where id=? and board_id=? and external_id is null`, rt.SourceTicketID, rt.BoardID).Scan(&id, &currentUpdatedAt, &currentDisplayNumber)
	} else {
		err = tx.QueryRowContext(ctx, `select id, updated_at, display_number from tickets where board_id=? and external_id=?`, rt.BoardID, rt.ExternalID).Scan(&id, &currentUpdatedAt, &currentDisplayNumber)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Ticket{}, false, nil
		}
		return Ticket{}, false, err
	}

	displayID := strings.TrimSpace(rt.DisplayID)
	if displayID == "" {
		displayID = rt.ExternalID
	}
	if rt.DisplayNumber == 0 {
		rt.DisplayNumber = currentDisplayNumber
	}
	displayNumber, err := remoteDisplayNumber(ctx, tx, rt.BoardID, rt.DisplayNumber, id)
	if err != nil {
		return Ticket{}, false, err
	}

	applied := false
	if currentUpdatedAt.Equal(expectedUpdatedAt) {
		if rt.ArchivedAt != nil {
			var active int
			if err := tx.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=? and is_active=1`, id).Scan(&active); err != nil {
				return Ticket{}, false, err
			}
			if active > 0 {
				return Ticket{}, false, ErrTicketHasActiveSession
			}
		}
		var archived any
		if rt.ArchivedAt != nil {
			archived = *rt.ArchivedAt
		}
		query := `update tickets set column_id=?, external_url=?, external_updated_at=?, sync_version=?, display_id=?, display_number=?, title=?, body=?, archived_at=?, updated_at=?`
		args := []any{rt.ColumnID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, archived, rt.ExternalUpdatedAt}
		if sourceLink {
			query += `, external_id=? where id=? and updated_at=? and external_id is null`
			args = append(args, rt.ExternalID, id, expectedUpdatedAt)
		} else {
			query += ` where id=? and updated_at=?`
			args = append(args, id, expectedUpdatedAt)
		}
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return Ticket{}, false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return Ticket{}, false, err
		}
		applied = affected == 1
	}

	if !applied {
		rebasedAt := time.Now().UTC()
		if !rt.ExternalUpdatedAt.IsZero() && !rebasedAt.After(rt.ExternalUpdatedAt) {
			rebasedAt = rt.ExternalUpdatedAt.Add(time.Nanosecond)
		}
		if sourceLink {
			res, err := tx.ExecContext(ctx, `update tickets set external_id=?, external_url=?, external_updated_at=?, sync_version=?, display_id=?, display_number=?, updated_at=? where id=? and external_id is null`,
				rt.ExternalID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rebasedAt, id)
			if err != nil {
				return Ticket{}, false, err
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return Ticket{}, false, err
			}
			if affected == 0 {
				return Ticket{}, false, nil
			}
		} else {
			if _, err := tx.ExecContext(ctx, `update tickets set updated_at=case when updated_at<? then ? else updated_at end where id=?`, rebasedAt, rebasedAt, id); err != nil {
				return Ticket{}, false, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `update boards set next_ticket_number=max(next_ticket_number, ?) where id=?`, displayNumber+1, rt.BoardID); err != nil {
		return Ticket{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, false, err
	}
	ticket, err := s.TicketByID(ctx, id)
	return ticket, applied, err
}

func (s *Store) UpsertRemoteTicket(ctx context.Context, rt RemoteTicket) (Ticket, error) {
	if rt.BoardID == 0 || rt.ColumnID == 0 || strings.TrimSpace(rt.ExternalID) == "" {
		return Ticket{}, errors.New("remote ticket requires board, column, and external id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()
	displayID := strings.TrimSpace(rt.DisplayID)
	if displayID == "" {
		displayID = rt.ExternalID
	}
	if rt.DisplayNumber == 0 {
		if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(displayID), "GH-")); err == nil {
			rt.DisplayNumber = n
		}
	}
	now := time.Now().UTC()
	var archived any
	if rt.ArchivedAt != nil {
		archived = *rt.ArchivedAt
	}
	var id int64
	existingErr := tx.QueryRowContext(ctx, `select id from tickets where board_id=? and external_id=?`, rt.BoardID, rt.ExternalID).Scan(&id)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return Ticket{}, existingErr
	}
	displayNumberCurrentID := id
	if errors.Is(existingErr, sql.ErrNoRows) && rt.SourceTicketID != 0 {
		displayNumberCurrentID = rt.SourceTicketID
	}
	displayNumber, err := remoteDisplayNumber(ctx, tx, rt.BoardID, rt.DisplayNumber, displayNumberCurrentID)
	if err != nil {
		return Ticket{}, err
	}
	if rt.ArchivedAt != nil {
		ticketID := id
		if errors.Is(existingErr, sql.ErrNoRows) {
			ticketID = rt.SourceTicketID
		}
		if ticketID != 0 {
			var active int
			if err := tx.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=? and is_active=1`, ticketID).Scan(&active); err != nil {
				return Ticket{}, err
			}
			if active > 0 {
				return Ticket{}, ErrTicketHasActiveSession
			}
		}
	}
	if errors.Is(existingErr, sql.ErrNoRows) {
		if rt.SourceTicketID != 0 {
			var sourceBoardID int64
			if err := tx.QueryRowContext(ctx, `select board_id from tickets where id=?`, rt.SourceTicketID).Scan(&sourceBoardID); err != nil {
				return Ticket{}, err
			}
			if sourceBoardID != rt.BoardID {
				return Ticket{}, errors.New("remote ticket source belongs to a different board")
			}
			_, err = tx.ExecContext(ctx, `update tickets set column_id=?, external_id=?, external_url=?, external_updated_at=?, sync_version=?, display_id=?, display_number=?, title=?, body=?, archived_at=?, updated_at=? where id=?`,
				rt.ColumnID, rt.ExternalID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, archived, rt.ExternalUpdatedAt, rt.SourceTicketID)
			if err != nil {
				return Ticket{}, err
			}
			id = rt.SourceTicketID
		} else {
			pos, err := visibleTicketOrder.nextPosition(ctx, tx, rt.ColumnID)
			if err != nil {
				return Ticket{}, err
			}
			res, err := tx.ExecContext(ctx, `insert into tickets(board_id,column_id,external_id,external_url,external_updated_at,sync_version,display_id,display_number,title,body,harness,position,archived_at,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				rt.BoardID, rt.ColumnID, rt.ExternalID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, "pi", pos, archived, now, rt.ExternalUpdatedAt)
			if err != nil {
				return Ticket{}, err
			}
			id, _ = res.LastInsertId()
		}
	} else {
		_, err = tx.ExecContext(ctx, `update tickets set column_id=?, external_url=?, external_updated_at=?, sync_version=?, display_id=?, display_number=?, title=?, body=?, archived_at=?, updated_at=? where id=?`,
			rt.ColumnID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, archived, rt.ExternalUpdatedAt, id)
		if err != nil {
			return Ticket{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `update boards set next_ticket_number=max(next_ticket_number, ?) where id=?`, displayNumber+1, rt.BoardID); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}
	return s.TicketByID(ctx, id)
}

type sqlQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func remoteDisplayNumber(ctx context.Context, q sqlQueryer, boardID int64, desired int, currentTicketID int64) (int, error) {
	if desired <= 0 {
		if err := q.QueryRowContext(ctx, `select next_ticket_number from boards where id=?`, boardID).Scan(&desired); err != nil {
			return 0, err
		}
	}
	var existing int64
	err := q.QueryRowContext(ctx, `select id from tickets where board_id=? and display_number=?`, boardID, desired).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) || existing == currentTicketID {
		return desired, nil
	}
	if err != nil {
		return 0, err
	}
	var next int
	if err := q.QueryRowContext(ctx, `select coalesce(max(display_number),0)+1 from tickets where board_id=?`, boardID).Scan(&next); err != nil {
		return 0, err
	}
	if next <= desired {
		next = desired + 1
	}
	return next, nil
}

func (s *Store) UpsertRemoteNote(ctx context.Context, ticketID int64, externalID, body string, externalUpdatedAt time.Time) error {
	if strings.TrimSpace(externalID) == "" {
		return errors.New("remote note requires external id")
	}
	var id int64
	var deletedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `select id, deleted_at from ticket_notes where ticket_id=? and external_id=?`, ticketID, externalID).Scan(&id, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `insert into ticket_notes(ticket_id,external_id,external_updated_at,sync_version,body,created_at,updated_at) values(?,?,?,?,?,?,?)`, ticketID, externalID, externalUpdatedAt, externalUpdatedAt.Format(time.RFC3339Nano), body, time.Now().UTC(), externalUpdatedAt)
		return err
	}
	if err != nil {
		return err
	}
	if deletedAt.Valid {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `update ticket_notes set external_updated_at=?, sync_version=?, body=?, updated_at=? where id=?`, externalUpdatedAt, externalUpdatedAt.Format(time.RFC3339Nano), body, externalUpdatedAt, id)
	return err
}

func (s *Store) LinkLocalNoteToRemote(ctx context.Context, noteID int64, externalID string, externalUpdatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `update ticket_notes set external_id=?, external_updated_at=?, sync_version=?, updated_at=? where id=?`, externalID, externalUpdatedAt, externalUpdatedAt.Format(time.RFC3339Nano), externalUpdatedAt, noteID)
	return err
}
