package runtime

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/harness"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	herdrmux "github.com/carlotran4/kanbi/internal/multiplexer/herdr"
	"github.com/carlotran4/kanbi/internal/prompt"
	"github.com/carlotran4/kanbi/internal/session"
	"github.com/carlotran4/kanbi/internal/storage"
	workspacepkg "github.com/carlotran4/kanbi/internal/workspace"
	"golang.org/x/sys/unix"
)

var ErrPromptAlreadySent = session.ErrPromptAlreadySent

const defaultResumeCheckAfter = 2500 * time.Millisecond

//go:embed pi_session_ref_extension.ts
var piSessionRefExtension string

type RepairNeededError = session.RepairNeededError
type ResumeFailedError = session.ResumeFailedError
type PromptReadyError = session.PromptReadyError

type Manager struct {
	Config config.Config
	Store  *storage.Store

	// BackgroundError receives asynchronous session-ref persistence failures.
	// When nil, failures are written through the standard logger.
	BackgroundError func(error)
	// RefCapturePollInterval is configurable for deterministic tests. Zero uses
	// the production default.
	RefCapturePollInterval time.Duration

	background *backgroundState

	// ResumeCheckAfter is how long to wait after launching a resume command
	// before deciding whether the Herdr container survived startup. Zero uses the
	// production default.
	ResumeCheckAfter time.Duration

	herdrWorkspaceOnce sync.Once
	herdrWorkspaceID   string

	// WorkspaceService may be injected by tests. Production lazily constructs
	// the Git workspace service from Config.Paths and Store.
	WorkspaceService      *workspacepkg.Service
	workspaceOnce         sync.Once
	runtimeMu             sync.Mutex
	workspaceObservations map[int64]workspaceObservation
	workspaceCursor       int
}

func NewManager(cfg config.Config, store *storage.Store) *Manager {
	return NewManagerWithContext(context.Background(), cfg, store)
}

// NewManagerWithContext ties asynchronous manager work to the application
// context. Call Close before closing the store to cancel and join that work.
func NewManagerWithContext(ctx context.Context, cfg config.Config, store *storage.Store) *Manager {
	return &Manager{Config: cfg, Store: store, ResumeCheckAfter: defaultResumeCheckAfter, background: newBackgroundState(ctx)}
}

func (m *Manager) workspaceService() *workspacepkg.Service {
	m.workspaceOnce.Do(func() {
		if m.WorkspaceService == nil && m.Store != nil {
			m.WorkspaceService = &workspacepkg.Service{Store: m.Store, StateDir: m.Config.Paths.StateDir}
		}
	})
	return m.WorkspaceService
}

// PreflightTicketWorkspace performs read-only Git checks for the branch modal.
func (m *Manager) PreflightTicketWorkspace(ctx context.Context, ticket storage.Ticket, branch string) (storage.WorkspacePreflight, error) {
	service := m.workspaceService()
	if service == nil {
		return storage.WorkspacePreflight{}, errors.New("workspace storage is unavailable")
	}
	return service.Preflight(ctx, workspacepkg.ProvisionOptions{
		BoardID: ticket.BoardID, BoardUUID: ticket.BoardUUID, BoardCWD: ticket.BoardWorkdir,
		TicketID: ticket.ID, Branch: branch,
	})
}

// PrepareTicketWorkspace provisions a branch only after explicit TUI confirmation.
func (m *Manager) PrepareTicketWorkspace(ctx context.Context, ticket storage.Ticket, branch string, existing bool) error {
	service := m.workspaceService()
	if service == nil {
		return errors.New("workspace storage is unavailable")
	}
	_, err := service.Provision(ctx, workspacepkg.ProvisionOptions{
		BoardID: ticket.BoardID, BoardUUID: ticket.BoardUUID, BoardCWD: ticket.BoardWorkdir,
		TicketID: ticket.ID, Branch: branch, ExistingBranch: existing,
	})
	return err
}

