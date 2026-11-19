package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/carlotran4/poke/mailbox"
)

// Messages deliberately retain endpoint IDs without cascading foreign keys:
// deleting a board must not delete the other board's correspondence.
const messageSchema = `create table if not exists session_messages (
 id integer primary key autoincrement,
 sender integer not null,
 recipient integer not null,
 request_key text not null,
 body text not null,
 reply_to integer not null default 0,
 depth integer not null default 0 check(depth between 0 and 8),
 state text not null default 'queued' check(state in ('queued','notified','acknowledged')),
 created_at datetime not null,
 notified_at datetime,
 acknowledged_at datetime,
 unique(sender, request_key)
);
create index if not exists session_messages_inbox on session_messages(recipient,state,id);`

func migrateMessages(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, messageSchema)
	return err
}

// SessionMessage preserves the Kanbi JSON surface while Poke owns mailbox data.
type SessionMessage = mailbox.Message
type messageScanner interface{ Scan(...any) error }

func (s *Store) SessionMessage(ctx context.Context, id int64) (SessionMessage, error) {
	return mailbox.Get(ctx, s.db, id)
}
func (s *Store) SendSessionMessage(ctx context.Context, sender, recipient int64, key, body string, replyTo int64) (SessionMessage, error) {
	return mailbox.Send(ctx, s.db, func(ctx context.Context, tx *sql.Tx, sender, recipient int64) error {
		var err error
		var active bool
		if err = tx.QueryRowContext(ctx, `select s.is_active from sessions s left join operators o on o.id=s.operator_id where ((s.id=? and ?>0) or (s.operator_id=-? and ?<0)) and (s.operator_id is null or o.deleted_at is null) order by s.id desc limit 1`, sender, sender, sender, sender).Scan(&active); err != nil {
			return fmt.Errorf("sender session: %w", err)
		}
		if !active {
			return errors.New("sender session is closed")
		}
		var exists int
		if err = tx.QueryRowContext(ctx, `select id from sessions where id=? and ?>0 and operator_id is null union all select id from operators where id=-? and ?<0 and deleted_at is null limit 1`, recipient, recipient, recipient, recipient).Scan(&exists); err != nil {
			return fmt.Errorf("recipient session: %w", err)
		}
		return nil
	}, sender, recipient, key, body, replyTo)
}
func (s *Store) SessionInbox(ctx context.Context, recipient int64, all bool) ([]SessionMessage, error) {
	return mailbox.Inbox(ctx, s.db, recipient, all)
}

// SessionNotificationInbox is the opt-in native receiver's preflight. Runtime
// heuristics cannot authorize terminal input; the receiver must also know its
// own native turn/dialog/editor state. This method never consumes mail.
func (s *Store) SessionNotificationInbox(ctx context.Context, recipient int64) ([]SessionMessage, error) {
	if recipient < 0 {
		live, e := s.OperatorRunnerAlive(ctx)
		if e != nil || !live {
			return []SessionMessage{}, e
		}
		o, e := s.Operator(ctx, -recipient)
		if e != nil {
			return nil, e
		}
		if o.DeletedAt != nil {
			return []SessionMessage{}, nil
		}
		ses, ok, e := s.OperatorSession(ctx, o.ID)
		if e != nil {
			return nil, e
		}
		if !ok || !ses.IsActive || ses.Status == "starting" || ses.Status == "closing" {
			return []SessionMessage{}, nil
		}
		mail, e := s.SessionInbox(ctx, recipient, false)
		if e != nil {
			return nil, e
		}
		out := []SessionMessage{}
		for _, m := range mail {
			if m.State == "queued" && (m.Sender != 0 || !o.SchedulePaused || strings.HasPrefix(m.Body, "Wake reason: run now")) {
				out = append(out, m)
			}
		}
		return out, nil
	}

	var eligible bool
	err := s.db.QueryRowContext(ctx, `select s.is_active=1 and s.status not in ('starting','closing') and t.focus_paused=0 and t.archived_at is null and b.archived_at is null from sessions s join tickets t on t.id=s.ticket_id join boards b on b.id=t.board_id where s.id=?`, recipient).Scan(&eligible)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return []SessionMessage{}, nil
	}
	return s.SessionInbox(ctx, recipient, false)
}

