package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type orderedList struct {
	table              string
	scopeColumn        string
	visiblePredicate   string
	uniquePerScope     bool
	maintainsUpdatedAt bool
}

func (l orderedList) visibleClause() string {
	if l.visiblePredicate == "" {
		return "1=1"
	}
	return l.visiblePredicate
}

func (l orderedList) nextPosition(ctx context.Context, tx queryer, scopeID int64) (int, error) {
	query := fmt.Sprintf(`select coalesce(max(position)+1, 0) from %s where %s=? and %s`, l.table, l.scopeColumn, l.visibleClause())
	var pos int
	if err := tx.QueryRowContext(ctx, query, scopeID).Scan(&pos); err != nil {
		return 0, err
	}
	return pos, nil
}

func (l orderedList) compact(ctx context.Context, tx *sql.Tx, scopeID int64) error {
	query := fmt.Sprintf(`select id, position from %s where %s=? and %s order by position,id`, l.table, l.scopeColumn, l.visibleClause())
	rows, err := tx.QueryContext(ctx, query, scopeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id  int64
		pos int
	}
	var rowsToUpdate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.pos); err != nil {
			return err
		}
		rowsToUpdate = append(rowsToUpdate, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for pos, r := range rowsToUpdate {
		if r.pos == pos {
			continue
		}
		if err := l.setPosition(ctx, tx, r.id, pos, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (l orderedList) swapByDelta(ctx context.Context, tx *sql.Tx, id int64, delta int) error {
	if delta == 0 {
		return nil
	}
	selectItem := fmt.Sprintf(`select %s, position from %s where id=? and %s`, l.scopeColumn, l.table, l.visibleClause())
	var scopeID int64
	var pos int
	if err := tx.QueryRowContext(ctx, selectItem, id).Scan(&scopeID, &pos); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	target := pos + delta
	selectOther := fmt.Sprintf(`select id from %s where %s=? and %s and position=?`, l.table, l.scopeColumn, l.visibleClause())
	var otherID int64
	if err := tx.QueryRowContext(ctx, selectOther, scopeID, target).Scan(&otherID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	now := time.Now().UTC()
	if l.uniquePerScope {
		temp, err := l.sparePosition(ctx, tx, scopeID)
		if err != nil {
			return err
		}
		if err := l.setPosition(ctx, tx, id, temp, now); err != nil {
			return err
		}
	}
	if err := l.setPosition(ctx, tx, otherID, pos, now); err != nil {
		return err
	}
	return l.setPosition(ctx, tx, id, target, now)
}

func (l orderedList) moveToFront(ctx context.Context, tx *sql.Tx, id, toScopeID int64) error {
	selectItem := fmt.Sprintf(`select %s from %s where id=?`, l.scopeColumn, l.table)
	var fromScopeID int64
	if err := tx.QueryRowContext(ctx, selectItem, id).Scan(&fromScopeID); err != nil {
		return err
	}
	minQuery := fmt.Sprintf(`select coalesce(min(position)-1, 0) from %s where %s=? and %s`, l.table, l.scopeColumn, l.visibleClause())
	var frontPos int
	if err := tx.QueryRowContext(ctx, minQuery, toScopeID).Scan(&frontPos); err != nil {
		return err
	}
	now := time.Now().UTC()
	query := fmt.Sprintf(`update %s set %s=?, position=?%s where id=?`, l.table, l.scopeColumn, updatedAtAssignment(l.maintainsUpdatedAt))
	args := []any{toScopeID, frontPos}
	if l.maintainsUpdatedAt {
		args = append(args, now)
	}
	args = append(args, id)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if err := l.compact(ctx, tx, fromScopeID); err != nil {
		return err
	}
	if toScopeID != fromScopeID {
		return l.compact(ctx, tx, toScopeID)
	}
	return nil
}

func (l orderedList) sparePosition(ctx context.Context, tx *sql.Tx, scopeID int64) (int, error) {
	query := fmt.Sprintf(`select coalesce(min(position)-1, -1) from %s where %s=?`, l.table, l.scopeColumn)
	var pos int
	if err := tx.QueryRowContext(ctx, query, scopeID).Scan(&pos); err != nil {
		return 0, err
	}
	return pos, nil
}

func (l orderedList) setPosition(ctx context.Context, tx *sql.Tx, id int64, pos int, now time.Time) error {
	query := fmt.Sprintf(`update %s set position=?%s where id=?`, l.table, updatedAtAssignment(l.maintainsUpdatedAt))
	args := []any{pos}
	if l.maintainsUpdatedAt {
		args = append(args, now)
	}
	args = append(args, id)
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func updatedAtAssignment(enabled bool) string {
	if !enabled {
		return ""
	}
	return `, updated_at=?`
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var columnOrder = orderedList{table: "columns", scopeColumn: "board_id", uniquePerScope: true, maintainsUpdatedAt: true}
var visibleTicketOrder = orderedList{table: "tickets", scopeColumn: "column_id", visiblePredicate: "archived_at is null", maintainsUpdatedAt: true}
