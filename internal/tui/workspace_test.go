package tui

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestNormalizeBranchNamePreservesSlashStructure(t *testing.T) {
	if got := normalizeBranchName("feat/Implement Worktrees"); got != "feat/implement-worktrees" {
		t.Fatalf("normalizeBranchName = %q", got)
	}
	if got := normalizeBranchName("  !!!  "); got != "ticket" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestTicketWorkspaceLineCommunicatesStateWithoutColor(t *testing.T) {
	ticket := storage.Ticket{
		WorkspaceID:         sql.NullInt64{Int64: 1, Valid: true},
		WorkspaceBranch:     sql.NullString{String: "feat/worktrees", Valid: true},
		WorkspaceState:      sql.NullString{String: storage.WorkspaceStateReady, Valid: true},
		WorkspaceStatusJSON: sql.NullString{String: `{"ahead":3,"behind":1,"dirty":true,"changed_files":7,"mergeable":true,"mergeability_known":true}`, Valid: true},
	}
	got := ticketWorkspaceLine(ticket)
	for _, want := range []string{" feat/worktrees", "+3", "-1", "dirty", "mergeable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q missing %q", got, want)
		}
	}
	ticket.WorkspaceState = sql.NullString{String: storage.WorkspaceStateResolving, Valid: true}
	if got := ticketWorkspaceLine(ticket); !strings.Contains(got, "resolving") {
		t.Fatalf("resolving line = %q", got)
	}
}
