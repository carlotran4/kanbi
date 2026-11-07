package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Operators own ordinary session attempts without owning tickets or boards.
func migrateOperators(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `create table if not exists operators (
 id integer primary key autoincrement, name text not null, harness text not null,
 cwd text not null, cron text not null default '', timezone text not null default 'UTC',
 scheduled_prompt text not null default '', schedule_paused integer not null default 0,
 next_wake datetime, last_wake datetime, last_error text not null default '', deleted_at datetime,
 created_at datetime not null, updated_at datetime not null);
 create table if not exists operator_runners(owner text primary key,pid integer not null,seen_at datetime not null);
 create table if not exists operator_wakeups (
 id integer primary key autoincrement, operator_id integer not null references operators(id),
 reason text not null, prompt text not null default '', due_at datetime not null,
 state text not null default 'pending', session_id integer, error text not null default '',
 created_at datetime not null, submitted_at datetime);
 create unique index if not exists operator_pending_recurring on operator_wakeups(operator_id) where reason='scheduled check' and state in ('pending','queued');`); err != nil {
		return err
	}
	columns, err := tableColumns(ctx, tx, "sessions")
	if err != nil {
		return err
	}
	if columns["operator_id"] {
		return nil
	}
	var sequence int64
	if e := tx.QueryRowContext(ctx, `select coalesce((select seq from sqlite_sequence where name='sessions'),0)`).Scan(&sequence); e != nil {
		return e
	}
	var old string
	if err := tx.QueryRowContext(ctx, `select sql from sqlite_master where type='table' and name='sessions'`).Scan(&old); err != nil {
		return err
	}
	replacement := strings.Replace(old, "sessions", "sessions_operators", 1)
	replacement = regexp.MustCompile(`(?i)ticket_id integer not null(?: references tickets\(id\) on delete cascade)?`).ReplaceAllStringFunc(replacement, func(v string) string {
		return regexp.MustCompile(`(?i) not null`).ReplaceAllString(v, "") + ", operator_id integer references operators(id)"
	})
	// Table constraints must follow all column declarations.
	i := strings.LastIndex(replacement, ")")
	replacement = replacement[:i] + ", check ((ticket_id is null) <> (operator_id is null))" + replacement[i:]
	if _, err := tx.ExecContext(ctx, replacement); err != nil {
		return err
	}
	cols, err := tx.QueryContext(ctx, `pragma table_info(sessions)`)
	if err != nil {
		return err
	}
	var names []string
	for cols.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if err = cols.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
			cols.Close()
			return err
		}
		names = append(names, `"`+name+`"`)
	}
	if err = cols.Close(); err != nil {
		return err
	}
	// Save every explicit index before replacing the table; FK enforcement is
	// disabled only on the pinned migration connection, so child rows stay intact.
	rows, err := tx.QueryContext(ctx, `select sql from sqlite_master where type='index' and tbl_name='sessions' and sql is not null`)
	if err != nil {
		return err
	}
	var indexes []string
	for rows.Next() {
		var q string
		if err = rows.Scan(&q); err != nil {
			rows.Close()
			return err
		}
		indexes = append(indexes, q)
	}
	rows.Close()
	list := strings.Join(names, ",")
	if _, err = tx.ExecContext(ctx, `insert into sessions_operators (`+list+`) select `+list+` from sessions; drop table sessions; alter table sessions_operators rename to sessions`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `update sqlite_sequence set seq=max(seq,?) where name='sessions'`, sequence); err != nil {
		return err
	}
	for _, q := range indexes {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `create unique index sessions_one_active_per_operator on sessions(operator_id) where is_active=1;
 create trigger operator_harness_immutable before update of harness on operators when old.harness<>new.harness begin select raise(abort,'operator harness is immutable'); end;`)
	return err
}

