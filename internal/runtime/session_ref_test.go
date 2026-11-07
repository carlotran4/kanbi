package runtime

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
)

const claudeNativeRef = `{"result":{"agent":{"agent_status":"working","agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"native-claude-ref"}}}}`

func TestClaudeNativeRefCapturedWithoutPromptAndResumedAfterClose(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // No Claude history exists to scan.
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Native Claude", "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	bin, logPath := writeFakeHerdr(t, map[string]string{"agent get": claudeNativeRef})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := NewManager(cfg, store)
	manager.ResumeCheckAfter = time.Millisecond
	defer manager.Close()
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	first, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || first.HarnessSessionRef.String != "native-claude-ref" {
		t.Fatalf("native capture: %+v %v", first, err)
	}
	if err := manager.CloseSession(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	ticket, err = store.TicketByID(ctx, ticket.ID)
	if err != nil || ticket.SessionActive || ticket.SessionRef.String != "native-claude-ref" {
		t.Fatalf("closed: %+v %v", ticket, err)
	}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	latest, _, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || latest.ID == first.ID || latest.HarnessSessionRef != first.HarnessSessionRef {
		t.Fatalf("resume: %+v %v", latest, err)
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), "--resume native-claude-ref") {
		t.Fatalf("native resume missing: %s", log)
	}
}

func TestClaudeLateNativeRefCapturedByRefreshRecoveryAndClose(t *testing.T) {
	for _, operation := range []string{"refresh", "recovery", "close"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			store, ctx := newRuntimeTestStore(t)
			view := defaultBoardView(t, ctx, store)
			ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Edited ticket", "changed since launch", "claude")
			if err != nil {
				t.Fatal(err)
			}
			id, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
				Harness: "claude", Multiplexer: "herdr", Status: kanban.StateRunning,
				MuxContainerID: sqlString("agent-789"), MuxMetadata: sqlString(`{"pane_id":"pane-123"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			bin, logPath := writeFakeHerdr(t, map[string]string{"agent get": claudeNativeRef})
			cfg := config.Defaults(config.Paths{})
			cfg.Multiplexer.Herdr.Binary = bin
			manager := NewManager(cfg, store)
			defer manager.Close()
			ticket, _ = store.TicketByID(ctx, ticket.ID)
			switch operation {
			case "refresh":
				err = manager.RefreshRuntime(ctx)
			case "recovery":
				_, err = manager.recoverMissingSessionRef(ctx, ticket)
			case "close":
				err = manager.CloseSession(ctx, ticket)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := store.LatestSession(ctx, ticket.ID)
			if err != nil || !ok || got.ID != id || got.HarnessSessionRef.String != "native-claude-ref" {
				t.Fatalf("capture: %+v %v", got, err)
			}
			if operation != "close" {
				log, _ := os.ReadFile(logPath)
				if !got.IsActive || strings.Contains(string(log), "start") || strings.Contains(string(log), "close") {
					t.Fatalf("observation replaced agent: %+v %s", got, log)
				}
			}
		})
	}
}

func TestNativeSessionRefRejectsUnrelatedReports(t *testing.T) {
	for _, bad := range []multiplexer.AgentSession{
		{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: "other"},
		{Source: "custom:reporter", Agent: "claude", Kind: "id", Value: "unverified"},
		{Source: "herdr:claude", Agent: "claude", Kind: "path", Value: "/not/a/claude/id"},
		{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "bad\nref"},
	} {
		if got := nativeSessionRef(multiplexer.Detection{Session: &bad}, "claude"); got != "" {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestNativeSessionRefDoesNotOverwriteManualRepairOrNewAttempt(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Repair", "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "claude", Status: kanban.StateRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSessionRef(ctx, first, "manual-ref"); err != nil {
		t.Fatal(err)
	}
	second, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "claude", Status: kanban.StateRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CaptureSessionRef(ctx, first, "late-native-ref"); err != nil {
		t.Fatal(err)
	}
	old, _, _ := store.SessionByID(ctx, first)
	latest, _, _ := store.SessionByID(ctx, second)
	if old.HarnessSessionRef.String != "manual-ref" || latest.HarnessSessionRef.Valid {
		t.Fatalf("capture overwrote repair or new attempt: %+v %+v", old, latest)
	}
}

func TestClaudeNativeRefSurvivesExternalPaneExit(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "External exit", "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "claude", Multiplexer: "herdr", Status: kanban.StateRunning, MuxContainerID: sqlString("agent-789"), MuxMetadata: sqlString(`{"pane_id":"pane-123"}`)})
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := writeFakeHerdr(t, map[string]string{"agent get": claudeNativeRef})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := NewManager(cfg, store)
	defer manager.Close()
	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	bin, logPath := writeFakeHerdr(t, map[string]string{"agent get": `{"state":"unknown"}`, "agent read": "__NOT_FOUND__"})
	manager.Config.Multiplexer.Herdr.Binary = bin
	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || got.ID != id || got.IsActive || got.Status != kanban.StateExited || got.HarnessSessionRef.String != "native-claude-ref" {
		t.Fatalf("external exit lost resume ref/history: %+v %v", got, err)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "start") || strings.Contains(string(log), "close") {
		t.Fatalf("observation controlled agent: %s", log)
	}
}
