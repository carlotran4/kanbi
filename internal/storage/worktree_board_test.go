package storage

import (
	"context"
	"strings"
	"testing"
)

func TestBoardWorktreeModeIsChosenAtCreation(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := s.CreateBoardWithOptions(ctx, CreateBoardOptions{Name: "Isolated", Workdir: t.TempDir(), WorktreeMode: WorktreeModeGit, TicketBackend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if board.WorktreeMode != WorktreeModeGit {
		t.Fatalf("mode=%q", board.WorktreeMode)
	}
	loaded, err := s.BoardByID(ctx, board.ID)
	if err != nil || loaded.WorktreeMode != WorktreeModeGit {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestGitBoardSessionClaimRequiresCurrentWorkspace(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, _ := s.DefaultBoard(ctx)
	view, _ := s.BoardViewByID(ctx, board.ID)
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "work", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBoardWorktreeMode(ctx, board.ID, WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "s", TmuxWindowName: "w"}, false)
	if err == nil || !strings.Contains(err.Error(), "prepared workspace") {
		t.Fatalf("claim error=%v", err)
	}
}

func TestBoardCannotDisableWorktreesAfterWorkspaceHistoryExists(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, _ := s.DefaultBoard(ctx)
	view, _ := s.BoardViewByID(ctx, board.ID)
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "work", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBoardWorktreeMode(ctx, board.ID, WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateWorkspaceClaim(ctx, Workspace{TicketID: ticket.ID, BoardID: board.ID, Kind: WorkspaceKindGitWorktree, State: WorkspaceStateIntegrated, IsCurrent: true, OwnsWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBoardWorktreeMode(ctx, board.ID, WorktreeModeOff); err == nil || !strings.Contains(err.Error(), "workspace history") {
		t.Fatalf("disable error=%v", err)
	}
}
