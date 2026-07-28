package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) focusStatusTx(ctx context.Context, tx *sql.Tx, policy FocusPolicy) (FocusStatus, error) {
	status := FocusStatus{Enabled: policy.Enabled, Limit: policy.Limit, WorkflowKeys: append([]string(nil), policy.WorkflowKeys...)}
	if !policy.Enabled || len(policy.WorkflowKeys) == 0 {
		return status, nil
	}
	query := `select count(*) from tickets t join columns c on c.id=t.column_id join boards b on b.id=t.board_id where b.archived_at is null and t.archived_at is null and coalesce(t.focus_paused,0)=0 and c.workflow_key in (` + placeholders(len(policy.WorkflowKeys)) + `)`
	args := make([]any, len(policy.WorkflowKeys))
	for i, key := range policy.WorkflowKeys {
		args[i] = key
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&status.Used); err != nil {
		return FocusStatus{}, err
	}
	status.OverCapacity = status.Used > status.Limit
	return status, nil
}

// FocusStatus returns globally focused work across all unarchived boards.
func (s *Store) FocusStatus(ctx context.Context) (FocusStatus, error) {
	policy := s.FocusPolicy()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FocusStatus{}, err
	}
	defer tx.Rollback()
	status, err := s.focusStatusTx(ctx, tx, policy)
	if err != nil {
		return FocusStatus{}, err
	}
	return status, tx.Commit()
}

// FocusedTickets lists unpaused commitments eligible for replacement. It is
// intentionally global, including tickets owned by boards other than the view.
func (s *Store) FocusedTickets(ctx context.Context) ([]Ticket, error) {
	policy := s.FocusPolicy()
	if !policy.Enabled || len(policy.WorkflowKeys) == 0 {
		return nil, nil
	}
	args := make([]any, len(policy.WorkflowKeys))
	for i, key := range policy.WorkflowKeys {
		args[i] = key
	}
	return s.queryProjectedTickets(ctx, `join columns c on c.id=t.column_id join boards b on b.id=t.board_id where b.archived_at is null and t.archived_at is null and coalesce(t.focus_paused,0)=0 and c.workflow_key in (`+placeholders(len(args))+`) order by t.updated_at, t.id`, args...)
}

func focusColumnTx(ctx context.Context, tx *sql.Tx, columnID int64, policy FocusPolicy) (bool, error) {
	if !policy.Enabled || len(policy.WorkflowKeys) == 0 {
		return false, nil
	}
	var key string
	if err := tx.QueryRowContext(ctx, `select workflow_key from columns where id=?`, columnID).Scan(&key); err != nil {
		return false, err
	}
	for _, focusKey := range policy.WorkflowKeys {
		if key == focusKey {
			return true, nil
		}
	}
	return false, nil
}

