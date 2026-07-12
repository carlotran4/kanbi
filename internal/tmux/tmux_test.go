package tmux

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/harness"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/session"
	"github.com/carlotran4/kanbi/internal/storage"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls   []call
	pane    string
	windows map[string]string
}

type piRefWritingRunner struct {
	fakeRunner
	ref string
}

type invalidatingLaunchRunner struct {
	fakeRunner
	store    *storage.Store
	ticketID int64
}

func (r *invalidatingLaunchRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := r.fakeRunner.Run(ctx, name, args...)
	if err == nil && len(args) > 0 && args[0] == "new-window" {
		if ses, active, lookupErr := r.store.ActiveSession(ctx, r.ticketID); lookupErr == nil && active {
			_ = r.store.FailSessionLaunch(ctx, ses.ID, "simulated persistence race")
		}
	}
	return out, err
}

func (r *piRefWritingRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if len(args) >= 1 && args[0] == "new-window" {
		cmd := args[len(args)-1]
		marker := "KANBI_SESSION_REF_FILE="
		if idx := strings.Index(cmd, marker); idx >= 0 {
			start := idx + len(marker)
			end := start
			for end < len(cmd) && cmd[end] != '\'' && cmd[end] != ' ' {
				end++
			}
			refFile := cmd[start:end]
			_ = os.MkdirAll(filepath.Dir(refFile), 0o755)
			_ = os.WriteFile(refFile, []byte(`{"sessionId":"`+r.ref+`"}`+"\n"), 0o644)
		}
	}
	return r.fakeRunner.Run(ctx, name, args...)
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	switch {
	case len(args) >= 1 && args[0] == "has-session":
		return "", nil
	case len(args) >= 1 && args[0] == "list-windows":
		var b strings.Builder
		b.WriteString("board\n")
		for _, name := range f.windows {
			b.WriteString(name)
			b.WriteString("\n")
		}
		return b.String(), nil
	case len(args) >= 1 && args[0] == "new-window":
		windowName := ""
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-n" {
				windowName = args[i+1]
				break
			}
		}
		if f.windows == nil {
			f.windows = map[string]string{}
		}
		id := "@7"
		if len(f.windows) > 0 {
			id = fmt.Sprintf("@%d", 7+len(f.windows))
		}
		f.windows[id] = windowName
		return id + "\n", nil
	case len(args) >= 1 && args[0] == "display-message":
		if len(args) >= 4 && args[len(args)-1] == "#{window_name}" {
			targetArg := args[len(args)-2]
			targetID := targetArg
			if idx := strings.Index(targetArg, ":"); idx >= 0 {
				targetID = targetArg[idx+1:]
			}
			if f.windows != nil {
				if name, ok := f.windows[targetID]; ok {
					return name + "\n", nil
				}
			}
			return "", errors.New("missing")
		}
		if len(args) >= 4 && args[len(args)-1] == "#{window_id}" {
			targetArg := args[len(args)-2]
			windowName := targetArg
			if idx := strings.Index(targetArg, ":"); idx >= 0 {
				windowName = targetArg[idx+1:]
			}
			if windowName == "board" {
				return "@0\n", nil
			}
			for id, name := range f.windows {
				if targetArg == id || windowName == id || windowName == name {
					return id + "\n", nil
				}
			}
			return "", errors.New("missing")
		}
		return "@7\n", nil
	case len(args) >= 1 && args[0] == "capture-pane":
		if f.pane != "" {
			return f.pane, nil
		}
		return "PROMPT_READY\n", nil
	default:
		return "", nil
	}
}