type Operator struct {
	State           string     `json:"state,omitempty"`
	Attention       string     `json:"attention,omitempty"`
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Harness         string     `json:"harness"`
	CWD             string     `json:"cwd"`
	Cron            string     `json:"cron"`
	Timezone        string     `json:"timezone"`
	ScheduledPrompt string     `json:"scheduled_prompt"`
	SchedulePaused  bool       `json:"schedule_paused"`
	NextWake        *time.Time `json:"next_wake"`
	LastWake        *time.Time `json:"last_wake"`
	LastError       string     `json:"last_error"`
	DeletedAt       *time.Time `json:"deleted_at"`
}

const operatorSelect = `select id,name,harness,cwd,cron,timezone,scheduled_prompt,schedule_paused,next_wake,last_wake,last_error,deleted_at from operators`

func scanOperator(row messageScanner) (o Operator, err error) {
	err = row.Scan(&o.ID, &o.Name, &o.Harness, &o.CWD, &o.Cron, &o.Timezone, &o.ScheduledPrompt, &o.SchedulePaused, &o.NextWake, &o.LastWake, &o.LastError, &o.DeletedAt)
	return
}
func (s *Store) Operator(ctx context.Context, id int64) (Operator, error) {
	return scanOperator(s.db.QueryRowContext(ctx, operatorSelect+` where id=?`, id))
}
func (s *Store) Operators(ctx context.Context) ([]Operator, error) {
	rows, err := s.db.QueryContext(ctx, operatorSelect+` where deleted_at is null order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operator{}
	for rows.Next() {
		o, e := scanOperator(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) CreateOperator(ctx context.Context, o Operator) (Operator, error) {
	if strings.TrimSpace(o.Name) == "" || o.CWD == "" {
		return o, errors.New("operator name and working directory are required")
	}
	switch o.Harness {
	case "pi", "codex", "claude", "copilot":
	default:
		return o, errors.New("unsupported operator harness")
	}
	now := time.Now().UTC()
	r, err := s.db.ExecContext(ctx, `insert into operators(name,harness,cwd,cron,timezone,scheduled_prompt,next_wake,created_at,updated_at) values(?,?,?,?,?,?,?,?,?)`, o.Name, o.Harness, o.CWD, o.Cron, o.Timezone, o.ScheduledPrompt, o.NextWake, now, now)
	if err != nil {
		return o, err
	}
	o.ID, err = r.LastInsertId()
	return o, err
}
func (s *Store) UpdateOperator(ctx context.Context, o Operator) error {
	r, e := s.db.ExecContext(ctx, `update operators set name=?,cwd=?,cron=?,timezone=?,scheduled_prompt=?,next_wake=?,updated_at=? where id=? and deleted_at is null and harness=?`, o.Name, o.CWD, o.Cron, o.Timezone, o.ScheduledPrompt, o.NextWake, time.Now().UTC(), o.ID, o.Harness)
	return requireAffected(r, e)
}
func (s *Store) PauseOperator(ctx context.Context, id int64, paused bool, next *time.Time) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `update operators set schedule_paused=?,next_wake=?,updated_at=? where id=? and deleted_at is null`, paused, next, time.Now().UTC(), id)
	if e = requireAffected(r, e); e != nil {
		return e
	}
	if paused {
		// A deliberate pause consumes missed recurring checks, while preserving
		// each one-time followup and all peer mail independently.
		if _, e = tx.ExecContext(ctx, `update session_messages set state='acknowledged',notification_status='notified',notification_error='recurring check cancelled by intentional schedule pause' where sender=0 and recipient=? and notification_status in ('pending','deferred') and request_key in (select 'operator-wake-'||id from operator_wakeups where operator_id=? and reason='scheduled check'); update operator_wakeups set state='cancelled' where operator_id=? and reason='scheduled check' and state in ('pending','queued')`, -id, id, id); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) DeleteOperator(ctx context.Context, id int64) error {
	r, e := s.db.ExecContext(ctx, `update operators set deleted_at=?,schedule_paused=1,next_wake=null where id=? and deleted_at is null`, time.Now().UTC(), id)
	return requireAffected(r, e)
}
func (s *Store) OperatorSession(ctx context.Context, id int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, sessionSelectSQL+` where operator_id=? order by id desc limit 1`, id)
}
func (s *Store) ClaimOperatorSession(ctx context.Context, id int64, ses Session) (int64, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var harness string
	if e = tx.QueryRowContext(ctx, `select harness from operators where id=? and deleted_at is null`, id).Scan(&harness); e != nil {
		return 0, e
	}
	if harness != ses.Harness {
		return 0, errors.New("operator harness mismatch")
	}
	now := time.Now().UTC()
	r, e := tx.ExecContext(ctx, `insert into sessions(operator_id,harness,harness_session_ref,tmux_session_name,tmux_window_name,multiplexer,launch_cwd,status,is_active,started_at,created_at,updated_at) values(?,?,?,'','','herdr',?,'starting',1,?,?,?)`, id, harness, nullableString(ses.HarnessSessionRef.String), ses.LaunchCWD.String, now, now, now)
	if e != nil {
		return 0, e
	}
	sid, e := r.LastInsertId()
	if e != nil {
		return 0, e
	}
	return sid, tx.Commit()
}

// OperatorTicket is only an adapter to the existing lifecycle request, never a
// persisted ticket. Its negative ID avoids collisions in per-owner ref assets.
func (s *Store) OperatorTicket(ctx context.Context, id int64) (Ticket, error) {
	o, e := s.Operator(ctx, id)
	if e != nil {
		return Ticket{}, e
	}
	if o.DeletedAt != nil {
		return Ticket{}, errors.New("operator is deleted")
	}
	t := Ticket{ID: -id, OperatorID: id, DisplayID: fmt.Sprintf("O-%d", id), Title: o.Name, Harness: o.Harness, BoardWorkdir: o.CWD, Runtime: "not_started"}
	ses, ok, e := s.OperatorSession(ctx, id)
	if e != nil {
		return t, e
	}
	if ok {
		t.SessionID = sql.NullInt64{Int64: ses.ID, Valid: true}
		t.SessionRef = ses.HarnessSessionRef
		t.SessionActive = ses.IsActive
		t.Runtime = ses.Status
		t.SessionLaunchCWD = ses.LaunchCWD
		t.Multiplexer = sql.NullString{String: ses.Multiplexer, Valid: true}
		t.MuxNamespace = ses.MuxNamespace
		t.MuxContainerID = ses.MuxContainerID
		t.MuxContainerName = ses.MuxContainerName
		t.MuxMetadata = ses.MuxMetadata
		t.LastAttentionReason = ses.LastAttentionReason
	}
	return t, nil
}

type OperatorWakeup struct {
	ID         int64     `json:"id"`
	OperatorID int64     `json:"operator_id"`
	Reason     string    `json:"reason"`
	Prompt     string    `json:"prompt"`
	DueAt      time.Time `json:"due_at"`
	State      string    `json:"state"`
	Error      string    `json:"error"`
}

func (s *Store) QueueOperatorWake(ctx context.Context, id int64, reason, prompt string, due time.Time) (int64, error) {
	r, e := s.db.ExecContext(ctx, `insert into operator_wakeups(operator_id,reason,prompt,due_at,created_at) select ?,?,?,?,? where exists(select 1 from operators where id=? and deleted_at is null)`, id, reason, prompt, due.UTC(), time.Now().UTC(), id)
	if e != nil {
		return 0, e
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return 0, errors.New("operator unavailable")
	}
	return r.LastInsertId()
}
func (s *Store) OperatorWakeups(ctx context.Context, id int64) ([]OperatorWakeup, error) {
	rows, e := s.db.QueryContext(ctx, `select id,operator_id,reason,prompt,due_at,state,error from operator_wakeups where operator_id=? order by id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OperatorWakeup{}
	for rows.Next() {
		var w OperatorWakeup
		if e = rows.Scan(&w.ID, &w.OperatorID, &w.Reason, &w.Prompt, &w.DueAt, &w.State, &w.Error); e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Store) CancelOperatorWake(ctx context.Context, id int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `update operator_wakeups set state='cancelled' where id=? and (state='pending' or (state='queued' and exists(select 1 from session_messages m where m.sender=0 and m.recipient=-operator_id and m.request_key='operator-wake-'||operator_wakeups.id and m.notification_status in ('pending','deferred'))))`, id)
	if e = requireAffected(r, e); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `update session_messages set state='acknowledged',notification_status='notified',notification_error='wakeup cancelled explicitly' where sender=0 and request_key=? and notification_status in ('pending','deferred')`, fmt.Sprintf("operator-wake-%d", id))
	if e != nil {
		return e
	}
	return tx.Commit()
}

