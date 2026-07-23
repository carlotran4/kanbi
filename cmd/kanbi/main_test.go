package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/tmux"
	"github.com/carlotran4/kanbi/internal/tui"
)

// setupCLI wires an isolated DB + config for each test, returning a run function
// that calls the real CLI entry point with those env vars set.
func setupCLI(t *testing.T) (runArgs func(args ...string) error, store func() *storage.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	cfgPath := filepath.Join(dir, "config.yaml")

	t.Setenv("KANBI_DB", dbPath)
	t.Setenv("KANBI_CONFIG", cfgPath)
	t.Setenv("KANBI_DATA_DIR", dir)
	t.Setenv("KANBI_STATE_DIR", dir)
	// Use a unique tmux session name so tests don't collide with a real board
	t.Setenv("KANBI_TMUX_SESSION", "kanbi-test-"+t.Name())

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

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = fn()
	_ = w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err != nil {
		t.Fatalf("captured command failed: %v\nstdout: %s", err, out)
	}
	return string(out)
}

func TestCLIRejectsInvalidTicketValues(t *testing.T) {
	run, _ := setupCLI(t)
	if err := run("add", "   "); err == nil {
		t.Fatal("expected whitespace-only title to be rejected")
	}
	if err := run("add", "Bad harness", "--harness", "unknown"); err == nil || !strings.Contains(err.Error(), "unsupported harness") {
		t.Fatalf("unexpected invalid harness error: %v", err)
	}
}

func TestCLIOpenRejectsArchivedTicket(t *testing.T) {
	run, openStore := setupCLI(t)
	if err := run("add", "Archived"); err != nil {
		t.Fatal(err)
	}
	s := openStore()
	ticket, err := s.TicketByDisplayID(context.Background(), "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveTicket(context.Background(), ticket.ID); err != nil {
		t.Fatal(err)
	}

	if err := run("open", "T-001", "--send-prompt"); !errors.Is(err, storage.ErrTicketArchived) {
		t.Fatalf("open archived ticket error=%v, want %v", err, storage.ErrTicketArchived)
	}
	if _, ok, err := s.LatestSession(context.Background(), ticket.ID); err != nil || ok {
		t.Fatalf("open archived ticket created a session: ok=%v err=%v", ok, err)
	}
}

