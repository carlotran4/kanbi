package app

import (
	"context"
	"github.com/carlotran4/kanbi/internal/storage"
	"time"
)

func (s *Service) TicketWakeups(ctx context.Context, id int64) ([]storage.TicketWakeup, error) {
	return s.Store.TicketWakeups(ctx, id)
}
func (s *Service) ScheduleTicketPrompt(ctx context.Context, id int64, prompt string, due time.Time) error {
	return s.Store.ScheduleTicketPrompt(ctx, id, prompt, due)
}
func (s *Service) CancelTicketWake(ctx context.Context, ticketID, id int64) error {
	return s.Store.CancelTicketWake(ctx, ticketID, id)
}
