package tmux

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-kanban/internal/config"
	"agent-kanban/internal/storage"
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
		f.windows["@7"] = windowName
		return "@7\n", nil
	case len(args) >= 1 && args[0] == "display-message":
		if len(args) >= 5 && args[len(args)-1] == "#{window_name}" {
			if f.windows != nil {
				if name, ok := f.windows[args[len(args)-2]]; ok {
					return name + "\n", nil
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

func TestWindowNameAndShellCommand(t *testing.T) {
	if got := WindowName("T-001", "Fix OAuth Redirect!"); got != "T-001-fix-oauth-redirect" {
		t.Fatalf("window name = %s", got)
	}
	if got := ShellCommand([]string{"/tmp/fake harness", "resume", "abc"}); got != "'/tmp/fake harness' resume abc" {
		t.Fatalf("shell command = %s", got)
	}
}

func TestOpenTicketCreatesWindowAndPastesPrompt(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	cfg.PromptReadyTimeout = time.Second
	cfg.Harnesses["pi"] = config.Harness{Start: []string{"/tmp/fake-pi"}, PromptReady: "PROMPT_READY"}
	runner := &fakeRunner{}
	manager := &Manager{Config: cfg, Runner: runner}
	ticket := storage.Ticket{ID: 1, DisplayID: "T-001", Title: "Demo", Body: "Body", Harness: "pi"}
	if err := manager.OpenTicket(context.Background(), ticket, true); err != nil {
		t.Fatal(err)
	}
	var sawNew, sawSetBuffer, sawPaste bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			sawNew = true
			if !strings.Contains(strings.Join(c.args, " "), "T-001-demo") {
				t.Fatalf("new-window args = %#v", c.args)
			}
		}
		if len(c.args) > 0 && c.args[0] == "set-buffer" {
			sawSetBuffer = strings.Contains(strings.Join(c.args, "\n"), "# T-001: Demo\n\nBody")
		}
		if len(c.args) > 0 && c.args[0] == "paste-buffer" {
			sawPaste = true
		}
	}
	if !sawNew || !sawSetBuffer || !sawPaste {
		t.Fatalf("calls missing new=%v set=%v paste=%v calls=%+v", sawNew, sawSetBuffer, sawPaste, runner.calls)
	}
}

func TestPasteModeCapturesSessionRefFromPane(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view, _ := store.BoardView(ctx)
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

func TestOpenTicketRecordsWindowID(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
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
	if got.WindowID.String != "@7" || got.WindowName.String != "T-001-demo" {
		t.Fatalf("window metadata = id:%q name:%q", got.WindowID.String, got.WindowName.String)
	}
}

func TestOpenTicketExistingActiveWindowDoesNotCreateNewSessionRow(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view, _ := store.BoardView(ctx)
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

func TestRefreshRuntimeDetectsAttentionState(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view, _ := store.BoardView(ctx)
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
	view, _ := store.BoardView(ctx)
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
	view, _ := store.BoardView(ctx)
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

func TestCloseSessionSendsGracefulExitBeforeKill(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view, _ := store.BoardView(ctx)
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
		WindowID:      sqlString("@7"),
		WindowName:    sqlString("T-001-demo"),
		SessionActive: true,
	}
	if err := manager.SwitchToTicket(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "switch-client" && strings.Join(c.args, " ") == "switch-client -t @7" {
			return
		}
	}
	t.Fatalf("switch-client @7 not called: %+v", runner.calls)
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

func sqlString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
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
