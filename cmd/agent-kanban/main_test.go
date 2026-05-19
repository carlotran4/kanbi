package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-kanban/internal/config"
	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
)

// setupCLI wires an isolated DB + config for each test, returning a run function
// that calls the real CLI entry point with those env vars set.
func setupCLI(t *testing.T) (runArgs func(args ...string) error, store func() *storage.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	cfgPath := filepath.Join(dir, "config.yaml")

	t.Setenv("AGENT_KANBAN_DB", dbPath)
	t.Setenv("AGENT_KANBAN_CONFIG", cfgPath)
	t.Setenv("AGENT_KANBAN_DATA_DIR", dir)
	t.Setenv("AGENT_KANBAN_STATE_DIR", dir)
	// Use a unique tmux session name so tests don't collide with a real board
	t.Setenv("AGENT_KANBAN_TMUX_SESSION", "agent-kanban-test-"+t.Name())

	runArgs = func(args ...string) error {
		return run(args)
	}
	store = func() *storage.Store {
		s, err := storage.Open(dbPath)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		if err := s.Init(context.Background()); err != nil {
			t.Fatalf("init store: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	return runArgs, store
}

// ---- parseAddArgs ----

func TestParseAddArgsPositional(t *testing.T) {
	title, body, harness, err := parseAddArgs([]string{"My ticket"})
	if err != nil || title != "My ticket" || body != "" || harness != "pi" {
		t.Fatalf("title=%q body=%q harness=%q err=%v", title, body, harness, err)
	}
}

func TestParseAddArgsAllFlags(t *testing.T) {
	title, body, harness, err := parseAddArgs([]string{"Title", "--body", "some body", "--harness", "codex"})
	if err != nil || title != "Title" || body != "some body" || harness != "codex" {
		t.Fatalf("title=%q body=%q harness=%q err=%v", title, body, harness, err)
	}
}

func TestParseAddArgsMissingBodyValue(t *testing.T) {
	_, _, _, err := parseAddArgs([]string{"Title", "--body"})
	if err == nil {
		t.Fatal("expected error for missing --body value")
	}
}

func TestParseAddArgsMissingHarnessValue(t *testing.T) {
	_, _, _, err := parseAddArgs([]string{"Title", "--harness"})
	if err == nil {
		t.Fatal("expected error for missing --harness value")
	}
}

func TestParseAddArgsDoubleTitleRejected(t *testing.T) {
	_, _, _, err := parseAddArgs([]string{"First", "Second"})
	if err == nil {
		t.Fatal("expected error for double positional title")
	}
}

func TestParseAddArgsUnknownFlag(t *testing.T) {
	_, _, _, err := parseAddArgs([]string{"Title", "--unknown"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// ---- parseOpenArgs ----

func TestParseOpenArgsDisplayID(t *testing.T) {
	id, send, err := parseOpenArgs([]string{"T-001"})
	if err != nil || id != "T-001" || send {
		t.Fatalf("id=%q send=%v err=%v", id, send, err)
	}
}

func TestParseOpenArgsSendPrompt(t *testing.T) {
	id, send, err := parseOpenArgs([]string{"T-001", "--send-prompt"})
	if err != nil || id != "T-001" || !send {
		t.Fatalf("id=%q send=%v err=%v", id, send, err)
	}
}

func TestParseOpenArgsDoubleIDRejected(t *testing.T) {
	_, _, err := parseOpenArgs([]string{"T-001", "T-002"})
	if err == nil {
		t.Fatal("expected error for double display ID")
	}
}

func TestParseOpenArgsUnknownFlag(t *testing.T) {
	_, _, err := parseOpenArgs([]string{"--foo"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// ---- CLI integration: add + list ----

func TestRunAddCreatesTicketAndListShowsIt(t *testing.T) {
	run, getStore := setupCLI(t)

	if err := run("add", "My first ticket", "--body", "do the thing", "--harness", "codex"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	s := getStore()
	tickets, err := s.ListTickets(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(tickets))
	}
	got := tickets[0]
	if got.Title != "My first ticket" || got.Body != "do the thing" || got.Harness != "codex" {
		t.Fatalf("ticket = %+v", got)
	}
	if got.DisplayID != "T-001" {
		t.Fatalf("display id = %q", got.DisplayID)
	}
}

func TestRunAddMultipleTicketsIncrementDisplayIDs(t *testing.T) {
	run, getStore := setupCLI(t)

	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		if err := run("add", title); err != nil {
			t.Fatalf("add %q failed: %v", title, err)
		}
	}

	s := getStore()
	tickets, _ := s.ListTickets(context.Background(), false)
	if len(tickets) != 3 {
		t.Fatalf("expected 3 tickets, got %d", len(tickets))
	}
	for i, want := range []string{"T-001", "T-002", "T-003"} {
		if tickets[i].DisplayID != want {
			t.Errorf("ticket[%d].DisplayID = %q, want %q", i, tickets[i].DisplayID, want)
		}
	}
}

func TestRunAddDefaultHarnessIsPi(t *testing.T) {
	run, getStore := setupCLI(t)

	if err := run("add", "No harness specified"); err != nil {
		t.Fatal(err)
	}
	s := getStore()
	tickets, _ := s.ListTickets(context.Background(), false)
	if tickets[0].Harness != "pi" {
		t.Fatalf("default harness = %q, want pi", tickets[0].Harness)
	}
}

func TestRunAddEmptyTitleReturnsError(t *testing.T) {
	run, _ := setupCLI(t)
	if err := run("add"); err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestRunListEmptyBoardPrintsNothing(t *testing.T) {
	run, _ := setupCLI(t)
	// list on an empty board should not error
	if err := run("list"); err != nil {
		t.Fatalf("list on empty board failed: %v", err)
	}
}

func TestRunUnknownCommandReturnsError(t *testing.T) {
	run, _ := setupCLI(t)
	err := run("notacommand")
	if err == nil || !strings.Contains(err.Error(), "notacommand") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

// ---- boardService wiring ----

func TestBoardServiceUpdateTicketRenamesWindowWhenTitleChanges(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("AGENT_KANBAN_DB", dbPath)
	t.Setenv("AGENT_KANBAN_CONFIG", filepath.Join(dir, "config.yaml"))

	ctx := context.Background()
	cfg, _ := config.Load()
	s, _ := storage.Open(dbPath)
	t.Cleanup(func() { _ = s.Close() })
	_ = s.Init(ctx)

	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Original", "", "pi")

	// Give the ticket an active session with a window name
	sessionID, _ := s.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "pi",
		TmuxSessionName: cfg.TmuxSession,
		TmuxWindowName:  tmux.WindowName(ticket.DisplayID, ticket.Title),
		Status:          "running",
	})
	_ = sessionID

	var renamedTo string
	runner := &captureRenameRunner{onRename: func(newName string) { renamedTo = newName }}
	manager := &tmux.Manager{Config: cfg, Store: s, Runner: runner}
	svc := boardService{store: s, manager: manager}

	ticket, _ = s.TicketByID(ctx, ticket.ID)
	if err := svc.UpdateTicket(ctx, ticket.ID, "Updated Title", "", "pi"); err != nil {
		t.Fatalf("UpdateTicket failed: %v", err)
	}

	if renamedTo != tmux.WindowName(ticket.DisplayID, "Updated Title") {
		t.Fatalf("tmux window renamed to %q, want %q", renamedTo, tmux.WindowName(ticket.DisplayID, "Updated Title"))
	}

	got, _ := s.TicketByID(ctx, ticket.ID)
	if got.Title != "Updated Title" {
		t.Fatalf("DB title = %q, want Updated Title", got.Title)
	}
}

func TestBoardServiceUpdateTicketSkipsRenameWhenNoWindow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("AGENT_KANBAN_DB", dbPath)
	t.Setenv("AGENT_KANBAN_CONFIG", filepath.Join(dir, "config.yaml"))

	ctx := context.Background()
	cfg, _ := config.Load()
	s, _ := storage.Open(dbPath)
	t.Cleanup(func() { _ = s.Close() })
	_ = s.Init(ctx)

	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Original", "", "pi")
	// No session, no window

	var renamed bool
	runner := &captureRenameRunner{onRename: func(_ string) { renamed = true }}
	manager := &tmux.Manager{Config: cfg, Store: s, Runner: runner}
	svc := boardService{store: s, manager: manager}

	if err := svc.UpdateTicket(ctx, ticket.ID, "New Title", "", "pi"); err != nil {
		t.Fatalf("UpdateTicket failed: %v", err)
	}
	if renamed {
		t.Fatal("should not attempt rename when ticket has no window")
	}
}

func TestBoardServiceOpenTicketRoutesSendPromptVsSwitch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("AGENT_KANBAN_DB", dbPath)
	t.Setenv("AGENT_KANBAN_CONFIG", filepath.Join(dir, "config.yaml"))

	ctx := context.Background()
	cfg, _ := config.Load()
	s, _ := storage.Open(dbPath)
	t.Cleanup(func() { _ = s.Close() })
	_ = s.Init(ctx)

	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Route test", "", "pi")

	mock := &mockOpener{}
	// Use nil store on the manager so captureSessionRef is never attempted,
	// keeping the test instant. Session row is created manually below.
	manager := &tmux.Manager{Config: cfg, Store: nil, Runner: mock}
	svc := boardService{store: s, manager: manager}

	// sendPrompt=true → manager.OpenTicket → new-window
	if err := svc.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatalf("open with send failed: %v", err)
	}
	if !mock.sawNewWindow {
		t.Fatal("sendPrompt=true should call new-window")
	}

	// Manually record the active session so SwitchToTicket can find the window.
	manager.Store = s
	_, _ = s.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: cfg.TmuxSession,
		TmuxWindowName: tmux.WindowName(ticket.DisplayID, ticket.Title),
		TmuxWindowID:   sql.NullString{String: "@7", Valid: true},
		Status:         "running",
	})
	ticket, _ = s.TicketByID(ctx, ticket.ID)

	// sendPrompt=false on ticket with active window → SwitchToTicket → switch-client
	mock.sawSwitch = false
	if err := svc.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatalf("open without send failed: %v", err)
	}
	if !mock.sawSwitch {
		t.Fatal("sendPrompt=false on active ticket should call switch-client")
	}
}

type mockOpener struct {
	sawNewWindow bool
	sawSwitch    bool
	windows      map[string]string
}

func (m *mockOpener) Run(ctx context.Context, name string, args ...string) (string, error) {
	if m.windows == nil {
		m.windows = map[string]string{}
	}
	switch {
	case len(args) > 0 && args[0] == "new-window":
		m.sawNewWindow = true
		winName := ""
		for i, a := range args {
			if a == "-n" && i+1 < len(args) {
				winName = args[i+1]
			}
		}
		m.windows["@7"] = winName
		return "@7\n", nil
	case len(args) > 0 && args[0] == "switch-client":
		m.sawSwitch = true
		return "", nil
	case len(args) > 0 && args[0] == "list-windows":
		var b strings.Builder
		b.WriteString("board\n")
		for _, n := range m.windows {
			b.WriteString(n + "\n")
		}
		return b.String(), nil
	case len(args) > 0 && args[0] == "display-message":
		if len(args) > 0 && args[len(args)-1] == "#{window_name}" {
			for _, n := range m.windows {
				return n + "\n", nil
			}
		}
		return "@7\n", nil
	default:
		return "", nil
	}
}

func TestBoardServiceUpdateSessionRefRequiresActiveSession(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("AGENT_KANBAN_DB", dbPath)
	t.Setenv("AGENT_KANBAN_CONFIG", filepath.Join(dir, "config.yaml"))

	ctx := context.Background()
	cfg, _ := config.Load()
	s, _ := storage.Open(dbPath)
	t.Cleanup(func() { _ = s.Close() })
	_ = s.Init(ctx)

	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "No session", "", "pi")

	svc := boardService{store: s, manager: tmux.NewManager(cfg, s)}
	err := svc.UpdateSessionRef(ctx, ticket, "some-ref")
	if err == nil {
		t.Fatal("expected error updating session ref with no session")
	}
}

// ---- fake runners for boardService tests ----

type captureRenameRunner struct {
	onRename func(string)
}

func (r *captureRenameRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	for i, a := range args {
		if a == "rename-window" || (i > 0 && args[i-1] == "rename-window") {
			// last arg is the new name
		}
	}
	if len(args) > 0 && args[0] == "rename-window" {
		if r.onRename != nil {
			r.onRename(args[len(args)-1])
		}
		return "", nil
	}
	// Simulate a window existing for the original name so ticketWindowRef finds it
	if len(args) > 0 && args[0] == "list-windows" {
		return "board\nT-001-original\n", nil
	}
	if len(args) > 0 && args[0] == "display-message" {
		return "@7\n", nil
	}
	return "", nil
}

type routingRunner struct {
	onNew    func()
	onSwitch func()
	// track windows created
	windows map[string]string
}

func (r *routingRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if r.windows == nil {
		r.windows = map[string]string{}
	}
	switch {
	case len(args) > 0 && args[0] == "new-window":
		windowName := ""
		for i, a := range args {
			if a == "-n" && i+1 < len(args) {
				windowName = args[i+1]
			}
		}
		r.windows["@7"] = windowName
		if r.onNew != nil {
			r.onNew()
		}
		return "@7\n", nil
	case len(args) > 0 && args[0] == "switch-client":
		if r.onSwitch != nil {
			r.onSwitch()
		}
		return "", nil
	case len(args) > 0 && args[0] == "list-windows":
		var b strings.Builder
		b.WriteString("board\n")
		for _, n := range r.windows {
			b.WriteString(n + "\n")
		}
		return b.String(), nil
	case len(args) > 0 && args[0] == "display-message":
		for id, n := range r.windows {
			if len(args) >= 2 && args[len(args)-2] == "-t" {
				if args[len(args)-1] == "#{window_name}" {
					return n + "\n", nil
				}
				if args[len(args)-1] == "#{window_id}" {
					return id + "\n", nil
				}
			}
		}
		if len(args) > 0 && args[len(args)-1] == "#{window_id}" {
			for _, v := range r.windows {
				_ = v
				return "@7\n", nil
			}
		}
		return "@7\n", nil
	case len(args) > 0 && args[0] == "has-session":
		return "", nil
	default:
		return "", nil
	}
}

// Satisfy the tmux.Runner interface for the test runners above.
// (Runner is defined in tmux package, these structs implement it.)
var _ interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
} = (*captureRenameRunner)(nil)

var _ interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
} = (*routingRunner)(nil)

// Ensure os import is used.
var _ = os.Getenv
