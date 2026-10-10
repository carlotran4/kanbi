package main

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestLegacyIntegrationCancellationPreservesHistoryAndCandidate(t *testing.T) {
	run, open := setupCLI(t)
	store := open()
	board, err := store.DefaultBoard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "candidate.txt")
	if err := os.WriteFile(candidate, []byte("committed candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", os.Getenv("KANBI_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, state := range []string{"ready", "promoting", "cleanup_required"} {
		_, err := db.Exec(`insert into integration_runs(public_id,board_id,state,repository_root,common_dir,worktree_path,branch_name,source_branch,source_sha,harness,token_hash,prompt,created_at,updated_at) values(?,?,?,'/repo',?,?,'candidate','main','source','pi','hash','historical prompt',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, state, board.ID, state, "/repo/.git/"+state, candidate)
		if err != nil {
			t.Fatal(err)
		}
	}
	out := captureStdout(t, func() error { return run("integration", "list") })
	if !strings.Contains(out, `"public_id": "ready"`) {
		t.Fatalf("history=%s", out)
	}
	captureStdout(t, func() error { return run("integration", "cancel", "ready") })
	captureStdout(t, func() error { return run("integration", "cancel", "ready") }) // idempotent
	old, err := store.IntegrationRunByPublicID(context.Background(), "ready")
	if err != nil || old.State != storage.IntegrationStateCancelled || old.Prompt != "historical prompt" {
		t.Fatalf("history=%+v err=%v", old, err)
	}
	if contents, err := os.ReadFile(candidate); err != nil || string(contents) != "committed candidate" {
		t.Fatalf("candidate=%q err=%v", contents, err)
	}
	for _, state := range []string{"promoting", "cleanup_required"} {
		if err := run("integration", "cancel", state); err == nil || !strings.Contains(err.Error(), "previous Kanbi version") {
			t.Fatalf("%s cancellation=%v", state, err)
		}
		old, err := store.IntegrationRunByPublicID(context.Background(), state)
		if err != nil || old.State != state {
			t.Fatalf("interrupted run changed: %+v err=%v", old, err)
		}
	}
	for _, command := range []string{"report", "promote"} {
		if err := run("integration", command, "ready"); err == nil || !strings.Contains(err.Error(), "retired") {
			t.Fatalf("%s=%v", command, err)
		}
	}
}

func TestDoctorRejectsRetiredHerdrLaunchAPI(t *testing.T) {
	report := probeDoctor(context.Background(), config.Defaults(config.Paths{}), doctorProber{
		lookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		commandOutput: func(name string, args ...string) ([]byte, error) {
			if strings.Join(args, " ") == "agent start --help" {
				return []byte("--cwd --workspace"), nil
			}
			return []byte(`{"server":{"running":true,"compatible":true}}`), nil
		},
		openStore:  func(context.Context, config.Config) (io.Closer, error) { return noopCloser{}, nil },
		ensureDirs: func(config.Config) error { return nil },
	})
	if err := report.FatalErr(); err == nil || !strings.Contains(err.Error(), "unsupported Herdr launch API") {
		t.Fatalf("doctor=%v", err)
	}
}
