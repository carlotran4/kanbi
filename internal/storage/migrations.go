package storage

import (
	"context"
	"database/sql"

	"fmt"

	"os"

	"time"
)

type migration struct {
	version int
	name    string
	apply   func(context.Context, *sql.Tx) error
}

var migrations = []migration{
	{version: 1, name: "legacy schema compatibility", apply: migrateLegacySchema},
	{version: 2, name: "projection and lifecycle indexes", apply: migrateIndexes},
	{version: 3, name: "sync leases and note tombstones", apply: migrateProductionSafety},
	{version: 4, name: "runtime diagnostics and remote push state", apply: migrateRuntimeHardening},
	{version: 5, name: "board archive, workflow keys, filter presets", apply: migrateBoardArchiveAndWorkflow},
	{version: 6, name: "ticket workspaces and session launch cwd", apply: migrateTicketWorkspaces},
	{version: 7, name: "repository integration runs", apply: migrateIntegrationRuns},
	{version: 8, name: "global focus checkpoints", apply: migrateGlobalFocus},
}

// CurrentSchemaVersion is the newest SQLite migration understood by this build.
func CurrentSchemaVersion() int { return migrations[len(migrations)-1].version }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `create table if not exists schema_migrations (
  version integer primary key,
  name text not null,
  applied_at datetime not null
)`); err != nil {
		return fmt.Errorf("create schema migration ledger: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version,name,applied_at) values(0,'migration lock',?) on conflict(version) do update set applied_at=schema_migrations.applied_at`, time.Now().UTC()); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `select coalesce(max(version),0) from schema_migrations`).Scan(&current); err != nil {
		return err
	}
	latest := migrations[len(migrations)-1].version
	if current > latest {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, latest)
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := m.apply(ctx, tx); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version,name,applied_at) values(?,?,?)`, m.version, m.name, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func migrateLegacySchema(ctx context.Context, tx *sql.Tx) error {
	boardColumns, err := tableColumns(ctx, tx, "boards")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{
		{"workdir", "text"}, {"ticket_backend", "text not null default 'local'"},
		{"backend_query", "text"}, {"backend_config", "text"},
		{"last_sync_at", "datetime"}, {"last_sync_error", "text"},
	} {
		if !boardColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table boards add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		if _, err := tx.ExecContext(ctx, `update boards set workdir=? where workdir is null or workdir=''`, cwd); err != nil {
			return err
		}
	}
	ticketColumns, err := tableColumns(ctx, tx, "tickets")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{{"external_id", "text"}, {"external_url", "text"}, {"external_updated_at", "datetime"}, {"sync_version", "text"}} {
		if !ticketColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table tickets add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	sessionColumns, err := tableColumns(ctx, tx, "sessions")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{
		{"started_at", "datetime"}, {"closed_at", "datetime"}, {"last_output_at", "datetime"},
		{"last_state_change_at", "datetime"}, {"last_detected_state", "text"},
		{"last_attention_reason", "text"}, {"last_detection_source", "text"},
		{"last_observed_excerpt", "text"}, {"multiplexer", "text not null default 'tmux'"},
		{"mux_namespace", "text"}, {"mux_container_id", "text"},
		{"mux_container_name", "text"}, {"mux_metadata", "text"},
	} {
		if !sessionColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table sessions add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	for _, statement := range []string{
		`update sessions set multiplexer='tmux' where multiplexer is null or multiplexer=''`,
		`update sessions set mux_namespace=tmux_session_name where mux_namespace is null and tmux_session_name is not null`,
		`update sessions set mux_container_id=tmux_window_id where mux_container_id is null and tmux_window_id is not null`,
		`update sessions set mux_container_name=tmux_window_name where mux_container_name is null and tmux_window_name is not null`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `update sessions set is_active=0 where is_active=1 and id not in (select max(id) from sessions where is_active=1 group by ticket_id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create unique index if not exists sessions_one_active_per_ticket on sessions(ticket_id) where is_active=1`); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `create table if not exists ticket_notes (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  external_id text,
  external_updated_at datetime,
  sync_version text,
  body text not null default '',
  created_at datetime not null,
  updated_at datetime not null
)`); err != nil {
		return err
	}
	noteColumns, err := tableColumns(ctx, tx, "ticket_notes")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{{"external_id", "text"}, {"external_updated_at", "datetime"}, {"sync_version", "text"}} {
		if !noteColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table ticket_notes add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}

	for _, statement := range []string{
		`create unique index if not exists boards_name_nocase_uq on boards(name collate nocase)`,
		`create unique index if not exists columns_board_name_uq on columns(board_id,name)`,
		`create unique index if not exists tickets_board_external_id_uq on tickets(board_id,external_id) where external_id is not null`,
		`create unique index if not exists notes_ticket_external_id_uq on ticket_notes(ticket_id,external_id) where external_id is not null`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("enforce storage identity invariant: %w", err)
		}
	}
	return nil
}

func migrateProductionSafety(ctx context.Context, tx *sql.Tx) error {
	cols, err := tableColumns(ctx, tx, "ticket_notes")
	if err != nil {
		return err
	}
	if !cols["deleted_at"] {
		if _, err := tx.ExecContext(ctx, `alter table ticket_notes add column deleted_at datetime`); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `create table if not exists board_sync_leases (
  board_id integer primary key references boards(id) on delete cascade,
  owner text not null,
  expires_at datetime not null
)`)
	return err
}

func migrateIndexes(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`create index if not exists idx_columns_board_position on columns(board_id,position)`,
		`create index if not exists idx_tickets_column_archived_position on tickets(column_id,archived_at,position)`,
		`create index if not exists idx_tickets_board_external_id on tickets(board_id,external_id) where external_id is not null`,
		`create index if not exists idx_sessions_ticket_latest on sessions(ticket_id,id desc)`,
		`create index if not exists idx_sessions_ticket_active on sessions(ticket_id,is_active)`,
		`create index if not exists idx_ticket_notes_ticket on ticket_notes(ticket_id,id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateRuntimeHardening(ctx context.Context, tx *sql.Tx) error {
	ticketColumns, err := tableColumns(ctx, tx, "tickets")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{
		{"remote_push_state", "text"},
		{"remote_push_token", "text"},
		{"remote_push_attempted_at", "datetime"},
	} {
		if !ticketColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table tickets add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `create table if not exists runtime_diagnostics (
  id integer primary key autoincrement,
  created_at datetime not null,
  kind text not null,
  operation text not null,
  board_id integer references boards(id) on delete set null,
  ticket_id integer references tickets(id) on delete set null,
  session_id integer references sessions(id) on delete set null,
  attempt integer not null default 0,
  message text not null default '',
  cause text not null default ''
)`); err != nil {
		return err
	}
	for _, statement := range []string{
		`create index if not exists idx_runtime_diagnostics_board_created on runtime_diagnostics(board_id, created_at desc)`,
		`create index if not exists idx_runtime_diagnostics_kind_created on runtime_diagnostics(kind, created_at desc)`,
		`create index if not exists idx_tickets_remote_push_state on tickets(board_id, remote_push_state) where remote_push_state is not null`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateBoardArchiveAndWorkflow(ctx context.Context, tx *sql.Tx) error {
	boardColumns, err := tableColumns(ctx, tx, "boards")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{
		{"uuid", "text"},
		{"archived_at", "datetime"},
		{"sync_enabled", "integer not null default 1"},
		{"source_export_uuid", "text"},
	} {
		if !boardColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table boards add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `select id from boards where uuid is null or uuid=''`)
	if err != nil {
		return err
	}
	var missing []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range missing {
		uuid, err := NewUUIDv4()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `update boards set uuid=? where id=?`, uuid, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `update boards set sync_enabled=1 where sync_enabled is null`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create unique index if not exists boards_uuid_uq on boards(uuid)`); err != nil {
		return err
	}

	columnColumns, err := tableColumns(ctx, tx, "columns")
	if err != nil {
		return err
	}
	if !columnColumns["workflow_key"] {
		if _, err := tx.ExecContext(ctx, `alter table columns add column workflow_key text`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `update columns set workflow_key=name where workflow_key is null or workflow_key=''`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create unique index if not exists columns_board_workflow_key_uq on columns(board_id, workflow_key)`); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `create table if not exists master_filter_presets (
  id integer primary key autoincrement,
  name text not null,
  payload_json text not null,
  created_at datetime not null,
  updated_at datetime not null
)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create unique index if not exists master_filter_presets_name_nocase_uq on master_filter_presets(name collate nocase)`); err != nil {
		return err
	}
	return nil
}

func migrateGlobalFocus(ctx context.Context, tx *sql.Tx) error {
	cols, err := tableColumns(ctx, tx, "tickets")
	if err != nil {
		return err
	}
	if !cols["focus_paused"] {
		if _, err := tx.ExecContext(ctx, `alter table tickets add column focus_paused integer not null default 0`); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`create table if not exists pause_checkpoints (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  why text not null,
  completed text not null,
  next_action text not null,
  paused_at datetime not null,
  resumed_at datetime
)`,
		`create unique index if not exists pause_checkpoints_one_open_per_ticket on pause_checkpoints(ticket_id) where resumed_at is null`,
		`create index if not exists idx_pause_checkpoints_ticket_paused on pause_checkpoints(ticket_id, paused_at desc)`,
		`create index if not exists idx_tickets_focus_paused on tickets(focus_paused)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateTicketWorkspaces(ctx context.Context, tx *sql.Tx) error {
	boardColumns, err := tableColumns(ctx, tx, "boards")
	if err != nil {
		return err
	}
	if !boardColumns["worktree_mode"] {
		if _, err := tx.ExecContext(ctx, `alter table boards add column worktree_mode text not null default 'off'`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `update boards set worktree_mode='off' where worktree_mode is null or worktree_mode=''`); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `create table if not exists ticket_workspaces (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  board_id integer not null references boards(id) on delete cascade,
  kind text not null default 'git_worktree',
  state text not null,
  is_current integer not null default 0,
  owns_worktree integer not null default 1,
  repository_root text not null default '',
  common_dir text not null default '',
  worktree_path text not null default '',
  launch_subdir text not null default '',
  launch_cwd text not null default '',
  branch_name text not null default '',
  source_branch text not null default '',
  source_commit_sha text not null default '',
  base_commit_sha text not null default '',
  last_status_json text,
  last_error text,
  integrated_at datetime,
  retired_at datetime,
  created_at datetime not null,
  updated_at datetime not null
)`); err != nil {
		return err
	}

	sessionColumns, err := tableColumns(ctx, tx, "sessions")
	if err != nil {
		return err
	}
	if !sessionColumns["workspace_id"] {
		if _, err := tx.ExecContext(ctx, `alter table sessions add column workspace_id integer references ticket_workspaces(id) on delete set null`); err != nil {
			return err
		}
	}
	if !sessionColumns["launch_cwd"] {
		if _, err := tx.ExecContext(ctx, `alter table sessions add column launch_cwd text`); err != nil {
			return err
		}
	}

	for _, statement := range []string{
		`create unique index if not exists ticket_workspaces_one_current_per_ticket on ticket_workspaces(ticket_id) where is_current=1`,
		`create index if not exists idx_ticket_workspaces_ticket_id on ticket_workspaces(ticket_id,id desc)`,
		`create index if not exists idx_ticket_workspaces_board_state on ticket_workspaces(board_id,state)`,
		`create index if not exists idx_sessions_workspace_id on sessions(workspace_id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateIntegrationRuns(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `create table if not exists integration_runs (
  id integer primary key autoincrement,
  public_id text not null,
  board_id integer not null references boards(id) on delete cascade,
  state text not null,
  repository_root text not null,
  common_dir text not null,
  worktree_path text not null,
  branch_name text not null,
  source_branch text not null,
  source_sha text not null,
  candidate_sha text,
  harness text not null,
  token_hash text not null,
  prompt text not null,
  validation_command text,
  last_error text,
  multiplexer text,
  mux_namespace text,
  mux_container_id text,
  mux_container_name text,
  mux_metadata text,
  harness_session_ref text,
  created_at datetime not null,
  updated_at datetime not null,
  completed_at datetime,
  promoted_at datetime
)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create table if not exists integration_run_items (
  id integer primary key autoincrement,
  run_id integer not null references integration_runs(id) on delete cascade,
  workspace_id integer not null references ticket_workspaces(id) on delete cascade,
  ticket_id integer not null references tickets(id) on delete cascade,
  branch_name text not null,
  head_sha text not null,
  position integer not null,
  unique(run_id, workspace_id),
  unique(run_id, position)
)`); err != nil {
		return err
	}
	for _, q := range []string{
		`create unique index if not exists integration_runs_public_id_uq on integration_runs(public_id)`,
		`create unique index if not exists integration_runs_one_active_repo_source on integration_runs(common_dir,source_branch) where state in ('planning','running','waiting_for_user','needs_permission','ready','blocked','promoting','cleanup_required')`,
		`create index if not exists integration_run_items_run_position on integration_run_items(run_id,position)`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

type contextQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func tableColumns(ctx context.Context, db contextQueryer, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}
