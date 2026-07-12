package storage

import (
	"context"
	"errors"
	"time"
)

var ErrBoardSyncInProgress = errors.New("board sync is already in progress in another Kanbi process")

// AcquireBoardSyncLease serializes provider sync across Store instances. An
// abandoned lease becomes available after ttl so crashes do not wedge a board.
func (s *Store) AcquireBoardSyncLease(ctx context.Context, boardID int64, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from board_sync_leases where board_id=? and expires_at<=?`, boardID, now); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `insert into board_sync_leases(board_id,owner,expires_at) values(?,?,?) on conflict(board_id) do nothing`, boardID, owner, now.Add(ttl))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) RenewBoardSyncLease(ctx context.Context, boardID int64, owner string, ttl time.Duration) (bool, error) {
	res, err := s.db.ExecContext(ctx, `update board_sync_leases set expires_at=? where board_id=? and owner=?`, time.Now().UTC().Add(ttl), boardID, owner)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) ReleaseBoardSyncLease(ctx context.Context, boardID int64, owner string) error {
	_, err := s.db.ExecContext(ctx, `delete from board_sync_leases where board_id=? and owner=?`, boardID, owner)
	return err
}