// MarkMessageNotified records native acceptance, not model processing. Unknown
// outcomes stay queued; native adapters must deduplicate by message ID.
func (s *Store) MarkMessageNotified(ctx context.Context, recipient, id int64) error {
	return mailbox.Notified(ctx, s.db, recipient, id, mailbox.Guard{SQL: `exists
(select 1 from sessions s left join tickets t on t.id=s.ticket_id left join boards b on b.id=t.board_id left join operators o on o.id=s.operator_id
where ((s.id=session_messages.recipient and s.operator_id is null) or (-s.operator_id=session_messages.recipient and s.id=(select max(id) from sessions where operator_id=s.operator_id))) and s.is_active=1 and s.status not in ('starting','closing') and ((o.id is not null and o.deleted_at is null) or (t.focus_paused=0 and t.archived_at is null and b.archived_at is null)))`})
}
func (s *Store) AcknowledgeSessionMessage(ctx context.Context, recipient, id int64) error {
	return mailbox.Acknowledge(ctx, s.db, recipient, id)
}

type MessageRecipient struct {
	Address   string `json:"address"`
	SessionID int64  `json:"session_id"`
	TicketID  int64  `json:"ticket_id"`
	Board     string `json:"board"`
	DisplayID string `json:"display_id"`
	Harness   string `json:"harness"`
	Active    bool   `json:"active"`
	Paused    bool   `json:"paused"`
	Archived  bool   `json:"archived"`
	Status    string `json:"status"`
	Resumable bool   `json:"has_session_ref"`
	Delivery  string `json:"delivery"`
}

const messageRecipientSelect = `select * from (select s.id,s.ticket_id,b.name as board,t.display_id,s.harness,s.is_active,t.focus_paused,t.archived_at is not null or b.archived_at is not null,s.status,trim(coalesce(s.harness_session_ref,''))<>'' from sessions s join tickets t on t.id=s.ticket_id join boards b on b.id=t.board_id union all select -o.id,0,'',o.name,o.harness,coalesce(s.is_active,0),0,o.deleted_at is not null,coalesce(s.status,'not_started'),trim(coalesce(s.harness_session_ref,''))<>'' from operators o left join sessions s on s.id=(select max(id) from sessions where operator_id=o.id)) s`

func scanMessageRecipient(row messageScanner) (r MessageRecipient, err error) {
	err = row.Scan(&r.SessionID, &r.TicketID, &r.Board, &r.DisplayID, &r.Harness, &r.Active, &r.Paused, &r.Archived, &r.Status, &r.Resumable)
	r.Address = fmt.Sprintf("%d", r.SessionID)
	if r.SessionID < 0 {
		r.Address = fmt.Sprintf("operator:%d", -r.SessionID)
	}
	r.Delivery = "unsupported_harness"
	switch r.Harness {
	case "pi":
		r.Delivery = "pi_extension"
	case "codex":
		r.Delivery = "codex_queue"
	case "claude":
		r.Delivery = "claude_socket"
	case "copilot":
		r.Delivery = "copilot_terminal_best_effort"
	}
	if r.Archived {
		r.Delivery = "archived"
		if r.SessionID < 0 {
			r.Delivery = "retired"
		}
	} else if r.Paused {
		r.Delivery = "paused"
	} else if !r.Active {
		if r.Resumable {
			r.Delivery = "closed_explicit_resume_required"
		} else {
			r.Delivery = "closed_unresumable"
		}
	} else if r.Status == "starting" || r.Status == "closing" {
		r.Delivery = r.Status
	}
	return
}

func (s *Store) MessageRecipient(ctx context.Context, id int64) (MessageRecipient, error) {
	return scanMessageRecipient(s.db.QueryRowContext(ctx, messageRecipientSelect+` where s.id=?`, id))
}

// Discover includes historical attempts; addresses never silently retarget a
// fresh or resumed attempt. A stored ref alone is not proof of resumability.
func (s *Store) MessageRecipients(ctx context.Context) ([]MessageRecipient, error) {
	rows, err := s.db.QueryContext(ctx, messageRecipientSelect+` order by (s.id<0) desc,s.id desc limit 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MessageRecipient{}
	for rows.Next() {
		r, err := scanMessageRecipient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
