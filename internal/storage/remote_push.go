package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

// MarkTicketRemotePushPending records a durable create attempt before a
// non-idempotent provider issue create. Callers must embed the token in the
// remote body and recover-by-token before creating again.
func (s *Store) MarkTicketRemotePushPending(ctx context.Context, ticketID int64, token string) error {
	token = strings.TrimSpace(token)
	if ticketID <= 0 || token == "" {
		return errors.New("ticket id and remote push token are required")
	}
	now := time.Now().UTC()
	// Do not touch tickets.updated_at: push bookkeeping is local runtime state and
	// must not look like a user edit that wins newest-updated conflict resolution.
	res, err := s.db.ExecContext(ctx, `update tickets set remote_push_state=?, remote_push_token=?, remote_push_attempted_at=? where id=?`,
		RemotePushStatePending, token, now, ticketID)
	return requireAffected(res, err)
}

// ClearTicketRemotePush clears pending/failed push metadata after a successful
// remote link (external_id present).
func (s *Store) ClearTicketRemotePush(ctx context.Context, ticketID int64) error {
	if ticketID <= 0 {
		return errors.New("ticket id is required")
	}
	res, err := s.db.ExecContext(ctx, `update tickets set remote_push_state=null, remote_push_token=null, remote_push_attempted_at=null where id=?`, ticketID)
	return requireAffected(res, err)
}

// MarkTicketRemotePushFailed marks a pending create as failed/degraded so the
// operator can recover without re-issuing CreateIssue automatically.
func (s *Store) MarkTicketRemotePushFailed(ctx context.Context, ticketID int64, reason string) error {
	if ticketID <= 0 {
		return errors.New("ticket id is required")
	}
	now := time.Now().UTC()
	// Keep the existing token so find-or-link recovery remains possible.
	res, err := s.db.ExecContext(ctx, `update tickets set remote_push_state=?, remote_push_attempted_at=? where id=?`,
		RemotePushStateFailed, now, ticketID)
	if err := requireAffected(res, err); err != nil {
		return err
	}
	_ = reason // reason is persisted by sync/runtime diagnostics, not on the ticket row.
	return nil
}
