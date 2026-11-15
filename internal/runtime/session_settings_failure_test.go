package runtime

import (
	"os"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestStartFreshCloseFailureDoesNotLaunchOrReplaceAttempt(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Settings", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", Multiplexer: "herdr", Status: "running", HarnessSessionRef: sqlString("old-ref"), MuxContainerID: sqlString("agent-789"), MuxMetadata: sqlString(`{"pane_id":"pane-123"}`)})
	if err != nil {
		t.Fatal(err)
	}
	bin, logPath := writeFakeHerdr(t, map[string]string{})
	script, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), `"pane close") echo '' ;;`, `"pane close") echo close-failed >&2; exit 1 ;;`, 1))
	if err := os.WriteFile(bin, script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := NewManager(cfg, store)
	defer manager.Close()
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	if err := manager.StartFreshTicket(ctx, ticket, true); err == nil {
		t.Fatal("expected close failure")
	}
	latest, _, _ := store.LatestSession(ctx, ticket.ID)
	log, _ := os.ReadFile(logPath)
	if latest.ID != id || latest.HarnessSessionRef.String != "old-ref" || strings.Contains(string(log), "agent start") {
		t.Fatalf("close failure launched/replaced: %+v %s", latest, log)
	}
}