func (m *Manager) ResolveTicketWorkspace(ctx context.Context, ticket storage.Ticket) error {
	workspace, ok, err := m.Store.CurrentWorkspace(ctx, ticket.ID)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("ticket has no current workspace")
	}
	if ticket.SessionActive {
		if err := m.CloseSession(ctx, ticket); err != nil {
			return err
		}
	}
	obs, mergeErr := m.workspaceService().Resolve(ctx, workspace)
	if mergeErr != nil && len(obs.Conflicts) == 0 {
		return mergeErr
	}
	fresh, err := m.Store.TicketByID(ctx, ticket.ID)
	if err != nil {
		return err
	}
	if err := m.OpenTicket(ctx, fresh, false); err != nil {
		return err
	}
	fresh, err = m.Store.TicketByID(ctx, ticket.ID)
	if err != nil {
		return err
	}
	ref := ContainerRefFromTicket(fresh)
	adapter, err := m.multiplexerAdapter(ref.Kind)
	if err != nil {
		return err
	}
	message := "Resolve the in-progress Git merge conflicts in this ticket worktree. Conflicting files: " + strings.Join(obs.Conflicts, ", ") + ". Run relevant checks and commit the resolution. Do not merge into the source branch."
	if err := adapter.SendText(ctx, ref, message); err != nil {
		return err
	}
	return adapter.SendKeys(ctx, ref, "Enter")
}

func (m *Manager) IntegrateTicketWorkspace(ctx context.Context, ticket storage.Ticket) error {
	workspace, ok, err := m.Store.CurrentWorkspace(ctx, ticket.ID)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("ticket has no current workspace")
	}
	return m.workspaceService().Integrate(ctx, workspace, workspacepkg.IntegrateOptions{Close: func(ctx context.Context) error {
		fresh, err := m.Store.TicketByID(ctx, ticket.ID)
		if err != nil {
			return err
		}
		if !fresh.SessionActive {
			return nil
		}
		return m.CloseSession(ctx, fresh)
	}})
}

func ticketLaunchCWD(ticket storage.Ticket) string {
	if ticket.SessionLaunchCWD.Valid && strings.TrimSpace(ticket.SessionLaunchCWD.String) != "" {
		return ticket.SessionLaunchCWD.String
	}
	if ticket.WorkspaceLaunchCWD.Valid && strings.TrimSpace(ticket.WorkspaceLaunchCWD.String) != "" {
		return ticket.WorkspaceLaunchCWD.String
	}
	return ticket.BoardWorkdir
}

func (m *Manager) defaultMultiplexerKind() multiplexer.Kind { return multiplexer.KindHerdr }

func (m *Manager) herdrAdapter() *herdrmux.Adapter {
	cfg := m.Config.Multiplexer.Herdr
	return herdrmux.NewAdapter(herdrmux.Config{Binary: cfg.Binary, Session: cfg.Session, WorkspaceStrategy: cfg.WorkspaceStrategy, FocusOnOpen: cfg.FocusOnOpen})
}

func (m *Manager) multiplexerAdapter(kind multiplexer.Kind) (multiplexer.Interface, error) {
	if kind != multiplexer.KindHerdr {
		return nil, fmt.Errorf("runtime %q is retired or unsupported; close its terminal externally and start a new Herdr attempt", kind)
	}
	return m.herdrAdapter(), nil
}

func (m *Manager) currentHerdrWorkspace(ctx context.Context) string {
	m.herdrWorkspaceOnce.Do(func() {
		if id, err := m.herdrAdapter().CurrentWorkspace(ctx); err == nil {
			m.herdrWorkspaceID = id
		}
	})
	return m.herdrWorkspaceID
}

func (m *Manager) EnsureSession(ctx context.Context) error { return m.herdrAdapter().Ensure(ctx, "") }

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
	if m.Store == nil || !ticket.SessionID.Valid || !m.nativeHarness(ticket.Harness) || (ticket.SessionActive && ContainerRefFromTicket(ticket).Kind != multiplexer.KindHerdr) {
		return ticket, nil
	}
	promptText := prompt.Render(ticket.DisplayID, ticket.Title, ticket.Body)
	cwd := ticketLaunchCWD(ticket)
	if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
		if harness.ValidateSessionRefInCWD(ticket.Harness, ticket.SessionRef.String, promptText, cwd) {
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
		if ref, ok := readPiSessionRefFile(refFilePath, ""); ok {
			if err := m.Store.UpdateSessionRef(ctx, ses.ID, ref); err != nil {
				return ticket, err
			}
			ticket.SessionRef = sql.NullString{String: ref, Valid: true}
			return ticket, nil
		}
	}
	// Codex capture correlates history with rollout metadata during launch.
	// Do not retry it later: newer identical prompts can make an old launch
	// ambiguous even when cwd and timestamps are checked.
	if ticket.Harness == "codex" {
		return ticket, nil
	}
	ref, found := harness.CaptureSessionRefInCWD(ticket.Harness, promptText, cwd, ses.StartedAt.Time)
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
	if !ticket.SessionActive {
		return nil
	}
	ref := ContainerRefFromTicket(ticket)
	if ref.Kind != multiplexer.KindHerdr {
		return nil
	}
	valid, err := m.herdrAdapter().Validate(ctx, ref)
	if err != nil || !valid {
		return err
	}
	updated := ticket
	updated.Title = title
	if err := m.herdrAdapter().Rename(ctx, ref, TicketWindowName(updated)); err != nil {
		return err
	}
	return m.Store.RenameSessionWindow(ctx, ticket.ID, TicketWindowName(updated))
}

