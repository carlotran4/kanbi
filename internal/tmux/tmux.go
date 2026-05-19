package tmux

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"agent-kanban/internal/config"
	"agent-kanban/internal/harness"
	"agent-kanban/internal/prompt"
	"agent-kanban/internal/storage"
)

var ErrPromptAlreadySent = errors.New("prompt already sent; open session instead")

const defaultResumeCheckAfter = 2500 * time.Millisecond

type RepairNeededError struct {
	Ticket storage.Ticket
	Reason string
}

func (e RepairNeededError) Error() string {
	if e.Reason == "" {
		return "ticket session needs repair"
	}
	return e.Reason
}

type ResumeFailedError struct {
	Ticket storage.Ticket
	Err    error
}

func (e ResumeFailedError) Error() string {
	if e.Err == nil {
		return "resume failed"
	}
	return "resume failed: " + e.Err.Error()
}

func (e ResumeFailedError) Unwrap() error { return e.Err }

type PromptReadyError struct {
	WindowName string
	Prompt     string
	Ready      string
	Err        error
}

func (e PromptReadyError) Error() string {
	if e.Err == nil {
		return "prompt readiness timeout"
	}
	return e.Err.Error()
}

func (e PromptReadyError) Unwrap() error { return e.Err }

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

type Manager struct {
	Config config.Config
	Store  *storage.Store
	Runner Runner

	// ResumeCheckAfter is how long to wait after launching a resume command
	// before deciding whether the tmux window survived startup. Zero uses the
	// production default.
	ResumeCheckAfter time.Duration
}

func NewManager(cfg config.Config, store *storage.Store) *Manager {
	return &Manager{Config: cfg, Store: store, Runner: ExecRunner{}, ResumeCheckAfter: defaultResumeCheckAfter}
}

func (m *Manager) EnsureSession(ctx context.Context) error {
	if _, err := m.run(ctx, "has-session", "-t", m.Config.TmuxSession); err == nil {
		return m.EnsureBoardWindow(ctx)
	}
	if _, err := m.run(ctx, "new-session", "-d", "-s", m.Config.TmuxSession, "-n", m.Config.Tmux.BoardWindowName); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}
	return nil
}

func (m *Manager) EnsureBoardWindow(ctx context.Context) error {
	name := m.Config.Tmux.BoardWindowName
	if name == "" {
		name = "board"
	}
	exists, err := m.windowExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = m.run(ctx, "new-window", "-d", "-t", m.Config.TmuxSession, "-n", name)
	return err
}

