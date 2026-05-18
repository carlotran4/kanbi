package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"agent-kanban/internal/config"
)

func TestHarnessCommandConstruction(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	cfg.Harnesses["pi"] = config.Harness{
		Start:  []string{"/bin/pi"},
		Resume: []string{"/bin/pi", "resume", "{session_ref}"},
	}
	start, err := StartCommand(cfg, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(start, []string{"/bin/pi"}) {
		t.Fatalf("start = %#v", start)
	}
	resume, err := ResumeCommand(cfg, "pi", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume, []string{"/bin/pi", "resume", "abc123"}) {
		t.Fatalf("resume = %#v", resume)
	}
}

func TestCodexDefaultUsesPromptArgumentMode(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	start, sent, err := StartCommandWithPrompt(cfg, "codex", "# T-001: Demo\n\nBody", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "--no-alt-screen", "# T-001: Demo\n\nBody"}
	if !sent || !reflect.DeepEqual(start, want) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(cfg, "codex", "019e-test")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"codex", "resume", "--no-alt-screen", "019e-test"}
	if !reflect.DeepEqual(resume, want) {
		t.Fatalf("resume=%#v", resume)
	}
}

func TestPiDefaultUsesPromptArgumentModeAndSessionResume(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	start, sent, err := StartCommandWithPrompt(cfg, "pi", "# T-001: Demo\n\nBody", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pi", "# T-001: Demo\n\nBody"}
	if !sent || !reflect.DeepEqual(start, want) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(cfg, "pi", "019e-test")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"pi", "--session", "019e-test"}
	if !reflect.DeepEqual(resume, want) {
		t.Fatalf("resume=%#v", resume)
	}
}

func TestCopilotDefaultUsesInteractivePromptAndResumeFlag(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	start, sent, err := StartCommandWithPrompt(cfg, "copilot", "# T-001: Demo\n\nBody", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gh", "copilot", "--", "-i", "# T-001: Demo\n\nBody"}
	if !sent || !reflect.DeepEqual(start, want) {
		t.Fatalf("start=%#v sent=%v", start, sent)
	}
	resume, err := ResumeCommand(cfg, "copilot", "abc1234")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"gh", "copilot", "--", "--resume=abc1234"}
	if !reflect.DeepEqual(resume, want) {
		t.Fatalf("resume=%#v", resume)
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
	ref, ok := CaptureSessionRef(config.Defaults(config.Paths{}), "codex", prompt, time.Unix(1999, 0))
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
	ref, ok := CaptureSessionRef(config.Defaults(config.Paths{}), "pi", prompt, time.Unix(1893455999, 0))
	if !ok || ref != "019e-pi-test" {
		t.Fatalf("ref=%q ok=%v", ref, ok)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
