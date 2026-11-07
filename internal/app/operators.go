package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/carlotran4/kanbi/internal/storage"
)

type OperatorManager interface {
	SaveOperator(context.Context, storage.Operator, string) (storage.Operator, error)
	ReadOperatorInstructions(int64) (string, error)
	PauseOperator(context.Context, int64, bool) error
	OpenOperator(context.Context, int64, bool) error
	RunOperator(context.Context, int64, string) error
	StopOperator(context.Context, int64) error
}

func (s *Service) ListOperators(ctx context.Context) ([]storage.Operator, error) {
	ops, e := s.Store.Operators(ctx)
	if e != nil {
		return nil, e
	}
	for i := range ops {
		t, e := s.Store.OperatorTicket(ctx, ops[i].ID)
		if e != nil {
			return nil, e
		}
		ops[i].State = t.Runtime
		switch t.Runtime {
		case "needs_permission", "waiting_for_user", "error", "repair_needed":
			ops[i].Attention = t.LastAttentionReason.String
		}
		wakes, e := s.Store.OperatorWakeups(ctx, ops[i].ID)
		if e != nil {
			return nil, e
		}
		for _, w := range wakes {
			if w.State == "pending" && (!ops[i].SchedulePaused || w.Reason == "run now") && (ops[i].NextWake == nil || w.DueAt.Before(*ops[i].NextWake)) {
				due := w.DueAt
				ops[i].NextWake = &due
			}
		}
		mail, e := s.Store.SessionInbox(ctx, -ops[i].ID, false)
		if e != nil {
			return nil, e
		}
		for _, msg := range mail {
			if msg.State == "queued" {
				ops[i].Attention = fmt.Sprintf("Queued work: message %d", msg.ID)
			}
			if msg.NotificationError != "" {
				ops[i].Attention = fmt.Sprintf("message %d: %s", msg.ID, msg.NotificationError)
				break
			}
		}
		if ops[i].LastError != "" {
			ops[i].State = "failed"
			ops[i].Attention = ops[i].LastError
		}
	}
	return ops, nil
}
func (s *Service) ReadOperatorInstructions(id int64) (string, error) {
	m, ok := s.Manager.(OperatorManager)
	if !ok {
		return "", errors.New("operator runtime unavailable")
	}
	return m.ReadOperatorInstructions(id)
}
func (s *Service) SaveOperator(ctx context.Context, o storage.Operator, instructions string) (storage.Operator, error) {
	m, ok := s.Manager.(OperatorManager)
	if !ok {
		return o, errors.New("operator runtime unavailable")
	}
	return m.SaveOperator(ctx, o, instructions)
}
func (s *Service) OperatorAction(ctx context.Context, id int64, action string) error {
	m, ok := s.Manager.(OperatorManager)
	if !ok {
		return errors.New("operator runtime unavailable")
	}
	switch action {
	case "open":
		return m.OpenOperator(ctx, id, false)
	case "run":
		if e := s.Store.SetOperatorError(ctx, id, ""); e != nil {
			return e
		}
		return m.RunOperator(ctx, id, "")
	case "pause", "resume":
		return m.PauseOperator(ctx, id, action == "pause")
	case "stop":
		return m.StopOperator(ctx, id)
	case "fresh":
		if e := m.OpenOperator(ctx, id, true); e != nil {
			return e
		}
		return s.Store.SetOperatorError(ctx, id, "")
	case "close":
		t, e := s.Store.OperatorTicket(ctx, id)
		if e != nil {
			return e
		}
		return s.Manager.CloseSession(ctx, t)
	case "delete":
		return s.Store.DeleteOperator(ctx, id)
	default:
		return errors.New("unknown Operator action")
	}
}
