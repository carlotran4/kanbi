package boardruntime

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
)

func TestHerdrBoardLaunchUsesWorkspaceRootPane(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	cfg.Multiplexer.Herdr.Binary = "herdr"
	cfg.Multiplexer.Herdr.Session = "default"
	t.Setenv("KANBI_CONFIG", "/tmp/kanbi config.yaml")
	if got := strings.Join(HerdrStatusCommand(cfg).Args, " "); !strings.Contains(got, "herdr status") {
		t.Fatalf("unexpected Herdr status command: %s", got)
	}
	workspace := HerdrWorkspaceCommand(cfg)
	if got := strings.Join(workspace.Args, " "); !strings.Contains(got, "workspace create --label kanbi --focus") {
		t.Fatalf("unexpected Herdr workspace command: %s", got)
	}
	paneID := RootPaneID([]byte(`{"id":"cli:workspace:create","result":{"root_pane":{"pane_id":"w1:p1"}}}`))
	if paneID != "w1:p1" {
		t.Fatalf("pane id = %q", paneID)
	}
	run := HerdrPaneRunCommand(cfg, paneID, "/tmp/kanbi path")
	args := strings.Join(run.Args, " ")
	if !strings.Contains(args, "pane run w1:p1") || !strings.Contains(args, "KANBI_INNER=1") || !strings.Contains(args, "'KANBI_CONFIG=/tmp/kanbi config.yaml'") || !strings.Contains(args, "'/tmp/kanbi path' --board") {
		t.Fatalf("unexpected Herdr pane run command: %v", run.Args)
	}
	if run.Stdout != io.Discard {
		t.Fatalf("Herdr pane run JSON should be discarded, stdout=%#v", run.Stdout)
	}
}

func TestHerdrBoardLaunchStartsServerWhenUnavailable(t *testing.T) {
	dir := t.TempDir()
	callsPath := filepath.Join(dir, "calls")
	serverStartedPath := filepath.Join(dir, "server-started")
	fakeHerdr := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + ShellQuoteArg(callsPath) + "\n" +
		"if [ \"$1\" = status ]; then\n" +
		"  if [ -f " + ShellQuoteArg(serverStartedPath) + " ]; then echo ok; exit 0; fi\n" +
		"  echo unavailable >&2; exit 1\n" +
		"fi\n" +
		"if [ \"$1\" = server ]; then touch " + ShellQuoteArg(serverStartedPath) + "; exit 0; fi\n" +
		"exit 99\n"
	if err := os.WriteFile(fakeHerdr, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(config.Paths{ConfigFile: filepath.Join(dir, "config.yaml")})
	cfg.Multiplexer.Herdr.Binary = fakeHerdr
	if err := EnsureHerdrAvailable(cfg); err != nil {
		t.Fatalf("expected Kanbi to start Herdr server: %v", err)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(strings.TrimSpace(string(calls)))
	serverCalls := 0
	for _, call := range got {
		if call == "server" {
			serverCalls++
		}
	}
	if len(got) < 3 || got[0] != "status" || got[len(got)-1] != "status" || serverCalls != 1 {
		t.Fatalf("unexpected Herdr calls: %v", got)
	}
}
