package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const fleetSchema = `create table if not exists fleet_journal (id integer primary key check(id=1), payload text not null)`

func (s *Store) FleetJournal(ctx context.Context) (string, error) {
	var p string
	err := s.db.QueryRowContext(ctx, `select payload from fleet_journal where id=1`).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return p, err
}
func (s *Store) SaveFleetJournal(ctx context.Context, payload string, create bool) error {
	if create {
		_, err := s.db.ExecContext(ctx, `insert into fleet_journal(id,payload) values(1,?)`, payload)
		return err
	}
	r, err := s.db.ExecContext(ctx, `update fleet_journal set payload=? where id=1`, payload)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("Fleet journal no longer exists")
	}
	return err
}
func (s *Store) DeleteFleetJournal(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `delete from fleet_journal where id=1`)
	return err
}

// PersistFleetReference changes only the container of the exact existing attempt,
// atomically with its restoration journal. Ref capture and history are untouched.
func (s *Store) PersistFleetReference(ctx context.Context, sessionID int64, namespace, id, metadata, payload string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `update sessions set mux_namespace=?,mux_container_id=?,mux_metadata=?,updated_at=? where id=? and multiplexer='herdr'`, namespace, id, metadata, time.Now().UTC(), sessionID)
	if err != nil {
		return err
	}
	if n, e := res.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return errors.New("Fleet session no longer exists")
	}
	res, err = tx.ExecContext(ctx, `update fleet_journal set payload=? where id=1`, payload)
	if err != nil {
		return err
	}
	if n, e := res.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return errors.New("Fleet journal no longer exists")
	}
	return tx.Commit()
}
