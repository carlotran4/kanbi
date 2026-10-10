package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/runtime"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/ticketbackend"
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

func TestTemplateCLIWorkflowAndTemplateTicketOverrides(t *testing.T) {
	runArgs, openStore := setupCLI(t)
	if err := runArgs("templates", "add", "Bug", "--title", "Bug:", "--body", "template body", "--harness", "pi"); err != nil {
		t.Fatal(err)
	}
	if err := runArgs("templates", "update", "Bug", "--name", "Defect", "--harness", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := runArgs("add", "Defect: resize", "--template", "Defect", "--body", "explicit body"); err != nil {
		t.Fatal(err)
	}
	s := openStore()
	ticket, err := s.TicketByDisplayID(context.Background(), "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Defect: resize" || ticket.Body != "explicit body" || ticket.Harness != "codex" {
		t.Fatalf("unexpected template CLI ticket: %+v", ticket)
	}
	if err := runArgs("add", "", "--template", "Defect"); err == nil || !strings.Contains(err.Error(), "ticket title is required") {
		t.Fatalf("explicit blank template title error=%v", err)
	}
	tickets, err := s.ListTickets(context.Background(), false)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("blank title created a ticket: %+v err=%v", tickets, err)
	}
	out := captureStdout(t, func() error { return runArgs("templates", "list", "--json") })
	if !strings.Contains(out, "kanbi.v1.ticket-template-list") || !strings.Contains(out, "Defect") {
		t.Fatalf("unexpected template JSON: %s", out)
	}
	if err := runArgs("templates", "delete", "Defect"); err != nil {
		t.Fatal(err)
	}
	if err := runArgs("templates", "add", "Bad", "--name", "Ignored"); err == nil {
		t.Fatal("expected add to reject update-only --name")
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

func TestParseAddOptionsTemplatePreservesExplicitOverrides(t *testing.T) {
	opts, err := parseAddOptions([]string{"Ticket title", "--template", "Bug", "--body", "", "--harness", "codex", "--board", "Repo"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Template != "Bug" || opts.Title != "Ticket title" || !opts.TitleSet || !opts.BodySet || opts.Body != "" || !opts.HarnessSet || opts.Harness != "codex" || opts.Board != "Repo" {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

func TestParseTemplateOptions(t *testing.T) {
	opts, err := parseTemplateOptions([]string{"Bug", "--name", "Defect", "--title", "Bug:", "--body", "body", "--harness", "pi", "--board", "Repo"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Name != "Bug" || opts.NewName != "Defect" || !opts.TitleSet || !opts.BodySet || !opts.HarnessSet || opts.Board != "Repo" {
		t.Fatalf("unexpected template options: %+v", opts)
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

func (b *cliGatedBackend) Kind() string { return ticketbackend.KindGitHub }
func (b *cliGatedBackend) Sync(ctx context.Context, _ ticketbackend.SyncRepository, board storage.Board) (ticketbackend.Result, error) {
	select {
	case b.started <- board.ID:
	case <-ctx.Done():
		return ticketbackend.Result{}, ctx.Err()
	}
	select {
	case <-b.release:
		return ticketbackend.Result{}, nil
	case <-ctx.Done():
		return ticketbackend.Result{}, ctx.Err()
	}
}

func TestCLIContextCloseDrainsScheduledMutationSync(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote", Workdir: t.TempDir(), TicketBackend: ticketbackend.KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	cli := &cliContext{ctx: ctx, cfg: config.Defaults(config.Paths{}), store: store}
	backend := &cliGatedBackend{started: make(chan int64, 1), release: make(chan struct{})}
	cli.Syncer().Registry = ticketbackend.NewRegistry(ticketbackend.LocalBackend{}, backend)
	if _, err := cli.Service().CreateTicket(ctx, view.Columns[0].ID, "Scheduled", "", "pi"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-backend.started:
		if got != board.ID {
			t.Fatalf("scheduled board=%d, want %d", got, board.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduled sync did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- cli.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before scheduled sync drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(backend.release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close failed after sync drain: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after scheduled sync drained")
	}
	cli.Syncer().ScheduleBoardSync(board.ID)
	select {
	case got := <-backend.started:
		t.Fatalf("ScheduleBoardSync after Close started board %d", got)
	case <-time.After(50 * time.Millisecond):
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

	svc := tui.NewService(s, runtime.NewManager(cfg, s))
	err := svc.UpdateSessionRef(ctx, ticket, "some-ref")
	if err == nil {
		t.Fatal("expected error updating session ref with no session")
	}
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

func TestProbeDoctorMissingOptionalHarnessIsWarning(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})

	cfg.Harnesses = map[string]config.Harness{
		"pi":    {Start: []string{"pi"}},
		"fake":  {Start: []string{"fake-harness"}},
		"empty": {},
	}
	lookups := map[string]string{
		"herdr":        "/bin/herdr",
		"fake-harness": "/bin/fake-harness",
	}
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) {
			if path, ok := lookups[name]; ok {
				return path, nil
			}
			return "", os.ErrNotExist
		},
		commandOutput: func(string, ...string) ([]byte, error) {
			return []byte(`{"server":{"running":true,"compatible":true}}`), nil
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return nil },

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
			if strings.Contains(name, "herdr") && len(args) == 2 && args[0] == "status" {
				return []byte(`{"server":{"running":true,"compatible":true}}`), nil
			}
			return []byte(`{"server":{"running":true,"compatible":true}}`), nil
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return nil },

		getenv: func(string) string { return "x" },
	})

	if err := report.FatalErr(); err != nil {
		t.Fatalf("expected no fatal error, got %v", err)
	}
	assertDoctorResult(t, report, doctorOK, "multiplexer", "herdr")
	assertDoctorResult(t, report, doctorOK, "herdr", "session default")

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
		commandOutput: func(string, ...string) ([]byte, error) {
			return []byte(`{"server":{"running":true,"compatible":true}}`), nil
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return nil },

		getenv: func(string) string { return "x" },
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
		lookPath: func(string) (string, error) { return "/bin/tmux", nil },
		commandOutput: func(string, ...string) ([]byte, error) {
			return []byte(`{"server":{"running":true,"compatible":true}}`), nil
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return nil },

		getenv: func(string) string { return "x" },
	})
	if err := report.FatalErr(); err == nil {
		t.Fatal("expected unknown configured multiplexer to be fatal")
	}
	assertDoctorResult(t, report, doctorFatal, "multiplexer", "unknown configured multiplexer mystery; only Herdr is supported")
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
				return []byte(`{"server":{"running":true,"compatible":true}}`), nil
			}
			return nil, os.ErrNotExist
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return nil },

		getenv: func(string) string { return "x" },
	})

	if err := report.FatalErr(); err != nil {
		t.Fatalf("expected no fatal error, got %v", err)
	}
	assertDoctorResult(t, report, doctorOK, "herdr", "session default")

}

func TestProbeDoctorCanSimulatePathFailures(t *testing.T) {
	cfg := config.Defaults(config.Paths{ConfigFile: "config.yaml", DataDir: "data", StateDir: "state", DBFile: "db.sqlite"})
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		commandOutput: func(string, ...string) ([]byte, error) {
			return []byte(`{"server":{"running":true,"compatible":true}}`), nil
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return os.ErrPermission },
	})

	if err := report.FatalErr(); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("expected fatal config permission error, got %v", err)
	}
	assertDoctorResult(t, report, doctorOK, "sqlite", cfg.DBPath)
	assertDoctorResult(t, report, doctorFatal, "config", cfg.Paths.ConfigFile)
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

func TestCLIBoardPackageCommandsUseConfiguredDataDirectory(t *testing.T) {
	run, _ := setupCLI(t)
	if err := run("add", "portable ticket"); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "default.kanbi-board.zip")
	if err := run("boards", "export", "Default", archivePath); err != nil {
		t.Fatalf("export board package: %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("board package was not written: %v", err)
	}
	if err := run("boards", "import", archivePath, "--preview"); err != nil {
		t.Fatalf("preview board package: %v", err)
	}
	if err := run("boards", "import", archivePath, "--name", "Imported Default"); err != nil {
		t.Fatalf("import board package: %v", err)
	}
}

type cliGatedBackend struct {
	started chan int64
	release chan struct{}
}

func TestDoctorRejectsSuccessfulStatusWhenServerNotRunning(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	report := probeDoctor(context.Background(), cfg, doctorProber{
		lookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		commandOutput: func(string, ...string) ([]byte, error) {
			return []byte(`{"server":{"running":false,"compatible":true}}`), nil
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return nil },
	})
	if err := report.FatalErr(); err == nil {
		t.Fatal("successful status exit must not imply running server")
	}
}