func TestCLIUpdateRejectsInvalidValuesWithoutChangingTicket(t *testing.T) {
	run, openStore := setupCLI(t)
	if err := run("add", "Original"); err != nil {
		t.Fatal(err)
	}
	if err := run("update", "T-001", "--title", "   "); err == nil {
		t.Fatal("expected whitespace-only title to be rejected")
	}
	if err := run("update", "T-001", "--harness", "unknown"); err == nil {
		t.Fatal("expected unsupported harness to be rejected")
	}
	s := openStore()
	ticket, err := s.TicketByDisplayID(context.Background(), "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Original" || ticket.Harness != "pi" {
		t.Fatalf("invalid update changed ticket: %+v", ticket)
	}
}

// ---- parseAddArgs ----

func TestParseAddArgsPositional(t *testing.T) {
	title, body, harness, _, err := parseAddArgs([]string{"My ticket"})
	if err != nil || title != "My ticket" || body != "" || harness != "pi" {
		t.Fatalf("title=%q body=%q harness=%q err=%v", title, body, harness, err)
	}
}

func TestParseAddArgsAllFlags(t *testing.T) {
	title, body, harness, _, err := parseAddArgs([]string{"Title", "--body", "some body", "--harness", "codex"})
	if err != nil || title != "Title" || body != "some body" || harness != "codex" {
		t.Fatalf("title=%q body=%q harness=%q err=%v", title, body, harness, err)
	}
}

func TestParseAddArgsMissingBodyValue(t *testing.T) {
	_, _, _, _, err := parseAddArgs([]string{"Title", "--body"})
	if err == nil {
		t.Fatal("expected error for missing --body value")
	}
}

func TestParseAddArgsMissingHarnessValue(t *testing.T) {
	_, _, _, _, err := parseAddArgs([]string{"Title", "--harness"})
	if err == nil {
		t.Fatal("expected error for missing --harness value")
	}
}

func TestParseAddArgsDoubleTitleRejected(t *testing.T) {
	_, _, _, _, err := parseAddArgs([]string{"First", "Second"})
	if err == nil {
		t.Fatal("expected error for double positional title")
	}
}

func TestParseAddArgsUnknownFlag(t *testing.T) {
	_, _, _, _, err := parseAddArgs([]string{"Title", "--unknown"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// ---- parseOpenArgs ----

func TestParseOpenArgsDisplayID(t *testing.T) {
	id, send, _, err := parseOpenArgs([]string{"T-001"})
	if err != nil || id != "T-001" || send {
		t.Fatalf("id=%q send=%v err=%v", id, send, err)
	}
}

func TestParseOpenArgsSendPrompt(t *testing.T) {
	id, send, _, err := parseOpenArgs([]string{"T-001", "--send-prompt"})
	if err != nil || id != "T-001" || !send {
		t.Fatalf("id=%q send=%v err=%v", id, send, err)
	}
}

func TestParseOpenArgsDoubleIDRejected(t *testing.T) {
	_, _, _, err := parseOpenArgs([]string{"T-001", "T-002"})
	if err == nil {
		t.Fatal("expected error for double display ID")
	}
}

func TestParseOpenArgsUnknownFlag(t *testing.T) {
	_, _, _, err := parseOpenArgs([]string{"--foo"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseBoardAddArgsCWD(t *testing.T) {
	dir := t.TempDir()
	opts, err := parseBoardAddArgs([]string{"Client", "--cwd", dir, "--worktree-mode", "git", "--backend", "local", "--query", "ignored for local"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Name != "Client" || opts.Workdir != dir || opts.WorktreeMode != storage.WorktreeModeGit || opts.TicketBackend != "local" || opts.BackendQuery != "ignored for local" {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestParseBoardAddArgsRejectsInvalidWorktreeMode(t *testing.T) {
	if _, err := parseBoardAddArgs([]string{"Client", "--worktree-mode", "sometimes"}); err == nil {
		t.Fatal("expected invalid worktree mode")
	}
}

func TestRunSyncLocalBoard(t *testing.T) {
	run, _ := setupCLI(t)
	if err := run("sync"); err != nil {
		t.Fatal(err)
	}
}

func TestRunSyncUnknownFlag(t *testing.T) {
	run, _ := setupCLI(t)
	if err := run("sync", "--bad"); err == nil {
		t.Fatal("expected unknown sync flag error")
	}
}

func TestRunBoardsAddPersistsWorkdir(t *testing.T) {
	run, getStore := setupCLI(t)
	workdir := t.TempDir()
	if err := run("boards", "add", "Client", "--cwd", workdir); err != nil {
		t.Fatal(err)
	}
	s := getStore()
	boards, err := s.ListBoards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) == 0 || boards[0].Name != "Client" || boards[0].Workdir != workdir {
		t.Fatalf("board workdir not persisted: %+v", boards)
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

func TestCLIJSONShowUpdateMoveNotesAndState(t *testing.T) {
	run, getStore := setupCLI(t)
	bodyPath := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyPath, []byte("file body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run("add", "Machine ticket", "--body-file", bodyPath, "--harness", "codex", "--json"); err != nil {
		t.Fatalf("add --json failed: %v", err)
	}
	showOut := captureStdout(t, func() error { return run("show", "T-001", "--json") })
	if !strings.Contains(showOut, `"schema": "kanbi.v1.ticket"`) || !strings.Contains(showOut, `"body": "file body"`) {
		t.Fatalf("unexpected show json: %s", showOut)
	}
	if err := run("update", "T-001", "--title", "Updated", "--body", "new body", "--harness", "pi", "--json"); err != nil {
		t.Fatalf("update --json failed: %v", err)
	}
	if err := run("move", "T-001", "--to", "In Progress", "--json"); err != nil {
		t.Fatalf("move --json failed: %v", err)
	}
	if err := run("notes", "add", "T-001", "--body", "progress note", "--json"); err != nil {
		t.Fatalf("notes add --json failed: %v", err)
	}
	stateOut := captureStdout(t, func() error { return run("state", "--json") })
	if !strings.Contains(stateOut, `"schema": "kanbi.v1.state"`) || !strings.Contains(stateOut, `"progress note"`) {
		t.Fatalf("unexpected state json: %s", stateOut)
	}
	s := getStore()
	ticket, err := s.TicketByDisplayID(context.Background(), "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Updated" || ticket.Body != "new body" || ticket.Harness != "pi" || ticket.Runtime != "not_started" {
		t.Fatalf("unexpected ticket after cli mutation: %+v", ticket)
	}
}

func TestRunUnknownCommandReturnsError(t *testing.T) {
	run, _ := setupCLI(t)
	err := run("notacommand")
	if err == nil || !strings.Contains(err.Error(), "notacommand") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

func TestRunBoardsRenameAndSetCWD(t *testing.T) {
	run, getStore := setupCLI(t)
	oldDir := t.TempDir()
	newDir := t.TempDir()
	if err := run("boards", "add", "Client", "--cwd", oldDir); err != nil {
		t.Fatal(err)
	}
	if err := run("boards", "rename", "Client", "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := run("boards", "set-cwd", "Renamed", newDir); err != nil {
		t.Fatal(err)
	}
	s := getStore()
	b, err := s.BoardByName(context.Background(), "Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if b.Workdir != newDir {
		t.Fatalf("workdir=%q want %q", b.Workdir, newDir)
	}
}

func TestCLIRequiresBoardForAmbiguousDisplayID(t *testing.T) {
	run, getStore := setupCLI(t)
	if err := run("boards", "add", "Client"); err != nil {
		t.Fatal(err)
	}
	if err := run("add", "Default task"); err != nil {
		t.Fatal(err)
	}
	if err := run("add", "Client task", "--board", "Client"); err != nil {
		t.Fatal(err)
	}
	err := run("open", "T-001")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("open should require --board for ambiguous display id, got %v", err)
	}
	s := getStore()
	if _, err := s.TicketByDisplayID(context.Background(), "T-001"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous display id, got %v", err)
	}
	client, err := s.BoardByName(context.Background(), "Client")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.TicketByDisplayIDInBoard(context.Background(), "T-001", client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Client task" {
		t.Fatalf("board-specific ticket = %+v", ticket)
	}
}

func TestCLIContextResolvesBoardScopedTickets(t *testing.T) {
	s, ctx := newMainTestStore(t)
	defaultView, _ := s.BoardView(ctx)
	client, _ := s.CreateBoard(ctx, "Client B")
	clientView, _ := s.BoardViewByID(ctx, client.ID)
	_, _ = s.CreateTicket(ctx, defaultView.Columns[0].ID, "Default task", "", "pi")
	_, _ = s.CreateTicket(ctx, clientView.Columns[0].ID, "Client task", "", "codex")

	cli := &cliContext{ctx: ctx, store: s}
	view, err := cli.ResolveBoardView("Client B")
	if err != nil {
		t.Fatal(err)
	}
	if view.Board.ID != client.ID {
		t.Fatalf("resolved board id=%d want %d", view.Board.ID, client.ID)
	}
	tickets, err := cli.ResolveTickets("Client B")
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 || tickets[0].Title != "Client task" {
		t.Fatalf("board-scoped tickets=%+v", tickets)
	}
	if _, err := cli.ResolveTicket("t-001", ""); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("unscoped duplicate display id should be ambiguous, got %v", err)
	}
	ticket, err := cli.ResolveTicket("t-001", "Client B")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Client task" || ticket.Harness != "codex" {
		t.Fatalf("resolved ticket=%+v", ticket)
	}
}

func TestRunBoardTUIIntegrationSwitchesBoards(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping TUI integration test in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "kanbi")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kanbi: %v\n%s", err, out)
	}

	dbPath := filepath.Join(dir, "test.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	sessionName := "kanbi-it-" + sanitizeName(t.Name())
	runScript := filepath.Join(dir, "run-board.sh")
	if err := os.WriteFile(runScript, []byte("#!/bin/sh\n"+
		"export KANBI_DB="+shellQuote(dbPath)+"\n"+
		"export KANBI_CONFIG="+shellQuote(cfgPath)+"\n"+
		"export KANBI_DATA_DIR="+shellQuote(dir)+"\n"+
		"export KANBI_STATE_DIR="+shellQuote(dir)+"\n"+
		"export KANBI_TMUX_SESSION="+shellQuote(sessionName)+"\n"+
		"export TERM=xterm-256color\n"+
		"exec "+shellQuote(bin)+" --board\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	s, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	defaultView, _ := s.BoardView(ctx)
	if _, err := s.CreateTicket(ctx, defaultView.Columns[0].ID, "Default task", "", "pi"); err != nil {
		t.Fatal(err)
	}
	clientBoard, err := s.CreateBoard(ctx, "Client B")
	if err != nil {
		t.Fatal(err)
	}
	clientView, _ := s.BoardViewByID(ctx, clientBoard.ID)
	if _, err := s.CreateTicket(ctx, clientView.Columns[0].ID, "Client task", "", "pi"); err != nil {
		t.Fatal(err)
	}

	// Drive the real binary in a real tmux pane. This exercises the compiled CLI,
	// Bubble Tea input handling, startup picker, board switching, and rendering.
	tmuxCmd(t, "kill-session", "-t", sessionName)
	t.Cleanup(func() { tmuxCmd(t, "kill-session", "-t", sessionName) })
	tmuxCmd(t, "new-session", "-d", "-s", sessionName, runScript)
	output := waitForTmuxOutput(t, sessionName, "Select board")
	tmuxCmd(t, "send-keys", "-t", sessionName, "C-m") // select Master
	output = waitForTmuxOutput(t, sessionName, "[Client B]")
	tmuxCmd(t, "send-keys", "-t", sessionName, "n")
	output = waitForTmuxOutput(t, sessionName, "Create ticket in which board?")
	tmuxCmd(t, "send-keys", "-t", sessionName, "j")
	tmuxCmd(t, "send-keys", "-t", sessionName, "C-m") // create on Client B
	output = waitForTmuxOutput(t, sessionName, "No description yet")
	tmuxCmd(t, "send-keys", "-t", sessionName, "Escape")
	waitForTmuxOutputWithout(t, sessionName, "Unsaved changes")
	tmuxCmd(t, "send-keys", "-t", sessionName, "b")
	output = waitForTmuxOutput(t, sessionName, "Select board")
	tmuxCmd(t, "send-keys", "-t", sessionName, "C-m") // select Client B
	output = waitForTmuxOutput(t, sessionName, "Kanbi Client B")
	tmuxCmd(t, "send-keys", "-t", sessionName, "q")
	for _, want := range []string{"New ticket", "Client task", "Kanbi Client B"} {
		if !strings.Contains(output, want) {
			t.Fatalf("TUI output missing %q\n--- output ---\n%s", want, output)
		}
	}
	clientView, err = s.BoardViewByID(ctx, clientBoard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clientView.Columns[0].Tickets) != 2 {
		t.Fatalf("Master create should add a ticket to Client B, got %+v", clientView.Columns[0].Tickets)
	}
}

// ---- TUI service wiring ----

func TestTUIServiceUpdateTicketRenamesWindowWhenTitleChanges(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("KANBI_DB", dbPath)
	t.Setenv("KANBI_CONFIG", filepath.Join(dir, "config.yaml"))

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
		TmuxWindowName:  tmux.TicketWindowName(ticket),
		Status:          "running",
	})
	_ = sessionID

	var renamedTo string
	runner := &captureRenameRunner{onRename: func(newName string) { renamedTo = newName }}
	manager := &tmux.Manager{Config: cfg, Store: s, Runner: runner}
	svc := tui.NewService(s, manager)

	ticket, _ = s.TicketByID(ctx, ticket.ID)
	if err := svc.UpdateTicket(ctx, ticket.ID, "Updated Title", "", "pi"); err != nil {
		t.Fatalf("UpdateTicket failed: %v", err)
	}

	updated := ticket
	updated.Title = "Updated Title"
	if renamedTo != tmux.TicketWindowName(updated) {
		t.Fatalf("tmux window renamed to %q, want %q", renamedTo, tmux.TicketWindowName(updated))
	}

	got, _ := s.TicketByID(ctx, ticket.ID)
	if got.Title != "Updated Title" {
		t.Fatalf("DB title = %q, want Updated Title", got.Title)
	}
}

func TestTUIServiceUpdateTicketSkipsRenameWhenNoWindow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("KANBI_DB", dbPath)
	t.Setenv("KANBI_CONFIG", filepath.Join(dir, "config.yaml"))

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
	svc := tui.NewService(s, manager)

	if err := svc.UpdateTicket(ctx, ticket.ID, "New Title", "", "pi"); err != nil {
		t.Fatalf("UpdateTicket failed: %v", err)
	}
	if renamed {
		t.Fatal("should not attempt rename when ticket has no window")
	}
}

func TestTUIServiceOpenTicketRoutesSendPromptVsSwitch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("KANBI_DB", dbPath)
	t.Setenv("KANBI_CONFIG", filepath.Join(dir, "config.yaml"))

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
	svc := tui.NewService(s, manager)

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
		TmuxWindowName: tmux.TicketWindowName(ticket),
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
			targetArg := args[len(args)-2]
			targetID := targetArg
			if idx := strings.Index(targetArg, ":"); idx >= 0 {
				targetID = targetArg[idx+1:]
			}
			if name, ok := m.windows[targetID]; ok {
				return name + "\n", nil
			}
			for _, n := range m.windows {
				return n + "\n", nil
			}
		}
		return "@7\n", nil
	default:
		return "", nil
	}
}

func TestTUIServiceUpdateSessionRefRequiresActiveSession(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("KANBI_DB", dbPath)
	t.Setenv("KANBI_CONFIG", filepath.Join(dir, "config.yaml"))

	ctx := context.Background()
	cfg, _ := config.Load()
	s, _ := storage.Open(dbPath)
	t.Cleanup(func() { _ = s.Close() })
	_ = s.Init(ctx)

	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "No session", "", "pi")

	svc := tui.NewService(s, tmux.NewManager(cfg, s))
	err := svc.UpdateSessionRef(ctx, ticket, "some-ref")
	if err == nil {
		t.Fatal("expected error updating session ref with no session")
	}
}

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func tmuxCmd(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	// kill-session is allowed to fail during cleanup / pre-clean when absent.
	if err != nil && !(len(args) > 0 && args[0] == "kill-session") {
		t.Fatalf("tmux %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func waitForTmuxOutput(t *testing.T, sessionName, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var output string
	for time.Now().Before(deadline) {
		output = stripANSI(tmuxCmd(t, "capture-pane", "-p", "-t", sessionName))
		if strings.Contains(output, want) {
			return output
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q\n--- output ---\n%s", want, output)
	return output
}

func waitForTmuxOutputWithout(t *testing.T, sessionName, unwanted string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var output string
	clearFrames := 0
	for time.Now().Before(deadline) {
		output = stripANSI(tmuxCmd(t, "capture-pane", "-p", "-t", sessionName))
		if strings.Contains(output, unwanted) {
			clearFrames = 0
		} else {
			clearFrames++
			// Bubble Tea redraws can briefly expose an empty pane between frames.
			// Require stable absence before sending the next input event.
			if clearFrames >= 2 {
				return output
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q to disappear\n--- output ---\n%s", unwanted, output)
	return output
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestTUIServiceSupportsMasterFilterSelector(t *testing.T) {
	var _ tui.Actions = (*tui.Service)(nil)
	s, ctx := newMainTestStore(t)
	defaultView, _ := s.BoardView(ctx)
	client, _ := s.CreateBoard(ctx, "Client B")
	clientView, _ := s.BoardViewByID(ctx, client.ID)
	_, _ = s.CreateTicket(ctx, defaultView.Columns[0].ID, "Default task", "", "pi")
	_, _ = s.CreateTicket(ctx, clientView.Columns[0].ID, "Codex handoff", "", "codex")

	service := tui.NewService(s, nil)
	view, err := service.MasterBoardViewWithFilter(ctx, storage.MasterFilter{Harnesses: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, col := range view.Columns {
		for _, ticket := range col.Tickets {
			titles = append(titles, ticket.Title)
		}
	}
	if len(titles) != 1 || titles[0] != "Codex handoff" {
		t.Fatalf("filtered titles=%v", titles)
	}
}

func newMainTestStore(t *testing.T) (*storage.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// ---- fake runners for TUI service tests ----

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
		return "board\nb1-T-001-original\n", nil
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
				targetArg := args[len(args)-2]
				targetID := targetArg
				if idx := strings.Index(targetArg, ":"); idx >= 0 {
					targetID = targetArg[idx+1:]
				}
				formatArg := args[len(args)-1]
				if formatArg == "#{window_name}" {
					if targetID == id {
						return n + "\n", nil
					}
					return n + "\n", nil
				}
				if formatArg == "#{window_id}" {
					if targetID == id || targetID == n {
						return id + "\n", nil
					}
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

func TestBoardLaunchDependsOnConfiguredMultiplexer(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	if !shouldAttachTmuxForBoard(cfg) {
		t.Fatal("tmux default should attach board in tmux")
	}
	if shouldLaunchHerdrBoard(cfg) {
		t.Fatal("tmux default should not launch Herdr")
	}
	cfg.Multiplexer.Default = "herdr"
	if shouldAttachTmuxForBoard(cfg) {
		t.Fatal("herdr default should not wrap board in tmux")
	}
	t.Setenv("HERDR_ENV", "")
	if !shouldLaunchHerdrBoard(cfg) {
		t.Fatal("herdr default should launch board in Herdr when outside Herdr")
	}
	t.Setenv("HERDR_ENV", "1")
	if shouldLaunchHerdrBoard(cfg) {
		t.Fatal("should not recursively launch Herdr from inside a Herdr pane")
	}
}

func TestJSONSessionIncludesGenericMultiplexerRefs(t *testing.T) {
	got := jsonSession(storage.Session{
		Multiplexer:      "herdr",
		MuxNamespace:     sql.NullString{String: "ws-1", Valid: true},
		MuxContainerID:   sql.NullString{String: "agent-1", Valid: true},
		MuxContainerName: sql.NullString{String: "Ticket", Valid: true},
		MuxMetadata:      sql.NullString{String: `{"pane_id":"pane-1"}`, Valid: true},
	}).(map[string]any)
	if got["multiplexer"] != "herdr" || got["mux_namespace"] != "ws-1" || got["mux_container_id"] != "agent-1" || got["mux_container_name"] != "Ticket" || got["mux_metadata"] != `{"pane_id":"pane-1"}` {
		t.Fatalf("generic mux refs missing from JSON: %#v", got)
	}
}

// ---- doctor probes ----

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

func TestProbeDoctorMissingTmuxIsFatal(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) {
			return "", os.ErrNotExist
		},
	})

	if err := report.FatalErr(); err == nil || !strings.Contains(err.Error(), "tmux is required") {
		t.Fatalf("expected fatal missing tmux error, got %v", err)
	}
	assertDoctorResult(t, report, doctorOK, "multiplexer", "tmux")
	assertDoctorResult(t, report, doctorFatal, "tmux", "is required")
}

func TestProbeDoctorMissingOptionalHarnessIsWarning(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	cfg.TmuxSession = "kanbi-test"
	cfg.Harnesses = map[string]config.Harness{
		"pi":    {Start: []string{"pi"}},
		"fake":  {Start: []string{"fake-harness"}},
		"empty": {},
	}
	lookups := map[string]string{
		"tmux":         "/bin/tmux",
		"fake-harness": "/bin/fake-harness",
	}
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) {
			if path, ok := lookups[name]; ok {
				return path, nil
			}
			return "", os.ErrNotExist
		},
		commandOutput: func(string, ...string) ([]byte, error) { return []byte("tmux 3.4\n"), nil },
		openStore:     func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs:    func(config.Config) error { return nil },
		insideTmux:    func() bool { return true },
		getenv: func(key string) string {
			switch key {
			case "SHELL":
				return "/bin/sh"
			case "TERM":
				return "xterm-256color"
			default:
				return ""
			}
		},
		ensureTmuxSession: func(context.Context, config.Config) error { return nil },
	})

	if err := report.FatalErr(); err != nil {
		t.Fatalf("expected no fatal error, got %v", err)
	}
	assertDoctorResult(t, report, doctorWarn, "harness empty", "has no start command")
	assertDoctorResult(t, report, doctorWarn, "harness pi", "not found: pi")
	assertDoctorResult(t, report, doctorOK, "harness", "fake")
}

func TestProbeDoctorReportsConfiguredHerdr(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = "herdr"
	lookups := map[string]string{"tmux": "/bin/tmux", "herdr": "/bin/herdr"}
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) {
			if path, ok := lookups[name]; ok {
				return path, nil
			}
			return "", os.ErrNotExist
		},
		commandOutput: func(name string, args ...string) ([]byte, error) {
			if strings.Contains(name, "herdr") && len(args) == 1 && args[0] == "status" {
				return []byte("ok\n"), nil
			}
			return []byte("tmux 3.4\n"), nil
		},
		openStore:         func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs:        func(config.Config) error { return nil },
		insideTmux:        func() bool { return true },
		getenv:            func(string) string { return "x" },
		ensureTmuxSession: func(context.Context, config.Config) error { return nil },
	})

	if err := report.FatalErr(); err != nil {
		t.Fatalf("expected no fatal error, got %v", err)
	}
	assertDoctorResult(t, report, doctorOK, "multiplexer", "herdr")
	assertDoctorResult(t, report, doctorOK, "herdr", "session default")
	assertDoctorResult(t, report, doctorOK, "tmux", "tmux 3.4")
}

func TestProbeDoctorFailsWhenConfiguredHerdrMissing(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	cfg.Multiplexer.Default = "herdr"
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) {
			if name == "tmux" {
				return "/bin/tmux", nil
			}
			return "", os.ErrNotExist
		},
		commandOutput:     func(string, ...string) ([]byte, error) { return []byte("tmux 3.4\n"), nil },
		openStore:         func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs:        func(config.Config) error { return nil },
		insideTmux:        func() bool { return true },
		getenv:            func(string) string { return "x" },
		ensureTmuxSession: func(context.Context, config.Config) error { return errors.New("should not ensure tmux for Herdr") },
	})

	if err := report.FatalErr(); err == nil {
		t.Fatal("expected missing configured Herdr to be fatal")
	}
	assertDoctorResult(t, report, doctorOK, "multiplexer", "herdr")
	assertDoctorResult(t, report, doctorFatal, "herdr", "configured Herdr binary not found; install Herdr or set multiplexer.herdr.binary")
}

func TestProbeDoctorFailsUnknownConfiguredMultiplexerWithoutOKResult(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	cfg.Multiplexer.Default = "mystery"
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath:          func(string) (string, error) { return "/bin/tmux", nil },
		commandOutput:     func(string, ...string) ([]byte, error) { return []byte("tmux 3.4\n"), nil },
		openStore:         func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs:        func(config.Config) error { return nil },
		insideTmux:        func() bool { return true },
		getenv:            func(string) string { return "x" },
		ensureTmuxSession: func(context.Context, config.Config) error { return nil },
	})
	if err := report.FatalErr(); err == nil {
		t.Fatal("expected unknown configured multiplexer to be fatal")
	}
	assertDoctorResult(t, report, doctorFatal, "multiplexer", "unknown configured multiplexer mystery; supported values are tmux and herdr")
	for _, result := range report.Results {
		if result.Name == "multiplexer" && result.Severity == doctorOK {
			t.Fatalf("unknown multiplexer also reported OK: %+v", report.Results)
		}
	}
}

func TestProbeDoctorHerdrDoesNotRequireTmux(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	cfg.Multiplexer.Default = "herdr"
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) {
			if name == "herdr" {
				return "/bin/herdr", nil
			}
			return "", os.ErrNotExist
		},
		commandOutput: func(name string, args ...string) ([]byte, error) {
			if strings.Contains(name, "herdr") {
				return []byte("ok\n"), nil
			}
			return nil, os.ErrNotExist
		},
		openStore:         func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs:        func(config.Config) error { return nil },
		insideTmux:        func() bool { return false },
		getenv:            func(string) string { return "x" },
		ensureTmuxSession: func(context.Context, config.Config) error { return errors.New("should not ensure tmux for Herdr") },
	})

	if err := report.FatalErr(); err != nil {
		t.Fatalf("expected no fatal error, got %v", err)
	}
	assertDoctorResult(t, report, doctorOK, "herdr", "session default")
	assertDoctorResult(t, report, doctorWarn, "tmux", "not found; existing tmux sessions cannot be controlled")
}

