package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
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

type SessionMessage struct {
	WakePrompt              string     `json:"wake_prompt,omitempty"`
	NotificationStatus      string     `json:"notification_status"`
	NotificationTransport   string     `json:"notification_transport"`
	NotificationError       string     `json:"notification_error,omitempty"`
	NotificationAttemptedAt *time.Time `json:"notification_attempted_at"`
	ID                      int64      `json:"id"`
	Sender                  int64      `json:"sender"`
	Recipient               int64      `json:"recipient"`
	RequestKey              string     `json:"request_key"`
	Body                    string     `json:"body"`
	ReplyTo                 int64      `json:"reply_to,omitempty"`
	Depth                   int        `json:"depth"`
	State                   string     `json:"state"`
	CreatedAt               time.Time  `json:"created_at"`
	NotifiedAt              *time.Time `json:"notified_at"`
	AcknowledgedAt          *time.Time `json:"acknowledged_at"`
}

const messageSelect = `select id,sender,recipient,request_key,body,reply_to,depth,state,created_at,notified_at,acknowledged_at,notification_status,notification_transport,notification_error,notification_attempted_at from session_messages`

type messageScanner interface{ Scan(...any) error }

func scanMessage(row messageScanner) (m SessionMessage, err error) {
	err = row.Scan(&m.ID, &m.Sender, &m.Recipient, &m.RequestKey, &m.Body, &m.ReplyTo, &m.Depth, &m.State, &m.CreatedAt, &m.NotifiedAt, &m.AcknowledgedAt, &m.NotificationStatus, &m.NotificationTransport, &m.NotificationError, &m.NotificationAttemptedAt)
	return
}

func (s *Store) SessionMessage(ctx context.Context, id int64) (SessionMessage, error) {
	return scanMessage(s.db.QueryRowContext(ctx, messageSelect+` where id=?`, id))
}

// SendSessionMessage commits before any notification. A retry must reuse its
// sender-scoped key and identical content. Replies derive their destination from
// the parent, never from caller-supplied addressing.
func (s *Store) SendSessionMessage(ctx context.Context, sender, recipient int64, key, body string, replyTo int64) (SessionMessage, error) {
	if sender == 0 || strings.TrimSpace(key) == "" || len(key) > 128 || strings.TrimSpace(body) == "" || len(body) > 16384 || replyTo < 0 {
		return SessionMessage{}, errors.New("sender, request key (1–128 bytes), and body (1–16384 bytes) are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionMessage{}, err
	}
	defer tx.Rollback()
	// Reserve the write lock before reading, avoiding read-to-write lock upgrades.
	if _, err = tx.ExecContext(ctx, `update session_messages set state=state where id=-1`); err != nil {
		return SessionMessage{}, err
	}
	depth := 0
	if replyTo != 0 {
		parent, e := scanMessage(tx.QueryRowContext(ctx, messageSelect+` where id=?`, replyTo))
		if e != nil {
			return SessionMessage{}, e
		}
		if parent.Recipient != sender {
			return SessionMessage{}, errors.New("only the recipient may reply")
		}
		recipient = parent.Sender
		depth = parent.Depth + 1
		if depth > 8 {
			return SessionMessage{}, errors.New("reply limit reached (8 hops); start a human-reviewed thread")
		}
	}
	if recipient == 0 || sender == recipient {
		return SessionMessage{}, errors.New("a different recipient session is required")
	}
	existing, e := scanMessage(tx.QueryRowContext(ctx, messageSelect+` where sender=? and request_key=?`, sender, key))
	if e == nil {
		if existing.Recipient != recipient || existing.Body != body || existing.ReplyTo != replyTo {
			return SessionMessage{}, errors.New("request key already used with different content")
		}
		return existing, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return SessionMessage{}, e
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `select s.is_active from sessions s left join operators o on o.id=s.operator_id where ((s.id=? and ?>0) or (s.operator_id=-? and ?<0)) and (s.operator_id is null or o.deleted_at is null) order by s.id desc limit 1`, sender, sender, sender, sender).Scan(&active); err != nil {
		return SessionMessage{}, fmt.Errorf("sender session: %w", err)
	}
	if !active {
		return SessionMessage{}, errors.New("sender session is closed")
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `select id from sessions where id=? and ?>0 and operator_id is null union all select id from operators where id=-? and ?<0 and deleted_at is null limit 1`, recipient, recipient, recipient, recipient).Scan(&exists); err != nil {
		return SessionMessage{}, fmt.Errorf("recipient session: %w", err)
	}
	result, err := tx.ExecContext(ctx, `insert into session_messages(sender,recipient,request_key,body,reply_to,depth,created_at) values(?,?,?,?,?,?,?)`, sender, recipient, key, body, replyTo, depth, time.Now().UTC())
	if err != nil {
		return SessionMessage{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return SessionMessage{}, err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, messageSelect+` where id=?`, id))
	if err != nil {
		return SessionMessage{}, err
	}
	return m, tx.Commit()
}

// Inbox is non-consuming; notification is not acknowledgement.
func (s *Store) SessionInbox(ctx context.Context, recipient int64, includeAcknowledged bool) ([]SessionMessage, error) {
	query := messageSelect + ` where recipient=?`
	if !includeAcknowledged {
		query += ` and state<>'acknowledged'`
	}
	rows, err := s.db.QueryContext(ctx, query+` order by id limit 100`, recipient)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []SessionMessage{}
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			return nil, e
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
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
	res, err := s.db.ExecContext(ctx, `update session_messages set state=case when state='queued' then 'notified' else state end,notified_at=coalesce(notified_at,?),notification_status='notified',notification_error='' where id=? and recipient=? and exists
(select 1 from sessions s left join tickets t on t.id=s.ticket_id left join boards b on b.id=t.board_id left join operators o on o.id=s.operator_id
where ((s.id=session_messages.recipient and s.operator_id is null) or (-s.operator_id=session_messages.recipient and s.id=(select max(id) from sessions where operator_id=s.operator_id))) and s.is_active=1 and s.status not in ('starting','closing') and ((o.id is not null and o.deleted_at is null) or (t.focus_paused=0 and t.archived_at is null and b.archived_at is null)))`, time.Now().UTC(), id, recipient)
	return requireAffected(res, err)
}
func (s *Store) AcknowledgeSessionMessage(ctx context.Context, recipient, id int64) error {
	res, err := s.db.ExecContext(ctx, `update session_messages set state='acknowledged',acknowledged_at=coalesce(acknowledged_at,?),notification_status='notified',notification_error='' where id=? and recipient=?`, time.Now().UTC(), id, recipient)
	return requireAffected(res, err)
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
