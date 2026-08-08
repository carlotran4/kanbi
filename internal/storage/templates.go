package storage

import (
	"context"
	"database/sql"
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
	templates, err := s.ListTicketTemplates(ctx, boardID)
	if err != nil {
		return TicketTemplate{}, err
	}
	name = strings.TrimSpace(name)
	var match TicketTemplate
	for _, tmpl := range templates {
		if !strings.EqualFold(tmpl.Name, name) {
			continue
		}
		if match.ID != 0 {
			return TicketTemplate{}, errors.New("template name is ambiguous on this board")
		}
		match = tmpl
	}
	if match.ID == 0 {
		return TicketTemplate{}, sql.ErrNoRows
	}
	return match, nil
}

func templateNameConflictTx(ctx context.Context, tx *sql.Tx, boardID int64, name string, excludeID int64) error {
	rows, err := tx.QueryContext(ctx, `select id,name from ticket_templates where board_id=?`, boardID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var existing string
		if err := rows.Scan(&id, &existing); err != nil {
			return err
		}
		if id != excludeID && strings.EqualFold(existing, name) {
			return errors.New("template name already exists on this board")
		}
	}
	return rows.Err()
}

func (s *Store) CreateTicketTemplate(ctx context.Context, boardID int64, name, title, body, harnessName string) (TicketTemplate, error) {
	write, err := normalizeTemplateWrite(name, title, body, harnessName)
	if err != nil {
		return TicketTemplate{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketTemplate{}, err
	}
	defer tx.Rollback()
	if res, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=?`, boardID); err != nil {
		return TicketTemplate{}, err
	} else if err := requireAffected(res, nil); err != nil {
		return TicketTemplate{}, err
	}
	if err := templateNameConflictTx(ctx, tx, boardID, write.Name, 0); err != nil {
		return TicketTemplate{}, err
	}
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `insert into ticket_templates(board_id,name,title,body,harness,created_at,updated_at) values(?,?,?,?,?,?,?)`, boardID, write.Name, write.Title, write.Body, write.Harness, now, now)
	if err != nil {
		return TicketTemplate{}, ticketTemplateWriteError(err)
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return TicketTemplate{}, err
	}
	return s.TicketTemplateByID(ctx, id)
}

func (s *Store) UpdateTicketTemplate(ctx context.Context, id int64, name, title, body, harnessName string) error {
	write, err := normalizeTemplateWrite(name, title, body, harnessName)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var boardID int64
	if err := tx.QueryRowContext(ctx, `select board_id from ticket_templates where id=?`, id).Scan(&boardID); err != nil {
		return err
	}
	if err := templateNameConflictTx(ctx, tx, boardID, write.Name, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `update ticket_templates set name=?,title=?,body=?,harness=?,updated_at=? where id=?`, write.Name, write.Title, write.Body, write.Harness, time.Now().UTC(), id)
	if err := requireAffected(res, ticketTemplateWriteError(err)); err != nil {
		return err
	}
	return tx.Commit()
}

func ticketTemplateWriteError(err error) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		return errors.New("template name already exists on this board")
	}
	return err
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
	title := tmpl.Title
	if strings.TrimSpace(title) == "" {
		title = tmpl.Name
	}
	if overrides.Title != nil {
		title = *overrides.Title
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
