package storage

import ()

const schema = `
pragma foreign_keys = on;

create table if not exists boards (
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
);

create table if not exists columns (
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  name text not null,
  position integer not null,
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, position)
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
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, display_id),
  unique(board_id, display_number)
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

create table if not exists ticket_notes (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  external_id text,
  external_updated_at datetime,
  sync_version text,
  body text not null default '',
  created_at datetime not null,
  updated_at datetime not null
);
`
