package app

import (
	"context"
	"fmt"

	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
)

type TerminalPreviewer interface {
	PreviewTicket(context.Context, storage.Ticket) (multiplexer.TerminalPreview, error)
}

func (s *Service) PreviewTicket(ctx context.Context, ticket storage.Ticket) (multiplexer.TerminalPreview, error) {
	reader, ok := s.Manager.(TerminalPreviewer)
	if !ok {
		return multiplexer.TerminalPreview{}, fmt.Errorf("terminal preview unavailable")
	}
	return reader.PreviewTicket(ctx, ticket)
}
