package storage

import (
	"context"

	"errors"

	"strings"
	"time"
)

func (s *Store) AddNote(ctx context.Context, ticketID int64, body string) (Note, error) {
	if strings.TrimSpace(body) == "" {
		return Note{}, errors.New("note body is required")
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `insert into ticket_notes(ticket_id,body,created_at,updated_at) values(?,?,?,?)`, ticketID, body, now, now)
	if err != nil {
		return Note{}, err
	}
	id, _ := res.LastInsertId()
	return Note{ID: id, TicketID: ticketID, Body: body, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) NoteByID(ctx context.Context, noteID int64) (Note, error) {
	var n Note
	err := s.reader().QueryRowContext(ctx, `select id, ticket_id, external_id, external_updated_at, sync_version, body, deleted_at, created_at, updated_at from ticket_notes where id=?`, noteID).Scan(&n.ID, &n.TicketID, &n.ExternalID, &n.ExternalUpdatedAt, &n.SyncVersion, &n.Body, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

func (s *Store) UpdateNote(ctx context.Context, noteID int64, body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("note body is required")
	}
	res, err := s.db.ExecContext(ctx, `update ticket_notes set body=?, updated_at=? where id=?`, body, time.Now().UTC(), noteID)
	return requireAffected(res, err)
}

func (s *Store) DeleteNote(ctx context.Context, noteID int64) error {
	now := time.Now().UTC()
	// Provider-backed notes remain as tombstones so a still-present remote
	// comment cannot be imported again. Purely local notes can be removed.
	res, err := s.db.ExecContext(ctx, `update ticket_notes set deleted_at=?, updated_at=? where id=? and external_id is not null`, now, now, noteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	res, err = s.db.ExecContext(ctx, `delete from ticket_notes where id=?`, noteID)
	return requireAffected(res, err)
}

// RestoreNote clears a local note tombstone. It does not resurrect remote comments.
func (s *Store) RestoreNote(ctx context.Context, noteID int64) error {
	res, err := s.db.ExecContext(ctx, `update ticket_notes set deleted_at=null, updated_at=? where id=? and deleted_at is not null`, time.Now().UTC(), noteID)
	return requireAffected(res, err)
}

func (s *Store) ListNotes(ctx context.Context, ticketID int64) ([]Note, error) {
	return s.listNotes(ctx, ticketID, false)
}

func (s *Store) ListNotesForSync(ctx context.Context, ticketID int64) ([]Note, error) {
	return s.listNotes(ctx, ticketID, true)
}

func (s *Store) listNotes(ctx context.Context, ticketID int64, includeDeleted bool) ([]Note, error) {
	query := `select id, ticket_id, external_id, external_updated_at, sync_version, body, deleted_at, created_at, updated_at from ticket_notes where ticket_id=?`
	if !includeDeleted {
		query += ` and deleted_at is null`
	}
	query += ` order by id asc`
	rows, err := s.reader().QueryContext(ctx, query, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.TicketID, &n.ExternalID, &n.ExternalUpdatedAt, &n.SyncVersion, &n.Body, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
