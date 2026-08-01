package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

// BackupTo creates a transactionally consistent SQLite snapshot, including
// committed WAL content, using SQLite's VACUUM INTO operation.
func (s *Store) BackupTo(ctx context.Context, destination string) error {
	if err := ensureParent(destination); err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	quoted := strings.ReplaceAll(destination, "'", "''")
	if _, err := s.db.ExecContext(ctx, `vacuum into '`+quoted+`'`); err != nil {
		return fmt.Errorf("create SQLite snapshot: %w", err)
	}
	return os.Chmod(destination, 0o600)
}

func ValidateDatabase(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite3", sqliteDSN(path, false))
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, `pragma integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	rows, err := db.QueryContext(ctx, `select version from schema_migrations where version>0 order by version`)
	if err != nil {
		return fmt.Errorf("invalid Kanbi database: %w", err)
	}
	defer rows.Close()
	version := 0
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			return err
		}
		version++
		if got != version {
			return fmt.Errorf("invalid Kanbi migration ledger: expected version %d, found %d", version, got)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	latest := migrations[len(migrations)-1].version
	if version != latest {
		return fmt.Errorf("backup schema version %d is not the supported version %d", version, latest)
	}
	for _, table := range []string{"boards", "columns", "tickets", "sessions", "ticket_notes"} {
		var count int
		if err := db.QueryRowContext(ctx, `select count(*) from sqlite_master where type='table' and name=?`, table).Scan(&count); err != nil || count != 1 {
			return fmt.Errorf("invalid Kanbi database: required table %s is missing", table)
		}
	}
	fkRows, err := db.QueryContext(ctx, `pragma foreign_key_check`)
	if err != nil {
		return err
	}
	defer fkRows.Close()
	if fkRows.Next() {
		return errors.New("invalid Kanbi database: foreign-key violations found")
	}
	return fkRows.Err()
}
