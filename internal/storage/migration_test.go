package storage

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFileStoreEnablesSQLiteSafetyPragmas(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/kanbi.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, `pragma foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d, want 1", foreignKeys)
	}
	var busyTimeout int
	if err := s.db.QueryRowContext(ctx, `pragma busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != int(sqliteBusyTimeout.Milliseconds()) {
		t.Fatalf("busy_timeout=%d, want %d", busyTimeout, sqliteBusyTimeout.Milliseconds())
	}
	var journalMode string
	if err := s.db.QueryRowContext(ctx, `pragma journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode=%q, want WAL", journalMode)
	}
}

func TestInitRecordsVersionedMigrationsAndIndexes(t *testing.T) {
	s, ctx := newTestStore(t)
	if err := s.Init(ctx); err != nil {
		t.Fatalf("second init: %v", err)
	}
	var version, count int
	if err := s.db.QueryRowContext(ctx, `select max(version), count(*) from schema_migrations where version > 0`).Scan(&version, &count); err != nil {
		t.Fatal(err)
	}
	if version != migrations[len(migrations)-1].version || count != len(migrations) {
		t.Fatalf("migration ledger version=%d count=%d", version, count)
	}
	for _, index := range []string{"idx_tickets_column_archived_position", "idx_sessions_ticket_latest", "idx_ticket_notes_ticket"} {
		var found int
		if err := s.db.QueryRowContext(ctx, `select count(*) from sqlite_master where type='index' and name=?`, index).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != 1 {
			t.Errorf("index %s not installed", index)
		}
	}
}

func TestInitRejectsNewerSchemaVersion(t *testing.T) {
	s, ctx := newTestStore(t)
	newer := migrations[len(migrations)-1].version + 1
	if _, err := s.db.ExecContext(ctx, `insert into schema_migrations(version,name,applied_at) values(?,?,?)`, newer, "future", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Init error=%v, want unsupported schema error", err)
	}
}

func TestInitRejectsForeignKeyCorruption(t *testing.T) {
	s, ctx := newTestStore(t)
	if _, err := s.db.ExecContext(ctx, `pragma foreign_keys=off`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `insert into sessions(ticket_id,harness,tmux_session_name,tmux_window_name,status,is_active,created_at,updated_at) values(999999,'pi','test','orphan','running',0,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `pragma foreign_keys=on`); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err == nil || !strings.Contains(err.Error(), "foreign-key violation") {
		t.Fatalf("Init error=%v, want foreign-key violation", err)
	}
}

func TestConcurrentInitSerializesMigrations(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/kanbi.db"
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			<-start
			errs <- s.Init(ctx)
		}(store)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Init: %v", err)
		}
	}
	var count int
	if err := a.db.QueryRowContext(ctx, `select count(*) from schema_migrations where version > 0`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(migrations) {
		t.Fatalf("migration count=%d, want %d", count, len(migrations))
	}
}

func TestBusyTimeoutAllowsConcurrentWriterToComplete(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/kanbi.db"
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	board, err := a.DefaultBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `update boards set updated_at=? where id=?`, time.Now().UTC(), board.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.RenameBoard(ctx, board.ID, "Renamed after lock") }()
	time.Sleep(100 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("waiting writer failed: %v", err)
	}
}
