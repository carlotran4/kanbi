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
	err := s.db.QueryRowContext(ctx, `select id, ticket_id, external_id, external_updated_at, sync_version, body, created_at, updated_at from ticket_notes where id=?`, noteID).Scan(&n.ID, &n.TicketID, &n.ExternalID, &n.ExternalUpdatedAt, &n.SyncVersion, &n.Body, &n.CreatedAt, &n.UpdatedAt)
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
	res, err := s.db.ExecContext(ctx, `delete from ticket_notes where id=?`, noteID)
	return requireAffected(res, err)
}

func (s *Store) ListNotes(ctx context.Context, ticketID int64) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx, `select id, ticket_id, external_id, external_updated_at, sync_version, body, created_at, updated_at from ticket_notes where ticket_id=? order by id asc`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.TicketID, &n.ExternalID, &n.ExternalUpdatedAt, &n.SyncVersion, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
