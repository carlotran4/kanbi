package harness

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestHarnessCommandConstruction(t *testing.T) {
	harnesses := DefaultConfigs()
	harnesses["pi"] = Config{
		Start:  []string{"/bin/pi"},
		Resume: []string{"/bin/pi", "resume", "{session_ref}"},
	}
	start, err := StartCommand(harnesses, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(start, []string{"/bin/pi"}) {
		t.Fatalf("start = %#v", start)
	}
	resume, err := ResumeCommand(harnesses, "pi", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume, []string{"/bin/pi", "resume", "abc123"}) {
		t.Fatalf("resume = %#v", resume)
	}
}

func TestCodexDefaultUsesPromptArgumentMode(t *testing.T) {
	harnesses := DefaultConfigs()
	start, sent, err := StartCommandWithPrompt(harnesses, "codex", "# T-001: Demo\n\nBody", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "--no-alt-screen", "# T-001: Demo\n\nBody"}
	if !sent || !reflect.DeepEqual(start, want) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(harnesses, "codex", "019e-test")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"codex", "resume", "--no-alt-screen", "019e-test"}
	if !reflect.DeepEqual(resume, want) {
		t.Fatalf("resume=%#v", resume)
	}
}

func TestPiDefaultUsesPromptArgumentModeAndSessionResume(t *testing.T) {
	harnesses := DefaultConfigs()
	start, sent, err := StartCommandWithPrompt(harnesses, "pi", "# T-001: Demo\n\nBody", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pi", "# T-001: Demo\n\nBody"}
	if !sent || !reflect.DeepEqual(start, want) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(harnesses, "pi", "019e-test")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"pi", "--session", "019e-test"}
	if !reflect.DeepEqual(resume, want) {
		t.Fatalf("resume=%#v", resume)
	}
}

func TestCopilotDefaultUsesInteractivePromptAndResumeFlag(t *testing.T) {
	harnesses := DefaultConfigs()
	// No-prompt open: should launch plain `copilot` without -i
	noPromptStart, err := StartCommand(harnesses, "copilot")
	if err != nil {
		t.Fatal(err)
	}
	wantNoPrompt := []string{"copilot"}
	if !reflect.DeepEqual(noPromptStart, wantNoPrompt) {
		t.Fatalf("no-prompt start=%#v", noPromptStart)
	}
	// Prompt open: should use -i flag with prompt appended
	start, sent, err := StartCommandWithPrompt(harnesses, "copilot", "# T-001: Demo\n\nBody", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"copilot", "-i", "# T-001: Demo\n\nBody"}
	if !sent || !reflect.DeepEqual(start, want) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(harnesses, "copilot", "abc1234")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"copilot", "--resume=abc1234"}
	if !reflect.DeepEqual(resume, want) {
		t.Fatalf("resume=%#v", resume)
	}
}

func TestFakePasteHarnessContractRemainsConfigurable(t *testing.T) {
	harnesses := DefaultConfigs()
	harnesses["fake"] = Config{
		Start:       []string{"fake-harness"},
		Resume:      []string{"fake-harness", "--session", "{session_ref}"},
		Exit:        []string{"C-c", "exit", "Enter"},
		PromptReady: "PROMPT_READY",
		PromptMode:  PromptModePaste,
		SessionRef:  "SESSION_REF=",
	}

	start, sent, err := StartCommandWithPrompt(harnesses, "fake", "prompt body", true)
	if err != nil {
		t.Fatal(err)
	}
	if sent || !reflect.DeepEqual(start, []string{"fake-harness"}) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(harnesses, "fake", "fake-123")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume, []string{"fake-harness", "--session", "fake-123"}) {
		t.Fatalf("resume=%#v", resume)
	}
	if got := PromptReadyPattern(harnesses, "fake"); got != "PROMPT_READY" {
		t.Fatalf("prompt ready = %q", got)
	}
	if got := SessionRefPattern(harnesses, "fake"); got != "SESSION_REF=" {
		t.Fatalf("session ref marker = %q", got)
	}
}

