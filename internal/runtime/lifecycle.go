package runtime

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
		return l.manager.herdrAdapter().Focus(ctx, ref)
	case lifecycleActionRepair:
		return RepairNeededError{Ticket: decision.Ticket, Reason: decision.Reason}
	case lifecycleActionStart, lifecycleActionResume:
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
		if containerRef.Kind != multiplexer.KindHerdr {
			return lifecycleDecision{}, RepairNeededError{Ticket: ticket, Reason: "legacy runtime retired; close the old terminal externally before explicitly starting a new attempt"}
		}
		valid, validateErr := l.manager.herdrAdapter().Validate(ctx, containerRef)
		if validateErr != nil {
			return lifecycleDecision{}, validateErr
		}
		if valid {
			policyReq.ContainerValid = true
			policyReq.ContainerRef = containerRef.Target()
		}

	}
	return session.Decide(policyReq)
}

func (l ticketLifecycle) launch(ctx context.Context, decision lifecycleDecision, sendPrompt bool) error {
	ticket := decision.Ticket
	name := TicketWindowName(ticket)
	var renderedPrompt string
	var promptAlreadySent bool
	var refFile string
	var refToken string
	var err error
	if sendPrompt && ticket.Harness == "codex" && decision.Action != lifecycleActionResume {
		unlock, lockErr := l.manager.acquireCodexCaptureLock(ctx)
		if lockErr != nil {
			return lockErr
		}
		defer unlock()
	}
	launchStartedAt := time.Now().UTC()
	if sendPrompt {
		renderedPrompt = prompt.Render(ticket.DisplayID, ticket.Title, ticket.Body)
	}
	muxKind := multiplexer.KindHerdr
	command, err := harness.StartCommand(l.manager.Config.Harnesses, ticket.Harness)
	if err != nil {
		return err
	}
	herdrPaneFirst := l.manager.herdrAdapter().UsesAgentStart(ctx, command, ticket.Harness)
	resuming := decision.Action == lifecycleActionResume
	if resuming {
		command, err = harness.ResumeCommand(l.manager.Config.Harnesses, ticket.Harness, ticket.SessionRef.String)
		if err != nil {
			return err
		}
	} else if !herdrPaneFirst || !sendPrompt {
		command, promptAlreadySent, err = harness.StartCommandWithPrompt(l.manager.Config.Harnesses, ticket.Harness, renderedPrompt, sendPrompt)
		if err != nil {
			return err
		}
	}
	if ticket.Harness == "pi" && sendPrompt {
		command, refFile, refToken, err = l.manager.commandWithPiSessionRefCapture(ticket, command)
		if err != nil {
			return err
		}
	}

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
	claim := storage.Session{TicketID: ticket.ID, Harness: ticket.Harness, Multiplexer: string(muxKind), Status: kanban.StateStarting, LaunchCWD: sql.NullString{String: launchCWD, Valid: strings.TrimSpace(launchCWD) != ""}}
	if launchWorkspace.ID != 0 {
		claim.WorkspaceID = sql.NullInt64{Int64: launchWorkspace.ID, Valid: true}
	}
	if ticket.SessionRef.Valid {
		claim.HarnessSessionRef = ticket.SessionRef
	}
	if muxKind == multiplexer.KindHerdr {
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
	namespace := claim.MuxNamespace.String
	adapter, adapterErr := l.manager.multiplexerAdapter(muxKind)
	if adapterErr != nil {
		err = adapterErr
	} else {
		containerRef, err = adapter.Launch(ctx, multiplexer.LaunchSpec{Name: name, CWD: launchCWD, Command: command, AgentKind: ticket.Harness, Namespace: namespace})
	}
	if err != nil {
		err = failClaim(err)
		if resuming {
			return ResumeFailedError{Ticket: ticket, Err: err}
		}
		return err
	}
	// Some interactive Pi and Claude versions only populate their editor from an
	// initial prompt argument. Explicitly submit once after the container is live.
	// Versions that auto-submit positional prompts treat Enter on the now-empty
	// editor as a no-op.
	if (ticket.Harness == "pi" || ticket.Harness == "claude") && sendPrompt && promptAlreadySent {
		adapter, adapterErr := l.manager.multiplexerAdapter(containerRef.Kind)
		if adapterErr != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				adapterErr = errors.Join(adapterErr, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return failClaim(adapterErr)
		}
		if submitErr := adapter.SendKeys(ctx, containerRef, "Enter"); submitErr != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				submitErr = errors.Join(submitErr, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return fmt.Errorf("submit %s initial prompt: %w", ticket.Harness, failClaim(submitErr))
		}
	}
	if resuming {
		checkAfter := l.manager.ResumeCheckAfter
		if checkAfter <= 0 {
			checkAfter = defaultResumeCheckAfter
		}
		liveErr := l.manager.waitHerdrContainerLive(ctx, containerRef, checkAfter)
		if liveErr != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				liveErr = errors.Join(liveErr, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return ResumeFailedError{Ticket: ticket, Err: failClaim(liveErr)}
		}
	}

	ses := storage.Session{TicketID: ticket.ID, Harness: ticket.Harness, Multiplexer: string(containerRef.Kind), Status: kanban.StateRunning, LaunchCWD: sql.NullString{String: launchCWD, Valid: strings.TrimSpace(launchCWD) != ""}}
	if launchWorkspace.ID != 0 {
		ses.WorkspaceID = sql.NullInt64{Int64: launchWorkspace.ID, Valid: true}
	}
	if ses.Multiplexer == "" {
		ses.Multiplexer = string(multiplexer.KindHerdr)
	}
	ApplyContainerRefToSession(&ses, containerRef)
	if ticket.SessionRef.Valid {
		ses.HarnessSessionRef = sql.NullString{String: ticket.SessionRef.String, Valid: true}
	} else if promptAlreadySent && l.manager.Store != nil {
		deadline := time.Now().Add(2 * time.Second)
		for {
			out, readErr := adapter.Read(ctx, containerRef, multiplexer.ReadOptions{Lines: 200})
			if errors.Is(readErr, multiplexer.ErrContainerNotFound) {
				return failClaim(fmt.Errorf("harness exited during startup: %w", readErr))
			}
			if readErr == nil {
				if ref, ok := harness.ParseSessionRef(out, harness.SessionRefPattern(l.manager.Config.Harnesses, ticket.Harness)); ok {
					ses.HarnessSessionRef = sql.NullString{String: ref, Valid: true}
					break
				}
			}
			if l.manager.nativeHarness(ticket.Harness) || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				cause := ctx.Err()
				if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
					cause = errors.Join(cause, cleanupErr)
				}
				return failClaim(cause)
			case <-time.After(50 * time.Millisecond):
			}
		}
		if ref, ok := l.manager.captureSessionRef(ctx, ticket.Harness, renderedPrompt, launchCWD, launchStartedAt, refFile, refToken); ok && !ses.HarnessSessionRef.Valid {
			ses.HarnessSessionRef = sql.NullString{String: ref, Valid: true}
		}
	}
	if l.manager.Store != nil {
		if err := l.manager.Store.CompleteSessionLaunch(ctx, claimID, ses); err != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return failClaim(err)
		}
		insertedSessionID := claimID
		// If we didn't capture the ref before upserting (e.g. Pi extension fires slowly),
		// keep polling and update exactly the session row created by this launch.
		if !ses.HarnessSessionRef.Valid && sendPrompt && refFile != "" {
			l.manager.startSessionRefCapture(insertedSessionID, ticket.Harness, defaultPiRefCaptureTimeout, func() (string, bool) {
				if ref, ok := readPiSessionRefFile(refFile, refToken); ok {
					return ref, true
				}
				if !l.manager.nativeHarness(ticket.Harness) {
					return "", false
				}
				return harness.CaptureSessionRefInCWD(ticket.Harness, renderedPrompt, launchCWD, launchStartedAt)
			})
		}
		if !ses.HarnessSessionRef.Valid && sendPrompt && !promptAlreadySent && ticket.Harness == "copilot" && l.manager.nativeHarness(ticket.Harness) {
			harnessName := ticket.Harness
			cwd := launchCWD
			promptText := renderedPrompt
			since := launchStartedAt
			l.manager.startSessionRefCapture(insertedSessionID, harnessName, defaultPiRefCaptureTimeout, func() (string, bool) {
				return harness.CaptureSessionRefInCWD(harnessName, promptText, cwd, since)
			})
		}
		// Claude Code can sit on the workspace-trust dialog for an unbounded
		// amount of time, so keep polling its projects dir well past the
		// synchronous capture deadline.
		if !ses.HarnessSessionRef.Valid && sendPrompt && ticket.Harness == "claude" && l.manager.nativeHarness(ticket.Harness) {
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
		var promptErr error
		if herdrPaneFirst {
			// Pane-first Herdr reports success only after the canonical harness is
			// detected and ready, so multiline prompts can be sent literally now.
			if promptErr = adapter.SendText(ctx, containerRef, renderedPrompt); promptErr == nil {
				promptErr = adapter.SendKeys(ctx, containerRef, "enter")
			}
		} else {
			promptErr = l.manager.WaitAndSendPrompt(ctx, adapter, containerRef, readOptions, renderedPrompt, ready, l.manager.Config.PromptReadyTimeout)
		}
		if promptErr != nil {
			if cleanupErr := l.cleanupLaunchedContainer(ctx, containerRef); cleanupErr != nil {
				promptErr = errors.Join(promptErr, fmt.Errorf("cleanup failed container: %w", cleanupErr))
			}
			return PromptReadyError{WindowName: name, Prompt: renderedPrompt, Ready: ready, Err: failClaim(promptErr)}
		}
		out, _ := adapter.Read(ctx, containerRef, readOptions)
		if l.manager.Store != nil {
			ref, captured := harness.ParseSessionRef(out, harness.SessionRefPattern(l.manager.Config.Harnesses, ticket.Harness))
			if !captured && herdrPaneFirst {
				// Pane-first launches deliver the prompt after the durable claim is
				// completed. Capture synchronously so short-lived CLI commands do not
				// cancel the only opportunity to persist the resume handle on exit.
				ref, captured = l.manager.captureSessionRef(ctx, ticket.Harness, renderedPrompt, launchCWD, launchStartedAt, refFile, refToken)
			}
			if captured {
				// Prompt delivery can outlive this claim if another process starts
				// fresh. Persist against the claim created for this launch, never
				// whichever attempt happens to be active now.
				_ = l.manager.Store.UpdateSessionRef(ctx, claimID, ref)
			}
		}
	}
	if l.manager.Config.Multiplexer.Herdr.FocusOnOpen {
		return l.manager.herdrAdapter().Focus(ctx, containerRef)
	}
	return nil
}

func (l ticketLifecycle) cleanupLaunchedContainer(ctx context.Context, ref multiplexer.ContainerRef) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return l.manager.herdrAdapter().Close(cleanupCtx, ref)
}
