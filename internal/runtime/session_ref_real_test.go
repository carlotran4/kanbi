package runtime

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
)

// No authenticated harness is launched. Exercise an official session report and
// the actual installed Herdr get/read APIs on an owned disposable server.
func TestRealHerdrNativeSessionRef(t *testing.T) {
	if os.Getenv("KANBI_REAL_HERDR_REF_TESTS") != "1" {
		t.Skip("opt in with KANBI_REAL_HERDR_REF_TESTS=1")
	}
	dir := filepath.Join(t.TempDir(), "runtime")
	fixture := filepath.Join("..", "..", "scripts", "herdr-fixture.py")
	if out, err := exec.Command("python3", fixture, "start", dir, "160", "45").CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("python3", fixture, "stop", dir).CombinedOutput(); err != nil {
			t.Errorf("cleanup: %v %s", err, out)
		}
	})
	t.Setenv("HERDR_CONFIG_PATH", filepath.Join(dir, "config.toml"))
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(dir, "herdr.sock"))
	run := func(args ...string) []byte {
		t.Helper()
		out, err := exec.Command("herdr", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("herdr %v: %v %s", args, err, out)
		}
		return out
	}
	var panes struct {
		Result struct {
			Panes []struct {
				ID string `json:"pane_id"`
			} `json:"panes"`
		} `json:"result"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := json.Unmarshal(run("pane", "list"), &panes); err != nil {
			t.Fatal(err)
		}
		if len(panes.Result.Panes) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("initial pane did not become ready: %+v", panes)
		}
		time.Sleep(50 * time.Millisecond)
	}
	pane := panes.Result.Panes[0].ID
	const ref = "11111111-2222-4333-8444-555555555555"
	run("pane", "report-agent-session", pane, "--source", "herdr:claude", "--agent", "claude", "--agent-session-id", ref)
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Native report", "no matching prompt/history", "claude")
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]string{"pane_id": pane})
	id, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "claude", Multiplexer: "herdr", Status: kanban.StateRunning, MuxContainerID: sqlString(pane), MuxMetadata: sqlString(string(metadata))})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(config.Defaults(config.Paths{}), store)
	defer manager.Close()
	if err := manager.RefreshRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || got.ID != id || !got.IsActive || got.HarnessSessionRef.String != ref {
		t.Fatalf("native capture: %+v %v", got, err)
	}
	t.Log("real Herdr native pane ref persisted on the original active session without history scanning")
}