func TestBuiltinContractsMapToHarnessDocs(t *testing.T) {
	for _, name := range []string{"pi", "codex", "copilot", "claude"} {
		contract, ok := BuiltinContract(name)
		if !ok {
			t.Fatalf("missing built-in contract %q", name)
		}
		if contract.DocsAnchor == "" || !strings.Contains(contract.DocsAnchor, "docs/harness-contracts.md#"+name) {
			t.Fatalf("%s docs anchor = %q", name, contract.DocsAnchor)
		}
		if len(contract.Config.Start) == 0 || len(contract.Config.Resume) == 0 {
			t.Fatalf("%s command config incomplete: %#v", name, contract.Config)
		}
		if contract.CaptureRef == nil {
			t.Fatalf("%s missing capture function", name)
		}
	}
}

func TestDetectStateAllBranches(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		prev    string
		idle    time.Duration
		state   string
		source  string
		changed bool
	}{
		// Pattern: needs_permission takes priority over everything
		{"permission-word", "allow this action? yes/no", "", 0, StateNeedsPermission, "pattern", true},
		{"approve-word", "Please approve the change", "", 0, StateNeedsPermission, "pattern", true},
		{"proceed-word", "Do you want to proceed?", "", 0, StateNeedsPermission, "pattern", true},
		// Pattern: waiting_for_user
		{"waiting-for-user", "waiting for user input", "", 0, StateWaitingForUser, "pattern", true},
		{"prompt-ready", "PROMPT_READY\n", "", 0, StateWaitingForUser, "pattern", true},
		{"shell-prompt", "some output\n> ", "", 0, StateWaitingForUser, "pattern", true},
		// Transcript errors describe the agent's work, not the harness session.
		{"error-colon", "error: something went wrong", "", 0, StateRunning, "pane", true},
		{"panic", "panic: nil pointer", "", 0, StateRunning, "pane", true},
		{"traceback", "Traceback (most recent call last)", "", 0, StateRunning, "pane", true},
		{"historical-failure", "command failed\nfixed it; tests now pass", "", 0, StateRunning, "pane", true},
		// Output changed → running
		{"new-output", "new content here", "old content", 0, StateRunning, "pane", true},
		// Idle timeout with no new output → idle_unknown
		{"idle", "same output", "same output", 5 * time.Minute, StateIdleUnknown, "idle", false},
		// Empty output, no idle → running (default)
		{"empty-no-idle", "", "", 0, StateRunning, "pane", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, source, _, _, changed := DetectState(tc.output, tc.prev, tc.idle)
			if state != tc.state {
				t.Errorf("state = %q, want %q", state, tc.state)
			}
			if source != tc.source {
				t.Errorf("source = %q, want %q", source, tc.source)
			}
			if changed != tc.changed {
				t.Errorf("outputChanged = %v, want %v", changed, tc.changed)
			}
		})
	}
}

func TestDetectStatePermissionPriorityOverWaiting(t *testing.T) {
	// Output contains BOTH a waiting pattern and a permission pattern.
	// Permission must win — it requires user action to unblock the agent.
	output := "waiting for user\nallow this action?"
	state, _, _, _, _ := DetectState(output, "", 0)
	if state != StateNeedsPermission {
		t.Errorf("permission should beat waiting, got %q", state)
	}
}

func TestLastExcerptTruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("a", 2000)
	out := LastExcerpt(long, 1800)
	if len([]rune(out)) != 1800 {
		t.Errorf("excerpt len = %d, want 1800", len([]rune(out)))
	}
	// Excerpt of short string is returned unchanged
	short := "hello world"
	if got := LastExcerpt(short, 1800); got != short {
		t.Errorf("short excerpt = %q, want %q", got, short)
	}
	// Empty string returns empty
	if got := LastExcerpt("", 1800); got != "" {
		t.Errorf("empty excerpt = %q", got)
	}
}