func (m *Manager) OpenTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	if sendPrompt && (ticket.SessionID.Valid || ticket.SessionRef.Valid || ticket.WindowName.Valid) {
		return ErrPromptAlreadySent
	}
	if ticket.SessionID.Valid && !ticket.SessionRef.Valid && !ticket.SessionActive && !ticket.WindowName.Valid {
		return RepairNeededError{Ticket: ticket, Reason: "session started but no window or session ref is known"}
	}
	if err := m.EnsureSession(ctx); err != nil {
		return err
	}
	name := WindowName(ticket.DisplayID, ticket.Title)
	var windowID string
	var renderedPrompt string
	var promptAlreadySent bool
	launchStartedAt := time.Now().UTC()
	if sendPrompt {
		renderedPrompt = prompt.Render(ticket.DisplayID, ticket.Title, ticket.Body)
	}
	launchedNew := false
	if exists, err := m.windowExists(ctx, name); err != nil {
		return err
	} else if !exists {
		launchedNew = true
		command, err := harness.StartCommand(m.Config, ticket.Harness)
		if err != nil {
			return err
		}
		resuming := ticket.SessionRef.Valid && ticket.SessionRef.String != ""
		if resuming {
			command, err = harness.ResumeCommand(m.Config, ticket.Harness, ticket.SessionRef.String)
			if err != nil {
				return err
			}
		} else {
			command, promptAlreadySent, err = harness.StartCommandWithPrompt(m.Config, ticket.Harness, renderedPrompt, sendPrompt)
			if err != nil {
				return err
			}
		}
		out, err := m.run(ctx, append(newWindowArgs(m.Config.TmuxSession, name), ShellCommand(command))...)
		if err != nil {
			if m.Store != nil && ticket.SessionID.Valid {
				_ = m.Store.UpdateSessionRuntime(ctx, ticket.SessionID.Int64, "error", "tmux", err.Error(), "", false)
			}
			if resuming {
				return ResumeFailedError{Ticket: ticket, Err: err}
			}
			return err
		}
		windowID = strings.TrimSpace(out)
		// When resuming, the harness may reject the session ref and exit immediately
		// (e.g. "No session found matching '...'"). tmux new-window always succeeds
		// even if the launched process exits instantly, so verify the window survived
		// startup before registering the session as active.
		if resuming {
			checkAfter := m.ResumeCheckAfter
			if checkAfter <= 0 {
				checkAfter = defaultResumeCheckAfter
			}
			if liveErr := m.waitWindowLive(ctx, windowID, name, checkAfter); liveErr != nil {
				return ResumeFailedError{Ticket: ticket, Err: liveErr}
			}
		}
	} else {
		var err error
		windowID, err = m.windowIDByName(ctx, name)
		if err != nil {
			return err
		}
	}

	ses := storage.Session{
		TicketID:        ticket.ID,
		Harness:         ticket.Harness,
		TmuxSessionName: m.Config.TmuxSession,
		TmuxWindowName:  name,
		Status:          "running",
	}
	if windowID != "" {
		ses.TmuxWindowID = sql.NullString{String: windowID, Valid: true}
	}
	if ticket.SessionRef.Valid {
		ses.HarnessSessionRef = sql.NullString{String: ticket.SessionRef.String, Valid: true}
	} else if promptAlreadySent && m.Store != nil {
		if ref, ok := m.captureSessionRef(ctx, ticket.Harness, renderedPrompt, launchStartedAt); ok {
			ses.HarnessSessionRef = sql.NullString{String: ref, Valid: true}
		}
	}
	if m.Store != nil && (launchedNew || !ticket.SessionID.Valid || !ticket.SessionActive) {
		if _, err := m.Store.UpsertActiveSession(ctx, ticket.ID, ses); err != nil {
			return err
		}
	}
	if sendPrompt && !promptAlreadySent {
		ready := harness.PromptReadyPattern(m.Config, ticket.Harness)
		if err := m.WaitAndPastePrompt(ctx, name, renderedPrompt, ready, m.Config.PromptReadyTimeout); err != nil {
			return PromptReadyError{WindowName: name, Prompt: renderedPrompt, Ready: ready, Err: err}
		}
		if m.Store != nil {
			out, _ := m.CapturePane(ctx, name)
			if ref, ok := harness.ParseSessionRef(out, harness.SessionRefPattern(m.Config, ticket.Harness)); ok {
				if ses, active, err := m.Store.ActiveSession(ctx, ticket.ID); err == nil && active {
					_ = m.Store.UpdateSessionRef(ctx, ses.ID, ref)
				}
			}
		}
	}
	return nil
}

func (m *Manager) StartFreshTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	ticket.SessionID = sql.NullInt64{}
	ticket.SessionRef = sql.NullString{}
	ticket.WindowID = sql.NullString{}
	ticket.WindowName = sql.NullString{}
	return m.OpenTicket(ctx, ticket, sendPrompt)
}

func (m *Manager) SwitchToTicket(ctx context.Context, ticket storage.Ticket) error {
	name := WindowName(ticket.DisplayID, ticket.Title)
	if ticket.SessionID.Valid && !ticket.SessionActive {
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return m.OpenTicket(ctx, ticket, false)
		}
		return RepairNeededError{Ticket: ticket, Reason: "ticket has no active window and no session ref is known"}
	}
	ref, exists, err := m.ticketWindowRef(ctx, ticket, name)
	if err != nil {
		return err
	}
	if !exists {
		if ticket.SessionID.Valid && !ticket.SessionRef.Valid {
			return RepairNeededError{Ticket: ticket, Reason: "tmux window is missing and no session ref is known"}
		}
		return m.OpenTicket(ctx, ticket, false)
	}
	_, err = m.run(ctx, "switch-client", "-t", targetRef(m.Config.TmuxSession, ref))
	return err
}

