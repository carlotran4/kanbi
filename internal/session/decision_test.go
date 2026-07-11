package session

import (
	"database/sql"
	"errors"
	"testing"

	"kanbi/internal/storage"
)

func TestDecideLifecycleTable(t *testing.T) {
	started := storage.Ticket{
		SessionID:        sql.NullInt64{Int64: 7, Valid: true},
		SessionRef:       sql.NullString{String: "resume-7", Valid: true},
		TmuxSessionName:  sql.NullString{String: "runtime", Valid: true},
		WindowID:         sql.NullString{String: "@7", Valid: true},
		WindowName:       sql.NullString{String: "ticket-7", Valid: true},
		Multiplexer:      sql.NullString{String: "tmux", Valid: true},
		MuxNamespace:     sql.NullString{String: "runtime", Valid: true},
		MuxContainerID:   sql.NullString{String: "@7", Valid: true},
		MuxContainerName: sql.NullString{String: "ticket-7", Valid: true},
		MuxMetadata:      sql.NullString{String: `{}`, Valid: true},
		SessionActive:    true,
	}
	tests := []struct {
		name          string
		req           Request
		action        Action
		ref           string
		reason        string
		replaceActive bool
		promptErr     bool
	}{
		{name: "never started", req: Request{}, action: ActionStart},
		{name: "never started sends prompt", req: Request{SendPrompt: true}, action: ActionStart},
		{name: "active valid container focuses", req: Request{Ticket: started, ContainerValid: true, ContainerRef: "@7"}, action: ActionFocus, ref: "@7"},
		{name: "active stale container resumes", req: Request{Ticket: started}, action: ActionResume, replaceActive: true},
		{name: "active stale container without ref repairs", req: Request{Ticket: withoutSessionRef(started)}, action: ActionRepair, reason: "tmux window is missing and no session ref is known"},
		{name: "inactive session resumes", req: Request{Ticket: inactive(started)}, action: ActionResume},
		{name: "inactive session without ref repairs", req: Request{Ticket: withoutSessionRef(inactive(started))}, action: ActionRepair, reason: "ticket has no active window and no session ref is known"},
		{name: "start fresh clears history and starts", req: Request{Ticket: started, StartFresh: true}, action: ActionStart, replaceActive: true},
		{name: "start fresh may send prompt", req: Request{Ticket: started, StartFresh: true, SendPrompt: true}, action: ActionStart, replaceActive: true},
		{name: "second prompt rejected by session id", req: Request{Ticket: storage.Ticket{SessionID: sql.NullInt64{Int64: 1, Valid: true}}, SendPrompt: true}, promptErr: true},
		{name: "second prompt rejected by session ref", req: Request{Ticket: storage.Ticket{SessionRef: sql.NullString{String: "ref", Valid: true}}, SendPrompt: true}, promptErr: true},
		{name: "second prompt rejected by window name", req: Request{Ticket: storage.Ticket{WindowName: sql.NullString{String: "window", Valid: true}}, SendPrompt: true}, promptErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(tt.req)
			if tt.promptErr {
				if !errors.Is(err, ErrPromptAlreadySent) {
					t.Fatalf("Decide() error = %v, want ErrPromptAlreadySent", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if got.Action != tt.action || got.Ref != tt.ref || got.Reason != tt.reason || got.ReplaceActive != tt.replaceActive {
				t.Fatalf("Decide() = %+v, want action=%q ref=%q reason=%q replaceActive=%v", got, tt.action, tt.ref, tt.reason, tt.replaceActive)
			}
			if tt.req.StartFresh && hasRuntimeMetadata(got.Ticket) {
				t.Fatalf("start-fresh decision retained prior runtime metadata: %+v", got.Ticket)
			}
		})
	}
}

func hasRuntimeMetadata(ticket storage.Ticket) bool {
	return ticket.SessionID.Valid || ticket.SessionRef.Valid || ticket.TmuxSessionName.Valid ||
		ticket.WindowID.Valid || ticket.WindowName.Valid || ticket.Multiplexer.Valid ||
		ticket.MuxNamespace.Valid || ticket.MuxContainerID.Valid || ticket.MuxContainerName.Valid ||
		ticket.MuxMetadata.Valid || ticket.SessionActive
}

func withoutSessionRef(ticket storage.Ticket) storage.Ticket {
	ticket.SessionRef = sql.NullString{}
	return ticket
}

func inactive(ticket storage.Ticket) storage.Ticket {
	ticket.SessionActive = false
	return ticket
}
