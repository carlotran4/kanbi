package runtime

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestCloseLegacyIntegrationUsesRecordedContainerAndPreservesRun(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	bin, log := writeFakeHerdr(t, map[string]string{"agent get": `{"state":"blocked"}`})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	m := NewManager(cfg, store)
	defer m.Close()
	run := storage.IntegrationRun{PublicID: "old", State: "ready", Multiplexer: sql.NullString{String: "herdr", Valid: true}, MuxContainerID: sql.NullString{String: "old-agent", Valid: true}, MuxMetadata: sql.NullString{String: `{"pane_id":"old-pane"}`, Valid: true}}
	if err := m.CloseLegacyIntegration(ctx, run); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "agent get old-agent") || !strings.Contains(string(data), "pane close old-pane") {
		t.Fatalf("commands=%s", data)
	}
	if run.State != "ready" {
		t.Fatal("runtime rewrote history")
	}
}
