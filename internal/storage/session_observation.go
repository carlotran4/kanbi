package storage

import (
	"context"
	"errors"
	"time"

	"github.com/carlotran4/kanbi/internal/kanban"
)

// Polling is optimistic: a close, manual mark, rename, ref capture, or launch
// can complete while a slow multiplexer read is pending. Only apply a result
// to the exact unchanged active attempt that was observed. A lost comparison
// is benign; the next poll reads fresh state. Heartbeat/output semantics match
// UpdateSessionRuntime, which remains the explicit lifecycle command seam.
func (s *Store) UpdateObservedSessionRuntime(ctx context.Context, observed Session, status, source, reason, excerpt string, outputChanged bool) error {
	if status == "" {
		return errors.New("runtime status is required")
	}
	now := time.Now().UTC()
	var lastOutput, lastStateChange any
	if outputChanged {
		lastOutput = now
	}
	if observed.Status != status {
		lastStateChange = now
	}
	_, err := s.db.ExecContext(ctx, `update sessions set
status=?,last_seen_tmux_at=?,last_output_at=coalesce(?,last_output_at),
last_state_change_at=coalesce(?,last_state_change_at),last_detected_state=?,
last_attention_reason=?,last_detection_source=?,
last_observed_excerpt=coalesce(?,last_observed_excerpt),updated_at=?
where id=? and is_active=1 and cast(updated_at as text)=? and status not in (?,?)`,
		status, now, lastOutput, lastStateChange, status, nullableString(reason), nullableString(source), nullableString(excerpt), now,
		observed.ID, observed.observationVersion, kanban.StateStarting, kanban.StateClosing)
	return err
}

func (s *Store) MarkObservedSessionMissing(ctx context.Context, observed Session) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `update sessions set
status=case when trim(coalesce(harness_session_ref,''))<>'' then ? else ? end,
is_active=0,closed_at=?,last_state_change_at=?,
last_detected_state=case when trim(coalesce(harness_session_ref,''))<>'' then ? else ? end,
last_attention_reason='terminal container missing',
last_detection_source=coalesce(nullif(multiplexer,''),'tmux'),updated_at=?
where id=? and is_active=1 and cast(updated_at as text)=? and status not in (?,?)`,
		kanban.StateExited, kanban.StateRepairNeeded, now, now, kanban.StateExited, kanban.StateRepairNeeded, now,
		observed.ID, observed.observationVersion, kanban.StateStarting, kanban.StateClosing)
	return err
}

func (s *Store) RecordObservedSessionFailure(ctx context.Context, observed Session, source, reason string) error {
	_, err := s.db.ExecContext(ctx, `update sessions set last_attention_reason=?,last_detection_source=?,updated_at=?
where id=? and is_active=1 and cast(updated_at as text)=? and status not in (?,?)`, nullableString(reason), nullableString(source), time.Now().UTC(),
		observed.ID, observed.observationVersion, kanban.StateStarting, kanban.StateClosing)
	return err
}
