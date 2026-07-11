package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"time"
)

type Store struct {
	db *sql.DB
}

const sqliteBusyTimeout = 5 * time.Second

func Open(path string) (*Store, error) {
	if err := ensureParent(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", sqliteDSN(path, false))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite3", sqliteDSN(":memory:", true))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func sqliteDSN(path string, memory bool) string {
	if memory {
		return fmt.Sprintf(":memory:?_foreign_keys=on&_busy_timeout=%d", sqliteBusyTimeout.Milliseconds())
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	u := url.URL{Scheme: "file", Path: absolute}
	query := u.Query()
	query.Set("_foreign_keys", "on")
	query.Set("_busy_timeout", strconv.FormatInt(sqliteBusyTimeout.Milliseconds(), 10))
	query.Set("_journal_mode", "WAL")
	query.Set("_synchronous", "NORMAL")
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) verifyForeignKeys(ctx context.Context) error {
	var enabled int
	if err := s.db.QueryRowContext(ctx, `pragma foreign_keys`).Scan(&enabled); err != nil {
		return fmt.Errorf("read SQLite foreign-key setting: %w", err)
	}
	if enabled != 1 {
		return errors.New("SQLite foreign-key enforcement is disabled")
	}
	rows, err := s.db.QueryContext(ctx, `pragma foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check SQLite foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return err
		}
		return fmt.Errorf("foreign-key violation in table %s row %v referencing %s (constraint %d)", table, rowID, parent, foreignKeyID)
	}
	return rows.Err()
}

func (s *Store) Init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	if err := s.enforceSessionInvariant(ctx); err != nil {
		return err
	}
	if err := s.ensureDefaultBoard(ctx); err != nil {
		return err
	}
	return s.verifyForeignKeys(ctx)
}

func ensureParent(path string) error {
	if path == ":memory:" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