func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.EnsureSession(ctx); err != nil {
		return err
	}
	if m.Store == nil {
		return nil
	}
	if service := m.workspaceService(); service != nil {
		workspaces, listErr := m.Store.ListCurrentWorkspaces(ctx, 0)
		if listErr != nil {
			return listErr
		}
		for _, workspace := range workspaces {
			if workspace.State == storage.WorkspaceStateIntegrated {
				continue
			}
			if validateErr := service.Validate(ctx, workspace); validateErr != nil {
				if markErr := m.Store.MarkWorkspaceState(ctx, workspace.ID, storage.WorkspaceStateRepairNeeded, validateErr.Error()); markErr != nil {
					return markErr
				}
			}
		}
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
		if !sessionID.Valid || !ticket.SessionActive || ticket.Runtime == kanban.StateStarting {
			continue
		}
		if ContainerRefFromTicket(ticket).Kind != multiplexer.KindHerdr {
			if err := m.Store.RecordSessionObservationFailure(ctx, sessionID.Int64, "system", "legacy runtime retired; terminal was not inspected or closed"); err != nil {
				return err
			}
			continue
		}

		adapter := m.herdrAdapter()
		ref := ContainerRefFromTicket(ticket)
		valid, validateErr := adapter.Validate(ctx, ref)
		if validateErr != nil {
			if err := m.Store.RecordSessionObservationFailure(ctx, sessionID.Int64, "herdr", validateErr.Error()); err != nil {
				return err
			}
			continue
		}
		if !valid {
			if err := m.Store.MarkSessionMissing(ctx, sessionID.Int64); err != nil {
				return err
			}
			continue
		}
		detection, _ := adapter.Detect(ctx, ref)
		if detection.Source == multiplexer.DetectionSourceUnknown {
			continue
		}
		if err := m.Store.UpdateSessionRuntime(ctx, sessionID.Int64, detection.State, string(detection.Source), detection.Reason, detection.Excerpt, false); err != nil {
			return err
		}
	}
	return nil
}

const workspaceObserveInterval = 5 * time.Second

type workspaceObservation struct {
	at                        time.Time
	state, path, sourceBranch string
}