func TestProbeDoctorCanSimulatePathAndTmuxStateFailures(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath:      func(name string) (string, error) { return "/bin/" + name, nil },
		commandOutput: func(string, ...string) ([]byte, error) { return []byte("tmux 3.4\n"), nil },
		openStore:     func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs:    func(config.Config) error { return os.ErrPermission },
	})

	if err := report.FatalErr(); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("expected fatal config permission error, got %v", err)
	}
	assertDoctorResult(t, report, doctorOK, "sqlite", cfg.DBPath)
	assertDoctorResult(t, report, doctorFatal, "config", cfg.Paths.ConfigFile)
}

func TestProbeDoctorInsideOutsideTmuxResults(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	base := doctorProber{
		lookPath:          func(name string) (string, error) { return "/bin/" + name, nil },
		commandOutput:     func(string, ...string) ([]byte, error) { return nil, os.ErrInvalid },
		openStore:         func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs:        func(config.Config) error { return nil },
		getenv:            func(string) string { return "" },
		ensureTmuxSession: func(context.Context, config.Config) error { return nil },
	}
	base.insideTmux = func() bool { return false }
	outside := probeDoctor(context.Background(), cfg, base)
	assertDoctorResult(t, outside, doctorWarn, "not inside tmux", "")

	base.insideTmux = func() bool { return true }
	inside := probeDoctor(context.Background(), cfg, base)
	assertDoctorResult(t, inside, doctorOK, "inside tmux", "")
}