func TestParseSessionRefEdgeCases(t *testing.T) {
	// Marker present but value is empty — should not return a ref
	_, ok := ParseSessionRef("SESSION_REF=\n", "SESSION_REF=")
	if ok {
		t.Error("empty ref value should not be ok")
	}
	// Multiple lines — returns first matching ref
	ref, ok := ParseSessionRef("other\nSESSION_REF=first\nSESSION_REF=second\n", "SESSION_REF=")
	if !ok || ref != "first" {
		t.Errorf("ref = %q ok = %v, want first/true", ref, ok)
	}
	// Ref with trailing whitespace is trimmed
	ref, ok = ParseSessionRef("SESSION_REF=abc123   \n", "SESSION_REF=")
	if !ok || ref != "abc123" {
		t.Errorf("ref = %q ok = %v, want abc123/true", ref, ok)
	}
	// No marker → not ok
	_, ok = ParseSessionRef("no marker here\n", "SESSION_REF=")
	if ok {
		t.Error("absent marker should not be ok")
	}
}

func TestCodexHistoryPicksMostRecentMatchingSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "# T-001: Demo\n\nBody"
	// Three entries: one too old, two matching — should return the latest
	history := `{"session_id":"old","ts":100,"text":"# T-001: Demo\n\nBody"}` + "\n" +
		`{"session_id":"mid","ts":2000,"text":"# T-001: Demo\n\nBody"}` + "\n" +
		`{"session_id":"newest","ts":3000,"text":"# T-001: Demo\n\nBody"}` + "\n" +
		`{"session_id":"wrong","ts":9999,"text":"different prompt"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "history.jsonl"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, ok := CaptureSessionRef("codex", prompt, time.Unix(1999, 0))
	if !ok || ref != "newest" {
		t.Errorf("ref = %q ok = %v, want newest/true", ref, ok)
	}
}

func TestCodexHistoryCaptureKeepsConcurrentIdenticalBasePromptsAttemptBound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	basePrompt := "# T-001: Demo\n\nBody"
	firstPrompt := CodexPromptWithAttemptToken(basePrompt, "attempt-one")
	secondPrompt := CodexPromptWithAttemptToken(basePrompt, "attempt-two")
	if firstPrompt == secondPrompt || !strings.HasPrefix(firstPrompt, basePrompt) || !strings.HasPrefix(secondPrompt, basePrompt) {
		t.Fatalf("attempt prompts must preserve the shared base and differ: %q / %q", firstPrompt, secondPrompt)
	}
	history := `{"session_id":"first-session","ts":2000,"text":` + quote(firstPrompt) + `}` + "\n" +
		`{"session_id":"second-session","ts":2001,"text":` + quote(secondPrompt) + `}` + "\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "history.jsonl"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}

	captures := []struct {
		prompt string
		want   string
	}{{firstPrompt, "first-session"}, {secondPrompt, "second-session"}}
	start := make(chan struct{})
	results := make(chan string, len(captures))
	var wg sync.WaitGroup
	for _, capture := range captures {
		capture := capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ref, ok := CaptureSessionRef("codex", capture.prompt, time.Unix(1999, 0))
			if !ok {
				results <- ""
				return
			}
			results <- ref
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	seen := map[string]bool{}
	for ref := range results {
		seen[ref] = true
	}
	for _, capture := range captures {
		if !seen[capture.want] {
			t.Fatalf("concurrent capture did not retain %q: %#v", capture.want, seen)
		}
	}
}

func TestCodexHistoryReturnsNothingWhenFileAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No ~/.codex/history.jsonl at all
	_, ok := CaptureSessionRef("codex", "any prompt", time.Now())
	if ok {
		t.Error("should return false when history file absent")
	}
}

func TestPiSessionIgnoresWrongCWD(t *testing.T) {
	home := t.TempDir()
	realCWD := t.TempDir()
	wrongCWD := t.TempDir()
	oldCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	_ = os.Chdir(realCWD)
	t.Setenv("HOME", home)

	sessionDir := filepath.Join(home, ".pi", "agent", "sessions", "--tmp--")
	_ = os.MkdirAll(sessionDir, 0o755)
	promptText := "# T-001: Demo\n\nBody"
	// Session with WRONG cwd should be ignored
	wrongSession := `{"type":"session","version":3,"id":"wrong-id","timestamp":"2030-01-01T00:00:00Z","cwd":` + quote(wrongCWD) + `}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2030-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":` + quote(promptText) + `}],"timestamp":1893456001000}}` + "\n"
	_ = os.WriteFile(filepath.Join(sessionDir, "2030-01-01T00-00-00-000Z_wrong-id.jsonl"), []byte(wrongSession), 0o644)

	ref, ok := CaptureSessionRef("pi", promptText, time.Unix(1893455999, 0))
	if ok {
		t.Errorf("should not capture session from wrong cwd, got ref=%q", ref)
	}
}

