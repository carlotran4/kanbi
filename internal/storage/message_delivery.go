package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func migrateMessageDelivery(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct{ name, typ string }{
		{"notification_status", "text not null default 'pending'"},
		{"notification_transport", "text not null default ''"},
		{"notification_error", "text not null default ''"},
		{"notification_attempted_at", "datetime"},
	} {
		columns, err := tableColumns(ctx, tx, "session_messages")
		if err != nil {
			return err
		}
		if !columns[column.name] {
			if _, err = tx.ExecContext(ctx, `alter table session_messages add column `+column.name+` `+column.typ); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, `update session_messages set notification_status='notified' where state in ('notified','acknowledged') and notification_status='pending'`)
	return err
}

// ClaimMessageNotification records an uncertain outcome BEFORE any side effect.
// A crash or competing notifier cannot silently repeat it. One unconfirmed handoff
// per recipient prevents retries racing uncertain transport acceptance.
func (s *Store) ClaimMessageNotification(ctx context.Context, recipient, id int64, transport string) (bool, error) {
	if recipient < 0 {
		live, e := s.OperatorRunnerAlive(ctx)
		if e != nil || !live {
			return false, e
		}
	}
	res, err := s.db.ExecContext(ctx, `update session_messages set notification_status='uncertain',notification_transport=?,notification_error='notification in flight or acceptance unknown',notification_attempted_at=?
where id=? and recipient=? and state='queued' and notification_status in ('pending','deferred')
and exists(select 1 from sessions s left join tickets t on t.id=s.ticket_id left join boards b on b.id=t.board_id left join operators o on o.id=s.operator_id where ((s.id=? and s.operator_id is null) or (-s.operator_id=? and s.id=(select max(id) from sessions where operator_id=s.operator_id))) and s.is_active=1 and s.status not in ('starting','closing') and ((o.id is not null and o.deleted_at is null and s.status in ('completed','ready')) or (t.focus_paused=0 and t.archived_at is null and b.archived_at is null)))
and (recipient>0 or sender<>0 or exists(select 1 from operators o where o.id=-recipient and (o.schedule_paused=0 or exists(select 1 from operator_wakeups w where w.operator_id=o.id and 'operator-wake-'||w.id=session_messages.request_key and w.reason='run now'))))
and not exists(select 1 from session_messages other where other.recipient=? and other.id<>session_messages.id and other.state<>'acknowledged' and other.notification_status in ('uncertain','submitted'))`, transport, time.Now().UTC(), id, recipient, recipient, recipient, recipient)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DeferMessageNotification is only for failures BEFORE a transport side effect.
func (s *Store) DeferMessageNotification(ctx context.Context, recipient, id int64, transport, reason string) error {
	_, err := s.db.ExecContext(ctx, `update session_messages set notification_status='deferred',notification_transport=?,notification_error=? where id=? and recipient=? and state='queued' and notification_status in ('pending','deferred')`, transport, RedactSecretText(reason), id, recipient)
	return err
}
func (s *Store) RecordMessageNotificationError(ctx context.Context, recipient, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `update session_messages set notification_error=? where id=? and recipient=? and state='queued' and notification_status='uncertain'`, RedactSecretText(reason), id, recipient)
	return err
}

// An explicit retry can duplicate a native notice if its prior acceptance was
// unknown. Delay retries beyond bounded transport timeouts; never retry silently.
func (s *Store) RetryMessageNotification(ctx context.Context, participant, id int64) error {
	res, err := s.db.ExecContext(ctx, `update session_messages set notification_status='pending',notification_error='explicit retry of uncertain notification' where id=? and (sender=? or recipient=?) and state='queued' and notification_status='uncertain' and notification_attempted_at<?`, id, participant, participant, time.Now().UTC().Add(-30*time.Second))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("only an uncertain queued notification older than 30 seconds can be explicitly retried")
	}
	return nil
}

func (s *Store) PendingMessageRecipients(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `select distinct m.recipient from session_messages m join sessions s on (s.id=m.recipient and s.operator_id is null) or (-s.operator_id=m.recipient and s.id=(select max(id) from sessions where operator_id=s.operator_id)) left join tickets t on t.id=s.ticket_id left join boards b on b.id=t.board_id left join operators o on o.id=s.operator_id where m.state='queued' and m.notification_status in ('pending','deferred') and s.is_active=1 and ((o.id is not null and o.deleted_at is null and s.status in ('completed','ready')) or (t.focus_paused=0 and t.archived_at is null and b.archived_at is null)) order by m.recipient limit 32`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) RecordMessageNotificationSubmitted(ctx context.Context, recipient, id int64) error {
	_, err := s.db.ExecContext(ctx, `update session_messages set notification_status='submitted',notification_error='' where id=? and recipient=? and state='queued' and notification_status='uncertain'`, id, recipient)
	return err
}

// Fetching a body is positive receiver evidence even when a socket transport
// cannot return a native receipt. It never acknowledges work on the receiver's behalf.
func (s *Store) ReceiptMessageRead(ctx context.Context, recipient, id int64) error {
	_, err := s.db.ExecContext(ctx, `update session_messages set state=case when state='queued' then 'notified' else state end,notified_at=coalesce(notified_at,?),notification_status='notified',notification_error='' where id=? and recipient=? and state<>'acknowledged'`, time.Now().UTC(), id, recipient)
	return err
}
func (s *Store) DeferNativeMessage(ctx context.Context, recipient, id int64) error {
	_, err := s.db.ExecContext(ctx, `update session_messages set notification_status='deferred',notification_error='native input state changed before submission' where id=? and recipient=? and state='queued' and notification_status='uncertain' and notification_transport='pi_extension'`, id, recipient)
	return err
}

// Only a transport that proves no bytes were submitted may release its claim.
func (s *Store) ReleaseUnsentNotification(ctx context.Context, recipient, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `update session_messages set notification_status='deferred',notification_error=? where id=? and recipient=? and state='queued' and notification_status='uncertain'`, RedactSecretText(reason), id, recipient)
	return err
}
