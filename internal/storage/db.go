package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"time"
)

type Store struct {
	db      *sql.DB
	focusMu sync.RWMutex
	focus   FocusPolicy
}

// SetFocusPolicy configures the process's global focus policy. The durable
// admission transaction serializes against other Kanbi processes using the
// same global configuration.
func (s *Store) SetFocusPolicy(policy FocusPolicy) {
	keys := make([]string, 0, len(policy.WorkflowKeys))
	seen := make(map[string]bool)
	for _, key := range policy.WorkflowKeys {
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if policy.Limit <= 0 {
		policy.Limit = 3
	}
	policy.WorkflowKeys = keys
	s.focusMu.Lock()
	s.focus = policy
	s.focusMu.Unlock()
}

func (s *Store) FocusPolicy() FocusPolicy {
	s.focusMu.RLock()
	defer s.focusMu.RUnlock()
	p := s.focus
	p.WorkflowKeys = append([]string(nil), p.WorkflowKeys...)
	return p
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
	if err := secureSQLiteFiles(path); err != nil {
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
		return fmt.Sprintf(":memory:?_foreign_keys=on&_busy_timeout=%d&_txlock=immediate", sqliteBusyTimeout.Milliseconds())
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
	// All explicit transactions begin with a reserved write lock. Focus
	// admission must count then write without a deferred-transaction upgrade
	// race; the existing busy timeout lets concurrent Kanbi processes queue.
	query.Set("_txlock", "immediate")
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
	deadline := time.Now().Add(sqliteBusyTimeout)
	for {
		err := s.initOnce(ctx)
		if err == nil || !isSQLiteBusy(err) || time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Store) initOnce(ctx context.Context) error {
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

func isSQLiteBusy(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && (sqliteErr.Code == sqlite3.ErrBusy || sqliteErr.Code == sqlite3.ErrLocked)
}

func ensureParent(path string) error {
	if path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err == nil {
		// The database path may intentionally live in a shared or system-owned
		// directory. Secure the database files themselves without changing an
		// existing parent directory's permissions.
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(dir, 0o700)
}

func secureSQLiteFiles(path string) error {
	if path == ":memory:" {
		return nil
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("secure SQLite file %s: %w", candidate, err)
		}
	}
	return nil
}
