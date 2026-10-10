package runtime

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/harness"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestOpenTicketWithHerdrDefaultStoresContainerMetadata(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Herdr Launch", "body", "codex")
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board","cwd":"` + view.Board.Workdir + `"}`,
		"agent start help": `--kind <KIND> --pane <ID>`,
		"tab create":       `{"result":{"root_pane":{"pane_id":"pane-123"},"tab":{"tab_id":"tab-456"}}}`,
		"agent start":      `{"result":{"agent":{"name":"agent-789"}}}`,
		"agent focus":      `{"ok":true}`,
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	cfg.Multiplexer.Herdr.FocusOnOpen = true
	manager := &Manager{Config: cfg, Store: store}

	if err := manager.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if !got.Multiplexer.Valid || got.Multiplexer.String != "herdr" || got.MuxNamespace.String != "ws-board" || got.MuxContainerID.String != "agent-789" {
		t.Fatalf("ticket mux metadata not stored: %+v", got)
	}
	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if !strings.Contains(log, "agent start b1-t-001-herdr-launch-") || !strings.Contains(log, "--kind codex --pane pane-123 -- --no-alt-screen") {
		t.Fatalf("fake Herdr did not receive the open-only pane-first harness command; log=%s", log)
	}
	if strings.Contains(log, "--no-alt-screen # T-001") {
		t.Fatalf("pane-first Herdr must not receive the rendered prompt as an agent argument; log=%s", log)
	}
	if !strings.Contains(log, "agent prompt agent-789 # T-001: Herdr Launch") {
		t.Fatalf("fake Herdr did not receive the prompt through pane input; log=%s", log)
	}
	createAt := strings.Index(log, "tab create --workspace ws-board --label b1-T-001-herdr-launch")
	startAt := strings.Index(log, "agent start b1-t-001-herdr-launch-")
	focusAt := strings.Index(log, "agent focus agent-789")
	if createAt < 0 || startAt < createAt || focusAt < startAt || strings.Contains(log, "pane split") || strings.Contains(log, "pane move") {
		t.Fatalf("Herdr focus must happen only after the new agent starts; log=%s", log)
	}
}

func TestPaneFirstHerdrCapturesCopilotRefAfterPanePrompt(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Herdr Copilot Ref", "body", "copilot")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	refDB, err := sql.Open("sqlite3", filepath.Join(home, ".copilot", "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer refDB.Close()
	if _, err := refDB.Exec(`
CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT, created_at TEXT);
CREATE TABLE turns (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, turn_index INTEGER NOT NULL, user_message TEXT);
INSERT INTO sessions(id,cwd,created_at) VALUES(?,?,?);
INSERT INTO turns(session_id,turn_index,user_message) VALUES(?,0,?);`,
		"copilot-herdr-ref", view.Board.Workdir, time.Now().UTC().Format(time.RFC3339Nano),
		"copilot-herdr-ref", "# T-001: Herdr Copilot Ref\n\nbody"); err != nil {
		t.Fatal(err)
	}
	if ref, ok := harness.CaptureSessionRefInCWD("copilot", "# T-001: Herdr Copilot Ref\n\nbody", view.Board.Workdir, time.Now().UTC()); !ok || ref != "copilot-herdr-ref" {
		t.Fatalf("copilot fixture ref=%q ok=%v", ref, ok)
	}
	bin, _ := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board","cwd":"` + view.Board.Workdir + `"}`,
		"agent start help": `--kind <KIND> --pane <ID>`,
		"tab create":       `{"result":{"root_pane":{"pane_id":"pane-123"},"tab":{"tab_id":"tab-456"}}}`,
		"agent start":      `{"result":{"agent":{"name":"agent-789"}}}`,
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	manager := NewManager(cfg, store)
	manager.RefCapturePollInterval = time.Millisecond
	t.Cleanup(manager.Close)

	if err := manager.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("latest session: ok=%v err=%v", ok, err)
	}
	waitForSessionRef(t, store, latest.ID, "copilot-herdr-ref")
}

