package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTicketWritePolicies(t *testing.T) {
	for _, tc := range []struct {
		name         string
		title        string
		body         string
		harness      string
		defaultBlank bool
		wantTitle    string
		wantHarness  string
		wantErr      string
	}{
		{"normalizes", "  Title  ", " body\n", " CODEX ", false, "Title", "codex", ""},
		{"create defaults blank", "Title", "body", "  ", true, "Title", "pi", ""},
		{"update rejects blank", "Title", "body", "  ", false, "", "", "unsupported harness"},
		{"rejects blank title", " \t", "body", "pi", true, "", "", "title is required"},
		{"rejects unsupported", "Title", "body", "unknown", true, "", "", "unsupported harness"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTicketWrite(tc.title, tc.body, tc.harness, tc.defaultBlank)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got.Title != tc.wantTitle || got.Harness != tc.wantHarness || got.Body != tc.body {
				t.Fatalf("normalizeTicketWrite() = %+v, %v", got, err)
			}
		})
	}
}

func TestTicketValidationIsConsistentAcrossCreateUpdateAndAggregateImport(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	created, err := s.CreateTicket(ctx, view.Columns[0].ID, "  Created  ", " body ", " CODEX ")
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "Created" || created.Harness != "codex" || created.Body != " body " {
		t.Fatalf("CreateTicket did not normalize: %+v", created)
	}
	if err := s.UpdateTicket(ctx, created.ID, " Updated ", " body\n", " CLAUDE "); err != nil {
		t.Fatal(err)
	}
	updated, err := s.TicketByID(ctx, created.ID)
	if err != nil || updated.Title != "Updated" || updated.Harness != "claude" || updated.Body != " body\n" {
		t.Fatalf("UpdateTicket did not normalize: %+v, %v", updated, err)
	}
	if err := s.ValidateTicketUpdate("valid", " "); err == nil {
		t.Fatal("ValidateTicketUpdate accepted blank harness")
	}
	if err := s.UpdateTicket(ctx, created.ID, "valid", "", " "); err == nil {
		t.Fatal("UpdateTicket accepted blank harness")
	}

	result, err := s.InsertBoardAggregate(ctx, validAggregate(" Imported ", " CoPiLot "))
	if err != nil {
		t.Fatal(err)
	}
	agg, err := s.LoadBoardAggregate(ctx, result.Board.ID)
	if err != nil || len(agg.Tickets) != 1 {
		t.Fatalf("aggregate import = %+v, %v", agg, err)
	}
	if got := agg.Tickets[0]; got.Title != "Imported" || got.Harness != "copilot" || got.Body != " body " {
		t.Fatalf("aggregate ticket not normalized: %+v", got)
	}
}

func TestAggregateValidationIsPreflightAndPortable(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	before, err := s.ListBoardsFiltered(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	bad := validAggregate(" \t", "pi")
	if _, err := s.InsertBoardAggregate(ctx, bad); err == nil {
		t.Fatal("blank imported title accepted")
	}
	bad = validAggregate("okay", "unsupported")
	if _, err := s.InsertBoardAggregate(ctx, bad); err == nil {
		t.Fatal("unsupported imported harness accepted")
	}
	after, err := s.ListBoardsFiltered(ctx, true)
	if err != nil || len(after) != len(before) {
		t.Fatalf("failed aggregate validation mutated boards: before=%d after=%d err=%v", len(before), len(after), err)
	}

	portable := validAggregate("okay", "")
	portable.Name = " Imported Board "
	portable.Workdir = "  /path/that/does/not/exist  "
	portable.TicketBackend = " GITHUB "
	portable.WorktreeMode = " GIT "
	result, err := s.InsertBoardAggregate(ctx, portable)
	if err != nil {
		t.Fatal(err)
	}
	if result.Board.Name != "Imported Board" || result.Board.Workdir != "/path/that/does/not/exist" || result.Board.TicketBackend != "github" || result.Board.WorktreeMode != WorktreeModeGit {
		t.Fatalf("aggregate board was not normalized portably: %+v", result.Board)
	}
	if err := s.SetBoardWorktreeMode(ctx, result.Board.ID, " "); err == nil {
		t.Fatal("SetBoardWorktreeMode accepted blank mode")
	}
}

func validAggregate(title, harness string) BoardAggregateInsert {
	now := time.Now().UTC()
	return BoardAggregateInsert{
		Name:          "Imported",
		Workdir:       " /portable/workdir ",
		TicketBackend: "",
		WorktreeMode:  "",
		Columns:       []AggregateColumn{{SourceID: 1, Name: "Open", WorkflowKey: "Open", Position: 0}},
		Tickets: []AggregateTicket{{
			SourceID: 1, ColumnSourceID: 1, DisplayID: "T-001", DisplayNumber: 1,
			Title: title, Body: " body ", Harness: harness, Position: 0, CreatedAt: now, UpdatedAt: now,
		}},
	}
}

func TestBoardValidationIsSharedAcrossCreateAndImport(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	created, err := s.CreateBoardWithOptions(ctx, CreateBoardOptions{
		Name:          " Created Board ",
		Workdir:       t.TempDir(),
		TicketBackend: " GITHUB ",
		WorktreeMode:  " GIT ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Created Board" || created.TicketBackend != "github" || created.WorktreeMode != WorktreeModeGit {
		t.Fatalf("CreateBoardWithOptions did not normalize: %+v", created)
	}
	if _, err := s.CreateBoardWithOptions(ctx, CreateBoardOptions{Name: " ", Workdir: t.TempDir()}); err == nil {
		t.Fatal("CreateBoardWithOptions accepted blank name")
	}
	if _, err := s.CreateBoardWithOptions(ctx, CreateBoardOptions{Name: "Bad Backend", Workdir: t.TempDir(), TicketBackend: "unsupported"}); err == nil {
		t.Fatal("CreateBoardWithOptions accepted unsupported backend")
	}
	if _, err := s.CreateBoardWithOptions(ctx, CreateBoardOptions{Name: "Bad Mode", Workdir: t.TempDir(), WorktreeMode: "unsupported"}); err == nil {
		t.Fatal("CreateBoardWithOptions accepted unsupported worktree mode")
	}
	if _, err := s.InsertBoardAggregate(ctx, BoardAggregateInsert{Name: " ", TicketBackend: "local", WorktreeMode: WorktreeModeOff}); err == nil {
		t.Fatal("InsertBoardAggregate accepted blank name")
	}
	if _, err := s.InsertBoardAggregate(ctx, BoardAggregateInsert{Name: "Bad Imported Backend", TicketBackend: "unsupported", WorktreeMode: WorktreeModeOff}); err == nil {
		t.Fatal("InsertBoardAggregate accepted unsupported backend")
	}
	if _, err := s.InsertBoardAggregate(ctx, BoardAggregateInsert{Name: "Bad Imported Mode", TicketBackend: "local", WorktreeMode: "unsupported"}); err == nil {
		t.Fatal("InsertBoardAggregate accepted unsupported worktree mode")
	}
}
