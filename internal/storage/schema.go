package storage

import ()

const schema = `
pragma foreign_keys = on;

create table if not exists boards (
  id integer primary key autoincrement,
  name text not null,
  uuid text not null,
  workdir text,
  next_ticket_number integer not null default 1,
  ticket_backend text not null default 'local',
  backend_query text,
  backend_config text,
  last_sync_at datetime,
  last_sync_error text,
  archived_at datetime,
  sync_enabled integer not null default 1,
  source_export_uuid text,
  worktree_mode text not null default 'off',
  created_at datetime not null,
  updated_at datetime not null
);

create table if not exists columns (
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  name text not null,
  workflow_key text not null,
  position integer not null,
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, position)
);

create table if not exists master_filter_presets (
  id integer primary key autoincrement,
  name text not null,
  payload_json text not null,
  created_at datetime not null,
  updated_at datetime not null
);

create table if not exists tickets (
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
  focus_paused integer not null default 0,
  remote_push_state text,
  remote_push_token text,
  remote_push_attempted_at datetime,
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, display_id),
  unique(board_id, display_number)
);

create table if not exists ticket_workspaces (
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
);

create table if not exists sessions (
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
  workspace_id integer references ticket_workspaces(id) on delete set null,
  launch_cwd text,
  status text not null,
  is_active integer not null default 1,
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
);

create table if not exists integration_runs (
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
);

create table if not exists integration_run_items (
  id integer primary key autoincrement,
  run_id integer not null references integration_runs(id) on delete cascade,
  workspace_id integer not null references ticket_workspaces(id) on delete cascade,
  ticket_id integer not null references tickets(id) on delete cascade,
  branch_name text not null,
  head_sha text not null,
  position integer not null,
  unique(run_id, workspace_id),
  unique(run_id, position)
);

create table if not exists board_sync_leases (
  board_id integer primary key references boards(id) on delete cascade,
  owner text not null,
  expires_at datetime not null
);

create table if not exists ticket_notes (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  external_id text,
  external_updated_at datetime,
  sync_version text,
  body text not null default '',
  deleted_at datetime,
  created_at datetime not null,
  updated_at datetime not null
);

create table if not exists pause_checkpoints (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  why text not null,
  completed text not null,
  next_action text not null,
  paused_at datetime not null,
  resumed_at datetime
);

create table if not exists runtime_diagnostics (
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
);
`