func TestPaneFirstHerdrPromptFailureClosesContainerAndDeactivatesClaim(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Herdr Prompt Failure", "body", "codex")
	if err != nil {
		t.Fatal(err)
	}
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board","cwd":"` + view.Board.Workdir + `"}`,
		"agent start help": `--kind <KIND> --pane <ID>`,
		"tab create":       `{"result":{"root_pane":{"pane_id":"pane-123"},"tab":{"tab_id":"tab-456"}}}`,
		"agent start":      `{"result":{"agent":{"name":"agent-789"}}}`,
		"agent prompt":     "__ERROR__",
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	cfg.Multiplexer.Herdr.FocusOnOpen = true
	manager := &Manager{Config: cfg, Store: store}

	if err := manager.OpenTicket(ctx, ticket, true); err == nil {
		t.Fatal("open unexpectedly succeeded")
	}
	latest, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("latest session: ok=%v err=%v", ok, err)
	}
	if latest.IsActive || latest.Status != kanban.StateError {
		t.Fatalf("failed prompt claim=%+v, want inactive error", latest)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "pane close pane-123") {
		t.Fatalf("failed prompt did not close Herdr pane; log=%s", logBytes)
	}
	if strings.Contains(log, "agent focus") || strings.Contains(log, "pane move pane-123 --new-tab --workspace ws-board --label b1-T-001-herdr-prompt-failure --focus") {
		t.Fatalf("failed prompt focused a container before cleanup; log=%s", logBytes)
	}
}

func TestOpenTicketWithLegacyHerdrKeepsPromptArgument(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Legacy Herdr", "multiline\nbody with 'quotes' and $shell", "codex")
	if err != nil {
		t.Fatal(err)
	}
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board","cwd":"` + view.Board.Workdir + `"}`,
		"agent start":      `{"pane_id":"pane-123","agent":{"name":"agent-789"}}`,
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store}

	if err := manager.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatal(err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "agent start") || !strings.Contains(log, "# T-001: Legacy Herdr") || !strings.Contains(log, "multiline") {
		t.Fatalf("legacy Herdr did not retain the argument-mode prompt; log=%s", log)
	}
	if strings.Contains(log, "pane send-text") {
		t.Fatalf("legacy Herdr must not receive a duplicate pane prompt; log=%s", log)
	}
}

func TestOpenTicketWithHerdrPasteModeUsesHerdrInput(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Herdr Paste", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	// Paste is a test-only configured command, not a persistable compiled
	// harness contract. Override the projected value only for manager coverage.
	ticket.Harness = "paste"
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board","cwd":"` + view.Board.Workdir + `"}`,
		"agent start":      `{"pane_id":"pane-123","agent":{"name":"agent-789"}}`,
		"agent read":       "PROMPT_READY\nSESSION_REF=ref-123",
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	cfg.Harnesses["paste"] = harness.Config{Start: []string{"fake-agent"}, PromptMode: harness.PromptModePaste, PromptReady: "PROMPT_READY", SessionRef: "SESSION_REF="}
	manager := &Manager{Config: cfg, Store: store}

	if err := manager.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if !got.SessionRef.Valid || got.SessionRef.String != "ref-123" {
		t.Fatalf("session ref not captured from Herdr output: %+v", got.SessionRef)
	}
	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if !strings.Contains(log, "pane send-text pane-123 # T-001: Herdr Paste") || !strings.Contains(log, "pane send-keys pane-123 enter") {
		t.Fatalf("fake herdr did not receive prompt via pane input; log=%s", log)
	}
}

func TestHerdrResumeFailureReturnsRepairErrorAndDeactivatesClaim(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Bad Herdr ref", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "old", TmuxWindowName: TicketWindowName(ticket), Status: kanban.StateError})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSessionClosed(ctx, oldID, kanban.StateError, "test", "missing"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSessionRef(ctx, oldID, "invalid-ref"); err != nil {
		t.Fatal(err)
	}
	bin, _ := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board"}`,
		"agent start":      `{"pane_id":"pane-123","agent":{"name":"agent-789"}}`,
		"agent read":       "__ERROR__",
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store, ResumeCheckAfter: time.Millisecond}

	refreshed, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.OpenTicket(ctx, refreshed, false)
	var resumeErr ResumeFailedError
	if !errors.As(err, &resumeErr) {
		t.Fatalf("open error = %T %v, want ResumeFailedError", err, err)
	}
	latest, ok, lookupErr := store.LatestSession(ctx, ticket.ID)
	if lookupErr != nil || !ok {
		t.Fatalf("latest session: ok=%v err=%v", ok, lookupErr)
	}
	if latest.IsActive || latest.Status != kanban.StateError {
		t.Fatalf("failed resume claim = %+v, want inactive error", latest)
	}
}

func TestRefreshRuntimeDoesNotObserveHerdrLaunchClaimBeforeContainerIsAttached(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Herdr starting", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	claimID, err := store.ClaimSession(ctx, ticket.ID, storage.Session{
		Harness:          "pi",
		Multiplexer:      "herdr",
		MuxNamespace:     sql.NullString{String: "ws-board", Valid: true},
		MuxContainerName: sql.NullString{String: "b1-T-001-herdr-starting", Valid: true},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := writeFakeHerdr(t, map[string]string{})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store}

	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	ses, ok, err := store.SessionByID(ctx, claimID)
	if err != nil || !ok {
		t.Fatalf("claim lookup = %+v, %v, %v", ses, ok, err)
	}
	if ses.Status != kanban.StateStarting || !ses.IsActive {
		t.Fatalf("watcher changed in-flight claim to status=%q active=%v", ses.Status, ses.IsActive)
	}
}

