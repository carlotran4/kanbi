package tmux

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"kanbi/internal/harness"
	"kanbi/internal/kanban"
	"kanbi/internal/prompt"
	"kanbi/internal/storage"
)

type lifecycleAction string

const (
	lifecycleActionStart  lifecycleAction = "start"
	lifecycleActionResume lifecycleAction = "resume"
	lifecycleActionSwitch lifecycleAction = "switch"
	lifecycleActionRepair lifecycleAction = "repair"
)

type lifecycleRequest struct {
	Ticket     storage.Ticket
	SendPrompt bool
	StartFresh bool
}

type lifecycleDecision struct {
	Ticket storage.Ticket
	Action lifecycleAction
	Ref    string
	Reason string
}

type ticketLifecycle struct {
	manager *Manager
}

func (m *Manager) lifecycle() ticketLifecycle {
	return ticketLifecycle{manager: m}
}

func (l ticketLifecycle) Execute(ctx context.Context, req lifecycleRequest) error {
	decision, err := l.Decide(ctx, req)
	if err != nil {
		return err
	}
	switch decision.Action {
	case lifecycleActionSwitch:
		return l.manager.switchWindow(ctx, ticketRuntimeSessionName(l.manager.Config.TmuxSession, decision.Ticket), decision.Ref)
	case lifecycleActionRepair:
		return RepairNeededError{Ticket: decision.Ticket, Reason: decision.Reason}
	case lifecycleActionStart, lifecycleActionResume:
		if err := l.manager.EnsureSession(ctx); err != nil {
			return err
		}
		return l.launch(ctx, decision, req.SendPrompt)
	default:
		return fmt.Errorf("unknown lifecycle action %q", decision.Action)
	}
}

func (l ticketLifecycle) Decide(ctx context.Context, req lifecycleRequest) (lifecycleDecision, error) {
	ticket := req.Ticket
	if req.StartFresh {
		ticket.SessionID = sql.NullInt64{}
		ticket.SessionRef = sql.NullString{}
		ticket.WindowID = sql.NullString{}
		ticket.WindowName = sql.NullString{}
		ticket.SessionActive = false
	}
	var err error
	if !req.SendPrompt {
		ticket, err = l.manager.recoverMissingSessionRef(ctx, ticket)
		if err != nil {
			return lifecycleDecision{}, err
		}
	}
	if req.SendPrompt && (ticket.SessionID.Valid || ticket.SessionRef.Valid || ticket.WindowName.Valid) {
		return lifecycleDecision{}, ErrPromptAlreadySent
	}
	name := TicketWindowName(ticket)
	if req.StartFresh {
		return lifecycleDecision{Ticket: ticket, Action: lifecycleActionStart}, nil
	}
	if ticket.SessionID.Valid && ticket.SessionActive {
		ref, exists, err := l.manager.ticketWindowRef(ctx, ticket, name)
		if err != nil {
			return lifecycleDecision{}, err
		}
		if exists {
			return lifecycleDecision{Ticket: ticket, Action: lifecycleActionSwitch, Ref: ref}, nil
		}
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return lifecycleDecision{Ticket: ticket, Action: lifecycleActionResume}, nil
		}
		return lifecycleDecision{Ticket: ticket, Action: lifecycleActionRepair, Reason: "tmux window is missing and no session ref is known"}, nil
	}
	if ticket.SessionID.Valid {
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return lifecycleDecision{Ticket: ticket, Action: lifecycleActionResume}, nil
		}
		return lifecycleDecision{Ticket: ticket, Action: lifecycleActionRepair, Reason: "ticket has no active window and no session ref is known"}, nil
	}
	return lifecycleDecision{Ticket: ticket, Action: lifecycleActionStart}, nil
}

