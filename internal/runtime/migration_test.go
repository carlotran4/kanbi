package runtime

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
)

func testHerdrManager(t *testing.T) (*Manager, string) {
	t.Helper()
	store, _ := newRuntimeTestStore(t)
	bin, log := writeFakeHerdr(t, map[string]string{
		"workspace list": `[]`, "workspace create": `{"id":"workspace"}`,
		"agent start help": `--kind --pane`,
		"tab create":       `{"result":{"root_pane":{"pane_id":"pane"},"tab":{"tab_id":"tab"}}}`,
		"agent start":      `{"result":{"agent":{"name":"agent"}}}`,
		"agent get":        `{"state":"running"}`,
	})
	cfg := config.Defaults(config.Paths{StateDir: t.TempDir()})
	cfg.Multiplexer.Herdr.Binary = bin
	cfg.Harnesses["pi"] = config.Harness{Start: []string{"/custom/pi"}, Resume: []string{"/custom/pi", "--session", "{session_ref}"}, PromptMode: "arg"}
	return &Manager{Config: cfg, Store: store, ResumeCheckAfter: time.Millisecond}, log
}

func TestLegacyActiveAttemptRequiresRepairAndRemainsHistory(t *testing.T) {
	m, log := testHerdrManager(t)
	ctx := t.Context()
	view := defaultBoardView(t, ctx, m.Store)
	ticket := createTicket(t, ctx, m.Store, view.Columns[0].ID, "Legacy", "", "pi")
	id, err := m.Store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "old", TmuxWindowName: "legacy", TmuxWindowID: sql.NullString{String: "@7", Valid: true}, Status: kanban.StateRunning})
	if err != nil {
		t.Fatal(err)
	}
	ticket, _ = m.Store.TicketByID(ctx, ticket.ID)
	var repair RepairNeededError
	if err := m.OpenTicket(ctx, ticket, false); !errors.As(err, &repair) {
		t.Fatalf("expected explicit repair: %v", err)
	}
	if err := m.CloseSession(ctx, ticket); err == nil {
		t.Fatal("legacy container must not be closed through Herdr")
	}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	ses, _, _ := m.Store.ActiveSession(ctx, ticket.ID)
	if ses.ID != id || ses.Multiplexer != "tmux" || ses.TmuxWindowID.String != "@7" {
		t.Fatalf("history rewritten: %+v", ses)
	}
	if data, _ := os.ReadFile(log); len(data) > 0 {
		t.Fatalf("legacy runtime caused Herdr commands: %s", data)
	}
}

func TestLegacyInactiveAttemptResumesIntoNewHerdrRow(t *testing.T) {
	m, log := testHerdrManager(t)
	ctx := t.Context()
	view := defaultBoardView(t, ctx, m.Store)
	ticket := createTicket(t, ctx, m.Store, view.Columns[0].ID, "Resume legacy", "", "pi")
	id, err := m.Store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "old", TmuxWindowName: "legacy", HarnessSessionRef: sql.NullString{String: "verified-ref", Valid: true}, Status: kanban.StateRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Store.MarkSessionClosed(ctx, id, kanban.StateClosed, "system", "test"); err != nil {
		t.Fatal(err)
	}
	ticket, _ = m.Store.TicketByID(ctx, ticket.ID)
	if err := m.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	old, ok, err := m.Store.SessionByID(ctx, id)
	if err != nil || !ok || old.IsActive || old.Multiplexer != "tmux" || old.TmuxWindowName != "legacy" {
		t.Fatalf("old history=%+v ok=%v err=%v", old, ok, err)
	}
	latest, _, _ := m.Store.LatestSession(ctx, ticket.ID)
	if latest.ID == id || latest.Multiplexer != "herdr" || latest.HarnessSessionRef.String != "verified-ref" || latest.TmuxWindowName != "" {
		t.Fatalf("new attempt=%+v", latest)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "--session") || !strings.Contains(string(data), "verified-ref") {
		t.Fatalf("resume command missing: %s", data)
	}
}

func TestFreshHerdrAttemptPreservesPreviousAndFocusDoesNotDuplicate(t *testing.T) {
	m, _ := testHerdrManager(t)
	ctx := t.Context()
	view := defaultBoardView(t, ctx, m.Store)
	ticket := createTicket(t, ctx, m.Store, view.Columns[0].ID, "Fresh", "", "pi")
	if err := m.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = m.Store.TicketByID(ctx, ticket.ID)
	oldID := ticket.SessionID.Int64
	if err := m.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	if err := m.StartFreshTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	old, ok, err := m.Store.SessionByID(ctx, oldID)
	if err != nil || !ok || old.IsActive {
		t.Fatalf("old=%+v ok=%v err=%v", old, ok, err)
	}
	latest, _, _ := m.Store.LatestSession(ctx, ticket.ID)
	if latest.ID != oldID+1 || !latest.IsActive || latest.Multiplexer != "herdr" {
		t.Fatalf("latest=%+v", latest)
	}
}

func TestLaunchCannotReplaceAnotherDurableClaim(t *testing.T) {
	m, log := testHerdrManager(t)
	ctx := t.Context()
	view := defaultBoardView(t, ctx, m.Store)
	ticket := createTicket(t, ctx, m.Store, view.Columns[0].ID, "Claim", "", "pi")
	id, err := m.Store.ClaimSession(ctx, ticket.ID, storage.Session{Harness: "pi", Multiplexer: "herdr", Status: kanban.StateStarting}, false)
	if err != nil {
		t.Fatal(err)
	}
	// A caller holding an old ticket snapshot must lose the atomic claim race.
	if err := m.OpenTicket(ctx, ticket, false); err == nil {
		t.Fatal("duplicate launch accepted")
	}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	ses, _, _ := m.Store.ActiveSession(ctx, ticket.ID)
	if ses.ID != id || ses.Status != kanban.StateStarting {
		t.Fatalf("claim disturbed: %+v", ses)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "tab create") || strings.Contains(string(data), "pane run") {
		t.Fatalf("losing claim launched process: %s", data)
	}
}

func TestHerdrCloseAndRenameUseStoredPaneAndTab(t *testing.T) {
	m, log := testHerdrManager(t)
	ctx := t.Context()
	view := defaultBoardView(t, ctx, m.Store)
	ticket := createTicket(t, ctx, m.Store, view.Columns[0].ID, "Original", "", "pi")
	if err := m.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	ticket, _ = m.Store.TicketByID(ctx, ticket.ID)
	if err := m.RenameTicketWindow(ctx, ticket, "Updated"); err != nil {
		t.Fatal(err)
	}
	ses, _, _ := m.Store.ActiveSession(ctx, ticket.ID)
	if !strings.Contains(ses.MuxContainerName.String, "updated") || ses.TmuxWindowName != "" {
		t.Fatalf("renamed session=%+v", ses)
	}
	if err := m.CloseSession(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	ses, _, _ = m.Store.LatestSession(ctx, ticket.ID)
	if ses.IsActive || ses.Status != kanban.StateClosed {
		t.Fatalf("close=%+v", ses)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "tab rename tab") || !strings.Contains(string(data), "pane close pane") {
		t.Fatalf("stored targets not used: %s", data)
	}
}