// MaterializeRecurring advances from the fixed clock, coalescing downtime to
// one pending check. The compare-and-write fences independent Kanbi instances.
func (s *Store) MaterializeRecurring(ctx context.Context, o Operator, now, next time.Time) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `update operators set next_wake=? where id=? and schedule_paused=0 and deleted_at is null and next_wake<=?`, next, o.ID, now)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 1 {
		_, e = tx.ExecContext(ctx, `insert or ignore into operator_wakeups(operator_id,reason,prompt,due_at,created_at) values(?,'scheduled check',?,?,?)`, o.ID, o.ScheduledPrompt, now, now)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) SetOperatorError(ctx context.Context, id int64, reason string) error {
	_, e := s.db.ExecContext(ctx, `update operators set last_error=? where id=?`, reason, id)
	return e
}

// Wakeups use the existing durable mailbox and native delivery path. A wake row
// and its single mailbox entry commit together, so a crash never loses followups.
func (s *Store) EnqueueOperatorWakeMessage(ctx context.Context, w OperatorWakeup) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `update operator_wakeups set state='queued' where id=? and state='pending' and exists(select 1 from operators o where o.id=operator_id and o.deleted_at is null and (o.schedule_paused=0 or operator_wakeups.reason='run now'))`, w.ID)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return tx.Commit()
	}
	_, e = tx.ExecContext(ctx, `insert into session_messages(sender,recipient,request_key,body,created_at) values(0,?,?,?,?)`, -w.OperatorID, fmt.Sprintf("operator-wake-%d", w.ID), "Wake reason: "+w.Reason+"\n\n"+w.Prompt, time.Now().UTC())
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) RegisterOperatorRunner(ctx context.Context, owner string, pid int) error {
	_, e := s.db.ExecContext(ctx, `insert into operator_runners(owner,pid,seen_at) values(?,?,?) on conflict(owner) do update set seen_at=excluded.seen_at`, owner, pid, time.Now().UTC())
	return e
}
func (s *Store) RemoveOperatorRunner(ctx context.Context, owner string) error {
	_, e := s.db.ExecContext(ctx, `delete from operator_runners where owner=?`, owner)
	return e
}
func (s *Store) OperatorRunnerAlive(ctx context.Context) (bool, error) {
	rows, e := s.db.QueryContext(ctx, `select pid from operator_runners where seen_at>?`, time.Now().UTC().Add(-30*time.Second))
	if e != nil {
		return false, e
	}
	defer rows.Close()
	for rows.Next() {
		var pid int
		if e = rows.Scan(&pid); e != nil {
			return false, e
		}
		if syscall.Kill(pid, 0) == nil {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) ReconcileOperatorReceipts(ctx context.Context) error {
	_, e := s.db.ExecContext(ctx, `update operator_wakeups set state='completed',submitted_at=(select notified_at from session_messages where sender=0 and recipient=-operator_id and request_key='operator-wake-'||operator_wakeups.id) where state='queued' and exists(select 1 from session_messages where sender=0 and recipient=-operator_id and request_key='operator-wake-'||operator_wakeups.id and notification_status='notified');
 update operators set last_wake=(select max(submitted_at) from operator_wakeups where operator_id=operators.id) where exists(select 1 from operator_wakeups where operator_id=operators.id and submitted_at is not null)`)
	return e
}