func TestAttachCommandCreatesDistinctBoardClientSession(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	cfg.TmuxSession = "kanbi-main"
	cfg.Tmux.BoardWindowName = "board"

	cmd1 := AttachCommand(cfg, "/tmp/kanbi")
	cmd2 := AttachCommand(cfg, "/tmp/kanbi")

	if containsArg(cmd1.Args, "-A") || containsArg(cmd2.Args, "-A") {
		t.Fatalf("AttachCommand must not use tmux -A; it should create an independent board client: %v", cmd1.Args)
	}
	s1 := argAfter(cmd1.Args, "-s")
	s2 := argAfter(cmd2.Args, "-s")
	if s1 == "" || s2 == "" {
		t.Fatalf("AttachCommand missing tmux session names: %v / %v", cmd1.Args, cmd2.Args)
	}
	if s1 == cfg.TmuxSession || s2 == cfg.TmuxSession {
		t.Fatalf("board client should not attach directly to shared ticket session %q: %v / %v", cfg.TmuxSession, cmd1.Args, cmd2.Args)
	}
	if s1 == s2 {
		t.Fatalf("separate launches should get separate board client sessions, got %q", s1)
	}
	if !containsArg(cmd1.Args, "KANBI_INNER=1") || !containsArg(cmd1.Args, "--board") {
		t.Fatalf("AttachCommand should launch inner board command: %v", cmd1.Args)
	}
	if got := argWithPrefix(cmd1.Args, "KANBI_TMUX_SESSION="); got != "KANBI_TMUX_SESSION="+s1 {
		t.Fatalf("AttachCommand should make the board client session its ticket runtime session, got %q in %v", got, cmd1.Args)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func argAfter(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func argWithPrefix(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return arg
		}
	}
	return ""
}

func TestLifecycleDecideSwitchesActiveValidatedWindow(t *testing.T) {
	ticket := storage.Ticket{
		ID:            1,
		DisplayID:     "T-001",
		Title:         "Demo",
		Harness:       "pi",
		SessionID:     sql.NullInt64{Int64: 1, Valid: true},
		SessionActive: true,
		WindowID:      sqlString("@7"),
		WindowName:    sqlString("T-001-demo"),
	}
	runner := &fakeRunner{windows: map[string]string{"@7": "T-001-demo"}}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	decision, err := manager.lifecycle().Decide(context.Background(), lifecycleRequest{Ticket: ticket})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != lifecycleActionSwitch || decision.Ref != "@7" {
		t.Fatalf("decision = %+v, want switch @7", decision)
	}
}

func TestLifecycleDecideRequiresRepairForStaleActiveWindowWithoutRef(t *testing.T) {
	ticket := storage.Ticket{
		ID:            1,
		DisplayID:     "T-001",
		Title:         "Demo",
		Harness:       "pi",
		SessionID:     sql.NullInt64{Int64: 1, Valid: true},
		SessionActive: true,
		WindowID:      sqlString("@7"),
		WindowName:    sqlString("T-001-demo"),
	}
	runner := &fakeRunner{windows: map[string]string{"@7": "T-999-other"}}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	decision, err := manager.lifecycle().Decide(context.Background(), lifecycleRequest{Ticket: ticket})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != lifecycleActionRepair {
		t.Fatalf("decision = %+v, want repair", decision)
	}
}

func TestLifecycleDecideStartFreshIgnoresPriorSessionMetadata(t *testing.T) {
	ticket := storage.Ticket{
		ID:            1,
		DisplayID:     "T-001",
		Title:         "Demo",
		Harness:       "pi",
		SessionID:     sql.NullInt64{Int64: 99, Valid: true},
		SessionActive: true,
		SessionRef:    sqlString("old-ref"),
		WindowID:      sqlString("@7"),
		WindowName:    sqlString("T-001-demo"),
	}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: &fakeRunner{}}
	decision, err := manager.lifecycle().Decide(context.Background(), lifecycleRequest{Ticket: ticket, StartFresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != lifecycleActionStart || decision.Ticket.SessionID.Valid || decision.Ticket.SessionRef.Valid || decision.Ticket.WindowID.Valid {
		t.Fatalf("decision = %+v, want fresh start with cleared prior metadata", decision)
	}
}

func TestLifecycleDecideRejectsUnknownStoredMultiplexer(t *testing.T) {
	manager := NewManager(config.Defaults(config.Paths{}), nil)
	defer manager.Close()
	ticket := storage.Ticket{
		SessionID:     sql.NullInt64{Int64: 1, Valid: true},
		SessionActive: true,
		Multiplexer:   sql.NullString{String: "future", Valid: true},
		WindowName:    sql.NullString{String: "ticket", Valid: true},
	}

	_, err := manager.lifecycle().Decide(context.Background(), lifecycleRequest{Ticket: ticket})
	if !errors.Is(err, session.ErrMultiplexerNotRegistered) {
		t.Fatalf("Decide() error = %v, want ErrMultiplexerNotRegistered", err)
	}
}

func TestReconcileMarksSessionMissingWhenWindowGone(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Recon", "", "pi")
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}

	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if !ticket.SessionActive {
		t.Fatal("expected active session after open")
	}

	// Clear the window from the fake runner so reconcile sees it missing
	runner.windows = map[string]string{}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if got.SessionActive {
		t.Fatal("session should be marked inactive after window disappears")
	}
	if got.Runtime != "error" {
		t.Fatalf("runtime should be error after missing window, got %q", got.Runtime)
	}
}

func TestRenameTicketWindowUpdatesDBAndTmux(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Original Title", "", "pi")
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}

	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if err := manager.RenameTicketWindow(ctx, ticket, "New Title"); err != nil {
		t.Fatal(err)
	}

	// tmux rename-window must have been called
	var sawRename bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "rename-window" {
			sawRename = true
			if !strings.Contains(strings.Join(c.args, " "), "T-001-new-title") {
				t.Fatalf("rename-window args wrong: %v", c.args)
			}
		}
	}
	if !sawRename {
		t.Fatal("rename-window not called")
	}

	// DB window name updated
	ses, ok, err := store.ActiveSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("active session ok=%v err=%v", ok, err)
	}
	updated := ticket
	updated.Title = "New Title"
	wantName := TicketWindowName(updated)
	if ses.TmuxWindowName != wantName {
		t.Fatalf("DB window name = %q, want %s", ses.TmuxWindowName, wantName)
	}
}