func (m *Manager) RenameTicketWindow(ctx context.Context, ticket storage.Ticket, title string) error {
	oldName := WindowName(ticket.DisplayID, ticket.Title)
	newName := WindowName(ticket.DisplayID, title)
	ref, exists, err := m.ticketWindowRef(ctx, ticket, oldName)
	if err != nil || !exists {
		return err
	}
	if _, err := m.run(ctx, "rename-window", "-t", targetRef(m.Config.TmuxSession, ref), newName); err != nil {
		return err
	}
	if m.Store != nil {
		return m.Store.RenameSessionWindow(ctx, ticket.ID, newName)
	}
	return nil
}

func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.EnsureSession(ctx); err != nil {
		return err
	}
	if m.Store == nil {
		return nil
	}
	tickets, err := m.Store.ListTickets(ctx, false)
	if err != nil {
		return err
	}
	for _, ticket := range tickets {
		sessionID := ticket.SessionID
		if !sessionID.Valid || !ticket.SessionActive || !ticket.WindowName.Valid {
			continue
		}
		_, exists, err := m.ticketWindowRef(ctx, ticket, ticket.WindowName.String)
		if err != nil {
			return err
		}
		if !exists {
			if err := m.Store.MarkSessionMissing(ctx, sessionID.Int64); err != nil {
				return err
			}
			continue
		}
		_ = m.Store.UpdateSessionRuntime(ctx, sessionID.Int64, ticket.Runtime, "tmux", "", "", false)
	}
	return nil
}

func (m *Manager) RefreshRuntime(ctx context.Context) error {
	if m.Store == nil {
		return nil
	}
	tickets, err := m.Store.ListTickets(ctx, false)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, ticket := range tickets {
		if !ticket.SessionID.Valid || !ticket.SessionActive {
			continue
		}
		ses, ok, err := m.Store.ActiveSession(ctx, ticket.ID)
		if err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		ref, exists, err := m.ticketWindowRef(ctx, ticket, ses.TmuxWindowName)
		if err != nil {
			return err
		}
		if !exists {
			if err := m.Store.MarkSessionMissing(ctx, ses.ID); err != nil {
				return err
			}
			continue
		}
		out, err := m.capturePaneRef(ctx, ref)
		if err != nil {
			if err := m.Store.UpdateSessionRuntime(ctx, ses.ID, "error", "tmux", err.Error(), "", false); err != nil {
				return err
			}
			continue
		}
		lastActivity := ses.LastOutputAt
		if !lastActivity.Valid {
			lastActivity = ses.LastStateChangeAt
		}
		var idleFor time.Duration
		if lastActivity.Valid && now.Sub(lastActivity.Time) >= m.Config.IdleUnknownAfter {
			idleFor = now.Sub(lastActivity.Time)
		}
		state, source, reason, excerpt, changed := harness.DetectState(out, ses.LastObservedExcerpt.String, idleFor)
		stateChanged := state != ses.Status
		if ses.LastDetectionSource.String == "manual" && source != "pattern" {
			state = ses.Status
			source = "manual"
			reason = ses.LastAttentionReason.String
			stateChanged = false
		}
		if err := m.Store.UpdateSessionRuntime(ctx, ses.ID, state, source, reason, excerpt, changed); err != nil {
			return err
		}
		if isAutoCloseEligible(state) && !stateChanged {
			ageBase := ses.LastStateChangeAt
			if !ageBase.Valid {
				ageBase = sql.NullTime{Time: now, Valid: true}
			}
			if now.Sub(ageBase.Time) >= m.Config.AutoCloseWaitingAfter {
				if err := m.CloseSession(ctx, ticket); err != nil {
					_ = m.Store.UpdateSessionRuntime(ctx, ses.ID, "error", "tmux", err.Error(), excerpt, false)
				}
			}
		}
	}
	return nil
}