// lockFocus serializes admissions across processes before reading the global
// count. SQLite's single writer is deliberately used here instead of a process
// local mutex.
func lockFocus(ctx context.Context, tx *sql.Tx, boardID int64) error {
	_, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=?`, boardID)
	return err
}

func requireFocusCapacity(ctx context.Context, tx *sql.Tx, policy FocusPolicy) error {
	status, err := (&Store{}).focusStatusTx(ctx, tx, policy)
	if err != nil {
		return err
	}
	if status.Enabled && status.Used >= status.Limit {
		return ErrFocusCapacity
	}
	return nil
}

// ValidatePauseCheckpoint validates the handoff before any runtime side effect.
func ValidatePauseCheckpoint(why, completed, next string) error {
	fields := []struct{ label, value string }{
		{"why it was paused", why},
		{"what has already been completed", completed},
		{"exact next action", next},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.label)
		}
	}
	return nil
}

func insertPauseCheckpoint(ctx context.Context, tx *sql.Tx, ticketID int64, why, completed, next string, now time.Time) error {
	var columnID int64
	var archived sql.NullTime
	var paused, activeSessions int
	if err := tx.QueryRowContext(ctx, `select t.column_id,t.archived_at,coalesce(t.focus_paused,0),count(s.id)
from tickets t left join sessions s on s.ticket_id=t.id and s.is_active=1
where t.id=? group by t.id`, ticketID).Scan(&columnID, &archived, &paused, &activeSessions); err != nil {
		return err
	}
	if archived.Valid {
		return ErrTicketArchived
	}
	if paused != 0 {
		return errors.New("ticket is already paused")
	}
	if activeSessions != 0 {
		return errors.New("ticket session became active before pause; close it and retry")
	}
	if _, err := tx.ExecContext(ctx, `update tickets set focus_paused=1,updated_at=? where id=?`, now, ticketID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `insert into pause_checkpoints(ticket_id,why,completed,next_action,paused_at) values(?,?,?,?,?)`, ticketID, strings.TrimSpace(why), strings.TrimSpace(completed), strings.TrimSpace(next), now)
	return err
}

// PauseTicket atomically records an append-only handoff checkpoint and marks a
// focused ticket paused. Runtime closure is intentionally performed by app
// before this method is called.
func (s *Store) PauseTicket(ctx context.Context, ticketID int64, why, completed, next string) error {
	if err := ValidatePauseCheckpoint(why, completed, next); err != nil {
		return err
	}
	policy := s.FocusPolicy()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var columnID int64
	var archived sql.NullTime
	if err := tx.QueryRowContext(ctx, `select column_id, archived_at from tickets where id=?`, ticketID).Scan(&columnID, &archived); err != nil {
		return err
	}
	if archived.Valid {
		return ErrTicketArchived
	}
	isFocus, err := focusColumnTx(ctx, tx, columnID, policy)
	if err != nil {
		return err
	}
	if !isFocus {
		return errors.New("ticket is not in a configured focus column")
	}
	if err := insertPauseCheckpoint(ctx, tx, ticketID, why, completed, next, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// ResumeTicket claims a focus slot and closes the latest open checkpoint in the
// same SQLite transaction. Callers then use normal session lifecycle handling.
func (s *Store) ResumeTicket(ctx context.Context, ticketID int64) error {
	policy := s.FocusPolicy()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var boardID, columnID int64
	var paused int
	if err := tx.QueryRowContext(ctx, `select board_id,column_id,coalesce(focus_paused,0) from tickets where id=? and archived_at is null`, ticketID).Scan(&boardID, &columnID, &paused); err != nil {
		return err
	}
	if paused == 0 {
		return errors.New("ticket is not paused")
	}
	isFocus, err := focusColumnTx(ctx, tx, columnID, policy)
	if err != nil {
		return err
	}
	if !isFocus {
		return errors.New("ticket is not in a configured focus column")
	}
	if err := lockFocus(ctx, tx, boardID); err != nil {
		return err
	}
	if err := requireFocusCapacity(ctx, tx, policy); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `update tickets set focus_paused=0,updated_at=? where id=?`, now, ticketID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update pause_checkpoints set resumed_at=? where ticket_id=? and resumed_at is null`, now, ticketID); err != nil {
		return err
	}
	return tx.Commit()
}

// PauseAndMove atomically pauses replacement and admits target into a focus
// column. The replacement's runtime must already have closed successfully.
func (s *Store) PauseAndMove(ctx context.Context, replacementID, targetID, destinationID int64, why, completed, next string) error {
	if err := ValidatePauseCheckpoint(why, completed, next); err != nil {
		return err
	}
	policy := s.FocusPolicy()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var replacementBoardID, replacementColumnID int64
	if err := tx.QueryRowContext(ctx, `select board_id,column_id from tickets where id=?`, replacementID).Scan(&replacementBoardID, &replacementColumnID); err != nil {
		return err
	}
	if err := lockFocus(ctx, tx, replacementBoardID); err != nil {
		return err
	}
	isFocus, err := focusColumnTx(ctx, tx, replacementColumnID, policy)
	if err != nil {
		return err
	}
	if !isFocus {
		return errors.New("replacement ticket is no longer focused")
	}
	if err := insertPauseCheckpoint(ctx, tx, replacementID, why, completed, next, time.Now().UTC()); err != nil {
		return err
	}
	if err := s.pauseStateForColumnMove(ctx, tx, targetID, destinationID, policy); err != nil {
		return err
	}
	if err := visibleTicketOrder.moveToFront(ctx, tx, targetID, destinationID); err != nil {
		return err
	}
	return tx.Commit()
}

