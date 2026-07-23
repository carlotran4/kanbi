package tmux

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/harness"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/prompt"
	"github.com/carlotran4/kanbi/internal/session"
	"github.com/carlotran4/kanbi/internal/storage"
)

type lifecycleAction = session.Action

const (
	lifecycleActionStart  = session.ActionStart
	lifecycleActionResume = session.ActionResume
	lifecycleActionSwitch = session.ActionFocus
	lifecycleActionRepair = session.ActionRepair
)

type lifecycleRequest struct {
	Ticket     storage.Ticket
	SendPrompt bool
	StartFresh bool
}

type lifecycleDecision = session.Decision

type ticketLifecycle struct {
	manager *Manager
}

func (m *Manager) lifecycle() ticketLifecycle {
	return ticketLifecycle{manager: m}
}

func (l ticketLifecycle) Execute(ctx context.Context, req lifecycleRequest) error {
	if l.manager.Store != nil {
		board, err := l.manager.Store.BoardByID(ctx, req.Ticket.BoardID)
		if err != nil {
			return err
		}
		req.Ticket.BoardWorktreeMode = board.WorktreeMode
	}
	if req.Ticket.BoardWorktreeMode == storage.WorktreeModeGit && !req.Ticket.SessionActive {
		workspace, ok, err := l.manager.Store.CurrentWorkspace(ctx, req.Ticket.ID)
		if err != nil {
			return err
		}
		if ok {
			service := l.manager.workspaceService()
			if workspace.State == storage.WorkspaceStateIntegrated {
				if _, err := service.Rehydrate(ctx, workspace); err != nil {
					return err
				}
			} else if err := service.Validate(ctx, workspace); err != nil {
				_ = l.manager.Store.MarkWorkspaceState(ctx, workspace.ID, storage.WorkspaceStateRepairNeeded, err.Error())
				return fmt.Errorf("validate ticket workspace: %w", err)
			}
			fresh, err := l.manager.Store.TicketByID(ctx, req.Ticket.ID)
			if err != nil {
				return err
			}
			req.Ticket = fresh
		}
	}
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
	var err error
	if !req.SendPrompt && !req.StartFresh {
		ticket, err = l.manager.recoverMissingSessionRef(ctx, ticket)
		if err != nil {
			return lifecycleDecision{}, err
		}
	}
	policyReq := session.Request{Ticket: ticket, SendPrompt: req.SendPrompt, StartFresh: req.StartFresh}
	if !req.StartFresh && !req.SendPrompt && ticket.SessionID.Valid && ticket.SessionActive {
		name := TicketWindowName(ticket)
		containerRef := ContainerRefFromTicket(ticket)
		if containerRef.Name == "" {
			containerRef.Name = name
		}
		adapter, err := l.manager.multiplexerAdapter(containerRef.Kind)
		if err != nil {
			return lifecycleDecision{}, err
		}
		valid, validateErr := adapter.Validate(ctx, containerRef)
		if validateErr != nil && containerRef.Kind != multiplexer.KindHerdr {
			return lifecycleDecision{}, validateErr
		}
		if valid {
			policyReq.ContainerValid = true
			policyReq.ContainerRef = containerRef.Target()
		} else if containerRef.Kind != multiplexer.KindHerdr && containerRef.ID != "" {
			// Preserve the existing safe name fallback: a stale stored ID may
			// focus only an expected-name match in the stored namespace.
			byName := containerRef
			byName.ID = ""
			valid, validateErr = adapter.Validate(ctx, byName)
			if validateErr != nil {
				return lifecycleDecision{}, validateErr
			}
			if valid {
				policyReq.ContainerValid = true
				policyReq.ContainerRef = byName.Name
			}
		}
	}
	return session.Decide(policyReq)
}

