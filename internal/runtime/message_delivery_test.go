package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func deliveryFixture(t *testing.T, harness string) (*Manager, int64, storage.SessionMessage, string) {
	t.Helper()
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	sender, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Sender", "", "codex")
	a, err := s.UpsertActiveSession(ctx, sender.ID, storage.Session{Harness: "codex", Status: "running", Multiplexer: "herdr"})
	if err != nil {
		t.Fatal(err)
	}
	receiver, _ := s.CreateTicket(ctx, view.Columns[0].ID, "Receiver", "", harness)
	b, err := s.UpsertActiveSession(ctx, receiver.ID, storage.Session{Harness: harness, Status: "running", Multiplexer: "herdr", HarnessSessionRef: sqlString("native-ref"), MuxContainerID: sqlString("agent-1"), MuxMetadata: sqlString(`{"pane_id":"pane-1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.SendSessionMessage(ctx, a, b, "one", "untrusted body", 0)
	if err != nil {
		t.Fatal(err)
	}
	bin, log := writeFakeHerdr(t, map[string]string{"agent get": `{"state":"done"}`, "agent read": "prompt"})
	cfg := config.Defaults(config.Paths{})
	cfg.DBPath = s.DatabasePath()
	cfg.Multiplexer.Herdr.Binary = bin
	return &Manager{Config: cfg, Store: s}, b, msg, log
}
func TestCopilotNotificationUsesValidatedLiteralInputOnce(t *testing.T) {
	m, id, msg, log := deliveryFixture(t, "copilot")
	ctx := context.Background()
	if err := m.NotifySessionMessages(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := m.NotifySessionMessages(ctx, id); err != nil {
		t.Fatal(err)
	}
	output, _ := os.ReadFile(log)
	if strings.Count(string(output), "pane send-text") != 1 || strings.Count(string(output), "pane send-keys pane-1 enter") != 1 || strings.Contains(string(output), "untrusted body") {
		t.Fatal(string(output))
	}
	saved, _ := m.Store.SessionMessage(ctx, msg.ID)
	if saved.NotificationStatus != "submitted" || saved.State != "queued" {
		t.Fatal(saved)
	}
}
func TestCopilotBusyAndPermissionDoNotReceiveKeys(t *testing.T) {
	for _, state := range []string{"working", "blocked"} {
		t.Run(state, func(t *testing.T) {
			m, id, msg, _ := deliveryFixture(t, "copilot")
			bin, log := writeFakeHerdr(t, map[string]string{"agent get": `{"state":"` + state + `","message":"permission required"}`})
			m.Config.Multiplexer.Herdr.Binary = bin
			if err := m.NotifySessionMessages(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			output, _ := os.ReadFile(log)
			if strings.Contains(string(output), "send-text") || strings.Contains(string(output), "send-keys") {
				t.Fatal(string(output))
			}
			saved, _ := m.Store.SessionMessage(context.Background(), msg.ID)
			if saved.NotificationStatus != "deferred" {
				t.Fatal(saved)
			}
		})
	}
}
func TestClaudeSocketSubmissionAndInboxReceipt(t *testing.T) {
	m, id, msg, _ := deliveryFixture(t, "claude")
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "inbox.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan []map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		decoder := json.NewDecoder(conn)
		var auth, message map[string]any
		decoder.Decode(&auth)
		decoder.Decode(&message)
		received <- []map[string]any{auth, message}
	}()
	endpoint := MessageEndpoint{SessionID: id, NativeRef: "native-ref", Socket: path, Token: "private-token"}
	if err = writeMessageEndpoint(MessageEndpointPath(m.Store.DatabasePath(), id), endpoint); err != nil {
		t.Fatal(err)
	}
	if err = m.NotifySessionMessages(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case frames := <-received:
		if frames[0]["token"] != "private-token" || frames[1]["type"] != "user" {
			t.Fatal(frames)
		}
		text := frames[1]["message"].(map[string]any)["content"].(string)
		if !strings.Contains(text, "messages inbox") || strings.Contains(text, "untrusted body") {
			t.Fatal(text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no native socket delivery")
	}
	saved, _ := m.Store.SessionMessage(ctx, msg.ID)
	if saved.State != "queued" || saved.NotificationStatus != "submitted" {
		t.Fatal(saved)
	}
	if err = m.Store.ReceiptMessageRead(ctx, id, msg.ID); err != nil {
		t.Fatal(err)
	}
	saved, _ = m.Store.SessionMessage(ctx, msg.ID)
	if saved.State != "notified" {
		t.Fatal(saved)
	}
}
func TestClaudeEndpointRejectsConversationChange(t *testing.T) {
	m, id, msg, _ := deliveryFixture(t, "claude")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/not-used.sock")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "private-token")
	err := RegisterClaudeMessageEndpoint(context.Background(), m.Store, id, strings.NewReader(`{"session_id":"another-conversation"}`))
	if err == nil {
		t.Fatal("new native conversation accepted under old attempt")
	}
	if err = m.NotifySessionMessages(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	saved, _ := m.Store.SessionMessage(context.Background(), msg.ID)
	if saved.NotificationStatus != "deferred" {
		t.Fatal(saved)
	}
}

func TestCodexNotificationUsesRecipientHomeAndDoesNotRepeat(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint("failure-", failure), func(t *testing.T) {
			m, id, msg, _ := deliveryFixture(t, "codex")
			ctx := context.Background()
			home := filepath.Join(t.TempDir(), "recipient-home")
			if err := writeMessageEndpoint(MessageEndpointPath(m.Store.DatabasePath(), id), MessageEndpoint{SessionID: id, CodexHome: home}); err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			log := filepath.Join(directory, "calls")
			binary := filepath.Join(directory, "codex")
			script := "#!/bin/sh\nprintf '%s\\n' \"$CODEX_HOME\" \"$@\" >> " + shellQuote(log) + "\n"
			if failure {
				script += "exit 1\n"
			}
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := writeMessageEndpoint(MessageEndpointPath(m.Store.DatabasePath(), id), MessageEndpoint{SessionID: id, CodexHome: home, CodexBinary: binary, NativePath: os.Getenv("PATH")}); err != nil {
				t.Fatal(err)
			}
			command := m.Config.Harnesses["codex"]
			command.Start = []string{binary}
			m.Config.Harnesses["codex"] = command
			t.Setenv("CODEX_HOME", "sender-home-must-not-leak")
			for i := 0; i < 2; i++ {
				if err := m.NotifySessionMessages(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			output, _ := os.ReadFile(log)
			if strings.Count(string(output), "\nqueue\n") != 1 || !strings.HasPrefix(string(output), home+"\n") || strings.Contains(string(output), "untrusted body") {
				t.Fatal(string(output))
			}
			saved, _ := m.Store.SessionMessage(ctx, msg.ID)
			if failure && saved.NotificationStatus != "uncertain" {
				t.Fatal(saved)
			}
			if !failure && saved.State != "notified" {
				t.Fatal(saved)
			}
		})
	}
}
func TestNativeReceiverAssetsPreserveCommandsAndArePrivate(t *testing.T) {
	for _, harness := range []string{"pi", "claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			m, id, _, _ := deliveryFixture(t, harness)
			if harness == "codex" {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			input := []string{"env", "EXISTING=preserved", harness, "--existing-option"}
			command, err := m.prepareMessageLaunch(input, id, harness, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(command, " ")
			if !strings.Contains(joined, "EXISTING=preserved") || !strings.Contains(joined, "--existing-option") {
				t.Fatal(command)
			}
			if harness == "pi" && (!strings.Contains(joined, " -e ") || !strings.Contains(joined, "pi-message-extension.mjs")) {
				t.Fatal(command)
			}
			if harness == "claude" && !strings.Contains(joined, " --plugin-dir ") {
				t.Fatal(command)
			}
			dir := filepath.Dir(MessageEndpointPath(m.Store.DatabasePath(), id))
			info, err := os.Stat(dir)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatal(info, err)
			}
		})
	}
}
