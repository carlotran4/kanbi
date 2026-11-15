package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func migrateTicketWakeups(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `create table if not exists ticket_wakeups (
 id integer primary key autoincrement,
 ticket_id integer not null references tickets(id) on delete cascade,
 prompt text not null,
 due_at datetime not null,
 state text not null default 'pending' check(state in ('pending','activating','queued','failed','cancelled')),
 error text not null default '',
 message_id integer references session_messages(id),
 created_at datetime not null
 ); create index if not exists ticket_wakeups_due on ticket_wakeups(state,due_at);`)
	return err
}

type TicketWakeup struct {
	ID, TicketID            int64
	Prompt                  string
	DueAt                   time.Time
	State, Error            string
	MessageID               sql.NullInt64
	Delivery, DeliveryError string
}

func (s *Store) ScheduleTicketPrompt(ctx context.Context, ticketID int64, prompt string, due time.Time) error {
	if strings.TrimSpace(prompt) == "" || len(prompt) > 16384 {
		return errors.New("prompt must contain 1–16384 bytes")
	}
	if !due.After(time.Now()) {
		return errors.New("scheduled time must be in the future")
	}
	r, err := s.db.ExecContext(ctx, `insert into ticket_wakeups(ticket_id,prompt,due_at,created_at) select id,?,?,? from tickets where id=? and archived_at is null`, prompt, due.UTC(), time.Now().UTC(), ticketID)
	return requireAffected(r, err)
}

// Zero ticketID selects due work across boards; a ticket query includes history.
func (s *Store) TicketWakeups(ctx context.Context, ticketID int64) ([]TicketWakeup, error) {
	rows, err := s.db.QueryContext(ctx, `select w.id,w.ticket_id,w.prompt,w.due_at,w.state,w.error,w.message_id,coalesce(m.notification_status,''),coalesce(m.notification_error,'') from ticket_wakeups w left join session_messages m on m.id=w.message_id where (?<>0 and w.ticket_id=?) or (?=0 and w.state in ('pending','activating') and w.due_at<=?) order by w.due_at,w.id`, ticketID, ticketID, ticketID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TicketWakeup{}
	for rows.Next() {
		var w TicketWakeup
		if err = rows.Scan(&w.ID, &w.TicketID, &w.Prompt, &w.DueAt, &w.State, &w.Error, &w.MessageID, &w.Delivery, &w.DeliveryError); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) ClaimTicketWake(ctx context.Context, id int64) (bool, error) {
	r, err := s.db.ExecContext(ctx, `update ticket_wakeups set state='activating' where id=? and state='pending' and due_at<=? and exists(select 1 from tickets t join boards b on b.id=t.board_id where t.id=ticket_wakeups.ticket_id and t.focus_paused=0 and t.archived_at is null and b.archived_at is null)`, id, time.Now().UTC())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
func (s *Store) FailTicketWake(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `update ticket_wakeups set state='failed',error=? where id=? and state='activating'`, RedactSecretText(reason), id)
	return err
}

// The mailbox entry and handoff state commit together. Delivery keeps the
// mailbox's existing uncertain-outcome fence and exact session-attempt address.
func (s *Store) EnqueueTicketWake(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `update ticket_wakeups set state='queued' where id=? and state='activating' and exists(select 1 from tickets t join boards b on b.id=t.board_id join sessions s on s.ticket_id=t.id where t.id=ticket_wakeups.ticket_id and t.focus_paused=0 and t.archived_at is null and b.archived_at is null and s.is_active=1 and s.status not in ('starting','closing'))`, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("ticket is no longer available for scheduled delivery")
	}
	r, err = tx.ExecContext(ctx, `insert into session_messages(sender,recipient,request_key,body,created_at) select 0,s.id,?,w.prompt,? from ticket_wakeups w join sessions s on s.ticket_id=w.ticket_id and s.is_active=1 where w.id=?`, fmt.Sprintf("ticket-wake-%d", id), time.Now().UTC(), id)
	if err != nil {
		return err
	}
	mid, err := r.LastInsertId()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `update ticket_wakeups set message_id=? where id=?`, mid, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CancelTicketWake(ctx context.Context, ticketID, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `update ticket_wakeups set state='cancelled' where id=? and ticket_id=? and (state in ('pending','failed') or (state='queued' and exists(select 1 from session_messages m where m.id=message_id and m.state='queued' and m.notification_status in ('pending','deferred'))))`, id, ticketID)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("only pending, failed or safely unsent prompts can be cancelled")
	}
	_, err = tx.ExecContext(ctx, `update session_messages set state='acknowledged',notification_status='notified',notification_error='scheduled prompt cancelled' where id=(select message_id from ticket_wakeups where id=?)`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
