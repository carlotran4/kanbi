package tmux

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"kanbi/internal/config"
	"kanbi/internal/harness"
	"kanbi/internal/kanban"
	"kanbi/internal/prompt"
	"kanbi/internal/storage"
)

var ErrPromptAlreadySent = errors.New("prompt already sent; open session instead")

const defaultResumeCheckAfter = 2500 * time.Millisecond

var boardSessionSeq atomic.Uint64

//go:embed pi_session_ref_extension.ts
var piSessionRefExtension string

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
	exists, err := m.windowExistsInSession(ctx, m.Config.TmuxSession, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = m.run(ctx, newWindowArgs(m.Config.TmuxSession, name, "")...)
	return err
}

func (m *Manager) OpenTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	return m.lifecycle().Execute(ctx, lifecycleRequest{Ticket: ticket, SendPrompt: sendPrompt})
}

func (m *Manager) StartFreshTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	return m.lifecycle().Execute(ctx, lifecycleRequest{Ticket: ticket, SendPrompt: sendPrompt, StartFresh: true})
}

func (m *Manager) SwitchToTicket(ctx context.Context, ticket storage.Ticket) error {
	return m.OpenTicket(ctx, ticket, false)
}

func (m *Manager) recoverMissingSessionRef(ctx context.Context, ticket storage.Ticket) (storage.Ticket, error) {
	if m.Store == nil || !ticket.SessionID.Valid {
		return ticket, nil
	}
	promptText := prompt.Render(ticket.DisplayID, ticket.Title, ticket.Body)
	if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
		if harness.ValidateSessionRef(ticket.Harness, ticket.SessionRef.String, promptText) {
			return ticket, nil
		}
		if err := m.Store.UpdateSessionRef(ctx, ticket.SessionID.Int64, ""); err != nil {
			return ticket, err
		}
		ticket.SessionRef = sql.NullString{}
	}
	ses, ok, err := m.Store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || !ses.StartedAt.Valid || ses.HarnessSessionRef.Valid {
		return ticket, err
	}
	// Check the stable ref file first (written by the bundled Pi extension at session start).
	if ticket.Harness == "pi" {
		refFilePath := m.piSessionRefFilePath(ticket.ID)
		if ref, ok := readPiSessionRefFile(refFilePath); ok {
			if err := m.Store.UpdateSessionRef(ctx, ses.ID, ref); err != nil {
				return ticket, err
			}
			ticket.SessionRef = sql.NullString{String: ref, Valid: true}
			return ticket, nil
		}
	}
	ref, found := harness.CaptureSessionRef(ticket.Harness, promptText, ses.StartedAt.Time)
	if !found {
		return ticket, nil
	}
	if err := m.Store.UpdateSessionRef(ctx, ses.ID, ref); err != nil {
		return ticket, err
	}
	ticket.SessionRef = sql.NullString{String: ref, Valid: true}
	return ticket, nil
}