func TestRefreshRuntimeMarksMissingHerdrContainerExitedWhenResumable(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Missing Herdr", "", "pi")
	_, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:           "pi",
		HarnessSessionRef: sql.NullString{String: "resume-ref", Valid: true},
		Multiplexer:       "herdr",
		MuxNamespace:      sql.NullString{String: "old-workspace", Valid: true},
		MuxContainerID:    sql.NullString{String: "old-agent", Valid: true},
		MuxContainerName:  sql.NullString{String: "old-agent", Valid: true},
		MuxMetadata:       sql.NullString{String: `{"pane_id":"old-pane"}`, Valid: true},
		Status:            kanban.StateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := writeFakeHerdr(t, map[string]string{
		"agent get":  `{"state":"unknown"}`,
		"agent read": "__NOT_FOUND__",
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store}

	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != kanban.StateExited || got.SessionActive {
		t.Fatalf("missing Herdr session = %+v, want inactive exited", got)
	}
}

func TestRefreshRuntimePrefersHerdrNativeState(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Herdr Refresh", "", "codex")
	_, _ = store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:          "codex",
		TmuxSessionName:  "",
		TmuxWindowName:   "",
		Multiplexer:      "herdr",
		MuxNamespace:     sql.NullString{String: "ws-board", Valid: true},
		MuxContainerID:   sql.NullString{String: "agent-789", Valid: true},
		MuxContainerName: sql.NullString{String: "b1-T-001-herdr-refresh", Valid: true},
		MuxMetadata:      sql.NullString{String: `{"pane_id":"pane-123"}`, Valid: true},
		Status:           kanban.StateRunning,
	})
	bin, _ := writeFakeHerdr(t, map[string]string{
		"agent get": `{"state":"blocked","message":"permission required"}`,
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store}

	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if got.Runtime != kanban.StateNeedsPermission || got.LastDetectionSource.String != "native" {
		t.Fatalf("runtime = %s source=%s reason=%s", got.Runtime, got.LastDetectionSource.String, got.LastAttentionReason.String)
	}
}

func writeFakeHerdr(t *testing.T, responses map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	logPath := filepath.Join(dir, "herdr.log")
	script := `#!/bin/sh
log="` + logPath + `"
echo "$*" >> "$log"
case "$1 $2" in
  "workspace list") echo '` + responses["workspace list"] + `' ;;
  "workspace create") echo '` + responses["workspace create"] + `' ;;
  "agent start") if [ "$3" = "--help" ]; then echo '` + responses["agent start help"] + `'; else echo '` + responses["agent start"] + `'; fi ;;
  "agent focus") echo '` + responses["agent focus"] + `' ;;
  "pane list") echo '` + responses["pane list"] + `' ;;
  "tab create") echo '` + responses["tab create"] + `' ;;
  "pane move") echo '` + responses["pane move"] + `' ;;
  "pane close") echo '` + responses["pane close"] + `' ;;
  "agent get") echo '` + responses["agent get"] + `' ;;
  "agent read") if [ '` + responses["agent read"] + `' = '__ERROR__' ]; then exit 1; elif [ '` + responses["agent read"] + `' = '__NOT_FOUND__' ]; then echo '{"error":{"code":"agent_not_found"}}'; exit 1; else echo '` + responses["agent read"] + `'; fi ;;
  "pane read") if [ '` + responses["agent read"] + `' = '__ERROR__' ]; then exit 1; elif [ '` + responses["agent read"] + `' = '__NOT_FOUND__' ]; then echo '{"code":"pane_not_found"}'; exit 1; else echo '` + responses["agent read"] + `'; fi ;;
  "agent prompt") if [ '` + responses["agent prompt"] + `' = '__ERROR__' ]; then echo 'prompt failed' >&2; exit 1; else echo '{"ok":true}'; fi ;;
  "pane send-text") if [ '` + responses["pane send-text"] + `' = '__ERROR__' ]; then echo 'send failed' >&2; exit 1; else echo '{"ok":true}'; fi ;;
  "pane send-keys") if [ '` + responses["pane send-keys"] + `' = '__ERROR__' ]; then echo 'keys failed' >&2; exit 1; else echo '{"ok":true}'; fi ;;
  *) echo '{}' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, logPath
}
