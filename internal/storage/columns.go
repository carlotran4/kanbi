package storage

import (
	"context"
	"database/sql"
	"errors"

	"strings"
	"time"
)

func (s *Store) AddColumn(ctx context.Context, boardID int64, name string) (Column, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Column{}, errors.New("column name is required")
	}
	if boardID == 0 {
		board, err := s.DefaultBoard(ctx)
		if err != nil {
			return Column{}, err
		}
		boardID = board.ID
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from columns where board_id=? and name=?`, boardID, name).Scan(&existing); err != nil {
		return Column{}, err
	}
	if existing > 0 {
		return Column{}, errors.New("column name already exists on this board")
	}
	pos, err := columnOrder.nextPosition(ctx, s.db, boardID)
	if err != nil {
		return Column{}, err
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `insert into columns(board_id,name,position,created_at,updated_at) values(?,?,?,?,?)`, boardID, name, pos, now, now)
	if err != nil {
		return Column{}, err
	}
	id, _ := res.LastInsertId()
	return Column{ID: id, BoardID: boardID, Name: name, Position: pos}, nil
}

func (s *Store) RenameColumn(ctx context.Context, columnID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("column name is required")
	}
	var boardID int64
	if err := s.db.QueryRowContext(ctx, `select board_id from columns where id=?`, columnID).Scan(&boardID); err != nil {
		return err
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from columns where board_id=? and name=? and id<>?`, boardID, name, columnID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return errors.New("column name already exists on this board")
	}
	res, err := s.db.ExecContext(ctx, `update columns set name=?, updated_at=? where id=?`, name, time.Now().UTC(), columnID)
	return requireAffected(res, err)
}

func (s *Store) DeleteColumn(ctx context.Context, columnID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var boardID int64
	var pos int
	if err := tx.QueryRowContext(ctx, `select board_id, position from columns where id=?`, columnID).Scan(&boardID, &pos); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `select count(*) from tickets where column_id=? and archived_at is null`, columnID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("Cannot delete non-empty column. Move tickets first.")
	}
	var fallbackColumnID int64
	if err := tx.QueryRowContext(ctx, `select id from columns where board_id=? and id<>? order by abs(position-?), position limit 1`, boardID, columnID, pos).Scan(&fallbackColumnID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("Cannot delete the last column.")
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `update tickets set column_id=?, updated_at=? where column_id=?`, fallbackColumnID, time.Now().UTC(), columnID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from columns where id=?`, columnID); err != nil {
		return err
	}
	if err := columnOrder.compact(ctx, tx, boardID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReorderColumn(ctx context.Context, columnID int64, delta int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := columnOrder.swapByDelta(ctx, tx, columnID, delta); err != nil {
		return err
	}
	return tx.Commit()
}