func TestPiSessionIgnoresTooOldSessions(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	oldCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	_ = os.Chdir(cwd)
	t.Setenv("HOME", home)

	sessionDir := filepath.Join(home, ".pi", "agent", "sessions", "--tmp--")
	_ = os.MkdirAll(sessionDir, 0o755)
	promptText := "# T-001: Demo\n\nBody"
	// Session timestamped 1 hour before since — should be ignored
	oldSession := `{"type":"session","version":3,"id":"old-id","timestamp":"2020-01-01T00:00:00Z","cwd":` + quote(cwd) + `}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2020-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":` + quote(promptText) + `}],"timestamp":1577836801000}}` + "\n"
	_ = os.WriteFile(filepath.Join(sessionDir, "2020-01-01T00-00-00-000Z_old-id.jsonl"), []byte(oldSession), 0o644)

	// since = now — the old session is way before since-2s
	ref, ok := CaptureSessionRef("pi", promptText, time.Now())
	if ok {
		t.Errorf("should not capture session from before since window, got ref=%q", ref)
	}
}

func TestDetectStatePatterns(t *testing.T) {
	state, source, reason, excerpt, changed := DetectState("Approve command? yes/no", "", 0)
	if state != StateNeedsPermission || source != "pattern" || reason == "" || excerpt == "" || !changed {
		t.Fatalf("permission detection = %s %s %q %q %v", state, source, reason, excerpt, changed)
	}
	state, _, _, _, _ = DetectState("same output", "same output", time.Minute)
	if state != StateIdleUnknown {
		t.Fatalf("idle detection = %s", state)
	}
}

func TestParseSessionRef(t *testing.T) {
	ref, ok := ParseSessionRef("hello\nSESSION_REF=abc123 ready\n", "SESSION_REF=")
	if !ok || ref != "abc123" {
		t.Fatalf("ref=%q ok=%v", ref, ok)
	}
}

func TestCaptureCodexSessionRefFromHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "# T-001: Demo\n\nBody"
	history := `{"session_id":"old","ts":1,"text":"# T-001: Demo\n\nBody"}` + "\n" +
		`{"session_id":"new","ts":2000,"text":"# T-001: Demo\n\nBody"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "history.jsonl"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, ok := CaptureSessionRef("codex", prompt, time.Unix(1999, 0))
	if !ok || ref != "new" {
		t.Fatalf("ref=%q ok=%v", ref, ok)
	}
}

func TestCapturePiSessionRefFromSessionFile(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	sessionDir := filepath.Join(home, ".pi", "agent", "sessions", "--tmp-test--")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "# T-001: Demo\n\nBody"
	session := `{"type":"session","version":3,"id":"019e-pi-test","timestamp":"2030-01-01T00:00:00Z","cwd":` + quote(cwd) + `}` + "\n" +
		`{"type":"message","id":"abcd1234","parentId":null,"timestamp":"2030-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":` + quote(prompt) + `}],"timestamp":1893456001000}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "2030-01-01T00-00-00-000Z_019e-pi-test.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, ok := CaptureSessionRef("pi", prompt, time.Unix(1893455999, 0))
	if !ok || ref != "019e-pi-test" {
		t.Fatalf("ref=%q ok=%v", ref, ok)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestCaptureCopilotSessionRefFromSessionStore(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	// Create a fake ~/.copilot/session-store.db
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(home, ".copilot", "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	promptText := "# T-001: Demo\n\nBody"
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

	sessionID := "822133b3-a89f-4d2b-9e76-06111cab7d67"

	// Old session with wrong prompt — should not be returned
	_, err = db.Exec(`INSERT INTO sessions(id, cwd, created_at) VALUES(?,?,?)`,
		"old-session-id", cwd, "2030-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO turns(session_id, turn_index, user_message) VALUES(?,?,?)`,
		"old-session-id", 0, "different prompt")
	if err != nil {
		t.Fatal(err)
	}

	// Correct session matching prompt, cwd, and created_at
	_, err = db.Exec(`INSERT INTO sessions(id, cwd, created_at) VALUES(?,?,?)`,
		sessionID, cwd, "2030-01-01T00:00:02Z")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO turns(session_id, turn_index, user_message) VALUES(?,?,?)`,
		sessionID, 0, promptText)
	if err != nil {
		t.Fatal(err)
	}

	ref, ok := CaptureSessionRef("copilot", promptText, time.Unix(1893456000, 0))
	if !ok || ref != sessionID {
		t.Fatalf("ref=%q ok=%v", ref, ok)
	}
}

func TestCaptureCopilotSessionRefDoesNotMatchSessionWithoutPromptTurn(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
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

	_, err = db.Exec(`INSERT INTO sessions(id, cwd, created_at) VALUES(?,?,?)`,
		"session-without-turn", cwd, "2030-01-01T00:00:02Z")
	if err != nil {
		t.Fatal(err)
	}

	ref, ok := CaptureSessionRef("copilot", "# T-001: Demo\n\nBody", time.Unix(1893456000, 0))
	if ok || ref != "" {
		t.Fatalf("ref=%q ok=%v", ref, ok)
	}
}

func TestValidateCopilotSessionRefRejectsSessionWithoutPromptTurn(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
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
		INSERT INTO sessions(id, cwd, created_at) VALUES('bad-ref', ?, '2030-01-01T00:00:02Z');
	`, cwd)
	if err != nil {
		t.Fatal(err)
	}

	if ValidateSessionRef("copilot", "bad-ref", "# T-001: Demo\n\nBody") {
		t.Fatal("expected copilot ref without prompt turn to be rejected")
	}
}

func TestCaptureCopilotSessionRefInExplicitWorkdir(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	promptText := "# T-001: Demo\n\nBody"

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
		INSERT INTO sessions(id, cwd, created_at) VALUES('workdir-ref', ?, '2030-01-01T00:00:02Z');
		INSERT INTO turns(session_id, turn_index, user_message) VALUES('workdir-ref', 0, ?);
	`, cwd, promptText)
	if err != nil {
		t.Fatal(err)
	}

	ref, ok := CaptureSessionRefInCWD("copilot", promptText, cwd, time.Unix(1893456000, 0))
	if !ok || ref != "workdir-ref" {
		t.Fatalf("ref=%q ok=%v", ref, ok)
	}
}

func TestClaudeDefaultUsesPromptArgumentMode(t *testing.T) {
	harnesses := DefaultConfigs()
	// No-prompt open
	noPromptStart, err := StartCommand(harnesses, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(noPromptStart, []string{"claude"}) {
		t.Fatalf("no-prompt start=%#v", noPromptStart)
	}
	// Prompt open: prompt is appended as final positional arg
	start, sent, err := StartCommandWithPrompt(harnesses, "claude", "# T-001: Demo\n\nBody", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude", "# T-001: Demo\n\nBody"}
	if !sent || !reflect.DeepEqual(start, want) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(harnesses, "claude", "9852755a-49b4-45a8-8b90-3247d6f2efd0")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"claude", "--resume", "9852755a-49b4-45a8-8b90-3247d6f2efd0"}
	if !reflect.DeepEqual(resume, want) {
		t.Fatalf("resume=%#v", resume)
	}
}

func TestCaptureClaudeSessionRefFromProjectsDir(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	promptText := "# T-001: Demo\n\nBody"

	// Directory name doesn't matter — capture matches by the `cwd` field
	// inside each jsonl entry, not by the encoded directory name.
	projectDir := filepath.Join(home, ".claude", "projects", "encoded-cwd-arbitrary")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Session A: meta entry + matching user prompt → should be picked
	sessionA := strings.Join([]string{
		`{"type":"user","isMeta":true,"sessionId":"session-a","cwd":` + quote(cwd) + `,"timestamp":"2030-01-01T00:00:00.100Z","message":{"role":"user","content":"<local-command-caveat>...</local-command-caveat>"}}`,
		`{"type":"user","isMeta":false,"sessionId":"session-a","cwd":` + quote(cwd) + `,"timestamp":"2030-01-01T00:00:01.000Z","message":{"role":"user","content":` + quote(promptText) + `}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "session-a.jsonl"), []byte(sessionA), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session B: matching prompt but wrong cwd → should be ignored
	wrongCWD := t.TempDir()
	sessionB := `{"type":"user","isMeta":false,"sessionId":"session-b","cwd":` + quote(wrongCWD) + `,"timestamp":"2030-01-01T00:00:02.000Z","message":{"role":"user","content":` + quote(promptText) + `}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "session-b.jsonl"), []byte(sessionB), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session C: right cwd but different prompt → ignored
	sessionC := `{"type":"user","isMeta":false,"sessionId":"session-c","cwd":` + quote(cwd) + `,"timestamp":"2030-01-01T00:00:03.000Z","message":{"role":"user","content":"unrelated"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "session-c.jsonl"), []byte(sessionC), 0o644); err != nil {
		t.Fatal(err)
	}

	since := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ref, ok := CaptureSessionRefInCWD("claude", promptText, cwd, since)
	if !ok || ref != "session-a" {
		t.Fatalf("ref=%q ok=%v, want session-a/true", ref, ok)
	}

	// CWD mismatch returns nothing
	if ref, ok := CaptureSessionRefInCWD("claude", promptText, t.TempDir(), since); ok {
		t.Fatalf("unexpected match for unrelated cwd: ref=%q", ref)
	}
}

func TestCaptureClaudeSessionRefHonorsSinceWindow(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	promptText := "# T-001: Demo\n\nBody"

	projectDir := filepath.Join(home, ".claude", "projects", "encoded")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Session predates the since window by more than the 2s slack — ignored
	oldSession := `{"type":"user","isMeta":false,"sessionId":"old","cwd":` + quote(cwd) + `,"timestamp":"2020-01-01T00:00:00Z","message":{"role":"user","content":` + quote(promptText) + `}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "old.jsonl"), []byte(oldSession), 0o644); err != nil {
		t.Fatal(err)
	}

	if ref, ok := CaptureSessionRefInCWD("claude", promptText, cwd, time.Now()); ok {
		t.Fatalf("expected no match for pre-window session, got ref=%q", ref)
	}
}

func TestValidateCopilotSessionRefInExplicitWorkdir(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	promptText := "# T-001: Demo\n\nBody"

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
		INSERT INTO sessions(id, cwd, created_at) VALUES('workdir-ref', ?, '2030-01-01T00:00:02Z');
		INSERT INTO turns(session_id, turn_index, user_message) VALUES('workdir-ref', 0, ?);
	`, cwd, promptText)
	if err != nil {
		t.Fatal(err)
	}

	if !ValidateSessionRefInCWD("copilot", "workdir-ref", promptText, cwd) {
		t.Fatal("expected explicit workdir copilot ref to validate")
	}
}