func (m *Manager) CloseSession(ctx context.Context, ticket storage.Ticket) error {
	if m.Store == nil {
		return nil
	}
	ses, ok, err := m.Store.ActiveSession(ctx, ticket.ID)
	if err != nil || !ok {
		return err
	}
	ref, exists, err := m.ticketWindowRef(ctx, ticket, ses.TmuxWindowName)
	if err != nil {
		return err
	}
	if !exists {
		return m.Store.MarkSessionMissing(ctx, ses.ID)
	}
	if err := m.Store.UpdateSessionRuntime(ctx, ses.ID, "closing", "system", "graceful close requested", "", false); err != nil {
		return err
	}
	for _, key := range harness.ExitKeys(m.Config, ses.Harness) {
		if _, err := m.run(ctx, "send-keys", "-t", targetRef(m.Config.TmuxSession, ref), key); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(m.Config.GracefulExitTimeout)
	for time.Now().Before(deadline) {
		exists, err := m.windowRefExists(ctx, ref)
		if err != nil {
			return err
		}
		if !exists {
			return m.Store.MarkSessionClosed(ctx, ses.ID, "closed", "tmux", "window exited")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := m.run(ctx, "kill-window", "-t", targetRef(m.Config.TmuxSession, ref)); err != nil {
		return err
	}
	return m.Store.MarkSessionClosed(ctx, ses.ID, "closed", "tmux", "graceful exit timed out; window closed")
}

func (m *Manager) WaitAndPastePrompt(ctx context.Context, windowName, text, ready string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, _ := m.CapturePane(ctx, windowName)
		if strings.Contains(out, ready) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("prompt readiness timeout waiting for %q", ready)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := m.run(ctx, "set-buffer", "--", text); err != nil {
		return err
	}
	if _, err := m.run(ctx, "paste-buffer", "-t", target(m.Config.TmuxSession, windowName)); err != nil {
		return err
	}
	_, err := m.run(ctx, "send-keys", "-t", target(m.Config.TmuxSession, windowName), "Enter")
	return err
}

func (m *Manager) CapturePane(ctx context.Context, windowName string) (string, error) {
	return m.run(ctx, "capture-pane", "-p", "-t", target(m.Config.TmuxSession, windowName))
}

func (m *Manager) PastePromptNow(ctx context.Context, windowName, text string) error {
	if _, err := m.run(ctx, "set-buffer", "--", text); err != nil {
		return err
	}
	if _, err := m.run(ctx, "paste-buffer", "-t", target(m.Config.TmuxSession, windowName)); err != nil {
		return err
	}
	_, err := m.run(ctx, "send-keys", "-t", target(m.Config.TmuxSession, windowName), "Enter")
	return err
}

// waitWindowLive waits a fixed interval after launching a resume window, then
// checks whether the window is still present. tmux new-window succeeds regardless
// of whether the launched process survives, so we must verify liveness separately.
//
// If the window is gone at check time, the harness rejected the session ref
// (e.g. "No session found matching '...'") and we return an error so the
// caller can surface a ResumeFailedError instead of recording a phantom session.
//
// The wait duration must exceed the typical time a failing harness takes to
// display its error and exit. For pi, a bad --session ref causes exit in ~1.8s.
func (m *Manager) waitWindowLive(ctx context.Context, windowID, windowName string, checkAfter time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(checkAfter):
	}
	exists, _ := m.windowRefExists(ctx, windowID)
	if !exists {
		// Fallback: check by name in case window ID is stale.
		exists, _ = m.windowExists(ctx, windowName)
	}
	if !exists {
		return fmt.Errorf("window exited immediately (harness may have rejected the session ref)")
	}
	return nil
}

func (m *Manager) capturePaneRef(ctx context.Context, ref string) (string, error) {
	return m.run(ctx, "capture-pane", "-p", "-t", targetRef(m.Config.TmuxSession, ref))
}

func (m *Manager) windowExists(ctx context.Context, windowName string) (bool, error) {
	_, err := m.windowIDByName(ctx, windowName)
	if errors.Is(err, ErrWindowMissing) {
		return false, nil
	}
	return err == nil, err
}

func (m *Manager) windowIDByName(ctx context.Context, windowName string) (string, error) {
	out, err := m.run(ctx, "list-windows", "-t", m.Config.TmuxSession, "-F", "#{window_name}")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if line == windowName {
			return m.displayWindowID(ctx, windowName)
		}
	}
	return "", ErrWindowMissing
}

func (m *Manager) displayWindowID(ctx context.Context, windowName string) (string, error) {
	out, err := m.run(ctx, "display-message", "-p", "-t", target(m.Config.TmuxSession, windowName), "#{window_id}")
	return strings.TrimSpace(out), err
}

func (m *Manager) windowRefExists(ctx context.Context, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	if strings.HasPrefix(ref, "@") {
		out, err := m.run(ctx, "display-message", "-p", "-t", ref, "#{window_id}")
		if err != nil {
			return false, nil
		}
		// tmux returns exit 0 with empty output when the window ID no longer exists.
		return strings.TrimSpace(out) != "", nil
	}
	return m.windowExists(ctx, ref)
}

func (m *Manager) ticketWindowRef(ctx context.Context, ticket storage.Ticket, fallbackName string) (string, bool, error) {
	expectedName := fallbackName
	if ticket.WindowName.Valid && ticket.WindowName.String != "" {
		expectedName = ticket.WindowName.String
	}
	if ticket.SessionActive && ticket.WindowID.Valid && ticket.WindowID.String != "" {
		actualName, exists, err := m.windowNameByID(ctx, ticket.WindowID.String)
		if err != nil {
			return "", false, err
		}
		if exists && actualName == expectedName {
			return ticket.WindowID.String, true, nil
		}
	}
	if expectedName == "" {
		return "", false, nil
	}
	exists, err := m.windowExists(ctx, expectedName)
	if err != nil {
		return "", false, err
	}
	return expectedName, exists, nil
}

func (m *Manager) windowNameByID(ctx context.Context, id string) (string, bool, error) {
	out, err := m.run(ctx, "display-message", "-p", "-t", id, "#{window_name}")
	if err != nil {
		return "", false, nil
	}
	return strings.TrimSpace(out), true, nil
}

// KillSession kills the entire tmux session, closing all windows.
func (m *Manager) KillSession(ctx context.Context) error {
	_, err := m.run(ctx, "kill-session", "-t", m.Config.TmuxSession)
	return err
}

func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	return m.Runner.Run(ctx, "tmux", args...)
}

