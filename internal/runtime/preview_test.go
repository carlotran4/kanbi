package runtime

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestPreviewReadsExistingAttemptWithoutMutation(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Preview", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", Multiplexer: "herdr", MuxContainerID: sql.NullString{String: "agent-1", Valid: true}, MuxMetadata: sql.NullString{String: `{"pane_id":"pane-1"}`, Valid: true}, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByID(ctx, ticket.ID)
	bin, logPath := writeFakeHerdr(t, map[string]string{"agent get": `{"state":"working"}`, "agent read": "screen"})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	m := &Manager{Config: cfg, Store: store}
	before, _, _ := store.SessionByID(ctx, id)
	result, err := m.PreviewTicket(ctx, ticket)
	if err != nil || result.Text != "screen\n" || result.CapturedAt.IsZero() || !strings.Contains(result.Source, "visible screen") {
		t.Fatalf("%+v %v", result, err)
	}
	after, _, _ := store.SessionByID(ctx, id)
	if before != after {
		t.Fatal("preview mutated session")
	}
	log, _ := os.ReadFile(logPath)
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		if !strings.HasPrefix(line, "agent get ") && !strings.HasPrefix(line, "pane read ") {
			t.Fatalf("unexpected operation %q", line)
		}
	}
	if !strings.Contains(string(log), "pane read pane-1 --source visible --format ansi --lines 160") {
		t.Fatal(string(log))
	}
	// Only screen reads fail; the independent transcript path remains available.
	script, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "case \"$1 $2\" in", "if [ \"$1 $2\" = \"pane read\" ] && [ \"$5\" = visible ]; then exit 1; fi\ncase \"$1 $2\" in", 1))
	if err := os.WriteFile(bin, script, 0755); err != nil {
		t.Fatal(err)
	}
	fallback, err := m.PreviewTicket(ctx, ticket)
	if err != nil || fallback.Source != "transcript fallback" || fallback.Warning == "" {
		t.Fatalf("fallback=%+v %v", fallback, err)
	}
	if err := store.MarkSessionClosed(ctx, id, "closed", "manual", "test close"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PreviewTicket(ctx, ticket); err == nil {
		t.Fatal("read inactive attempt")
	}
}
