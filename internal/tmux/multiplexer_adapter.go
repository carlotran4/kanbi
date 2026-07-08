package tmux

import (
	"context"
	"strings"
	"time"

	"kanbi/internal/kanban"
	"kanbi/internal/multiplexer"
	"kanbi/internal/storage"
)

// MultiplexerAdapter exposes the existing tmux Manager through the provider-neutral
// multiplexer.Interface without changing the Manager API used by current callers.
type MultiplexerAdapter struct {
	Manager *Manager
}

var _ multiplexer.Interface = MultiplexerAdapter{}

func NewMultiplexerAdapter(manager *Manager) MultiplexerAdapter {
	return MultiplexerAdapter{Manager: manager}
}

func (a MultiplexerAdapter) Kind() multiplexer.Kind { return multiplexer.KindTmux }

func (a MultiplexerAdapter) Ensure(ctx context.Context, namespace string) error {
	if namespace == "" || namespace == a.Manager.Config.TmuxSession {
		return a.Manager.EnsureSession(ctx)
	}
	if _, err := a.Manager.run(ctx, "has-session", "-t", namespace); err != nil {
		return err
	}
	return nil
}

func (a MultiplexerAdapter) Launch(ctx context.Context, spec multiplexer.LaunchSpec) (multiplexer.ContainerRef, error) {
	namespace := spec.Namespace
	if namespace == "" {
		namespace = a.Manager.Config.TmuxSession
	}
	name := spec.Name
	if name == "" {
		name = "kanbi"
	}
	if err := a.Ensure(ctx, namespace); err != nil {
		return multiplexer.ContainerRef{}, err
	}
	args := newWindowArgs(namespace, name, spec.CWD)
	if len(spec.Command) > 0 {
		args = append(args, ShellCommand(spec.Command))
	}
	out, err := a.Manager.run(ctx, args...)
	if err != nil {
		return multiplexer.ContainerRef{}, err
	}
	return multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: namespace, ID: strings.TrimSpace(out), Name: name, Metadata: spec.Metadata}, nil
}

func (a MultiplexerAdapter) Focus(ctx context.Context, ref multiplexer.ContainerRef) error {
	return a.Manager.switchWindow(ctx, adapterNamespace(a.Manager, ref), ref.Target())
}

func (a MultiplexerAdapter) Read(ctx context.Context, ref multiplexer.ContainerRef, opts multiplexer.ReadOptions) (string, error) {
	args := []string{"capture-pane", "-p"}
	if opts.Lines > 0 {
		args = append(args, "-S", "-"+itoa(opts.Lines))
	}
	args = append(args, "-t", targetRef(adapterNamespace(a.Manager, ref), ref.Target()))
	return a.Manager.run(ctx, args...)
}

func (a MultiplexerAdapter) SendKeys(ctx context.Context, ref multiplexer.ContainerRef, keys ...string) error {
	args := append([]string{"send-keys", "-t", targetRef(adapterNamespace(a.Manager, ref), ref.Target())}, keys...)
	_, err := a.Manager.run(ctx, args...)
	return err
}

func (a MultiplexerAdapter) Close(ctx context.Context, ref multiplexer.ContainerRef) error {
	_, err := a.Manager.run(ctx, "kill-window", "-t", targetRef(adapterNamespace(a.Manager, ref), ref.Target()))
	return err
}

func (a MultiplexerAdapter) Detect(ctx context.Context, ref multiplexer.ContainerRef) (multiplexer.Detection, error) {
	exists, err := a.Manager.windowRefExistsInSession(ctx, adapterNamespace(a.Manager, ref), ref.Target())
	if err != nil {
		return multiplexer.Detection{}, err
	}
	state := kanban.StateRunning
	reason := "tmux window exists"
	confidence := multiplexer.ConfidenceMedium
	if !exists {
		state = kanban.StateError
		reason = "tmux window missing"
		confidence = multiplexer.ConfidenceHigh
	}
	return multiplexer.Detection{State: state, Reason: reason, Source: multiplexer.DetectionSourceTmux, Confidence: confidence, ObservedAt: time.Now().UTC()}, nil
}

func ContainerRefFromSession(session storage.Session) multiplexer.ContainerRef {
	ref := multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: session.TmuxSessionName, Name: session.TmuxWindowName}
	if session.Multiplexer != "" {
		ref.Kind = multiplexer.Kind(session.Multiplexer)
	}
	if session.MuxNamespace.Valid {
		ref.Namespace = session.MuxNamespace.String
	}
	if session.TmuxWindowID.Valid {
		ref.ID = session.TmuxWindowID.String
	}
	if session.MuxContainerID.Valid {
		ref.ID = session.MuxContainerID.String
	}
	if session.MuxContainerName.Valid {
		ref.Name = session.MuxContainerName.String
	}
	if session.MuxMetadata.Valid {
		ref.Metadata = session.MuxMetadata.String
	}
	return ref
}

func ContainerRefFromTicket(ticket storage.Ticket) multiplexer.ContainerRef {
	ref := multiplexer.ContainerRef{Kind: multiplexer.KindTmux}
	if ticket.TmuxSessionName.Valid {
		ref.Namespace = ticket.TmuxSessionName.String
	}
	if ticket.WindowID.Valid {
		ref.ID = ticket.WindowID.String
	}
	if ticket.WindowName.Valid {
		ref.Name = ticket.WindowName.String
	}
	if ticket.Multiplexer.Valid && ticket.Multiplexer.String != "" {
		ref.Kind = multiplexer.Kind(ticket.Multiplexer.String)
	}
	if ticket.MuxNamespace.Valid {
		ref.Namespace = ticket.MuxNamespace.String
	}
	if ticket.MuxContainerID.Valid {
		ref.ID = ticket.MuxContainerID.String
	}
	if ticket.MuxContainerName.Valid {
		ref.Name = ticket.MuxContainerName.String
	}
	if ticket.MuxMetadata.Valid {
		ref.Metadata = ticket.MuxMetadata.String
	}
	return ref
}

func ApplyContainerRefToSession(session *storage.Session, ref multiplexer.ContainerRef) {
	if ref.Kind != "" {
		session.Multiplexer = string(ref.Kind)
	}
	if ref.Namespace != "" {
		session.MuxNamespace.Valid = true
		session.MuxNamespace.String = ref.Namespace
		if session.TmuxSessionName == "" && ref.Kind == multiplexer.KindTmux {
			session.TmuxSessionName = ref.Namespace
		}
	}
	if ref.ID != "" {
		session.MuxContainerID.Valid = true
		session.MuxContainerID.String = ref.ID
		if ref.Kind == multiplexer.KindTmux {
			session.TmuxWindowID.Valid = true
			session.TmuxWindowID.String = ref.ID
		}
	}
	if ref.Name != "" {
		session.MuxContainerName.Valid = true
		session.MuxContainerName.String = ref.Name
		if session.TmuxWindowName == "" && ref.Kind == multiplexer.KindTmux {
			session.TmuxWindowName = ref.Name
		}
	}
	if ref.Metadata != "" {
		session.MuxMetadata.Valid = true
		session.MuxMetadata.String = ref.Metadata
	}
}

func adapterNamespace(manager *Manager, ref multiplexer.ContainerRef) string {
	if ref.Namespace != "" {
		return ref.Namespace
	}
	return manager.Config.TmuxSession
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
