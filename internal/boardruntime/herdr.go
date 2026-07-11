// Package boardruntime starts the board process in its configured runtime.
package boardruntime

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"kanbi/internal/config"
)

func LaunchHerdrBoard(cfg config.Config, exe string) error {
	if err := EnsureHerdrAvailable(cfg); err != nil {
		return err
	}
	out, err := HerdrWorkspaceCommand(cfg).Output()
	if err != nil {
		return fmt.Errorf("create Kanbi Herdr workspace: %w", err)
	}
	paneID := RootPaneID(out)
	if paneID == "" {
		return fmt.Errorf("create Kanbi Herdr workspace: missing root pane id")
	}
	if err := HerdrPaneRunCommand(cfg, paneID, exe).Run(); err != nil {
		return fmt.Errorf("start kanbi in Herdr pane: %w", err)
	}
	return HerdrAttachCommand(cfg).Run()
}

func EnsureHerdrAvailable(cfg config.Config) error {
	if _, err := HerdrStatusCommand(cfg).CombinedOutput(); err == nil {
		return nil
	}
	start := HerdrServerCommand(cfg)
	if err := start.Start(); err != nil {
		return HerdrUnavailableError(cfg, err.Error())
	}
	_ = start.Process.Release()
	out, err := WaitForHerdrStatus(cfg, 5*time.Second)
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	return HerdrUnavailableError(cfg, detail)
}

func WaitForHerdrStatus(cfg config.Config, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var lastOut []byte
	var lastErr error
	for {
		out, err := HerdrStatusCommand(cfg).CombinedOutput()
		if err == nil {
			return out, nil
		}
		lastOut, lastErr = out, err
		if time.Now().After(deadline) {
			return lastOut, lastErr
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func HerdrUnavailableError(cfg config.Config, detail string) error {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "unknown error"
	}
	advice := "run `herdr status` for details, start Herdr with `herdr`, or set `multiplexer.default: tmux`"
	if cfg.Paths.ConfigFile != "" {
		advice += " in " + cfg.Paths.ConfigFile
	}
	return fmt.Errorf("Herdr is configured as Kanbi's multiplexer but is unavailable (%s); %s", detail, advice)
}

func herdrBinary(cfg config.Config) string {
	if cfg.Multiplexer.Herdr.Binary != "" {
		return cfg.Multiplexer.Herdr.Binary
	}
	return "herdr"
}

func HerdrStatusCommand(cfg config.Config) *exec.Cmd {
	cmd := exec.Command(herdrBinary(cfg), "status")
	cmd.Env = HerdrCommandEnv(cfg)
	return cmd
}
func HerdrServerCommand(cfg config.Config) *exec.Cmd {
	cmd := exec.Command(herdrBinary(cfg), "server")
	cmd.Env, cmd.Stdout, cmd.Stderr = HerdrCommandEnv(cfg), io.Discard, io.Discard
	return cmd
}
func HerdrWorkspaceCommand(cfg config.Config) *exec.Cmd {
	args := []string{"workspace", "create", "--label", "kanbi", "--focus"}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	cmd := exec.Command(herdrBinary(cfg), args...)
	cmd.Env, cmd.Stderr = HerdrCommandEnv(cfg), os.Stderr
	return cmd
}
func HerdrPaneRunCommand(cfg config.Config, paneID, exe string) *exec.Cmd {
	cmd := exec.Command(herdrBinary(cfg), "pane", "run", paneID, HerdrBoardShellCommand(exe))
	cmd.Env, cmd.Stdout, cmd.Stderr = HerdrCommandEnv(cfg), io.Discard, os.Stderr
	return cmd
}

func HerdrBoardShellCommand(exe string) string {
	parts := []string{"env", "KANBI_INNER=1"}
	for _, key := range []string{"KANBI_CONFIG", "KANBI_DB", "KANBI_DATA_DIR", "KANBI_STATE_DIR"} {
		if val := os.Getenv(key); val != "" {
			parts = append(parts, ShellQuoteArg(key+"="+val))
		}
	}
	parts = append(parts, ShellQuoteArg(exe), "--board")
	return strings.Join(parts, " ")
}

func RootPaneID(out []byte) string {
	var obj map[string]any
	if json.Unmarshal(out, &obj) != nil {
		return ""
	}
	for _, key := range []string{"result.root_pane.pane_id", "root_pane.pane_id", "pane_id"} {
		if s, ok := dottedJSON(obj, key).(string); ok {
			return s
		}
	}
	return ""
}
func dottedJSON(obj map[string]any, key string) any {
	var cur any = obj
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}
func ShellQuoteArg(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
func HerdrAttachCommand(cfg config.Config) *exec.Cmd {
	cmd := exec.Command(herdrBinary(cfg))
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = HerdrCommandEnv(cfg), os.Stdin, os.Stdout, os.Stderr
	return cmd
}
func HerdrCommandEnv(cfg config.Config) []string {
	env := os.Environ()
	if session := cfg.Multiplexer.Herdr.Session; session != "" {
		env = append(env, "HERDR_SESSION="+session)
	}
	return env
}