func (m *Manager) captureSessionRef(ctx context.Context, harnessName, promptText string, since time.Time) (string, bool) {
	// Copilot writes to session-store.db asynchronously and may take several
	// seconds after process start. Pi writes its JSONL file asynchronously too.
	// Use a longer deadline for these harnesses.
	deadline := 2 * time.Second
	switch harnessName {
	case "copilot", "pi":
		deadline = 8 * time.Second
	}
	end := time.Now().Add(deadline)
	for {
		ref, ok := harness.CaptureSessionRef(m.Config, harnessName, promptText, since)
		if ok {
			return ref, true
		}
		if time.Now().After(end) {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func WindowName(displayID, title string) string {
	return strings.Trim(displayID+"-"+slug(title), "-")
}

func ShellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func newWindowArgs(session, name string) []string {
	args := []string{"new-window", "-d", "-P", "-F", "#{window_id}"}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, "-t", session, "-n", name)
	return args
}

func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

func AttachCommand(cfg config.Config, exe string) *exec.Cmd {
	board := cfg.Tmux.BoardWindowName
	if board == "" {
		board = "board"
	}
	cmd := exec.Command("tmux", "new-session", "-A", "-s", cfg.TmuxSession, "-n", board, "env", "AGENT_KANBAN_INNER=1", exe, "--board")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func isAutoCloseEligible(state string) bool {
	return state == "waiting_for_user" || state == "needs_permission" || state == "exited"
}

func target(session, window string) string {
	return session + ":" + window
}

func targetRef(session, ref string) string {
	if strings.HasPrefix(ref, "@") {
		return ref
	}
	return target(session, ref)
}

func windowRef(ticket storage.Ticket, fallbackName string) string {
	if ticket.WindowID.Valid && ticket.WindowID.String != "" {
		return ticket.WindowID.String
	}
	return fallbackName
}

func slug(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		return "ticket"
	}
	return s
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./:", r))
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var ErrWindowMissing = errors.New("tmux window missing")
