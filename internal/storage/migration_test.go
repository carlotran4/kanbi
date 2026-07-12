package storage

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
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

func TestMigrateV5BackfillsWorkflowKeysAndBoardUUID(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/pre-v5.db"
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-v5 populated database: all columns from migrations 1-4, missing v5 fields.
	stmts := []string{
		`pragma foreign_keys=off`,
		`create table boards (
  id integer primary key autoincrement,
  name text not null,
  workdir text,
  next_ticket_number integer not null default 1,
  ticket_backend text not null default 'local',
  backend_query text,
  backend_config text,
  last_sync_at datetime,
  last_sync_error text,
  created_at datetime not null,
  updated_at datetime not null
)`,
		`create table columns (
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  name text not null,
  position integer not null,
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, position)
)`,
		`create table tickets (
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  column_id integer not null references columns(id) on delete restrict,
  external_id text,
  external_url text,
  external_updated_at datetime,
  sync_version text,
  display_id text not null,
  display_number integer not null,
  title text not null,
  body text not null default '',
  harness text not null default 'pi',
  position integer not null,
  archived_at datetime,
  remote_push_state text,
  remote_push_token text,
  remote_push_attempted_at datetime,
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, display_id),
  unique(board_id, display_number)
)`,
		`create table sessions (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  harness text not null,
  harness_session_ref text,
  harness_session_name text,
  tmux_session_name text not null,
  tmux_window_id text,
  tmux_window_name text not null,
  multiplexer text not null default 'tmux',
  mux_namespace text,
  mux_container_id text,
  mux_container_name text,
  mux_metadata text,
  status text not null,
  is_active integer not null default 0,
  started_at datetime,
  closed_at datetime,
  last_seen_tmux_at datetime,
  last_output_at datetime,
  last_state_change_at datetime,
  last_detected_state text,
  last_attention_reason text,
  last_detection_source text,
  last_observed_excerpt text,
  created_at datetime not null,
  updated_at datetime not null
)`,
		`create table ticket_notes (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  external_id text,
  external_updated_at datetime,
  sync_version text,
  body text not null default '',
  deleted_at datetime,
  created_at datetime not null,
  updated_at datetime not null
)`,
		`create table board_sync_leases (
  board_id integer primary key references boards(id) on delete cascade,
  owner text not null,
  expires_at datetime not null
)`,
		`create table runtime_diagnostics (
  id integer primary key autoincrement,
  created_at datetime not null,
  kind text not null,
  operation text not null,
  board_id integer,
  ticket_id integer,
  session_id integer,
  attempt integer not null default 0,
  message text not null default '',
  cause text not null default ''
)`,
		`create table schema_migrations (version integer primary key, name text not null, applied_at datetime not null)`,
		`insert into schema_migrations(version,name,applied_at) values(0,'migration lock','2026-01-01')`,
		`insert into schema_migrations(version,name,applied_at) values(1,'legacy schema compatibility','2026-01-01')`,
		`insert into schema_migrations(version,name,applied_at) values(2,'projection and lifecycle indexes','2026-01-01')`,
		`insert into schema_migrations(version,name,applied_at) values(3,'sync leases and note tombstones','2026-01-01')`,
		`insert into schema_migrations(version,name,applied_at) values(4,'runtime diagnostics and remote push state','2026-01-01')`,
		`insert into boards(name,next_ticket_number,workdir,ticket_backend,created_at,updated_at) values('Legacy',3,'/tmp','local','2026-01-01','2026-01-01')`,
		`insert into columns(board_id,name,position,created_at,updated_at) values(1,'Open',0,'2026-01-01','2026-01-01')`,
		`insert into columns(board_id,name,position,created_at,updated_at) values(1,'Review',1,'2026-01-01','2026-01-01')`,
		`insert into tickets(board_id,column_id,display_id,display_number,title,body,harness,position,created_at,updated_at) values(1,1,'T-001',1,'Legacy ticket','body','pi',0,'2026-01-01','2026-01-01')`,
		`pragma foreign_keys=on`,
	}
	for _, stmt := range stmts {
		if _, err := raw.Exec(stmt); err != nil {
			_ = raw.Close()
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	_ = raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := s.BoardByName(ctx, "Legacy")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(board.UUID) == "" {
		t.Fatal("migration should assign board uuid")
	}
	if board.SyncEnabled != true {
		t.Fatalf("sync_enabled default should be true: %+v", board)
	}
	view, err := s.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Columns) < 2 {
		t.Fatalf("columns=%+v", view.Columns)
	}
	for _, col := range view.Columns {
		if col.WorkflowKey == "" || col.WorkflowKey != col.Name {
			t.Fatalf("workflow key backfill failed: %+v", col)
		}
	}
	ticket, err := s.TicketByDisplayID(ctx, "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Legacy ticket" {
		t.Fatalf("populated ticket lost: %+v", ticket)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `select max(version) from schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() {
		t.Fatalf("version=%d want %d", version, CurrentSchemaVersion())
	}
}
