package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestMasterFilterModalAppliesSearchAndClear(t *testing.T) {
	store, ctx := newTestStore(t)
	client, _ := store.CreateBoard(ctx, "Client B")
	defaultView := defaultBoardView(t, ctx, store)
	clientView, _ := store.BoardViewByID(ctx, client.ID)
	_, _ = store.CreateTicket(ctx, defaultView.Columns[0].ID, "Default task", "", "pi")
	_, _ = store.CreateTicket(ctx, clientView.Columns[0].ID, "Codex handoff", "", "codex")

	model := New(ctx, NewService(store, nil))
	model.masterBoard = true
	model.reloadBoards()
	model.reload()
	if got := tuiTicketTitles(model); len(got) != 2 {
		t.Fatalf("master seed missing titles=%v\n%s", got, model.View())
	}

	model, _ = mustUpdate(t, model, "f")
	if !model.masterFilterOpen || !strings.Contains(model.View(), "Master filters") {
		t.Fatalf("filter modal not shown:\n%s", model.View())
	}
	model, _ = mustUpdate(t, model, "codex")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.View(), "filter:") || !strings.Contains(model.View(), "search=codex") {
		t.Fatalf("active filter not visible:\n%s", model.View())
	}
	if got := tuiTicketTitles(model); len(got) != 1 || got[0] != "Codex handoff" {
		t.Fatalf("search filter not applied titles=%v\n%s", got, model.View())
	}

	model, _ = mustUpdate(t, model, "f")
	model, _ = mustUpdate(t, model, "C")
	if model.masterFilterOpen || !model.masterFilter.Empty() {
		t.Fatalf("clear should close modal and reset filters")
	}
	if got := tuiTicketTitles(model); len(got) != 2 {
		t.Fatalf("clear did not restore all tickets titles=%v\n%s", got, model.View())
	}
}

func tuiTicketTitles(model Model) []string {
	var titles []string
	for _, col := range model.view.Columns {
		for _, ticket := range col.Tickets {
			titles = append(titles, ticket.Title)
		}
	}
	return titles
}

func TestMasterFilterCanToggleBoardHarnessRuntimeAndArchived(t *testing.T) {
	store, ctx := newTestStore(t)
	client, _ := store.CreateBoard(ctx, "Client B")
	clientView, _ := store.BoardViewByID(ctx, client.ID)
	active, _ := store.CreateTicket(ctx, clientView.Columns[0].ID, "Active codex", "", "codex")
	archived, _ := store.CreateTicket(ctx, clientView.Columns[0].ID, "Archived codex", "", "codex")
	_ = store.ArchiveTicket(ctx, archived.ID)
	_, _ = store.UpsertActiveSession(ctx, active.ID, storage.Session{Harness: "codex", TmuxSessionName: "ak", TmuxWindowName: "T-001-active", Status: "running"})

	model := New(ctx, NewService(store, nil))
	model.masterBoard = true
	model.reloadBoards()
	model.reload()
	model, _ = mustUpdate(t, model, "f")
	model.masterFilter.BoardIDs = []int64{client.ID}
	model.masterFilter.Harnesses = []string{"codex"}
	model.masterFilter.Runtimes = []string{"running"}
	model.masterFilter.IncludeArchived = true
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if got := tuiTicketTitles(model); len(got) != 1 || got[0] != "Active codex" {
		t.Fatalf("runtime filter should include active running only titles=%v\n%s", got, model.View())
	}
	if !strings.Contains(model.View(), "boards=Client B") || !strings.Contains(model.View(), "harness=codex") || !strings.Contains(model.View(), "runtime=running") || !strings.Contains(model.View(), "archived") {
		t.Fatalf("active filter summary missing:\n%s", model.View())
	}
}
