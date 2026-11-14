package runtime

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestHarnessPreferenceDoesNotChangeExistingConversation(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(map[bool]string{true: "active", false: "resumable"}[active], func(t *testing.T) {
			store, ctx := newRuntimeTestStore(t)
			view := defaultBoardView(t, ctx, store)
			ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Settings", "", "codex")
			if err != nil {
				t.Fatal(err)
			}
			oldID, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "claude", Multiplexer: "herdr", Status: "running", HarnessSessionRef: sqlString("old-ref"), MuxContainerID: sqlString("agent-789"), MuxMetadata: sqlString(`{"pane_id":"pane-123"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if !active {
				if err := store.MarkSessionClosed(ctx, oldID, "closed", "manual", "test"); err != nil {
					t.Fatal(err)
				}
			}
			bin, logPath := writeFakeHerdr(t, map[string]string{})
			cfg := config.Defaults(config.Paths{})
			cfg.Multiplexer.Herdr.Binary = bin
			manager := NewManager(cfg, store)
			manager.ResumeCheckAfter = time.Millisecond
			defer manager.Close()
			ticket, _ = store.TicketByID(ctx, ticket.ID)
			if err := manager.OpenTicket(ctx, ticket, false); err != nil {
				t.Fatal(err)
			}
			log, _ := os.ReadFile(logPath)
			if active {
				latest, _, _ := store.LatestSession(ctx, ticket.ID)
				if latest.ID != oldID || strings.Contains(string(log), "agent start") {
					t.Fatalf("focus replaced attempt: %s", log)
				}
			} else if !strings.Contains(string(log), "--kind claude") || !strings.Contains(string(log), "--resume old-ref") {
				t.Fatalf("wrong resume harness: %s", log)
			}
		})
	}
}

func TestNativeRefRecoveryRetainsConversationHarness(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Settings", "", "codex")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "claude", Multiplexer: "herdr", Status: "running", MuxContainerID: sqlString("agent-789"), MuxMetadata: sqlString(`{"pane_id":"pane-123"}`)})
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := writeFakeHerdr(t, map[string]string{"agent get": claudeNativeRef})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := NewManager(cfg, store)
	defer manager.Close()
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	ticket.Harness = "claude" // lifecycle selects the session's harness before recovery.
	got, err := manager.recoverMissingSessionRef(ctx, ticket)
	if err != nil || got.Harness != "claude" || got.SessionRef.String != "native-claude-ref" {
		t.Fatalf("recovery lost conversation identity: %+v %v", got, err)
	}
}

func TestStartFreshClosesActiveAndUsesNewHarnessWithoutOldRef(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Settings", "", "codex")
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "claude", Multiplexer: "herdr", Status: "running", HarnessSessionRef: sqlString("old-ref"), MuxContainerID: sqlString("agent-789"), MuxMetadata: sqlString(`{"pane_id":"pane-123"}`)})
	if err != nil {
		t.Fatal(err)
	}
	bin, logPath := writeFakeHerdr(t, map[string]string{})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := NewManager(cfg, store)
	defer manager.Close()
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if err := manager.StartFreshTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	old, _, _ := store.SessionByID(ctx, oldID)
	latest, _, _ := store.LatestSession(ctx, ticket.ID)
	if old.IsActive || old.HarnessSessionRef.String != "old-ref" || latest.ID == oldID || !latest.IsActive || latest.Harness != "codex" || latest.HarnessSessionRef.String == "old-ref" {
		t.Fatalf("history/fresh: old=%+v latest=%+v", old, latest)
	}
	log, _ := os.ReadFile(logPath)
	closeAt, startAt := strings.Index(string(log), "pane close"), strings.Index(string(log), "agent start ")
	if closeAt < 0 || startAt < closeAt || strings.Contains(string(log), "resume old-ref") {
		t.Fatalf("unsafe fresh: %s", log)
	}
}