func (l ticketLifecycle) launch(ctx context.Context, decision lifecycleDecision, sendPrompt bool) error {
	ticket := decision.Ticket
	name := TicketWindowName(ticket)
	var windowID string
	var renderedPrompt string
	var promptAlreadySent bool
	var refFile string
	launchStartedAt := time.Now().UTC()
	if sendPrompt {
		renderedPrompt = prompt.Render(ticket.DisplayID, ticket.Title, ticket.Body)
	}
	name, err := l.manager.availableWindowName(ctx, name)
	if err != nil {
		return err
	}
	launchedNew := true
	command, err := harness.StartCommand(l.manager.Config.Harnesses, ticket.Harness)
	if err != nil {
		return err
	}
	resuming := decision.Action == lifecycleActionResume
	if resuming {
		command, err = harness.ResumeCommand(l.manager.Config.Harnesses, ticket.Harness, ticket.SessionRef.String)
		if err != nil {
			return err
		}
	} else {
		command, promptAlreadySent, err = harness.StartCommandWithPrompt(l.manager.Config.Harnesses, ticket.Harness, renderedPrompt, sendPrompt)
		if err != nil {
			return err
		}
	}
	if ticket.Harness == "pi" && promptAlreadySent {
		command, refFile, err = l.manager.commandWithPiSessionRefCapture(ticket, command)
		if err != nil {
			return err
		}
	}
	out, err := l.manager.run(ctx, append(newWindowArgs(l.manager.Config.TmuxSession, name, ticket.BoardWorkdir), ShellCommand(command))...)
	if err != nil {
		if l.manager.Store != nil && ticket.SessionID.Valid {
			_ = l.manager.Store.UpdateSessionRuntime(ctx, ticket.SessionID.Int64, kanban.StateError, "tmux", err.Error(), "", false)
		}
		if resuming {
			return ResumeFailedError{Ticket: ticket, Err: err}
		}
		return err
	}
	windowID = strings.TrimSpace(out)
	if resuming {
		checkAfter := l.manager.ResumeCheckAfter
		if checkAfter <= 0 {
			checkAfter = defaultResumeCheckAfter
		}
		if liveErr := l.manager.waitWindowLive(ctx, l.manager.Config.TmuxSession, windowID, name, checkAfter); liveErr != nil {
			return ResumeFailedError{Ticket: ticket, Err: liveErr}
		}
	}

	ses := storage.Session{TicketID: ticket.ID, Harness: ticket.Harness, TmuxSessionName: l.manager.Config.TmuxSession, TmuxWindowName: name, Status: kanban.StateRunning}
	if windowID != "" {
		ses.TmuxWindowID = sql.NullString{String: windowID, Valid: true}
	}
	if ticket.SessionRef.Valid {
		ses.HarnessSessionRef = sql.NullString{String: ticket.SessionRef.String, Valid: true}
	} else if promptAlreadySent && l.manager.Store != nil {
		if ref, ok := l.manager.captureSessionRef(ctx, ticket.Harness, renderedPrompt, launchStartedAt, refFile); ok {
			ses.HarnessSessionRef = sql.NullString{String: ref, Valid: true}
		}
	}
	if l.manager.Store != nil && (launchedNew || !ticket.SessionID.Valid || !ticket.SessionActive) {
		if _, err := l.manager.Store.UpsertActiveSession(ctx, ticket.ID, ses); err != nil {
			return err
		}
		// If we didn't capture the ref before upserting (e.g. Pi extension fires slowly),
		// start a background goroutine to keep polling and update the DB once found.
		if !ses.HarnessSessionRef.Valid && promptAlreadySent && refFile != "" {
			ticketID := ticket.ID
			store := l.manager.Store
			go func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				end := time.Now().Add(30 * time.Second)
				for time.Now().Before(end) {
					select {
					case <-bgCtx.Done():
						return
					case <-time.After(500 * time.Millisecond):
					}
					if ref, ok := readPiSessionRefFile(refFile); ok {
						if activeSes, active, err := store.ActiveSession(bgCtx, ticketID); err == nil && active {
							_ = store.UpdateSessionRef(bgCtx, activeSes.ID, ref)
						}
						return
					}
				}
			}()
		}
	}
	if sendPrompt && !promptAlreadySent {
		ready := harness.PromptReadyPattern(l.manager.Config.Harnesses, ticket.Harness)
		if err := l.manager.WaitAndPastePrompt(ctx, name, renderedPrompt, ready, l.manager.Config.PromptReadyTimeout); err != nil {
			return PromptReadyError{WindowName: name, Prompt: renderedPrompt, Ready: ready, Err: err}
		}
		if l.manager.Store != nil {
			out, _ := l.manager.CapturePane(ctx, name)
			if ref, ok := harness.ParseSessionRef(out, harness.SessionRefPattern(l.manager.Config.Harnesses, ticket.Harness)); ok {
				if ses, active, err := l.manager.Store.ActiveSession(ctx, ticket.ID); err == nil && active {
					_ = l.manager.Store.UpdateSessionRef(ctx, ses.ID, ref)
				}
			}
		}
	}
	switchRef := windowID
	if switchRef == "" {
		switchRef = name
	}
	return l.manager.switchWindow(ctx, l.manager.Config.TmuxSession, switchRef)
}

func (m *Manager) switchWindow(ctx context.Context, sessionName, ref string) error {
	if sessionName == "" {
		sessionName = m.Config.TmuxSession
	}
	out, err := m.run(ctx, "switch-client", "-t", sessionName)
	if err != nil && !InsideTmux() && strings.Contains(strings.ToLower(out), "no current client") {
		return nil
	}
	if err != nil {
		return err
	}
	if ref == "" {
		return nil
	}
	_, err = m.run(ctx, "select-window", "-t", targetRef(sessionName, ref))
	return err
}
