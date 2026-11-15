package runtime

import (
	"context"
	"time"
)

// Called by the existing joined scheduler under the cross-process runtime lock.
func (m *Manager) refreshTicketWakeups(ctx context.Context) error {
	wakes, err := m.Store.TicketWakeups(ctx, 0)
	if err != nil {
		return err
	}
	for _, w := range wakes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if w.State == "activating" {
			if err = m.Store.FailTicketWake(ctx, w.ID, "interrupted activation; inspect the ticket session and schedule again"); err != nil {
				return err
			}
			continue
		}
		claimed, e := m.Store.ClaimTicketWake(ctx, w.ID)
		if e != nil {
			return e
		}
		if !claimed {
			continue
		}
		t, e := m.Store.TicketByID(ctx, w.TicketID)
		if e == nil {
			e = m.OpenTicket(ctx, t, !t.SessionID.Valid)
		}
		if e == nil {
			e = m.Store.EnqueueTicketWake(ctx, w.ID)
		}
		if e != nil {
			save, cancel := context.WithTimeout(context.Background(), time.Second)
			err = m.Store.FailTicketWake(save, w.ID, e.Error())
			cancel()
			if err != nil {
				return err
			}
		}
	}
	return nil
}
