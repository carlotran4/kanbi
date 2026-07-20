package tui

import (
	"database/sql"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func TestBoardPickerRequiresConfirmationToEnableWorktrees(t *testing.T) {
	m, store, ctx := newTestModel(t)
	m.boardPicker = true
	m.boardIndex = 1
	updated := m.updateBoardPicker(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if !updated.boardWorktreeEnabling {
		t.Fatal("expected worktree enable confirmation")
	}
	board, _ := store.DefaultBoard(ctx)
	if board.WorktreeMode != storage.WorktreeModeOff {
		t.Fatal("mode changed before confirmation")
	}
	updated = updated.updateBoardWorktreeEnable(tea.KeyMsg{Type: tea.KeyEnter})
	board, _ = store.DefaultBoard(ctx)
	if board.WorktreeMode != storage.WorktreeModeGit || updated.boardWorktreeEnabling {
		t.Fatalf("board=%+v modal=%v", board, updated.boardWorktreeEnabling)
	}
}

func TestTicketWorkspaceCardLineShowsHealthNotBranchName(t *testing.T) {
	ticket := storage.Ticket{
		WorkspaceID:         sql.NullInt64{Int64: 1, Valid: true},
		WorkspaceBranch:     sql.NullString{String: "feat/worktrees", Valid: true},
		WorkspaceState:      sql.NullString{String: storage.WorkspaceStateReady, Valid: true},
		WorkspaceStatusJSON: sql.NullString{String: `{"ahead":3,"behind":1,"dirty":true,"changed_files":7,"mergeable":true,"mergeability_known":true}`, Valid: true},
	}
	got := ticketWorkspaceCardLine(ticket, 38)
	for _, want := range []string{"git", "7 files", "+3/-1", "mergeable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q missing %q", got, want)
		}
	}
	if strings.Contains(got, ticket.WorkspaceBranch.String) {
		t.Fatalf("card workspace line should omit branch identity: %q", got)
	}
	ticket.WorkspaceStatusJSON = sql.NullString{String: `{"dirty":false,"changed_files":2}`, Valid: true}
	if got := ticketWorkspaceCardLine(ticket, 24); !strings.Contains(got, "2 files") || strings.Contains(got, "clean") {
		t.Fatalf("changed files should not be reported as clean: %q", got)
	}

	ticket.WorkspaceStatusJSON = sql.NullString{String: `{"ahead":3,"dirty":true,"changed_files":7,"conflicts":["a","b"],"mergeable":false,"mergeability_known":true}`, Valid: true}
	if got := ticketWorkspaceCardLine(ticket, 24); got != "git ✕ 2 conflicts" {
		t.Fatalf("conflicts should suppress lower-priority noise, got %q", got)
	}
	ticket.WorkspaceState = sql.NullString{String: storage.WorkspaceStateResolving, Valid: true}
	if got := ticketWorkspaceCardLine(ticket, 24); got != "git ◐ resolving" {
		t.Fatalf("resolving line = %q", got)
	}
	ticket.WorkspaceState = sql.NullString{String: storage.WorkspaceStateIntegrated, Valid: true}
	if got := ticketWorkspaceCardLine(ticket, 24); got != "git ✓ integrated" {
		t.Fatalf("integrated line = %q", got)
	}
	if detail := ticketWorkspaceDetailLine(ticket); !strings.Contains(detail, "feat/worktrees") || !strings.Contains(detail, "reopens same session") {
		t.Fatalf("workspace detail should retain full branch identity and reopen context: %q", detail)
	}
}

func TestInspectorBranchLinesWrapWithoutLosingBranchName(t *testing.T) {
	branch := "feat/a-very-long-branch-name/with-more-context"
	lines := inspectorBranchLines(branch, 18)
	joined := strings.TrimPrefix(lines[0], "Branch: ") + strings.Join(lines[1:], "")
	if joined != branch {
		t.Fatalf("wrapped branch changed: got %q, want %q (lines=%v)", joined, branch, lines)
	}
}