// PauseAndResume atomically pauses replacement and claims its released slot for
// target. Runtime resume happens after this durable transaction commits.
func (s *Store) PauseAndResume(ctx context.Context, replacementID, targetID int64, why, completed, next string) error {
	if err := ValidatePauseCheckpoint(why, completed, next); err != nil {
		return err
	}
	policy := s.FocusPolicy()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var replacementBoardID, replacementColumnID int64
	if err := tx.QueryRowContext(ctx, `select board_id,column_id from tickets where id=?`, replacementID).Scan(&replacementBoardID, &replacementColumnID); err != nil {
		return err
	}
	if err := lockFocus(ctx, tx, replacementBoardID); err != nil {
		return err
	}
	isFocus, err := focusColumnTx(ctx, tx, replacementColumnID, policy)
	if err != nil {
		return err
	}
	if !isFocus {
		return errors.New("replacement ticket is no longer focused")
	}
	if err := insertPauseCheckpoint(ctx, tx, replacementID, why, completed, next, time.Now().UTC()); err != nil {
		return err
	}
	var targetColumnID int64
	var targetPaused int
	if err := tx.QueryRowContext(ctx, `select column_id,coalesce(focus_paused,0) from tickets where id=? and archived_at is null`, targetID).Scan(&targetColumnID, &targetPaused); err != nil {
		return err
	}
	if targetPaused == 0 {
		return errors.New("ticket is not paused")
	}
	targetFocus, err := focusColumnTx(ctx, tx, targetColumnID, policy)
	if err != nil {
		return err
	}
	if !targetFocus {
		return errors.New("ticket is not in a configured focus column")
	}
	if err := requireFocusCapacity(ctx, tx, policy); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `update tickets set focus_paused=0,updated_at=? where id=?`, now, targetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update pause_checkpoints set resumed_at=? where ticket_id=? and resumed_at is null`, now, targetID); err != nil {
		return err
	}
	return tx.Commit()
}

func clearPausedForRemoteDestination(ctx context.Context, tx *sql.Tx, ticketID, destinationID int64, policy FocusPolicy) error {
	if !policy.Enabled {
		return nil
	}
	isFocus, err := focusColumnTx(ctx, tx, destinationID, policy)
	if err != nil || isFocus {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `update tickets set focus_paused=0 where id=? and focus_paused<>0`, ticketID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update pause_checkpoints set resumed_at=? where ticket_id=? and resumed_at is null`, now, ticketID)
	return err
}

func (s *Store) pauseStateForColumnMove(ctx context.Context, tx *sql.Tx, ticketID, destinationID int64, policy FocusPolicy) error {
	var boardID, sourceID int64
	var paused int
	if err := tx.QueryRowContext(ctx, `select board_id,column_id,coalesce(focus_paused,0) from tickets where id=?`, ticketID).Scan(&boardID, &sourceID, &paused); err != nil {
		return err
	}
	sourceFocus, err := focusColumnTx(ctx, tx, sourceID, policy)
	if err != nil {
		return err
	}
	destFocus, err := focusColumnTx(ctx, tx, destinationID, policy)
	if err != nil {
		return err
	}
	if !destFocus && paused != 0 {
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `update tickets set focus_paused=0,updated_at=? where id=?`, now, ticketID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `update pause_checkpoints set resumed_at=? where ticket_id=? and resumed_at is null`, now, ticketID); err != nil {
			return err
		}
	}
	if destFocus && !sourceFocus {
		if err := lockFocus(ctx, tx, boardID); err != nil {
			return err
		}
		if err := requireFocusCapacity(ctx, tx, policy); err != nil {
			return err
		}
		// A stale paused bit can exist after focus workflow keys change. Entry
		// from a non-focus column is always an admission as focused work; it
		// must never arrive silently paused.
		if paused != 0 {
			now := time.Now().UTC()
			if _, err := tx.ExecContext(ctx, `update tickets set focus_paused=0,updated_at=? where id=?`, now, ticketID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `update pause_checkpoints set resumed_at=? where ticket_id=? and resumed_at is null`, now, ticketID); err != nil {
				return err
			}
		}
	}
	return nil
}
