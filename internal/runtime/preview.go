package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/carlotran4/kanbi/internal/multiplexer"
	herdrmux "github.com/carlotran4/kanbi/internal/multiplexer/herdr"
	"github.com/carlotran4/kanbi/internal/storage"
)

// PreviewTicket reads only an existing attempt; it never reconciles or writes state.
func (m *Manager) PreviewTicket(ctx context.Context, ticket storage.Ticket) (multiplexer.TerminalPreview, error) {
	ctx, unlock, err := m.lockRuntime(ctx)
	if err != nil {
		return multiplexer.TerminalPreview{}, err
	}
	defer unlock()
	if err := m.runtimeReady(ctx); err != nil {
		return multiplexer.TerminalPreview{}, err
	}

	if m.Store == nil {
		return multiplexer.TerminalPreview{}, fmt.Errorf("terminal preview storage unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	fresh, err := m.Store.TicketByID(ctx, ticket.ID)
	if err != nil {
		return multiplexer.TerminalPreview{}, err
	}
	if !fresh.SessionActive || fresh.SessionID != ticket.SessionID || ContainerRefFromTicket(fresh) != ContainerRefFromTicket(ticket) {
		return multiplexer.TerminalPreview{}, fmt.Errorf("selected attempt is no longer active")
	}
	ref := ContainerRefFromTicket(fresh)
	if !supportedKind(ref.Kind) {
		return multiplexer.TerminalPreview{}, fmt.Errorf("screen preview unavailable for historical %s session", ref.Kind)
	}
	adapter, _ := m.multiplexerAdapter(ref.Kind)
	if a, ok := adapter.(*herdrmux.Adapter); ok {
		a.Runner = herdrmux.ExecRunner{Session: a.Config.Session, Preview: true}
	}
	valid, err := adapter.Validate(ctx, ref)
	if err != nil {
		return multiplexer.TerminalPreview{}, err
	}
	if !valid {
		return multiplexer.TerminalPreview{}, fmt.Errorf("terminal container unavailable")
	}
	text := ""
	screen, ok := adapter.(multiplexer.ScreenReader)
	if ok {
		text, err = screen.ReadScreen(ctx, ref)
	} else {
		err = fmt.Errorf("screen capture unsupported")
	}
	source, warning := "visible screen · ANSI", ""
	if err != nil {
		warning = "Screen unavailable; showing transcript fallback"
		source = "transcript fallback"
		text, err = adapter.Read(ctx, ref, multiplexer.ReadOptions{Lines: 160})
	}
	if err != nil {
		return multiplexer.TerminalPreview{}, err
	}
	current, err := m.Store.TicketByID(ctx, ticket.ID)
	if err != nil {
		return multiplexer.TerminalPreview{}, err
	}
	if !current.SessionActive || current.SessionID != fresh.SessionID || ContainerRefFromTicket(current) != ref {
		return multiplexer.TerminalPreview{}, fmt.Errorf("selected attempt changed during capture")
	}
	if len(text) > 256*1024 {
		text = text[:256*1024]
	}
	return multiplexer.TerminalPreview{Text: text, Source: source, Warning: warning, CapturedAt: time.Now()}, nil
}
