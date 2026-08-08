package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func normalizeTemplateWrite(name, title, body, harnessName string) (TicketTemplate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return TicketTemplate{}, errors.New("template name is required")
	}
	title = strings.TrimSpace(title)
	validated, err := normalizeTicketWrite("template validation", body, harnessName, true)
	if err != nil {
		return TicketTemplate{}, err
	}
	return TicketTemplate{Name: name, Title: title, Body: validated.Body, Harness: validated.Harness}, nil
}

func (s *Store) ListTicketTemplates(ctx context.Context, boardID int64) ([]TicketTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `select id,board_id,name,title,body,harness,created_at,updated_at from ticket_templates where board_id=? order by name collate nocase,id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketTemplate
	for rows.Next() {
		var t TicketTemplate
		if err := rows.Scan(&t.ID, &t.BoardID, &t.Name, &t.Title, &t.Body, &t.Harness, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TicketTemplateByID(ctx context.Context, id int64) (TicketTemplate, error) {
	var t TicketTemplate
	err := s.db.QueryRowContext(ctx, `select id,board_id,name,title,body,harness,created_at,updated_at from ticket_templates where id=?`, id).
		Scan(&t.ID, &t.BoardID, &t.Name, &t.Title, &t.Body, &t.Harness, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *Store) TicketTemplateByName(ctx context.Context, boardID int64, name string) (TicketTemplate, error) {
	var t TicketTemplate
	err := s.db.QueryRowContext(ctx, `select id,board_id,name,title,body,harness,created_at,updated_at from ticket_templates where board_id=? and name=? collate nocase`, boardID, strings.TrimSpace(name)).
		Scan(&t.ID, &t.BoardID, &t.Name, &t.Title, &t.Body, &t.Harness, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *Store) CreateTicketTemplate(ctx context.Context, boardID int64, name, title, body, harnessName string) (TicketTemplate, error) {
	write, err := normalizeTemplateWrite(name, title, body, harnessName)
	if err != nil {
		return TicketTemplate{}, err
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `insert into ticket_templates(board_id,name,title,body,harness,created_at,updated_at) values(?,?,?,?,?,?,?)`, boardID, write.Name, write.Title, write.Body, write.Harness, now, now)
	if err != nil {
		return TicketTemplate{}, err
	}
	id, _ := res.LastInsertId()
	return s.TicketTemplateByID(ctx, id)
}

func (s *Store) UpdateTicketTemplate(ctx context.Context, id int64, name, title, body, harnessName string) error {
	write, err := normalizeTemplateWrite(name, title, body, harnessName)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `update ticket_templates set name=?,title=?,body=?,harness=?,updated_at=? where id=?`, write.Name, write.Title, write.Body, write.Harness, time.Now().UTC(), id)
	return requireAffected(res, err)
}

func (s *Store) DeleteTicketTemplate(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `delete from ticket_templates where id=?`, id)
	return requireAffected(res, err)
}

// CreateTicketFromTemplate atomically snapshots a board-scoped template and
// creates one ordinary ticket in a column owned by the same board.
func (s *Store) CreateTicketFromTemplate(ctx context.Context, columnID, templateID int64, overrides TemplateTicketOverrides) (Ticket, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()

	var tmpl TicketTemplate
	if err := tx.QueryRowContext(ctx, `select id,board_id,name,title,body,harness,created_at,updated_at from ticket_templates where id=?`, templateID).
		Scan(&tmpl.ID, &tmpl.BoardID, &tmpl.Name, &tmpl.Title, &tmpl.Body, &tmpl.Harness, &tmpl.CreatedAt, &tmpl.UpdatedAt); err != nil {
		return Ticket{}, err
	}
	var columnBoardID int64
	if err := tx.QueryRowContext(ctx, `select board_id from columns where id=?`, columnID).Scan(&columnBoardID); err != nil {
		return Ticket{}, err
	}
	if columnBoardID != tmpl.BoardID {
		return Ticket{}, errors.New("cannot apply a template from another board")
	}
	title := strings.TrimSpace(overrides.Title)
	if title == "" {
		title = tmpl.Title
	}
	if strings.TrimSpace(title) == "" {
		title = tmpl.Name
	}
	body := tmpl.Body
	if overrides.Body != nil {
		body = *overrides.Body
	}
	harnessName := tmpl.Harness
	if strings.TrimSpace(overrides.Harness) != "" {
		harnessName = overrides.Harness
	}
	write, err := normalizeTicketWrite(title, body, harnessName, true)
	if err != nil {
		return Ticket{}, err
	}
	id, _, err := s.createTicketTx(ctx, tx, columnID, write)
	if err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, fmt.Errorf("create ticket from template: %w", err)
	}
	return s.TicketByID(ctx, id)
}
