package tmux

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"kanbi/internal/harness"
	"kanbi/internal/kanban"
	"kanbi/internal/multiplexer"
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
	Ticket        storage.Ticket
	Action        lifecycleAction
	Ref           string
	Reason        string
	ReplaceActive bool
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
		ref := ContainerRefFromTicket(decision.Ticket)
		if ref.Kind == multiplexer.KindHerdr {
			return l.manager.herdrAdapter().Focus(ctx, ref)
		}
		return l.manager.switchWindow(ctx, ticketRuntimeSessionName(l.manager.Config.TmuxSession, decision.Ticket), decision.Ref)
	case lifecycleActionRepair:
		return RepairNeededError{Ticket: decision.Ticket, Reason: decision.Reason}
	case lifecycleActionStart, lifecycleActionResume:
		if l.manager.defaultMultiplexerKind() != multiplexer.KindHerdr {
			if err := l.manager.EnsureSession(ctx); err != nil {
				return err
			}
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
		return lifecycleDecision{Ticket: ticket, Action: lifecycleActionStart, ReplaceActive: true}, nil
	}
	if ticket.SessionID.Valid && ticket.SessionActive {
		containerRef := ContainerRefFromTicket(ticket)
		if containerRef.Kind == multiplexer.KindHerdr {
			adapter := l.manager.herdrAdapter()
			detection, err := adapter.Detect(ctx, containerRef)
			if err == nil && detection.Source != multiplexer.DetectionSourceUnknown {
				return lifecycleDecision{Ticket: ticket, Action: lifecycleActionSwitch, Ref: containerRef.Target()}, nil
			}
			if _, readErr := adapter.Read(ctx, containerRef, multiplexer.ReadOptions{Lines: 1}); readErr == nil {
				return lifecycleDecision{Ticket: ticket, Action: lifecycleActionSwitch, Ref: containerRef.Target()}, nil
			}
		} else {
			ref, exists, err := l.manager.ticketWindowRef(ctx, ticket, name)
			if err != nil {
				return lifecycleDecision{}, err
			}
			if exists {
				return lifecycleDecision{Ticket: ticket, Action: lifecycleActionSwitch, Ref: ref}, nil
			}
		}
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return lifecycleDecision{Ticket: ticket, Action: lifecycleActionResume, ReplaceActive: true}, nil
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
	var err error
	launchStartedAt := time.Now().UTC()
	if sendPrompt {
		renderedPrompt = prompt.Render(ticket.DisplayID, ticket.Title, ticket.Body)
	}
	if l.manager.defaultMultiplexerKind() != multiplexer.KindHerdr {
		name, err = l.manager.availableWindowName(ctx, name)
		if err != nil {
			return err
		}
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

	muxKind := l.manager.defaultMultiplexerKind()
	claim := storage.Session{TicketID: ticket.ID, Harness: ticket.Harness, TmuxSessionName: l.manager.Config.TmuxSession, TmuxWindowName: name, Multiplexer: string(muxKind), Status: kanban.StateStarting}
	if ticket.SessionRef.Valid {
		claim.HarnessSessionRef = ticket.SessionRef
	}
	if muxKind == multiplexer.KindHerdr {
		claim.TmuxSessionName = ""
		claim.TmuxWindowName = ""
		claim.MuxContainerName = sql.NullString{String: name, Valid: true}
		if namespace := l.manager.currentHerdrWorkspace(ctx); namespace != "" {
			claim.MuxNamespace = sql.NullString{String: namespace, Valid: true}
		}
	}
	var claimID int64
	if l.manager.Store != nil {
		claimID, err = l.manager.Store.ClaimSession(ctx, ticket.ID, claim, decision.ReplaceActive)
		if err != nil {
			return err
		}
	}
	failClaim := func(cause error) error {
		if l.manager.Store == nil || claimID == 0 {
			return cause
		}
		if markErr := l.manager.Store.FailSessionLaunch(ctx, claimID, cause.Error()); markErr != nil {
			return errors.Join(cause, fmt.Errorf("mark launch failed: %w", markErr))
		}
		return cause
	}

	var containerRef multiplexer.ContainerRef
	if muxKind == multiplexer.KindHerdr {
		namespace := claim.MuxNamespace.String
		containerRef, err = l.manager.herdrAdapter().Launch(ctx, multiplexer.LaunchSpec{Name: name, CWD: ticket.BoardWorkdir, Command: command, Namespace: namespace})
	} else {
		var out string
		out, err = l.manager.run(ctx, append(newWindowArgs(l.manager.Config.TmuxSession, name, ticket.BoardWorkdir), ShellCommand(command))...)
		if err == nil {
			windowID = strings.TrimSpace(out)
			containerRef = multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: l.manager.Config.TmuxSession, ID: windowID, Name: name}
		}
	}
	if err != nil {
		err = failClaim(err)
		if resuming {
			return ResumeFailedError{Ticket: ticket, Err: err}
		}
		return err
	}
	if containerRef.Kind == multiplexer.KindTmux {
		windowID = containerRef.ID
	}
	if resuming && containerRef.Kind == multiplexer.KindTmux {
		checkAfter := l.manager.ResumeCheckAfter
		if checkAfter <= 0 {
			checkAfter = defaultResumeCheckAfter
		}
		if liveErr := l.manager.waitWindowLive(ctx, l.manager.Config.TmuxSession, windowID, name, checkAfter); liveErr != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				liveErr = errors.Join(liveErr, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return ResumeFailedError{Ticket: ticket, Err: failClaim(liveErr)}
		}
	}

	ses := storage.Session{TicketID: ticket.ID, Harness: ticket.Harness, TmuxSessionName: l.manager.Config.TmuxSession, TmuxWindowName: name, Multiplexer: string(containerRef.Kind), Status: kanban.StateRunning}
	if ses.Multiplexer == "" {
		ses.Multiplexer = string(multiplexer.KindTmux)
	}
	ApplyContainerRefToSession(&ses, containerRef)
	if windowID != "" {
		ses.TmuxWindowID = sql.NullString{String: windowID, Valid: true}
	}
	if ticket.SessionRef.Valid {
		ses.HarnessSessionRef = sql.NullString{String: ticket.SessionRef.String, Valid: true}
	} else if promptAlreadySent && l.manager.Store != nil {
		if ref, ok := l.manager.captureSessionRef(ctx, ticket.Harness, renderedPrompt, ticket.BoardWorkdir, launchStartedAt, refFile); ok {
			ses.HarnessSessionRef = sql.NullString{String: ref, Valid: true}
		}
	}
	if l.manager.Store != nil && (launchedNew || !ticket.SessionID.Valid || !ticket.SessionActive) {
		if err := l.manager.Store.CompleteSessionLaunch(ctx, claimID, ses); err != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return failClaim(err)
		}
		insertedSessionID := claimID
		// If we didn't capture the ref before upserting (e.g. Pi extension fires slowly),
		// keep polling and update exactly the session row created by this launch.
		if !ses.HarnessSessionRef.Valid && promptAlreadySent && refFile != "" {
			l.manager.startSessionRefCapture(insertedSessionID, ticket.Harness, defaultPiRefCaptureTimeout, func() (string, bool) {
				return readPiSessionRefFile(refFile)
			})
		}
		// Claude Code can sit on the workspace-trust dialog for an unbounded
		// amount of time, so keep polling its projects dir well past the
		// synchronous capture deadline.
		if !ses.HarnessSessionRef.Valid && promptAlreadySent && ticket.Harness == "claude" {
			harnessName := ticket.Harness
			cwd := ticket.BoardWorkdir
			promptText := renderedPrompt
			since := launchStartedAt
			l.manager.startSessionRefCapture(insertedSessionID, harnessName, defaultClaudeRefCaptureTimeout, func() (string, bool) {
				return harness.CaptureSessionRefInCWD(harnessName, promptText, cwd, since)
			})
		}
	}
	if sendPrompt && !promptAlreadySent {
		ready := harness.PromptReadyPattern(l.manager.Config.Harnesses, ticket.Harness)
		var out string
		if containerRef.Kind == multiplexer.KindHerdr {
			adapter := l.manager.herdrAdapter()
			if err := l.manager.WaitAndSendHerdrPrompt(ctx, adapter, containerRef, renderedPrompt, ready, l.manager.Config.PromptReadyTimeout); err != nil {
				if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
					err = errors.Join(err, fmt.Errorf("cleanup failed container: %w", cleanupErr))
				}
				return PromptReadyError{WindowName: name, Prompt: renderedPrompt, Ready: ready, Err: failClaim(err)}
			}
			out, _ = adapter.Read(ctx, containerRef, multiplexer.ReadOptions{Lines: 200})
		} else {
			if err := l.manager.WaitAndPastePrompt(ctx, name, renderedPrompt, ready, l.manager.Config.PromptReadyTimeout); err != nil {
				if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
					err = errors.Join(err, fmt.Errorf("cleanup failed container: %w", cleanupErr))
				}
				return PromptReadyError{WindowName: name, Prompt: renderedPrompt, Ready: ready, Err: failClaim(err)}
			}
			out, _ = l.manager.CapturePane(ctx, name)
		}
		if l.manager.Store != nil {
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
	if containerRef.Kind == multiplexer.KindHerdr {
		if l.manager.Config.Multiplexer.Herdr.FocusOnOpen {
			return l.manager.herdrAdapter().Focus(ctx, containerRef)
		}
		return nil
	}
	return l.manager.switchWindow(ctx, l.manager.Config.TmuxSession, switchRef)
}

func (l ticketLifecycle) cleanupLaunchedContainer(ctx context.Context, ref multiplexer.ContainerRef) error {
	if ref.Target() == "" {
		return nil
	}
	if ref.Kind == multiplexer.KindHerdr {
		return l.manager.herdrAdapter().Close(ctx, ref)
	}
	return NewMultiplexerAdapter(l.manager).Close(ctx, ref)
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
