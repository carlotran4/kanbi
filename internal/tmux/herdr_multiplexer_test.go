package tmux

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kanbi/internal/config"
	"kanbi/internal/harness"
	"kanbi/internal/kanban"
	"kanbi/internal/storage"
)

func TestOpenTicketWithHerdrDefaultStoresContainerMetadata(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Herdr Launch", "body", "codex")
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list":   `[]`,
		"workspace create": `{"id":"ws-board","cwd":"` + view.Board.Workdir + `"}`,
		"agent start":      `{"pane_id":"pane-123","tab_id":"tab-456","agent":{"name":"agent-789"}}`,
		"agent focus":      `{"ok":true}`,
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	cfg.Multiplexer.Herdr.Binary = bin
	cfg.Multiplexer.Herdr.FocusOnOpen = true
	manager := &Manager{Config: cfg, Store: store, Runner: &failIfTmuxRunner{t: t}}

	if err := manager.OpenTicket(ctx, ticket, true); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if !got.Multiplexer.Valid || got.Multiplexer.String != "herdr" || got.MuxNamespace.String != "ws-board" || got.MuxContainerID.String != "agent-789" {
		t.Fatalf("ticket mux metadata not stored: %+v", got)
	}
	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if !strings.Contains(log, "agent start") || !strings.Contains(log, "codex --no-alt-screen") {
		t.Fatalf("fake herdr did not receive harness launch command; log=%s", log)
	}
}

func TestOpenTicketWithHerdrPasteModeUsesHerdrInput(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
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
	manager := &Manager{Config: cfg, Store: store, Runner: &failIfTmuxRunner{t: t}}

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

func TestHerdrDefaultKeepsValidActiveTmuxSessionInTmux(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Keep tmux", "", "pi")
	windowName := TicketWindowName(ticket)
	firstID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "pi",
		TmuxSessionName: "old-live-tmux",
		TmuxWindowID:    sql.NullString{String: "@7", Valid: true},
		TmuxWindowName:  windowName,
		Multiplexer:     "tmux",
		Status:          kanban.StateRunning,
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Default = "herdr"
	runner := &fakeRunner{windows: map[string]string{"@7": windowName}}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}

	refreshed, _ := store.TicketByID(ctx, ticket.ID)
	if err := manager.OpenTicket(ctx, refreshed, false); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("latest session: ok=%v err=%v", ok, err)
	}
	if latest.ID != firstID || latest.Multiplexer != "tmux" {
		t.Fatalf("valid active tmux session should be kept, got latest=%+v firstID=%d", latest, firstID)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "new-window" {
			t.Fatalf("valid active tmux session under Herdr default must not launch replacement: %+v", c.args)
		}
	}
}

func TestMoveTicketToDefaultMultiplexerClosesTmuxAndResumesHerdr(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Move active tmux", "", "pi")
	windowName := TicketWindowName(ticket)
	oldID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "pi",
		TmuxSessionName: "old-live-tmux",
		TmuxWindowID:    sql.NullString{String: "@7", Valid: true},
		TmuxWindowName:  windowName,
		Multiplexer:     "tmux",
		Status:          kanban.StateRunning,
	})
	if err := store.UpdateSessionRef(ctx, oldID, "move-ref-123"); err != nil {
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
	cfg.GracefulExitTimeout = time.Nanosecond
	runner := &fakeRunner{windows: map[string]string{"@7": windowName}}
	manager := &Manager{Config: cfg, Store: store, Runner: runner}

	refreshed, _ := store.TicketByID(ctx, ticket.ID)
	if err := manager.MoveTicketToDefaultMultiplexer(ctx, refreshed); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("latest session: ok=%v err=%v", ok, err)
	}
	if latest.ID == oldID || latest.Multiplexer != "herdr" || latest.HarnessSessionRef.String != "move-ref-123" {
		t.Fatalf("move should create Herdr resume session, got latest=%+v oldID=%d", latest, oldID)
	}
	var sentExit, killed bool
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "send-keys" {
			sentExit = true
		}
		if len(c.args) > 0 && c.args[0] == "kill-window" {
			killed = true
		}
	}
	if !sentExit || !killed {
		t.Fatalf("expected graceful close attempts before Herdr resume, calls=%+v", runner.calls)
	}
	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if !strings.Contains(log, "agent start") || !strings.Contains(log, "pi --session move-ref-123") {
		t.Fatalf("fake Herdr did not launch resume command; log=%s", log)
	}
}

func TestHerdrDefaultResumesStaleTmuxSessionWithRefIntoHerdr(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Resume stale tmux", "", "pi")
	oldID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "pi",
		TmuxSessionName: "old-missing-tmux",
		TmuxWindowID:    sql.NullString{String: "@7", Valid: true},
		TmuxWindowName:  TicketWindowName(ticket),
		Multiplexer:     "tmux",
		Status:          kanban.StateRunning,
	})
	if err := store.UpdateSessionRef(ctx, oldID, "resume-ref-123"); err != nil {
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
	manager := &Manager{Config: cfg, Store: store, Runner: &missingRuntimeSessionRunner{missingSession: "old-missing-tmux"}}

	refreshed, _ := store.TicketByID(ctx, ticket.ID)
	if err := manager.OpenTicket(ctx, refreshed, false); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("latest session: ok=%v err=%v", ok, err)
	}
	if latest.ID == oldID || latest.Multiplexer != "herdr" || latest.HarnessSessionRef.String != "resume-ref-123" {
		t.Fatalf("stale tmux session with ref should resume into Herdr, got latest=%+v oldID=%d", latest, oldID)
	}
	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if !strings.Contains(log, "agent start") || !strings.Contains(log, "pi --session resume-ref-123") {
		t.Fatalf("fake Herdr did not launch resume command; log=%s", log)
	}
}

func TestRefreshRuntimePrefersHerdrNativeState(t *testing.T) {
	store, ctx := newTmuxTestStore(t)
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
	manager := &Manager{Config: cfg, Store: store, Runner: &failIfTmuxRunner{t: t}}

	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.TicketByID(ctx, ticket.ID)
	if got.Runtime != kanban.StateNeedsPermission || got.LastDetectionSource.String != "native" {
		t.Fatalf("runtime = %s source=%s reason=%s", got.Runtime, got.LastDetectionSource.String, got.LastAttentionReason.String)
	}
}

type failIfTmuxRunner struct{ t *testing.T }

func (r *failIfTmuxRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	r.t.Fatalf("unexpected tmux command %s %v", name, args)
	return "", nil
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
  "agent start") echo '` + responses["agent start"] + `' ;;
  "agent focus") echo '` + responses["agent focus"] + `' ;;
  "agent get") echo '` + responses["agent get"] + `' ;;
  "agent read") echo '` + responses["agent read"] + `' ;;
  "pane read") echo '` + responses["agent read"] + `' ;;
  "pane send-text") echo '{"ok":true}' ;;
  "pane send-keys") echo '{"ok":true}' ;;
  *) echo '{}' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, logPath
}