func TestOpenTicketResumesWithStoredRef(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Resume Me", "", "pi")
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner, ResumeCheckAfter: time.Millisecond}

	// Simulate a ticket that already has an inactive session with a ref
	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "kanbi",
		TmuxWindowName: "T-001-resume-me", Status: "running",
	})
	_ = store.UpdateSessionRef(ctx, sessionID, "019e-resume-ref")
	_ = store.MarkSessionClosed(ctx, sessionID, "closed", "tmux", "exited")

	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}

	// Resume command must include the ref
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			cmd := strings.Join(c.args, " ")
			if !strings.Contains(cmd, "--session") || !strings.Contains(cmd, "019e-resume-ref") {
				t.Fatalf("resume command missing ref: %v", c.args)
			}
			return
		}
	}
	t.Fatal("new-window not called for resume")
}

func TestOpenTicketRecoversMissingPiSessionRefFromHistory(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Recover Ref", "Body", "pi")

	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ref := "019e-recovered-ref"
	promptText := "# T-001: Recover Ref\n\nBody"
	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "kanbi",
		TmuxWindowName: "T-001-recover-ref", Status: "running",
	})
	ses, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || !ses.StartedAt.Valid {
		t.Fatalf("latest session ok=%v err=%v started=%v", ok, err, ses.StartedAt)
	}
	startedAt := ses.StartedAt.Time
	sessionDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	session := `{"type":"session","version":3,"id":` + jsonQuote(ref) + `,"timestamp":` + jsonQuote(startedAt.Format(time.RFC3339Nano)) + `,"cwd":` + jsonQuote(cwd) + `}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"timestamp":` + jsonQuote(startedAt.Add(time.Second).Format(time.RFC3339Nano)) + `,"message":{"role":"user","content":[{"type":"text","text":` + jsonQuote(promptText) + `}],"timestamp":0}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "session.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = store.MarkSessionClosed(ctx, sessionID, "error", "tmux", "tmux window missing")

	ticket, _ = store.TicketByID(ctx, ticket.ID)
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner, ResumeCheckAfter: time.Millisecond}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.TicketByID(ctx, ticket.ID)
	if !updated.SessionRef.Valid || updated.SessionRef.String != ref {
		t.Fatalf("session ref = %#v, want %q", updated.SessionRef, ref)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" && strings.Contains(strings.Join(c.args, " "), ref) {
			return
		}
	}
	t.Fatalf("resume command with recovered ref not called: %+v", runner.calls)
}

func TestOpenTicketRecoversMissingPiSessionRefFromRefFile(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Recover From File", "Body", "pi")

	stateDir := t.TempDir()
	ref := "019e-file-recovered-ref"

	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "kanbi",
		TmuxWindowName: "T-001-recover-from-file", Status: "running",
	})
	_ = store.MarkSessionClosed(ctx, sessionID, "error", "tmux", "tmux window missing")
	ticket, _ = store.TicketByID(ctx, ticket.ID)

	// Write the stable ref file (as the Pi extension would have, via piSessionRefFilePath).
	refDir := filepath.Join(stateDir, "pi-session-refs")
	_ = os.MkdirAll(refDir, 0o755)
	refPath := filepath.Join(refDir, fmt.Sprintf("ticket-%d.json", ticket.ID))
	_ = os.WriteFile(refPath, []byte(`{"sessionId":"`+ref+`"}`+"\n"), 0o644)

	runner := &fakeRunner{}
	cfg := config.Defaults(config.Paths{StateDir: stateDir})
	manager := &Manager{Config: cfg, Store: store, Runner: runner, ResumeCheckAfter: time.Millisecond}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.TicketByID(ctx, ticket.ID)
	if !updated.SessionRef.Valid || updated.SessionRef.String != ref {
		t.Fatalf("session ref = %#v, want %q", updated.SessionRef, ref)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" && strings.Contains(strings.Join(c.args, " "), ref) {
			return
		}
	}
	t.Fatalf("resume command with recovered ref not called: %+v", runner.calls)
}

func TestOpenTicketRejectsInvalidStoredCopilotSessionRefBeforeResume(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Copilot Resume", "Body", "copilot")

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(home, ".copilot", "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			cwd TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE turns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			turn_index INTEGER NOT NULL,
			user_message TEXT,
			UNIQUE(session_id, turn_index)
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO sessions(id, cwd, created_at) VALUES(?,?,?)`,
		"bad-copilot-ref", cwd, "2030-01-01T00:00:02Z")
	if err != nil {
		t.Fatal(err)
	}

	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "copilot",
		TmuxSessionName: "kanbi",
		TmuxWindowName:  "T-001-copilot-resume",
		Status:          "running",
	})
	_ = store.UpdateSessionRef(ctx, sessionID, "bad-copilot-ref")
	_ = store.MarkSessionClosed(ctx, sessionID, "error", "tmux", "window missing")

	ticket, _ = store.TicketByID(ctx, ticket.ID)
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: &fakeRunner{}}
	err = manager.OpenTicket(ctx, ticket, false)
	var repair RepairNeededError
	if !errors.As(err, &repair) {
		t.Fatalf("expected RepairNeededError, got %T: %v", err, err)
	}
	updated, _ := store.TicketByID(ctx, ticket.ID)
	if updated.SessionRef.Valid {
		t.Fatalf("expected invalid copilot ref to be cleared, got %+v", updated.SessionRef)
	}
}

