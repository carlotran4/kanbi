package storage

import "testing"

func TestMasterBoardViewWithFilterMatchesBoardRuntimeHarnessSearchAndArchive(t *testing.T) {
	s, ctx := newTestStore(t)
	defaultView := defaultBoardView(t, ctx, s)
	client, err := s.CreateBoard(ctx, "Client B")
	if err != nil {
		t.Fatal(err)
	}
	clientView := boardViewByID(t, ctx, s, client.ID)

	defaultTicket, _ := s.CreateTicket(ctx, defaultView.Columns[0].ID, "Default API", "plain body", "pi")
	clientTicket, _ := s.CreateTicket(ctx, clientView.Columns[0].ID, "Codex handoff", "needs review", "codex")
	archivedTicket, _ := s.CreateTicket(ctx, clientView.Columns[0].ID, "Archived note", "hidden", "copilot")
	_ = s.ArchiveTicket(ctx, archivedTicket.ID)
	_, _ = s.UpsertActiveSession(ctx, clientTicket.ID, Session{Harness: "codex", TmuxSessionName: "ak", TmuxWindowName: "T-001-codex", Status: "running"})

	view, err := s.MasterBoardViewWithFilter(ctx, MasterFilter{BoardIDs: []int64{client.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ticketTitles(view); len(got) != 1 || got[0] != "Codex handoff" {
		t.Fatalf("board filter titles=%v", got)
	}

	view, _ = s.MasterBoardViewWithFilter(ctx, MasterFilter{Runtimes: []string{"not_started"}})
	if got := ticketTitles(view); len(got) != 1 || got[0] != defaultTicket.Title {
		t.Fatalf("runtime filter titles=%v", got)
	}

	view, _ = s.MasterBoardViewWithFilter(ctx, MasterFilter{Harnesses: []string{"codex"}})
	if got := ticketTitles(view); len(got) != 1 || got[0] != clientTicket.Title {
		t.Fatalf("harness filter titles=%v", got)
	}

	view, _ = s.MasterBoardViewWithFilter(ctx, MasterFilter{Search: "client b"})
	if got := ticketTitles(view); len(got) != 1 || got[0] != clientTicket.Title {
		t.Fatalf("search board-name titles=%v", got)
	}

	view, _ = s.MasterBoardViewWithFilter(ctx, MasterFilter{IncludeArchived: true, Search: "archived"})
	if got := ticketTitles(view); len(got) != 1 || got[0] != archivedTicket.Title {
		t.Fatalf("archived search titles=%v", got)
	}
}

func ticketTitles(view BoardView) []string {
	var titles []string
	for _, col := range view.Columns {
		for _, ticket := range col.Tickets {
			titles = append(titles, ticket.Title)
		}
	}
	return titles
}
