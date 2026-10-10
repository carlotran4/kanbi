package runtime

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
	workspacepkg "github.com/carlotran4/kanbi/internal/workspace"
)

func TestOpenIntegratedWorkspaceRehydratesBeforeResumingStoredRef(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "develop")
	runGit("config", "user.email", "kanbi-test@example.invalid")
	runGit("config", "user.name", "Kanbi Test")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "base")
	if err := store.SetBoardWorkdir(ctx, view.Board.ID, repo); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBoardWorktreeMode(ctx, view.Board.ID, storage.WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Rehydrate", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	svc := &workspacepkg.Service{Store: store, StateDir: stateDir}
	w, err := svc.Provision(ctx, workspacepkg.ProvisionOptions{BoardID: view.Board.ID, BoardUUID: view.Board.UUID, BoardCWD: repo, TicketID: ticket.ID, Branch: "feat/rehydrate"})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "kanbi", TmuxWindowName: "rehydrate", WorkspaceID: sql.NullInt64{Int64: w.ID, Valid: true}, LaunchCWD: sqlString(w.LaunchCWD), Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSessionRef(ctx, sessionID, "resume-in-same-path"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSessionClosed(ctx, sessionID, "closed", "tmux", "integrating"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkspaceIntegrated(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	runGit("worktree", "remove", w.WorktreePath)

	ticket, err = store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list": `[]`, "workspace create": `{"id":"workspace"}`,
		"agent start help": `--kind --pane`, "tab create": `{"result":{"root_pane":{"pane_id":"pane"}}}`,
		"agent start": `{"result":{"agent":{"name":"agent"}}}`,
	})
	cfg := config.Defaults(config.Paths{StateDir: stateDir})
	cfg.Multiplexer.Herdr.Binary = bin
	cfg.Harnesses["pi"] = config.Harness{Start: []string{"/custom/pi"}, Resume: []string{"/custom/pi", "--session", "{session_ref}"}}
	manager := &Manager{Config: cfg, Store: store, ResumeCheckAfter: time.Millisecond, WorkspaceService: svc}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.LaunchCWD); err != nil {
		t.Fatalf("workspace not rehydrated: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "resume-in-same-path") || !strings.Contains(string(data), w.LaunchCWD) {
		t.Fatalf("resume ref/path missing: %s", data)
	}
}

func TestRecoverMissingCodexSessionRefDoesNotUseUnmarkedHistory(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Codex no unsafe recovery", "Body", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "codex", Multiplexer: "herdr", TmuxSessionName: "test", TmuxWindowName: "codex", Status: kanban.StateRunning}); err != nil {
		t.Fatal(err)
	}
	ticket, err = store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(map[string]any{
		"session_id": "unmarked-concurrent-session",
		"ts":         float64(time.Now().Unix()),
		"text":       "# T-001: Codex no unsafe recovery\n\nBody",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "history.jsonl"), append(entry, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(config.Defaults(config.Paths{}), store)
	recovered, err := manager.recoverMissingSessionRef(ctx, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.SessionRef.Valid {
		t.Fatalf("unsafe Codex history recovery stored %q", recovered.SessionRef.String)
	}
}

func TestOpenTicketRecoversMissingPiSessionRefFromHistory(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Recover Ref", "Body", "pi")

	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ref := "019e-recovered-ref"
	promptText := "# T-001: Recover Ref\n\nBody"
	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "kanbi",
		TmuxWindowName: "T-001-recover-ref", Status: "running",
	})
	ses, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok || !ses.StartedAt.Valid {
		t.Fatalf("latest session ok=%v err=%v started=%v", ok, err, ses.StartedAt)
	}
	startedAt := ses.StartedAt.Time
	sessionDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	session := `{"type":"session","version":3,"id":` + jsonQuote(ref) + `,"timestamp":` + jsonQuote(startedAt.Format(time.RFC3339Nano)) + `,"cwd":` + jsonQuote(cwd) + `}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"timestamp":` + jsonQuote(startedAt.Add(time.Second).Format(time.RFC3339Nano)) + `,"message":{"role":"user","content":[{"type":"text","text":` + jsonQuote(promptText) + `}],"timestamp":0}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "session.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = store.MarkSessionClosed(ctx, sessionID, "error", "tmux", "tmux window missing")

	ticket, _ = store.TicketByID(ctx, ticket.ID)
	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list": `[]`, "workspace create": `{"id":"workspace"}`,
		"agent start help": `--kind --pane`, "tab create": `{"result":{"root_pane":{"pane_id":"pane"}}}`,
		"agent start": `{"result":{"agent":{"name":"agent"}}}`,
	})
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = bin
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store, ResumeCheckAfter: time.Millisecond}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.TicketByID(ctx, ticket.ID)
	if !updated.SessionRef.Valid || updated.SessionRef.String != ref {
		t.Fatalf("session ref = %#v, want %q", updated.SessionRef, ref)
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), ref) {
		t.Fatalf("resume command missing ref: %s", data)
	}
}

func TestOpenTicketRecoversMissingPiSessionRefFromRefFile(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Recover From File", "Body", "pi")

	stateDir := t.TempDir()
	ref := "019e-file-recovered-ref"

	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "kanbi",
		TmuxWindowName: "T-001-recover-from-file", Status: "running",
	})
	_ = store.MarkSessionClosed(ctx, sessionID, "error", "tmux", "tmux window missing")
	ticket, _ = store.TicketByID(ctx, ticket.ID)

	// Write the stable ref file (as the Pi extension would have, via piSessionRefFilePath).
	refDir := filepath.Join(stateDir, "pi-session-refs")
	_ = os.MkdirAll(refDir, 0o755)
	refPath := filepath.Join(refDir, fmt.Sprintf("ticket-%d.json", ticket.ID))
	_ = os.WriteFile(refPath, []byte(`{"sessionId":"`+ref+`"}`+"\n"), 0o644)

	bin, logPath := writeFakeHerdr(t, map[string]string{
		"workspace list": `[]`, "workspace create": `{"id":"workspace"}`,
		"agent start help": `--kind --pane`, "tab create": `{"result":{"root_pane":{"pane_id":"pane"}}}`,
		"agent start": `{"result":{"agent":{"name":"agent"}}}`,
	})
	cfg := config.Defaults(config.Paths{StateDir: stateDir})
	cfg.Multiplexer.Herdr.Binary = bin
	manager := &Manager{Config: cfg, Store: store, ResumeCheckAfter: time.Millisecond}
	if err := manager.OpenTicket(ctx, ticket, false); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.TicketByID(ctx, ticket.ID)
	if !updated.SessionRef.Valid || updated.SessionRef.String != ref {
		t.Fatalf("session ref = %#v, want %q", updated.SessionRef, ref)
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), ref) {
		t.Fatalf("resume command missing ref: %s", data)
	}
}

func TestOpenTicketRejectsInvalidStoredCopilotSessionRefBeforeResume(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Copilot Resume", "Body", "copilot")

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(home, ".copilot", "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			cwd TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE turns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			turn_index INTEGER NOT NULL,
			user_message TEXT,
			UNIQUE(session_id, turn_index)
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO sessions(id, cwd, created_at) VALUES(?,?,?)`,
		"bad-copilot-ref", cwd, "2030-01-01T00:00:02Z")
	if err != nil {
		t.Fatal(err)
	}

	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness:         "copilot",
		TmuxSessionName: "kanbi",
		TmuxWindowName:  "T-001-copilot-resume",
		Status:          "running",
	})
	_ = store.UpdateSessionRef(ctx, sessionID, "bad-copilot-ref")
	_ = store.MarkSessionClosed(ctx, sessionID, "error", "tmux", "window missing")

	ticket, _ = store.TicketByID(ctx, ticket.ID)
	manager := &Manager{Config: config.Defaults(config.Paths{}), Store: store}
	err = manager.OpenTicket(ctx, ticket, false)
	var repair RepairNeededError
	if !errors.As(err, &repair) {
		t.Fatalf("expected RepairNeededError, got %T: %v", err, err)
	}
	updated, _ := store.TicketByID(ctx, ticket.ID)
	if updated.SessionRef.Valid {
		t.Fatalf("expected invalid copilot ref to be cleared, got %+v", updated.SessionRef)
	}
}
