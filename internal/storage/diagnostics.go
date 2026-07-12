package storage

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"
)

const maxRuntimeDiagnostics = 200

var (
	redactBearerRE  = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)(\S+)`)
	redactBasicRE   = regexp.MustCompile(`(?i)(authorization:\s*basic\s+)(\S+)`)
	redactTokenEqRE = regexp.MustCompile(`(?i)((?:api[_-]?token|access[_-]?token|bearer[_-]?token|github[_-]?token|jira[_-]?api[_-]?token|token)=)([^\s&;,]+)`)
	redactGHTokenRE = regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}\b`)
	redactGHOToken  = regexp.MustCompile(`\bgho_[A-Za-z0-9]{20,}\b`)
	redactGHSToken  = regexp.MustCompile(`\bghs_[A-Za-z0-9]{20,}\b`)
	redactJWTRE     = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
)

// RedactSecretText removes credential-shaped substrings from diagnostic text.
// It never stores prompts, session refs, or terminal excerpts by design; callers
// must not put those values into the message/cause fields.
func RedactSecretText(s string) string {
	if s == "" {
		return s
	}
	out := s
	out = redactBearerRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactBasicRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactTokenEqRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactGHTokenRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactGHOToken.ReplaceAllString(out, `[REDACTED]`)
	out = redactGHSToken.ReplaceAllString(out, `[REDACTED]`)
	out = redactJWTRE.ReplaceAllString(out, `[REDACTED]`)
	return out
}

func (s *Store) InsertRuntimeDiagnostic(ctx context.Context, in RuntimeDiagnosticInput) error {
	if s == nil || s.db == nil {
		return nil
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = DiagnosticKindRuntime
	}
	op := strings.TrimSpace(in.Operation)
	if op == "" {
		op = "unknown"
	}
	message := RedactSecretText(strings.TrimSpace(in.Message))
	cause := RedactSecretText(strings.TrimSpace(in.Cause))
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var boardID, ticketID, sessionID any
	if in.BoardID > 0 {
		boardID = in.BoardID
	}
	if in.TicketID > 0 {
		ticketID = in.TicketID
	}
	if in.SessionID > 0 {
		sessionID = in.SessionID
	}
	if _, err := tx.ExecContext(ctx, `insert into runtime_diagnostics(created_at,kind,operation,board_id,ticket_id,session_id,attempt,message,cause) values(?,?,?,?,?,?,?,?,?)`,
		now, kind, op, boardID, ticketID, sessionID, in.Attempt, message, cause); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from runtime_diagnostics where id not in (
		select id from runtime_diagnostics order by id desc limit ?
	)`, maxRuntimeDiagnostics); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListRuntimeDiagnostics(ctx context.Context, limit int) ([]RuntimeDiagnostic, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `select id, created_at, kind, operation, board_id, ticket_id, session_id, attempt, message, cause
from runtime_diagnostics order by id desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuntimeDiagnostic
	for rows.Next() {
		var d RuntimeDiagnostic
		var boardID, ticketID, sessionID sql.NullInt64
		if err := rows.Scan(&d.ID, &d.CreatedAt, &d.Kind, &d.Operation, &boardID, &ticketID, &sessionID, &d.Attempt, &d.Message, &d.Cause); err != nil {
			return nil, err
		}
		d.BoardID = boardID
		d.TicketID = ticketID
		d.SessionID = sessionID
		out = append(out, d)
	}
	return out, rows.Err()
}
