package runtime

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
)

//go:embed pi_message_extension.mjs
var piMessageExtension string

func (m *Manager) prepareMessageLaunch(command []string, id int64, harnessName, launchCWD string) ([]string, error) {
	if m.Store.DatabasePath() == "" || m.Store.DatabasePath() == ":memory:" {
		return command, nil
	}
	cli, err := os.Executable()
	if err != nil {
		return nil, err
	}
	path := MessageEndpointPath(m.Store.DatabasePath(), id)
	env := []string{"env", "KANBI_MAILBOX_CLI=" + cli, "KANBI_MESSAGE_ENDPOINT_FILE=" + path}
	native := command
	if len(native) > 0 && native[0] == "env" {
		native = native[1:]
		for len(native) > 0 && strings.Contains(native[0], "=") {
			if !strings.HasPrefix(native[0], "KANBI_MAILBOX_CLI=") && !strings.HasPrefix(native[0], "KANBI_MESSAGE_ENDPOINT_FILE=") {
				env = append(env, native[0])
			}
			native = native[1:]
		}
	}
	if len(native) == 0 {
		return nil, errors.New("empty messaging harness command")
	}
	switch {
	case harnessName == "codex" && native[0] == "codex":
		home := os.Getenv("CODEX_HOME")
		for _, assignment := range env {
			if value, ok := strings.CutPrefix(assignment, "CODEX_HOME="); ok {
				home = value
			}
		}
		if home == "" {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			home = filepath.Join(userHome, ".codex")
		}
		if !filepath.IsAbs(home) {
			home = filepath.Join(launchCWD, home)
		}
		binary, err := exec.LookPath("codex")
		if err != nil {
			return nil, err
		}
		binary, err = filepath.Abs(binary)
		if err != nil {
			return nil, err
		}
		nativePath := os.Getenv("PATH")
		for _, assignment := range env {
			if value, ok := strings.CutPrefix(assignment, "PATH="); ok {
				nativePath = value
			}
		}
		env = append(env, "CODEX_HOME="+home)
		if err := writeMessageEndpoint(path, MessageEndpoint{SessionID: id, CodexHome: home, CodexBinary: binary, NativePath: nativePath}); err != nil {
			return nil, err
		}
	case harnessName == "pi" && filepath.Base(native[0]) == "pi":
		extension := filepath.Join(filepath.Dir(path), "pi-message-extension.mjs")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err := writePrivateMessageFile(extension, []byte(piMessageExtension)); err != nil {
			return nil, err
		}
		native = append([]string{native[0], "-e", extension}, native[1:]...)
	case harnessName == "claude" && native[0] == "claude":
		dir := filepath.Join(filepath.Dir(path), "claude-plugin")
		for _, subdir := range []string{".claude-plugin", "hooks"} {
			if err := os.MkdirAll(filepath.Join(dir, subdir), 0700); err != nil {
				return nil, err
			}
		}
		manifest := `{"name":"kanbi-messages","version":"1.0.0","description":"Kanbi session mailbox receiver"}`
		if err := writePrivateMessageFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(manifest)); err != nil {
			return nil, err
		}
		hooks := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": `"$KANBI_MAILBOX_CLI" messages register-claude`, "timeout": 5}}}}}}
		data, _ := json.Marshal(hooks)
		if err := writePrivateMessageFile(filepath.Join(dir, "hooks", "hooks.json"), data); err != nil {
			return nil, err
		}
		native = append([]string{"claude", "--plugin-dir", dir}, native[1:]...)
	}
	return append(env, native...), nil
}

// Called only by the recipient's own CLI/native receiver. Expose a small
// notification claim surface without accepting arbitrary sender identities.
func ClaimNativeMessage(ctx context.Context, store *storage.Store, recipient, id int64) (bool, error) {
	unlock, err := store.LockRuntime(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	ses, ok, err := store.SessionByID(ctx, recipient)
	if err != nil {
		return false, err
	}
	if !ok || !ses.IsActive || ses.Harness != "pi" {
		return false, errors.New("native message claims require the owning Pi session")
	}
	endpoint, err := readMessageEndpoint(MessageEndpointPath(store.DatabasePath(), ses.ID))
	if err != nil || endpoint.Disabled || endpoint.SessionID != ses.ID || endpoint.NativeRef == "" || ses.HarnessSessionRef.Valid && ses.HarnessSessionRef.String != endpoint.NativeRef {
		return false, errors.New("Pi native conversation identity is not registered for this attempt")
	}
	if ses.OperatorID != 0 {
		recipient = -ses.OperatorID
	}
	return store.ClaimMessageNotification(ctx, recipient, id, "pi_extension")
}