func (m *Manager) RefreshRuntime(ctx context.Context) error {
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	if m.Store == nil {
		return nil
	}
	if err := m.RefreshIntegrationRuns(ctx); err != nil {
		return err
	}
	if service := m.workspaceService(); service != nil {
		workspaces, err := m.Store.ListCurrentWorkspaces(ctx, 0)
		if err != nil {
			return err
		}
		observeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		if m.workspaceObservations == nil {
			m.workspaceObservations = make(map[int64]workspaceObservation)
		}
		current := make(map[int64]bool, len(workspaces))
		for _, workspace := range workspaces {
			current[workspace.ID] = true
		}
		for id := range m.workspaceObservations {
			if !current[id] {
				delete(m.workspaceObservations, id)
			}
		}
		start := m.workspaceCursor
		for i := 0; i < len(workspaces); i++ {
			if observeCtx.Err() != nil {
				break
			}
			index := (start + i) % len(workspaces)
			workspace := workspaces[index]
			m.workspaceCursor = (index + 1) % len(workspaces)
			if workspace.State == storage.WorkspaceStateIntegrated {
				continue
			}
			previous, cached := m.workspaceObservations[workspace.ID]
			if cached && time.Since(previous.at) < workspaceObserveInterval && previous.state == workspace.State && previous.path == workspace.WorktreePath && previous.sourceBranch == workspace.SourceBranch {
				continue
			}
			if _, observeErr := service.Observe(observeCtx, workspace); observeErr != nil {
				_ = m.Store.RecordWorkspaceObservationError(observeCtx, workspace.ID, observeErr.Error())
			} else {
				m.workspaceObservations[workspace.ID] = workspaceObservation{at: time.Now(), state: workspace.State, path: workspace.WorktreePath, sourceBranch: workspace.SourceBranch}
			}
		}
		cancel()
	}
	tickets, err := m.Store.ActiveRuntimeTickets(ctx)
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
		// A starting session is a durable launch claim, not an observable runtime
		// container yet. Polling it can race the launch, change its status, and
		// cause CompleteSessionLaunch to reject the claim and close the new pane.
		if ses.Status == kanban.StateStarting || ses.Status == kanban.StateClosing {
			continue
		}
		containerRef := ContainerRefFromSession(ses)
		if containerRef.Kind != multiplexer.KindHerdr {
			continue
		}
		detectionSource := "herdr"
		adapter := m.herdrAdapter()
		detection, _ := adapter.Detect(ctx, containerRef)
		if detection.Source == multiplexer.DetectionSourceNative && detection.State != "" {
			if err := m.Store.UpdateObservedSessionRuntime(ctx, ses, detection.State, string(detection.Source), detection.Reason, detection.Excerpt, false); err != nil {
				return err
			}
			continue
		}
		out, err := adapter.Read(ctx, containerRef, multiplexer.ReadOptions{Lines: 200})

		if err != nil {
			if errors.Is(err, multiplexer.ErrContainerNotFound) {
				if markErr := m.Store.MarkObservedSessionMissing(ctx, ses); markErr != nil {
					return markErr
				}
				continue
			}
			if recordErr := m.Store.RecordObservedSessionFailure(ctx, ses, detectionSource, err.Error()); recordErr != nil {
				return recordErr
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
		if ses.LastDetectionSource.String == "manual" && source != "pattern" {
			state = ses.Status
			source = "manual"
			reason = ses.LastAttentionReason.String
		}
		if detectionSource == "herdr" && source != "pattern" {
			source = "heuristic"
		}
		if err := m.Store.UpdateObservedSessionRuntime(ctx, ses, state, source, reason, excerpt, changed); err != nil {
			return err
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
	if ses.Multiplexer != string(multiplexer.KindHerdr) {
		return fmt.Errorf("legacy runtime retired; close its terminal externally")
	}

	containerRef := ContainerRefFromSession(ses)
	if err := m.Store.UpdateSessionRuntime(ctx, ses.ID, kanban.StateClosing, "system", "graceful close requested", "", false); err != nil {
		return err
	}
	adapter := m.herdrAdapter()
	for _, key := range harness.ExitKeys(m.Config.Harnesses, ses.Harness) {
		if isTextExitCommand(key) {
			if err := adapter.SendText(ctx, containerRef, key); err != nil {
				return err
			}
			continue
		}
		if err := adapter.SendKeys(ctx, containerRef, key); err != nil {
			return err
		}
	}
	if err := adapter.Close(ctx, containerRef); err != nil {
		return err
	}
	return m.Store.MarkSessionClosed(ctx, ses.ID, kanban.StateClosed, "herdr", "pane closed")
}

func (m *Manager) WaitAndSendPrompt(ctx context.Context, adapter multiplexer.Interface, ref multiplexer.ContainerRef, readOptions multiplexer.ReadOptions, text, ready string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, _ := adapter.Read(ctx, ref, readOptions)
		if strings.Contains(out, ready) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("prompt readiness timeout waiting for %q", ready)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := adapter.SendText(ctx, ref, text); err != nil {
		return err
	}
	return adapter.SendKeys(ctx, ref, "Enter")
}

// SendTicketMessage sends literal text to the ticket's durable container. It is
// used for an explicit paused-work handoff after the normal resume succeeds.
func (m *Manager) SendTicketMessage(ctx context.Context, ticket storage.Ticket, text string) error {
	ref := ContainerRefFromTicket(ticket)
	adapter, err := m.multiplexerAdapter(ref.Kind)
	if err != nil {
		return err
	}
	if ref.Target() == "" {
		return multiplexer.ErrContainerNotFound
	}
	if err := adapter.SendText(ctx, ref, text); err != nil {
		return err
	}
	return adapter.SendKeys(ctx, ref, "Enter")
}

func isTextExitCommand(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "exit", "quit", "q":
		return true
	default:
		return false
	}
}

// waitHerdrContainerLive checks that a resume attempt survives startup.
func (m *Manager) waitHerdrContainerLive(ctx context.Context, ref multiplexer.ContainerRef, checkAfter time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(checkAfter):
	}
	if _, err := m.herdrAdapter().Read(ctx, ref, multiplexer.ReadOptions{Lines: 1}); err != nil {
		return fmt.Errorf("Herdr pane exited immediately (harness may have rejected the session ref): %w", err)
	}
	return nil
}

func (m *Manager) commandWithPiSessionRefCapture(ticket storage.Ticket, command []string) ([]string, string, string, error) {
	extPath, err := m.ensurePiSessionRefExtension()
	if err != nil {
		return nil, "", "", err
	}
	// Use a stable per-ticket path so recovery can reconstruct it. A prompt-start
	// is a new harness attempt, so remove any prior attempt's handoff before Pi
	// can be polled; otherwise a fresh worktree can inherit a stale session ref.
	refFile := m.piSessionRefFilePath(ticket.ID)
	if err := os.Remove(refFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", "", fmt.Errorf("clear stale Pi session ref handoff: %w", err)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, "", "", fmt.Errorf("create Pi session ref attempt token: %w", err)
	}
	attemptToken := hex.EncodeToString(random)
	out := make([]string, 0, len(command)+6)
	out = append(out,
		"env",
		"KANBI_SESSION_REF_FILE="+refFile,
		"KANBI_SESSION_REF_TOKEN="+attemptToken,
		"KANBI_TICKET_ID="+fmt.Sprint(ticket.ID),
	)
	if len(command) == 0 {
		return nil, "", "", errors.New("pi command is empty")
	}
	out = append(out, command[0], "-e", extPath)
	out = append(out, command[1:]...)
	return out, refFile, attemptToken, nil
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
	SessionID    string `json:"sessionId"`
	AttemptToken string `json:"attemptToken"`
}

func readPiSessionRefFile(path, expectedToken string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var payload piSessionRefPayload
	if err := json.Unmarshal(data, &payload); err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return "", false
	}
	if expectedToken != "" && payload.AttemptToken != expectedToken {
		return "", false
	}
	return strings.TrimSpace(payload.SessionID), true
}

