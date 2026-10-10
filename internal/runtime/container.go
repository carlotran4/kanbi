package runtime

import (
	"database/sql"
	"strings"

	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
)

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

	}
	if ref.ID != "" {
		session.MuxContainerID.Valid = true
		session.MuxContainerID.String = ref.ID

	}
	if ref.Name != "" {
		session.MuxContainerName.Valid = true
		session.MuxContainerName.String = ref.Name

	}
	if ref.Metadata != "" {
		session.MuxMetadata.Valid = true
		session.MuxMetadata.String = ref.Metadata
	}
}

// ContainerRefFromIntegrationRun decodes the durable generic multiplexer
// reference owned by a repository integration run. Blank legacy kinds predate
// generic runtime columns and retain tmux compatibility.
func ContainerRefFromIntegrationRun(run storage.IntegrationRun) multiplexer.ContainerRef {
	ref := multiplexer.ContainerRef{Kind: multiplexer.KindTmux}
	if run.Multiplexer.Valid && strings.TrimSpace(run.Multiplexer.String) != "" {
		ref.Kind = multiplexer.Kind(run.Multiplexer.String)
	}
	if run.MuxNamespace.Valid {
		ref.Namespace = run.MuxNamespace.String
	}
	if run.MuxContainerID.Valid {
		ref.ID = run.MuxContainerID.String
	}
	if run.MuxContainerName.Valid {
		ref.Name = run.MuxContainerName.String
	}
	if run.MuxMetadata.Valid {
		ref.Metadata = run.MuxMetadata.String
	}
	return ref
}

// ApplyContainerRefToIntegrationRun encodes all generic multiplexer fields
// without reconstructing integration-run lifecycle data.
func ApplyContainerRefToIntegrationRun(run *storage.IntegrationRun, ref multiplexer.ContainerRef) {
	run.Multiplexer = sql.NullString{String: string(ref.Kind), Valid: ref.Kind != ""}
	run.MuxNamespace = sql.NullString{String: ref.Namespace, Valid: ref.Namespace != ""}
	run.MuxContainerID = sql.NullString{String: ref.ID, Valid: ref.ID != ""}
	run.MuxContainerName = sql.NullString{String: ref.Name, Valid: ref.Name != ""}
	run.MuxMetadata = sql.NullString{String: ref.Metadata, Valid: ref.Metadata != ""}
}
