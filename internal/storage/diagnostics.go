package storage

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

const maxRuntimeDiagnostics = 200

var (
	redactBearerRE   = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)(\S+)`)
	redactBasicRE    = regexp.MustCompile(`(?i)(authorization:\s*basic\s+)(\S+)`)
	redactHeaderRE   = regexp.MustCompile(`(?i)((?:x-)?api[-_]?key|x-access-token)\s*[:=]\s*(\S+)`)
	redactTokenEqRE  = regexp.MustCompile(`(?i)((?:api[_-]?token|access[_-]?token|refresh[_-]?token|bearer[_-]?token|github[_-]?token|jira[_-]?api[_-]?token|token|password|secret|client[_-]?secret)=)([^\s&;,]+)`)
	redactGHTokenRE  = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)
	redactGLTokenRE  = regexp.MustCompile(`\bglpat-[A-Za-z0-9\-_]{20,}\b`)
	redactSlackTokRE = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	redactJWTRE      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	redactOpenAIRE   = regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)
	redactSessionRef = regexp.MustCompile(`(?i)((?:session[_-]?ref|harness[_-]?session[_-]?ref|session[_-]?id)\s*[:=]\s*)([^\s,;"']+)`)
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
	out = redactHeaderRE.ReplaceAllString(out, `${1}=[REDACTED]`)
	out = redactTokenEqRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactSessionRef.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactGHTokenRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactGLTokenRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactSlackTokRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactJWTRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactOpenAIRE.ReplaceAllString(out, `[REDACTED]`)
	return out
}

// SchemaMigration is one applied version from the schema_migrations ledger.
type SchemaMigration struct {
	Version   int
	Name      string
	AppliedAt time.Time
}

// ListSchemaMigrations returns applied migrations without mutating the database.
// Missing ledger tables yield an empty list and a non-nil error only for query failures.
func (s *Store) ListSchemaMigrations(ctx context.Context) ([]SchemaMigration, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store closed")
	}
	rows, err := s.db.QueryContext(ctx, `select version, name, applied_at from schema_migrations where version > 0 order by version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SchemaMigration
	for rows.Next() {
		var m SchemaMigration
		if err := rows.Scan(&m.Version, &m.Name, &m.AppliedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
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