func (m *Manager) RenameTicketWindow(ctx context.Context, ticket storage.Ticket, title string) error {
	oldName := TicketWindowName(ticket)
	updated := ticket
	updated.Title = title
	newName := TicketWindowName(updated)
	ref, exists, err := m.ticketWindowRef(ctx, ticket, oldName)
	if err != nil || !exists {
		return err
	}
	if _, err := m.run(ctx, "rename-window", "-t", targetRef(ticketRuntimeSessionName(m.Config.TmuxSession, ticket), ref), newName); err != nil {
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
		ticket, err = m.recoverMissingSessionRef(ctx, ticket)
		if err != nil {
			return err
		}
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
		ref, exists, err := m.ticketWindowRefInSession(ctx, ses.TmuxSessionName, ticket, ses.TmuxWindowName)
		if err != nil {
			return err
		}
		if !exists {
			if err := m.Store.MarkSessionMissing(ctx, ses.ID); err != nil {
				return err
			}
			continue
		}
		out, err := m.capturePaneRef(ctx, ses.TmuxSessionName, ref)
		if err != nil {
			if err := m.Store.UpdateSessionRuntime(ctx, ses.ID, kanban.StateError, "tmux", err.Error(), "", false); err != nil {
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
					_ = m.Store.UpdateSessionRuntime(ctx, ses.ID, kanban.StateError, "tmux", err.Error(), excerpt, false)
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
	ref, exists, err := m.ticketWindowRefInSession(ctx, ses.TmuxSessionName, ticket, ses.TmuxWindowName)
	if err != nil {
		return err
	}
	if !exists {
		return m.Store.MarkSessionMissing(ctx, ses.ID)
	}
	if err := m.Store.UpdateSessionRuntime(ctx, ses.ID, kanban.StateClosing, "system", "graceful close requested", "", false); err != nil {
		return err
	}
	for _, key := range harness.ExitKeys(m.Config.Harnesses, ses.Harness) {
		if _, err := m.run(ctx, "send-keys", "-t", targetRef(ses.TmuxSessionName, ref), key); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(m.Config.GracefulExitTimeout)
	for time.Now().Before(deadline) {
		exists, err := m.windowRefExistsInSession(ctx, ses.TmuxSessionName, ref)
		if err != nil {
			return err
		}
		if !exists {
			return m.Store.MarkSessionClosed(ctx, ses.ID, kanban.StateClosed, "tmux", "window exited")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := m.run(ctx, "kill-window", "-t", targetRef(ses.TmuxSessionName, ref)); err != nil {
		return err
	}
	return m.Store.MarkSessionClosed(ctx, ses.ID, kanban.StateClosed, "tmux", "graceful exit timed out; window closed")
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
	return m.capturePaneInSession(ctx, m.Config.TmuxSession, windowName)
}

func (m *Manager) capturePaneInSession(ctx context.Context, sessionName, windowName string) (string, error) {
	return m.run(ctx, "capture-pane", "-p", "-t", target(sessionName, windowName))
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
func (m *Manager) waitWindowLive(ctx context.Context, sessionName, windowID, windowName string, checkAfter time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(checkAfter):
	}
	exists, _ := m.windowRefExistsInSession(ctx, sessionName, windowID)
	if !exists {
		// Fallback: check by name in case window ID is stale.
		exists, _ = m.windowExistsInSession(ctx, sessionName, windowName)
	}
	if !exists {
		return fmt.Errorf("window exited immediately (harness may have rejected the session ref)")
	}
	return nil
}

func (m *Manager) capturePaneRef(ctx context.Context, sessionName, ref string) (string, error) {
	return m.run(ctx, "capture-pane", "-p", "-t", targetRef(sessionName, ref))
}

func (m *Manager) windowExists(ctx context.Context, windowName string) (bool, error) {
	return m.windowExistsInSession(ctx, m.Config.TmuxSession, windowName)
}

func (m *Manager) windowExistsInSession(ctx context.Context, sessionName, windowName string) (bool, error) {
	_, err := m.windowIDByNameInSession(ctx, sessionName, windowName)
	if errors.Is(err, ErrWindowMissing) {
		return false, nil
	}
	return err == nil, err
}

func (m *Manager) availableWindowName(ctx context.Context, base string) (string, error) {
	return m.availableWindowNameInSession(ctx, m.Config.TmuxSession, base)
}

func (m *Manager) availableWindowNameInSession(ctx context.Context, sessionName, base string) (string, error) {
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i+1)
		}
		exists, err := m.windowExistsInSession(ctx, sessionName, name)
		if err != nil {
			return "", err
		}
		if !exists {
			return name, nil
		}
	}
}

func (m *Manager) windowIDByName(ctx context.Context, windowName string) (string, error) {
	return m.windowIDByNameInSession(ctx, m.Config.TmuxSession, windowName)
}

func (m *Manager) windowIDByNameInSession(ctx context.Context, sessionName, windowName string) (string, error) {
	out, err := m.run(ctx, "list-windows", "-t", sessionName, "-F", "#{window_name}")
	if err != nil {
		if isTmuxMissingTarget(out, err) {
			return "", ErrWindowMissing
		}
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if line == windowName {
			return m.displayWindowIDInSession(ctx, sessionName, windowName)
		}
	}
	return "", ErrWindowMissing
}

func (m *Manager) displayWindowID(ctx context.Context, windowName string) (string, error) {
	return m.displayWindowIDInSession(ctx, m.Config.TmuxSession, windowName)
}

func (m *Manager) displayWindowIDInSession(ctx context.Context, sessionName, windowName string) (string, error) {
	out, err := m.run(ctx, "display-message", "-p", "-t", target(sessionName, windowName), "#{window_id}")
	return strings.TrimSpace(out), err
}

func (m *Manager) windowRefExistsInSession(ctx context.Context, sessionName, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	if strings.HasPrefix(ref, "@") {
		out, err := m.run(ctx, "display-message", "-p", "-t", targetRef(sessionName, ref), "#{window_id}")
		if err != nil {
			return false, nil
		}
		// tmux returns exit 0 with empty output when the window ID no longer exists.
		return strings.TrimSpace(out) != "", nil
	}
	return m.windowExistsInSession(ctx, sessionName, ref)
}

func (m *Manager) ticketWindowRef(ctx context.Context, ticket storage.Ticket, fallbackName string) (string, bool, error) {
	return m.ticketWindowRefInSession(ctx, ticketRuntimeSessionName(m.Config.TmuxSession, ticket), ticket, fallbackName)
}

func (m *Manager) ticketWindowRefInSession(ctx context.Context, sessionName string, ticket storage.Ticket, fallbackName string) (string, bool, error) {
	expectedName := fallbackName
	if ticket.WindowName.Valid && ticket.WindowName.String != "" {
		expectedName = ticket.WindowName.String
	}
	if ticket.SessionActive && ticket.WindowID.Valid && ticket.WindowID.String != "" {
		actualName, exists, err := m.windowNameByIDInSession(ctx, sessionName, ticket.WindowID.String)
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
	exists, err := m.windowExistsInSession(ctx, sessionName, expectedName)
	if err != nil {
		return "", false, err
	}
	return expectedName, exists, nil
}

func (m *Manager) windowNameByID(ctx context.Context, id string) (string, bool, error) {
	return m.windowNameByIDInSession(ctx, m.Config.TmuxSession, id)
}

func (m *Manager) windowNameByIDInSession(ctx context.Context, sessionName, id string) (string, bool, error) {
	out, err := m.run(ctx, "display-message", "-p", "-t", targetRef(sessionName, id), "#{window_name}")
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
	out, err := m.Runner.Run(ctx, "tmux", args...)
	if err != nil && strings.TrimSpace(out) != "" {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
	}
	return out, err
}

func isTmuxMissingTarget(out string, err error) bool {
	if err == nil {
		return false
	}
	combined := strings.ToLower(strings.TrimSpace(out + " " + err.Error()))
	return strings.Contains(combined, "can't find session") || strings.Contains(combined, "can't find window")
}

func (m *Manager) commandWithPiSessionRefCapture(ticket storage.Ticket, command []string) ([]string, string, error) {
	extPath, err := m.ensurePiSessionRefExtension()
	if err != nil {
		return nil, "", err
	}
	// Use a stable per-ticket path (no timestamp) so recoverMissingSessionRef can find it later.
	refFile := m.piSessionRefFilePath(ticket.ID)
	out := make([]string, 0, len(command)+5)
	out = append(out,
		"env",
		"KANBI_SESSION_REF_FILE="+refFile,
		"KANBI_TICKET_ID="+fmt.Sprint(ticket.ID),
	)
	if len(command) == 0 {
		return nil, "", errors.New("pi command is empty")
	}
	out = append(out, command[0], "-e", extPath)
	out = append(out, command[1:]...)
	return out, refFile, nil
}

// piSessionRefFilePath returns the stable ref file path for a given ticket ID.
// Using a stable (non-timestamped) path lets recovery code reconstruct the path
// from the ticket ID alone.
func (m *Manager) piSessionRefFilePath(ticketID int64) string {
	return filepath.Join(m.stateDir(), "pi-session-refs", fmt.Sprintf("ticket-%d.json", ticketID))
}

func (m *Manager) ensurePiSessionRefExtension() (string, error) {
	path := filepath.Join(m.stateDir(), "pi-session-ref-extension.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(piSessionRefExtension), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) stateDir() string {
	if m.Config.Paths.StateDir != "" {
		return m.Config.Paths.StateDir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "kanbi")
	}
	return filepath.Join(home, ".local", "state", "kanbi")
}

type piSessionRefPayload struct {
	SessionID string `json:"sessionId"`
}

func readPiSessionRefFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var payload piSessionRefPayload
	if err := json.Unmarshal(data, &payload); err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return "", false
	}
	return strings.TrimSpace(payload.SessionID), true
}

func (m *Manager) captureSessionRef(ctx context.Context, harnessName, promptText string, since time.Time, refFile string) (string, bool) {
	if harnessName == "pi" && refFile != "" {
		if ref, ok := readPiSessionRefFile(refFile); ok {
			return ref, true
		}
	}
	// Copilot writes to session-store.db asynchronously and may take several
	// seconds after process start. Pi writes its JSONL file asynchronously too.
	// Use a longer deadline for these harnesses.
	deadline := 2 * time.Second
	switch harnessName {
	case "copilot", "pi":
		deadline = 20 * time.Second
	}
	end := time.Now().Add(deadline)
	for {
		if harnessName == "pi" && refFile != "" {
			if ref, ok := readPiSessionRefFile(refFile); ok {
				return ref, true
			}
		}
		ref, ok := harness.CaptureSessionRef(harnessName, promptText, since)
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

func TicketWindowName(ticket storage.Ticket) string {
	prefix := ""
	if ticket.BoardID != 0 {
		prefix = fmt.Sprintf("b%d-", ticket.BoardID)
	}
	return strings.Trim(prefix+WindowName(ticket.DisplayID, ticket.Title), "-")
}

func ShellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func newWindowArgs(session, name, cwd string) []string {
	args := []string{"new-window", "-d", "-P", "-F", "#{window_id}"}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, "-t", session, "-n", name)
	return args
}

func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

func CurrentSessionName(ctx context.Context) (string, error) {
	out, err := ExecRunner{}.Run(ctx, "tmux", "display-message", "-p", "#S")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func AttachCommand(cfg config.Config, exe string) *exec.Cmd {
	board := cfg.Tmux.BoardWindowName
	if board == "" {
		board = "board"
	}
	sessionName := boardClientSessionName(cfg.TmuxSession)
	cmd := exec.Command("tmux", "new-session", "-s", sessionName, "-n", board, "env", "KANBI_INNER=1", "KANBI_TMUX_SESSION="+sessionName, exe, "--board")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func boardClientSessionName(mainSession string) string {
	if mainSession == "" {
		mainSession = config.DefaultSession
	}
	return fmt.Sprintf("%s-board-%d-%d-%d", mainSession, os.Getpid(), time.Now().UnixNano(), boardSessionSeq.Add(1))
}

func isAutoCloseEligible(state string) bool {
	return state == kanban.StateWaitingForUser || state == kanban.StateNeedsPermission || state == kanban.StateExited
}

func target(session, window string) string {
	return session + ":" + window
}

func targetRef(session, ref string) string {
	if ref == "" || session == "" {
		return ref
	}
	if strings.Contains(ref, ":") {
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

func ticketRuntimeSessionName(defaultSession string, ticket storage.Ticket) string {
	if ticket.TmuxSessionName.Valid && ticket.TmuxSessionName.String != "" {
		return ticket.TmuxSessionName.String
	}
	return defaultSession
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