func (l ticketLifecycle) launch(ctx context.Context, decision lifecycleDecision, sendPrompt bool) error {
	ticket := decision.Ticket
	name := TicketWindowName(ticket)
	var windowID string
	var renderedPrompt string
	var promptAlreadySent bool
	var refFile string
	var refToken string
	var err error
	launchStartedAt := time.Now().UTC()
	if sendPrompt {
		renderedPrompt = prompt.Render(ticket.DisplayID, ticket.Title, ticket.Body)
		if ticket.Harness == "codex" {
			renderedPrompt, err = l.manager.codexPromptWithAttemptToken(renderedPrompt)
			if err != nil {
				return err
			}
		}
	}
	if l.manager.defaultMultiplexerKind() != multiplexer.KindHerdr {
		name, err = l.manager.availableWindowNameInSession(ctx, l.manager.Config.TmuxSession, name)
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
		command, refFile, refToken, err = l.manager.commandWithPiSessionRefCapture(ticket, command)
		if err != nil {
			return err
		}
	}

	muxKind := l.manager.defaultMultiplexerKind()
	launchCWD := ticket.BoardWorkdir
	var launchWorkspace storage.Workspace
	if ticket.BoardWorktreeMode == storage.WorktreeModeGit {
		service := l.manager.workspaceService()
		if service == nil || l.manager.Store == nil {
			return errors.New("Git worktree mode requires durable storage and a workspace service")
		}
		var ok bool
		launchWorkspace, ok, err = l.manager.Store.CurrentWorkspace(ctx, ticket.ID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("ticket has no prepared Git workspace; reopen it and choose a branch")
		}
		if err := service.Validate(ctx, launchWorkspace); err != nil {
			_ = l.manager.Store.MarkWorkspaceState(ctx, launchWorkspace.ID, storage.WorkspaceStateRepairNeeded, err.Error())
			return fmt.Errorf("validate ticket workspace: %w", err)
		}
		launchCWD = launchWorkspace.LaunchCWD
	}
	claim := storage.Session{TicketID: ticket.ID, Harness: ticket.Harness, TmuxSessionName: l.manager.Config.TmuxSession, TmuxWindowName: name, Multiplexer: string(muxKind), Status: kanban.StateStarting, LaunchCWD: sql.NullString{String: launchCWD, Valid: strings.TrimSpace(launchCWD) != ""}}
	if launchWorkspace.ID != 0 {
		claim.WorkspaceID = sql.NullInt64{Int64: launchWorkspace.ID, Valid: true}
	}
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
		containerRef, err = l.manager.herdrAdapter().Launch(ctx, multiplexer.LaunchSpec{Name: name, CWD: launchCWD, Command: command, Namespace: namespace})
	} else {
		var out string
		out, err = l.manager.run(ctx, append(newWindowArgs(l.manager.Config.TmuxSession, name, launchCWD), ShellCommand(command))...)
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
	if resuming {
		checkAfter := l.manager.ResumeCheckAfter
		if checkAfter <= 0 {
			checkAfter = defaultResumeCheckAfter
		}
		var liveErr error
		if containerRef.Kind == multiplexer.KindHerdr {
			liveErr = l.manager.waitHerdrContainerLive(ctx, containerRef, checkAfter)
		} else {
			liveErr = l.manager.waitWindowLive(ctx, l.manager.Config.TmuxSession, windowID, name, checkAfter)
		}
		if liveErr != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				liveErr = errors.Join(liveErr, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return ResumeFailedError{Ticket: ticket, Err: failClaim(liveErr)}
		}
	}

	ses := storage.Session{TicketID: ticket.ID, Harness: ticket.Harness, TmuxSessionName: l.manager.Config.TmuxSession, TmuxWindowName: name, Multiplexer: string(containerRef.Kind), Status: kanban.StateRunning, LaunchCWD: sql.NullString{String: launchCWD, Valid: strings.TrimSpace(launchCWD) != ""}}
	if launchWorkspace.ID != 0 {
		ses.WorkspaceID = sql.NullInt64{Int64: launchWorkspace.ID, Valid: true}
	}
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
		if ref, ok := l.manager.captureSessionRef(ctx, ticket.Harness, renderedPrompt, launchCWD, launchStartedAt, refFile, refToken); ok {
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
				return readPiSessionRefFile(refFile, refToken)
			})
		}
		// Claude Code can sit on the workspace-trust dialog for an unbounded
		// amount of time, so keep polling its projects dir well past the
		// synchronous capture deadline.
		if !ses.HarnessSessionRef.Valid && promptAlreadySent && ticket.Harness == "claude" {
			harnessName := ticket.Harness
			cwd := launchCWD
			promptText := renderedPrompt
			since := launchStartedAt
			l.manager.startSessionRefCapture(insertedSessionID, harnessName, defaultClaudeRefCaptureTimeout, func() (string, bool) {
				return harness.CaptureSessionRefInCWD(harnessName, promptText, cwd, since)
			})
		}
	}
	if sendPrompt && !promptAlreadySent {
		ready := harness.PromptReadyPattern(l.manager.Config.Harnesses, ticket.Harness)
		adapter, adapterErr := l.manager.multiplexerAdapter(containerRef.Kind)
		if adapterErr != nil {
			return failClaim(adapterErr)
		}
		readOptions := multiplexer.ReadOptions{}
		if containerRef.Kind == multiplexer.KindHerdr {
			readOptions.Lines = 200
		}
		if err := l.manager.WaitAndSendPrompt(ctx, adapter, containerRef, readOptions, renderedPrompt, ready, l.manager.Config.PromptReadyTimeout); err != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return PromptReadyError{WindowName: name, Prompt: renderedPrompt, Ready: ready, Err: failClaim(err)}
		}
		out, _ := adapter.Read(ctx, containerRef, readOptions)
		if l.manager.Store != nil {
			if ref, ok := harness.ParseSessionRef(out, harness.SessionRefPattern(l.manager.Config.Harnesses, ticket.Harness)); ok {
				// Prompt delivery can outlive this claim if another process starts
				// fresh. Persist against the claim created for this launch, never
				// whichever attempt happens to be active now.
				_ = l.manager.Store.UpdateSessionRef(ctx, claimID, ref)
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