func TestOpenTicketResumeFailureReturnsResumeFailedError(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Fail Resume", "", "pi")

	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "kanbi",
		TmuxWindowName: "T-001-fail-resume", Status: "running",
	})
	_ = store.UpdateSessionRef(ctx, sessionID, "019e-bad-ref")
	_ = store.MarkSessionClosed(ctx, sessionID, "closed", "tmux", "exited")

	ticket, _ = store.TicketByID(ctx, ticket.ID)

	// Runner that fails new-window to simulate resume failure
	runner := &failNewWindowRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}
	err := manager.OpenTicket(ctx, ticket, false)

	var resumeErr ResumeFailedError
	if !errors.As(err, &resumeErr) {
		t.Fatalf("expected ResumeFailedError, got %T: %v", err, err)
	}
}

func TestOpenTicketRejectsSecondLaunchAfterDurableClaim(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Claim race", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "kanbi", TmuxWindowName: TicketWindowName(ticket)}, false); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}

	err = manager.OpenTicket(ctx, ticket, false)
	if !errors.Is(err, storage.ErrActiveSessionExists) {
		t.Fatalf("open error = %v, want ErrActiveSessionExists", err)
	}
	if hasCall(runner.calls, "new-window") {
		t.Fatal("second caller launched a container after another caller claimed the ticket")
	}
}

func TestLaunchPersistenceFailureClosesContainerAndInactivatesClaim(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Persist failure", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	runner := &invalidatingLaunchRunner{store: store, ticketID: ticket.ID}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}

	if err := manager.OpenTicket(ctx, ticket, false); err == nil {
		t.Fatal("expected completion failure")
	}
	if !hasCall(runner.calls, "kill-window") {
		t.Fatal("launched window was not cleaned up after persistence failure")
	}
	latest, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || latest.IsActive || latest.Status != kanban.StateError {
		t.Fatalf("latest session = %+v, %v, %v", latest, ok, err)
	}
}

func TestPastePromptFailureClosesContainerAndMarksAttemptError(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Paste failure", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(config.Paths{})
	cfg.PromptReadyTimeout = time.Millisecond
	pi := cfg.Harnesses["pi"]
	pi.PromptMode = harness.PromptModePaste
	pi.PromptReady = "NEVER_READY"
	cfg.Harnesses["pi"] = pi
	runner := &fakeRunner{pane: "still booting"}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}

	err = manager.OpenTicket(ctx, ticket, true)
	var promptErr PromptReadyError
	if !errors.As(err, &promptErr) {
		t.Fatalf("open error = %T %v, want PromptReadyError", err, err)
	}
	if !hasCall(runner.calls, "kill-window") {
		t.Fatal("prompt failure did not clean up launched window")
	}
	latest, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || latest.IsActive || latest.Status != kanban.StateError {
		t.Fatalf("latest session = %+v, %v, %v", latest, ok, err)
	}
}

func hasCall(calls []call, command string) bool {
	for _, c := range calls {
		if len(c.args) > 0 && c.args[0] == command {
			return true
		}
	}
	return false
}

func TestRefreshRuntimeMarksSessionMissingWhenWindowDisappears(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Disappear", "", "pi")
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}

	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}

	// Window disappears between ticks
	runner.windows = map[string]string{}
	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if got.SessionActive {
		t.Fatal("session should be inactive when window gone")
	}
	if got.Runtime != "error" {
		t.Fatalf("runtime = %q, want error", got.Runtime)
	}
}

func TestSlugEdgeCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World!", "hello-world"},
		{"", "ticket"},
		{"---", "ticket"},
		{strings.Repeat("a", 50), strings.Repeat("a", 40)},
		{"Fix OAuth/Redirect", "fix-oauth-redirect"},
	}
	for _, tc := range cases {
		if got := slug(tc.in); got != tc.want {
			t.Errorf("slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellQuoteEdgeCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"simple", "simple"},
		{"has space", "'has space'"},
		{"it's", `'it'\''s'`},
		{"/path/to/file", "/path/to/file"},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

type failNewWindowRunner struct{}

func (f *failNewWindowRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "new-window" {
		return "", errors.New("tmux new-window failed")
	}
	if len(args) > 0 && (args[0] == "has-session" || args[0] == "list-windows") {
		return "board\n", nil
	}
	return "", nil
}

func TestWindowNameAndShellCommand(t *testing.T) {
	if got := WindowName("T-001", "Fix OAuth Redirect!"); got != "T-001-fix-oauth-redirect" {
		t.Fatalf("window name = %s", got)
	}
	if got := ShellCommand([]string{"/tmp/fake harness", "resume", "abc"}); got != "'/tmp/fake harness' resume abc" {
		t.Fatalf("shell command = %s", got)
	}
}

func TestReadPiSessionRefFileRejectsMissingMalformedOrEmptyRefs(t *testing.T) {
	dir := t.TempDir()
	if _, ok := readPiSessionRefFile(filepath.Join(dir, "missing.json")); ok {
		t.Fatal("missing file should not yield a ref")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`not-json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPiSessionRefFile(bad); ok {
		t.Fatal("malformed JSON should not yield a ref")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"sessionId":"   "}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPiSessionRefFile(empty); ok {
		t.Fatal("blank session id should not yield a ref")
	}
	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"sessionId":"  019e-good  "}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, ok := readPiSessionRefFile(valid)
	if !ok || ref != "019e-good" {
		t.Fatalf("valid ref = %q ok=%v", ref, ok)
	}
}

func TestOpenTicketWithPiPromptCapturesSessionRefFromBundledExtension(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Demo", "Body", "pi")
	cfg := config.Defaults(config.Paths{StateDir: t.TempDir()})
	runner := &piRefWritingRunner{ref: "019e-extension-ref"}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}

	if err := manager.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatal(err)
	}

	got, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SessionRef.Valid || got.SessionRef.String != "019e-extension-ref" {
		t.Fatalf("session ref = %#v, want extension ref", got.SessionRef)
	}

	var sawExtension bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			cmd := c.args[len(c.args)-1]
			if strings.Contains(cmd, "env ") && strings.Contains(cmd, "KANBI_SESSION_REF_FILE=") && strings.Contains(cmd, " -e ") && strings.Contains(cmd, "pi-session-ref-extension.ts") {
				sawExtension = true
			}
		}
	}
	if !sawExtension {
		t.Fatalf("pi command did not load bundled session-ref extension: %+v", runner.calls)
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.StateDir, "pi-session-ref-extension.ts")); err != nil {
		t.Fatalf("extension was not materialized in state dir: %v", err)
	}
}

func TestOpenTicketCreatesWindowAndPastesPrompt(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	cfg.PromptReadyTimeout = time.Second
	cfg.Harnesses["pi"] = config.Harness{Start: []string{"/tmp/fake-pi"}, PromptReady: "PROMPT_READY"}
	runner := &fakeRunner{}
	manager := &Manager{Config: cfg, Runner: runner}
	workdir := t.TempDir()
	ticket := storage.Ticket{ID: 1, DisplayID: "T-001", Title: "Demo", Body: "Body", Harness: "pi", BoardWorkdir: workdir}
	if err := manager.OpenTicket(context.Background(), ticket, true); err != nil {
		t.Fatal(err)
	}
	var sawNew, sawSetBuffer, sawPaste, sawWorkdir bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			sawNew = true
			joined := strings.Join(c.args, " ")
			if !strings.Contains(joined, "T-001-demo") {
				t.Fatalf("new-window args = %#v", c.args)
			}
			if strings.Contains(joined, "-c "+workdir) {
				sawWorkdir = true
			}
		}
		if len(c.args) > 0 && c.args[0] == "set-buffer" {
			sawSetBuffer = strings.Contains(strings.Join(c.args, "\n"), "# T-001: Demo\n\nBody")
		}
		if len(c.args) > 0 && c.args[0] == "paste-buffer" {
			sawPaste = true
		}
	}
	if !sawNew || !sawWorkdir || !sawSetBuffer || !sawPaste {
		t.Fatalf("calls missing new=%v workdir=%v set=%v paste=%v calls=%+v", sawNew, sawWorkdir, sawSetBuffer, sawPaste, runner.calls)
	}
}

func TestPasteModeCapturesSessionRefFromPane(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Demo", "Body", "copilot")
	cfg := config.Defaults(config.Paths{})
	cfg.Harnesses["copilot"] = config.Harness{Start: []string{"/tmp/fake-copilot"}, PromptReady: "PROMPT_READY", PromptMode: "paste", SessionRef: "SESSION_REF="}
	runner := &fakeRunner{pane: "PROMPT_READY\nSESSION_REF=copilot-123\n"}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatal(err)
	}
	got, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SessionRef.Valid || got.SessionRef.String != "copilot-123" {
		t.Fatalf("session ref = %+v", got.SessionRef)
	}
}

func TestOpenTicketCreatesWindowAndSwitchesClient(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Demo", "Body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	var sawSwitchSession bool
	var sawSelectWindow bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "switch-client" && strings.Join(c.args, " ") == "switch-client -t kanbi" {
			sawSwitchSession = true
		}
		if len(c.args) > 0 && c.args[0] == "select-window" && strings.Join(c.args, " ") == "select-window -t kanbi:@7" {
			sawSelectWindow = true
		}
	}
	if !sawSwitchSession || !sawSelectWindow {
		t.Fatalf("window switch did not target session+window switch=%v select=%v calls=%+v", sawSwitchSession, sawSelectWindow, runner.calls)
	}
}

func TestOpenTicketRecordsWindowID(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Demo", "Body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(config.Paths{})
	cfg.Harnesses["pi"] = config.Harness{Start: []string{"/tmp/fake-pi"}, PromptReady: "PROMPT_READY"}
	manager := &Manager{Config: cfg, Store: store, Runner: &fakeRunner{}}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	got, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WindowID.String != "@7" || got.WindowName.String != TicketWindowName(ticket) {
		t.Fatalf("window metadata = id:%q name:%q", got.WindowID.String, got.WindowName.String)
	}
}

func TestOpenTicketExistingActiveWindowDoesNotCreateNewSessionRow(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Demo", "Body", "pi")
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	firstSessionID := ticket.SessionID.Int64
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if got.SessionID.Int64 != firstSessionID {
		t.Fatalf("existing active open created new session: before=%d after=%d", firstSessionID, got.SessionID.Int64)
	}
}

func TestStartFreshWithExistingWindowCreatesSeparateWindow(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Demo", "Body", "pi")
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	firstWindowID := ticket.WindowID.String
	firstWindowName := ticket.WindowName.String

	if err := manager.StartFreshTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if got.WindowID.String == firstWindowID {
		t.Fatalf("start fresh reused existing window id %q", firstWindowID)
	}
	if got.WindowName.String == firstWindowName {
		t.Fatalf("start fresh reused existing window name %q", firstWindowName)
	}
	if got.WindowName.String != firstWindowName+"-2" {
		t.Fatalf("fresh window name = %q, want %q", got.WindowName.String, firstWindowName+"-2")
	}
}

func TestRefreshRuntimeDetectsAttentionState(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Needs approval", "", "pi")
	cfg := config.Defaults(config.Paths{})
	runner := &fakeRunner{pane: "Approve command? yes/no\n"}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "needs_permission" || got.LastAttentionReason.String == "" {
		t.Fatalf("runtime = %+v", got)
	}
}

func TestRefreshRuntimePreservesManualStateFromHeuristics(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Manual", "", "pi")
	runner := &fakeRunner{pane: "quiet output\n"}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if err := store.UpdateSessionRuntime(ctx, ticket.SessionID.Int64, "waiting_for_user", "manual", "manual override", "quiet output", true); err != nil {
		t.Fatal(err)
	}
	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "waiting_for_user" || got.LastDetectionSource.String != "manual" {
		t.Fatalf("manual state should survive heuristic refresh: %+v", got)
	}
}

func TestRefreshRuntimeDoesNotAutoCloseImmediatelyOnNewAttentionState(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Wait", "", "pi")
	cfg := config.Defaults(config.Paths{})
	cfg.AutoCloseWaitingAfter = 0
	runner := &fakeRunner{pane: "PROMPT_READY\n"}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ActiveSession(ctx, ticket.ID); err != nil || !ok {
		t.Fatalf("session should remain active on first attention tick ok=%v err=%v", ok, err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "kill-window" {
			t.Fatalf("should not auto-close on fresh attention transition: %+v", runner.calls)
		}
	}
}

func TestOpenTicketRejectsSecondPromptSend(t *testing.T) {
	ticket := storage.Ticket{ID: 1, DisplayID: "T-001", Title: "Demo", Harness: "pi", SessionID: sql.NullInt64{Int64: 1, Valid: true}}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: &fakeRunner{}}
	if err := manager.OpenTicket(context.Background(), ticket, true); !errors.Is(err, ErrPromptAlreadySent) {
		t.Fatalf("err = %v", err)
	}
}

func TestSwitchToTicketRequiresRepairWhenWindowMissingWithoutRef(t *testing.T) {
	runner := &missingWindowRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	ticket := storage.Ticket{
		ID:        1,
		DisplayID: "T-001",
		Title:     "Demo",
		Harness:   "pi",
		SessionID: sql.NullInt64{Int64: 1, Valid: true},
	}
	err := manager.SwitchToTicket(context.Background(), ticket)
	var repair RepairNeededError
	if !errors.As(err, &repair) {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestStartFreshTicketPreservesOldSessionAndCreatesNew(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Fresh", "Body", "pi")
	runner := &fakeRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner}

	// First open to create a session
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	firstSessionID := ticket.SessionID.Int64

	// Start fresh: should deactivate old session and create new one
	if err := manager.StartFreshTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if ticket.SessionID.Int64 == firstSessionID {
		t.Fatalf("start fresh should create new session; still at id=%d", firstSessionID)
	}
	if !ticket.SessionActive {
		t.Fatalf("new session should be active")
	}

	// Old session should still exist in DB, inactive
	// Verify by creating a third session and checking we still have old history via the latest
	// (upsert marks old ones inactive but doesn't delete them)
	allTickets, err := store.ListTickets(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(allTickets) == 0 {
		t.Fatal("ticket list should not be empty")
	}
	// The active session query should only return new session
	gotActive, ok, err := store.ActiveSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("active session should exist ok=%v err=%v", ok, err)
	}
	if gotActive.ID == firstSessionID {
		t.Fatalf("active session should not be old session")
	}
}

func TestCloseSessionSendsGracefulExitBeforeKill(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Close me", "", "pi")
	cfg := config.Defaults(config.Paths{})
	cfg.GracefulExitTimeout = time.Millisecond
	runner := &fakeRunner{}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if err := manager.CloseSession(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	var sawSend, sawKill bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "send-keys" {
			sawSend = true
		}
		if len(c.args) > 0 && c.args[0] == "kill-window" {
			sawKill = true
		}
	}
	if !sawSend || !sawKill {
		t.Fatalf("close calls missing send=%v kill=%v calls=%+v", sawSend, sawKill, runner.calls)
	}
	if _, ok, _ := store.ActiveSession(ctx, ticket.ID); ok {
		t.Fatal("session still active after close")
	}
}

func TestSwitchToTicketPrefersStoredWindowID(t *testing.T) {
	runner := &fakeRunner{windows: map[string]string{"@7": "T-001-demo"}}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	ticket := storage.Ticket{
		DisplayID:     "T-001",
		Title:         "Demo",
		Harness:       "pi",
		SessionID:     sql.NullInt64{Int64: 1, Valid: true},
		WindowID:      sqlString("@7"),
		WindowName:    sqlString("T-001-demo"),
		SessionActive: true,
	}
	if err := manager.SwitchToTicket(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	var sawSwitchSession bool
	var sawSelectWindow bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "switch-client" && strings.Join(c.args, " ") == "switch-client -t kanbi" {
			sawSwitchSession = true
		}
		if len(c.args) > 0 && c.args[0] == "select-window" && strings.Join(c.args, " ") == "select-window -t kanbi:@7" {
			sawSelectWindow = true
		}
	}
	if !sawSwitchSession || !sawSelectWindow {
		t.Fatalf("window switch did not target session+window switch=%v select=%v calls=%+v", sawSwitchSession, sawSelectWindow, runner.calls)
	}
}

func TestSwitchToTicketUsesStoredRuntimeSession(t *testing.T) {
	runner := &fakeRunner{windows: map[string]string{"@7": "T-001-demo"}}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	ticket := storage.Ticket{
		DisplayID:       "T-001",
		Title:           "Demo",
		Harness:         "pi",
		SessionID:       sql.NullInt64{Int64: 1, Valid: true},
		TmuxSessionName: sqlString("kanbi-instance-b"),
		WindowName:      sqlString("T-001-demo"),
		SessionActive:   true,
	}
	if err := manager.SwitchToTicket(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	var sawSwitchSession bool
	var sawSelectWindow bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "switch-client" && strings.Join(c.args, " ") == "switch-client -t kanbi-instance-b" {
			sawSwitchSession = true
		}
		if len(c.args) > 0 && c.args[0] == "select-window" && strings.Join(c.args, " ") == "select-window -t kanbi-instance-b:T-001-demo" {
			sawSelectWindow = true
		}
	}
	if !sawSwitchSession || !sawSelectWindow {
		t.Fatalf("window switch did not target stored runtime session switch=%v select=%v calls=%+v", sawSwitchSession, sawSelectWindow, runner.calls)
	}
}

func TestSwitchToTicketValidatesStoredWindowIDInStoredRuntimeSession(t *testing.T) {
	runner := &fakeRunner{windows: map[string]string{"@7": "T-001-demo"}}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	ticket := storage.Ticket{
		DisplayID:       "T-001",
		Title:           "Demo",
		Harness:         "pi",
		SessionID:       sql.NullInt64{Int64: 1, Valid: true},
		TmuxSessionName: sqlString("kanbi-instance-b"),
		WindowID:        sqlString("@7"),
		WindowName:      sqlString("T-001-demo"),
		SessionActive:   true,
	}
	if err := manager.SwitchToTicket(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "display-message" && strings.Join(c.args, " ") == "display-message -p -t kanbi-instance-b:@7 #{window_name}" {
			return
		}
	}
	t.Fatalf("stored runtime session was not used to validate window id: %+v", runner.calls)
}

func TestSwitchToTicketRejectsStaleWindowIDWithWrongName(t *testing.T) {
	runner := &fakeRunner{windows: map[string]string{"@7": "T-006-test-ticket-2"}}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	ticket := storage.Ticket{
		DisplayID:  "T-005",
		Title:      "lskfjsflj",
		Harness:    "pi",
		SessionID:  sql.NullInt64{Int64: 5, Valid: true},
		WindowID:   sqlString("@7"),
		WindowName: sqlString("T-005-lskfjsflj"),
	}
	err := manager.SwitchToTicket(context.Background(), ticket)
	var repair RepairNeededError
	if !errors.As(err, &repair) {
		t.Fatalf("err = %T %v", err, err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "switch-client" {
			t.Fatalf("should not switch to stale id: %+v", runner.calls)
		}
	}
}

func TestCodexOpenTicketSendsPromptAsArgument(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	runner := &fakeRunner{}
	manager := &Manager{Config: cfg, Runner: runner}
	ticket := storage.Ticket{ID: 1, DisplayID: "T-001", Title: "Codex Demo", Body: "Body", Harness: "codex"}
	if err := manager.OpenTicket(context.Background(), ticket, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "set-buffer" {
			t.Fatalf("codex prompt arg mode should not paste: %+v", runner.calls)
		}
		if len(c.args) > 0 && c.args[0] == "new-window" {
			joined := strings.Join(c.args, "\n")
			if strings.Contains(joined, "codex --no-alt-screen") && strings.Contains(joined, "# T-001: Codex Demo\n\nBody") {
				return
			}
		}
	}
	t.Fatalf("codex new-window command missing prompt arg: %+v", runner.calls)
}

func TestEnsureSessionCreatesMissingSession(t *testing.T) {
	runner := &missingSessionRunner{}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner}
	if err := manager.EnsureSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.created {
		t.Fatal("expected new-session")
	}
}

func newTmuxTestStore(t *testing.T) (*storage.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return store, ctx
}

func defaultBoardView(t *testing.T, ctx context.Context, store *storage.Store) storage.BoardView {
	t.Helper()
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func createTicket(t *testing.T, ctx context.Context, store *storage.Store, columnID int64, title, body, harness string) storage.Ticket {
	t.Helper()
	ticket, err := store.CreateTicket(ctx, columnID, title, body, harness)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func sqlString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// evanescingRunner creates a window on new-window but then immediately removes it,
// simulating a harness process that exits instantly (e.g. pi rejecting a bad session ref).
type evanescingRunner struct {
	baseRunner *fakeRunner
	windowGone bool
}

func (e *evanescingRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if e.windowGone && len(args) > 0 && args[0] == "display-message" {
		// Simulate window no longer existing
		return "", errors.New("window not found")
	}
	out, err := e.baseRunner.Run(ctx, name, args...)
	if len(args) > 0 && args[0] == "new-window" {
		// Simulate immediate exit: remove the window right after creation.
		e.baseRunner.windows = map[string]string{}
		e.windowGone = true
	}
	return out, err
}

func TestOpenTicketWithStaleActiveRuntimeSessionResumesWithStoredRef(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Stale Runtime", "", "pi")
	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "pi",
		TmuxSessionName: "old-missing-board-session",
		TmuxWindowName:  "b1-T-001-stale-runtime",
		Status:          "running",
	})
	_ = store.UpdateSessionRef(ctx, sessionID, "019e-stale-runtime-ref")

	ticket, _ = store.TicketByID(ctx, ticket.ID)
	runner := &missingRuntimeSessionRunner{missingSession: "old-missing-board-session"}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store, Runner: runner, ResumeCheckAfter: time.Millisecond}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			cmd := strings.Join(c.args, " ")
			if !strings.Contains(cmd, "--session") || !strings.Contains(cmd, "019e-stale-runtime-ref") {
				t.Fatalf("resume command missing ref after stale runtime session: %v", c.args)
			}
			return
		}
	}
	t.Fatal("expected resume new-window call")
}

func TestCopilotOpenOnlyDoesNotIncludeInteractiveFlag(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	runner := &fakeRunner{}
	manager := &Manager{Config: cfg, Runner: runner}
	ticket := storage.Ticket{ID: 1, DisplayID: "T-001", Title: "Copilot Demo", Body: "Body", Harness: "copilot"}
	if err := manager.OpenTicket(context.Background(), ticket, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			joined := strings.Join(c.args, " ")
			if strings.Contains(joined, " -i") {
				t.Fatalf("copilot open-only must not include -i flag: %+v", c.args)
			}
			return
		}
	}
	t.Fatal("expected a new-window call")
}

func TestCopilotOpenTicketSendsPromptWithInteractiveFlag(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	runner := &fakeRunner{}
	manager := &Manager{Config: cfg, Runner: runner}
	ticket := storage.Ticket{ID: 1, DisplayID: "T-001", Title: "Copilot Demo", Body: "Body", Harness: "copilot"}
	if err := manager.OpenTicket(context.Background(), ticket, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			joined := strings.Join(c.args, " ")
			if strings.Contains(joined, "copilot -i") && strings.Contains(joined, "# T-001: Copilot Demo") && strings.Contains(joined, "Body") {
				return
			}
			t.Fatalf("copilot new-window command missing '-i' and prompt: %+v", c.args)
		}
	}
	t.Fatal("expected a new-window call")
}

func TestResumeWithImmediatelyExitingProcessReturnsResumeFailedError(t *testing.T) {
	base := &fakeRunner{}
	runner := &evanescingRunner{baseRunner: base}
	manager := &Manager{Config: config.Defaults(config.Paths{}), Runner: runner, ResumeCheckAfter: time.Millisecond}
	ticket := storage.Ticket{
		ID:         1,
		DisplayID:  "T-001",
		Title:      "Resume Fail Test",
		Harness:    "pi",
		SessionID:  sql.NullInt64{Int64: 42, Valid: true},
		SessionRef: sql.NullString{String: "dead-session-ref", Valid: true},
	}
	err := manager.OpenTicket(context.Background(), ticket, false)
	var resumeErr ResumeFailedError
	if !errors.As(err, &resumeErr) {
		t.Fatalf("expected ResumeFailedError, got %T: %v", err, err)
	}
	if resumeErr.Ticket.ID != ticket.ID {
		t.Fatalf("ResumeFailedError.Ticket.ID = %d, want %d", resumeErr.Ticket.ID, ticket.ID)
	}
	if !hasCall(base.calls, "kill-window") {
		t.Fatal("failed resume did not attempt compensating container cleanup")
	}
}

type missingRuntimeSessionRunner struct {
	fakeRunner
	missingSession string
}

func (m *missingRuntimeSessionRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if len(args) >= 3 && args[0] == "list-windows" && args[1] == "-t" && args[2] == m.missingSession {
		return "can't find session: " + m.missingSession + "\n", errors.New("exit status 1")
	}
	return m.fakeRunner.Run(ctx, name, args...)
}

type missingSessionRunner struct {
	created bool
}

func (m *missingSessionRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if args[0] == "has-session" {
		return "", errors.New("missing")
	}
	if args[0] == "new-session" {
		m.created = true
	}
	return "", nil
}

type missingWindowRunner struct{}

func (m *missingWindowRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	switch args[0] {
	case "display-message":
		return "", errors.New("missing")
	case "list-windows":
		return "board\n", nil
	default:
		return "", nil
	}
}
