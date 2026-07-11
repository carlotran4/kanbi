package session

import (
	"database/sql"

	"kanbi/internal/storage"
)

type Action string

const (
	ActionStart  Action = "start"
	ActionResume Action = "resume"
	ActionFocus  Action = "switch"
	ActionRepair Action = "repair"
)

type Request struct {
	Ticket         storage.Ticket
	SendPrompt     bool
	StartFresh     bool
	ContainerValid bool
	ContainerRef   string
}

type Decision struct {
	Ticket        storage.Ticket
	Action        Action
	Ref           string
	Reason        string
	ReplaceActive bool
}

// Decide applies the provider-neutral ticket/session lifecycle policy. Runtime
// validation and session-ref recovery are intentionally performed by callers;
// their results enter through Request so this decision table remains pure.
func Decide(req Request) (Decision, error) {
	ticket := req.Ticket
	if req.StartFresh {
		ticket.SessionID = sql.NullInt64{}
		ticket.SessionRef = sql.NullString{}
		ticket.TmuxSessionName = sql.NullString{}
		ticket.WindowID = sql.NullString{}
		ticket.WindowName = sql.NullString{}
		ticket.Multiplexer = sql.NullString{}
		ticket.MuxNamespace = sql.NullString{}
		ticket.MuxContainerID = sql.NullString{}
		ticket.MuxContainerName = sql.NullString{}
		ticket.MuxMetadata = sql.NullString{}
		ticket.SessionActive = false
	}
	if req.SendPrompt && (ticket.SessionID.Valid || ticket.SessionRef.Valid || ticket.WindowName.Valid) {
		return Decision{}, ErrPromptAlreadySent
	}
	if req.StartFresh {
		return Decision{Ticket: ticket, Action: ActionStart, ReplaceActive: true}, nil
	}
	if ticket.SessionID.Valid && ticket.SessionActive {
		if req.ContainerValid {
			return Decision{Ticket: ticket, Action: ActionFocus, Ref: req.ContainerRef}, nil
		}
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return Decision{Ticket: ticket, Action: ActionResume, ReplaceActive: true}, nil
		}
		return Decision{Ticket: ticket, Action: ActionRepair, Reason: "tmux window is missing and no session ref is known"}, nil
	}
	if ticket.SessionID.Valid {
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return Decision{Ticket: ticket, Action: ActionResume}, nil
		}
		return Decision{Ticket: ticket, Action: ActionRepair, Reason: "ticket has no active window and no session ref is known"}, nil
	}
	return Decision{Ticket: ticket, Action: ActionStart}, nil
}