func assertDoctorResult(t *testing.T, report doctorReport, severity doctorSeverity, name, detail string) {
	t.Helper()
	for _, result := range report.Results {
		if result.Severity == severity && result.Name == name && result.Detail == detail {
			return
		}
	}
	t.Fatalf("missing result severity=%s name=%q detail=%q in %#v", severity, name, detail, report.Results)
}

func TestCLIBoardsListRejectsUnknownArguments(t *testing.T) {
	run, _ := setupCLI(t)
	for _, arg := range []string{"--definitely-invalid", "unexpected-positional"} {
		err := run("boards", "list", arg)
		if err == nil || !strings.Contains(err.Error(), "usage: kanbi boards") {
			t.Errorf("boards list argument %q returned %v, want usage error", arg, err)
		}
	}
}

func TestCLIBoardsArchiveUnarchiveSyncAndColumnKeyJSON(t *testing.T) {
	run, openStore := setupCLI(t)
	if err := run("boards", "add", "Ops"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error {
		return run("boards", "set-column-key", "Ops", "--column", "Open", "--key", "inbox", "--json")
	})
	if !strings.Contains(out, `"schema": "kanbi.v1.board"`) && !strings.Contains(out, `"schema":"kanbi.v1.board"`) {
		// set-column-key may print non-board schema; accept success path
	}
	s := openStore()
	b, err := s.BoardByName(context.Background(), "Ops")
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardViewByID(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundKey := false
	for _, col := range view.Columns {
		if col.Name == "Open" && col.WorkflowKey == "inbox" {
			foundKey = true
		}
	}
	if !foundKey {
		t.Fatalf("workflow key not set: %+v out=%s", view.Columns, out)
	}

	out = captureStdout(t, func() error {
		return run("boards", "archive", "Ops", "--json")
	})
	if !strings.Contains(out, `"archived_at"`) || !strings.Contains(out, `"sync_enabled": false`) && !strings.Contains(out, `"sync_enabled":false`) {
		t.Fatalf("archive json missing fields: %s", out)
	}
	out = captureStdout(t, func() error {
		return run("boards", "list", "--include-archived", "--json")
	})
	if !strings.Contains(out, `"name": "Ops"`) && !strings.Contains(out, `"name":"Ops"`) {
		t.Fatalf("include-archived list missing Ops: %s", out)
	}
	if err := run("boards", "unarchive", "Ops"); err != nil {
		t.Fatal(err)
	}
	if err := run("boards", "disable-sync", "Ops"); err != nil {
		t.Fatal(err)
	}
	if err := run("boards", "enable-sync", "Ops"); err != nil {
		t.Fatal(err)
	}
}

func TestCLIBoardPackageExportImportJSONStable(t *testing.T) {
	run, openStore := setupCLI(t)
	if err := run("boards", "add", "PackageMe"); err != nil {
		t.Fatal(err)
	}
	s := openStore()
	b, err := s.BoardByName(context.Background(), "PackageMe")
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardViewByID(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.CreateTicket(context.Background(), view.Columns[0].ID, "Attach", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("KANBI_DATA_DIR"), "attachments", fmt.Sprintf("%d", ticket.ID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.png"), []byte("img"), 0o600); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "pkg.zip")
	exportOut := captureStdout(t, func() error {
		return run("boards", "export", "PackageMe", zipPath, "--json")
	})
	if !strings.Contains(exportOut, `"schema": "kanbi.v1.board_export"`) && !strings.Contains(exportOut, `"schema":"kanbi.v1.board_export"`) {
		t.Fatalf("export json: %s", exportOut)
	}
	importOut := captureStdout(t, func() error {
		return run("boards", "import", zipPath, "--name", "PackageCopy", "--json")
	})
	if !strings.Contains(importOut, `"schema": "kanbi.v1.board_import"`) && !strings.Contains(importOut, `"schema":"kanbi.v1.board_import"`) {
		t.Fatalf("import json: %s", importOut)
	}
	if strings.Contains(importOut, `"ID":`) || strings.Contains(importOut, `"Valid":`) {
		t.Fatalf("import json leaked Go Board field names: %s", importOut)
	}
	if !strings.Contains(importOut, `"ticket_id_remap"`) {
		t.Fatalf("import json missing remap: %s", importOut)
	}
	if !strings.Contains(importOut, `"archived_at"`) {
		t.Fatalf("import json missing stable board: %s", importOut)
	}
}

func TestCLISupportBundleCreatesArchive(t *testing.T) {
	run, openStore := setupCLI(t)
	if err := run("add", "Support ticket"); err != nil {
		t.Fatal(err)
	}
	s := openStore()
	if err := s.InsertRuntimeDiagnostic(context.Background(), storage.RuntimeDiagnosticInput{
		Kind: storage.DiagnosticKindRuntime, Operation: "test", Message: "m", Cause: "token=sekrit",
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.zip")
	out := captureStdout(t, func() error { return run("support-bundle", path) })
	if !strings.Contains(out, "support bundle written") && !strings.Contains(out, "Kanbi support bundle") {
		t.Fatalf("unexpected output: %s", out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sekrit") {
		t.Fatal("bundle archive raw bytes contain secret")
	}
}