func (m *Manager) acquireCodexCaptureLock(ctx context.Context) (func(), error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve Codex home for session-ref lock: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return nil, fmt.Errorf("create Codex home for session-ref lock: %w", err)
	}
	lockFile, err := os.OpenFile(filepath.Join(codexHome, ".kanbi-session-ref.flock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Codex capture lock: %w", err)
	}
	for {
		err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			var once sync.Once
			return func() {
				once.Do(func() {
					_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
					_ = lockFile.Close()
				})
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = lockFile.Close()
			return nil, fmt.Errorf("lock Codex session-ref capture: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = lockFile.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Native history is meaningful only for the canonical harness executable.
// Custom commands use their configured output marker and never scan user histories.
func (m *Manager) nativeHarness(name string) bool {
	h := m.Config.Harnesses[name]
	return len(h.Start) > 0 && h.Start[0] == name
}

func (m *Manager) captureSessionRef(ctx context.Context, harnessName, promptText, cwd string, since time.Time, refFile, refToken string) (string, bool) {
	if harnessName == "pi" && refFile != "" {
		if ref, ok := readPiSessionRefFile(refFile, refToken); ok {
			return ref, true
		}
	}
	if !m.nativeHarness(harnessName) {
		return "", false
	}
	// Copilot writes to session-store.db asynchronously and may take several
	// seconds after process start. Pi writes its JSONL file asynchronously too.
	// Claude Code is slow on cold start (workspace-trust dialog, plugin sync,
	// memory load) and only writes its session JSONL after the first turn lands.
	// Use a longer deadline for these harnesses.
	deadline := 2 * time.Second
	switch harnessName {
	case "copilot", "pi", "claude":
		deadline = 20 * time.Second
	}
	end := time.Now().Add(deadline)
	for {
		if harnessName == "pi" && refFile != "" {
			if ref, ok := readPiSessionRefFile(refFile, refToken); ok {
				return ref, true
			}
		}
		ref, ok := harness.CaptureSessionRefInCWD(harnessName, promptText, cwd, since)
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

var ErrWindowMissing = multiplexer.ErrContainerNotFound

// KillSession closes Kanbi-owned containers, never the Herdr server or other workspaces.
func (m *Manager) KillSession(ctx context.Context) error {
	if m.Store == nil {
		return nil
	}
	tickets, err := m.Store.ListTickets(ctx, false)
	if err != nil {
		return err
	}
	for _, ticket := range tickets {
		if ticket.SessionActive && ContainerRefFromTicket(ticket).Kind == multiplexer.KindHerdr {
			if err := m.CloseSession(ctx, ticket); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) PastePromptNow(ctx context.Context, name, text string) error {
	if m.Store == nil {
		return multiplexer.ErrContainerNotFound
	}
	tickets, err := m.Store.ListTickets(ctx, false)
	if err != nil {
		return err
	}
	for _, ticket := range tickets {
		ref := ContainerRefFromTicket(ticket)
		if ref.Name == name && ticket.SessionActive {
			return m.SendTicketMessage(ctx, ticket, text)
		}
	}
	return multiplexer.ErrContainerNotFound
}
