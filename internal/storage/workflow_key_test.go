package storage

import (
	"errors"
	"testing"
	"time"
)

func TestMasterAggregatesByWorkflowKeyAcrossRenames(t *testing.T) {
	s, ctx := newTestStore(t)
	a, err := s.CreateBoard(ctx, "Board A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(ctx, "Board B")
	if err != nil {
		t.Fatal(err)
	}
	aView := boardViewByID(t, ctx, s, a.ID)
	bView := boardViewByID(t, ctx, s, b.ID)
	// Map Board B's Review column key to Board A's exact display name after rename simulation.
	// Create Code Review on B and set both keys to "review".
	codeReview, err := s.AddColumn(ctx, b.ID, "Code Review")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetColumnWorkflowKey(ctx, codeReview.ID, "review"); err != nil {
		t.Fatal(err)
	}
	// Locate a column named Review on board A if default columns include it; otherwise add one.
	var reviewA int64
	for _, col := range aView.Columns {
		if col.Name == "Review" {
			reviewA = col.ID
			break
		}
	}
	if reviewA == 0 {
		col, err := s.AddColumn(ctx, a.ID, "Review")
		if err != nil {
			t.Fatal(err)
		}
		reviewA = col.ID
	}
	if err := s.SetColumnWorkflowKey(ctx, reviewA, "review"); err != nil {
		t.Fatal(err)
	}
	// Rename display name should not split Master when key stays the same.
	if err := s.RenameColumn(ctx, reviewA, "In Review"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(ctx, reviewA, "A review ticket", "", "pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(ctx, codeReview.ID, "B review ticket", "", "pi"); err != nil {
		t.Fatal(err)
	}

	master, err := s.MasterBoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, col := range master.Columns {
		if col.WorkflowKey != "review" {
			continue
		}
		found = true
		titles := map[string]bool{}
		for _, ticket := range col.Tickets {
			titles[ticket.Title] = true
		}
		if !titles["A review ticket"] || !titles["B review ticket"] {
			t.Fatalf("expected both tickets under review key, got titles=%v col=%+v", titles, col)
		}
	}
	if !found {
		t.Fatalf("expected synthetic review column, columns=%+v", master.Columns)
	}
	_ = bView
}

func TestSetColumnWorkflowKeyConflictAndRenamePreservesKey(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoard(ctx, "Keys")
	if err != nil {
		t.Fatal(err)
	}
	view := boardViewByID(t, ctx, s, board.ID)
	if len(view.Columns) < 2 {
		t.Fatal("need two columns")
	}
	key0 := view.Columns[0].WorkflowKey
	if err := s.SetColumnWorkflowKey(ctx, view.Columns[1].ID, key0); err == nil {
		t.Fatal("expected conflict when reusing workflow key on same board")
	}
	if err := s.RenameColumn(ctx, view.Columns[0].ID, "Renamed Display"); err != nil {
		t.Fatal(err)
	}
	col, err := s.ColumnByID(ctx, view.Columns[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if col.Name != "Renamed Display" || col.WorkflowKey != key0 {
		t.Fatalf("rename should preserve workflow key: %+v", col)
	}
}

func TestArchiveBoardRejectsActiveSyncLease(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoard(ctx, "Syncing")
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := s.AcquireBoardSyncLease(ctx, board.ID, "test-owner", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire lease: acquired=%v err=%v", acquired, err)
	}
	if err := s.ArchiveBoard(ctx, board.ID); !errors.Is(err, ErrBoardSyncInProgress) {
		t.Fatalf("archive with active sync lease err=%v", err)
	}
	if err := s.ReleaseBoardSyncLease(ctx, board.ID, "test-owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveBoard(ctx, board.ID); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveBoardSkipsMasterAndBlocksActiveSessions(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoard(ctx, "Archivable")
	if err != nil {
		t.Fatal(err)
	}
	view := boardViewByID(t, ctx, s, board.ID)
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "Live", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "s", TmuxWindowName: "w", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveBoard(ctx, board.ID); err != ErrBoardHasActiveSessions {
		t.Fatalf("ArchiveBoard with active session err=%v", err)
	}
	ses, ok, err := s.ActiveSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("expected active session: ok=%v err=%v", ok, err)
	}
	if err := s.MarkSessionClosed(ctx, ses.ID, "closed", "test", "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveBoard(ctx, board.ID); err != nil {
		t.Fatal(err)
	}
	boards, err := s.ListBoards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range boards {
		if b.ID == board.ID {
			t.Fatal("archived board should be hidden from default list")
		}
	}
	all, err := s.ListBoardsFiltered(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range all {
		if b.ID == board.ID {
			found = true
			if !b.ArchivedAt.Valid || b.SyncEnabled {
				t.Fatalf("expected archived+sync disabled: %+v", b)
			}
		}
	}
	if !found {
		t.Fatal("expected archived board in includeArchived list")
	}
	master, err := s.MasterBoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range master.Columns {
		for _, tk := range col.Tickets {
			if tk.BoardID == board.ID {
				t.Fatalf("master should exclude archived board tickets: %+v", tk)
			}
		}
	}
}

func TestFilterPresetRoundTripAndMissingBoardUUID(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoard(ctx, "PresetBoard")
	if err != nil {
		t.Fatal(err)
	}
	durable := DurableMasterFilter{BoardUUIDs: []string{board.UUID}, Search: "api"}
	p, err := s.SaveFilterPreset(ctx, "API only", durable)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "API only" || len(p.Filter.BoardUUIDs) != 1 {
		t.Fatalf("preset=%+v", p)
	}
	resolved, missing, err := s.ResolveMasterFilter(ctx, DurableMasterFilter{BoardUUIDs: []string{board.UUID, "missing-uuid"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.BoardIDs) != 1 || resolved.BoardIDs[0] != board.ID {
		t.Fatalf("resolved=%+v", resolved)
	}
	if len(missing) != 1 || missing[0] != "missing-uuid" {
		t.Fatalf("missing=%v", missing)
	}
}

func TestClaimSessionRefusesArchivedBoard(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoard(ctx, "NoSessions")
	if err != nil {
		t.Fatal(err)
	}
	view := boardViewByID(t, ctx, s, board.ID)
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "Idle", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveBoard(ctx, board.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "s", TmuxWindowName: "w", Status: "running"}); err == nil {
		t.Fatal("expected claim on archived board to fail")
	}
}

func TestLoadBoardAggregateRejectsActiveSessions(t *testing.T) {
	s, ctx := newTestStore(t)
	board, err := s.CreateBoard(ctx, "ExportLive")
	if err != nil {
		t.Fatal(err)
	}
	view := boardViewByID(t, ctx, s, board.ID)
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "Live", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", TmuxSessionName: "s", TmuxWindowName: "w", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadBoardAggregate(ctx, board.ID); err != ErrBoardHasActiveSessions {
		t.Fatalf("LoadBoardAggregate err=%v", err)
	}
}
