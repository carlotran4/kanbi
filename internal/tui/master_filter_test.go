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
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlL})
	if model.masterFilterOpen || !model.masterFilter.Empty() {
		t.Fatalf("clear should close modal and reset filters")
	}
	if got := tuiTicketTitles(model); len(got) != 2 {
		t.Fatalf("clear did not restore all tickets titles=%v\n%s", got, model.View())
	}
}

func TestMasterFilterEscapeDiscardsDraftChanges(t *testing.T) {
	store, ctx := newTestStore(t)
	client, _ := store.CreateBoard(ctx, "Client B")
	defaultView := defaultBoardView(t, ctx, store)
	clientView, _ := store.BoardViewByID(ctx, client.ID)
	_, _ = store.CreateTicket(ctx, defaultView.Columns[0].ID, "Default task", "", "pi")
	_, _ = store.CreateTicket(ctx, clientView.Columns[0].ID, "Codex handoff", "", "codex")

	model := New(ctx, NewService(store, nil))
	model.masterBoard = true
	model.masterFilter.BoardIDs = []int64{defaultView.Board.ID, client.ID}
	model.masterFilter.Search = "Default"
	model.reloadBoards()
	model.reload()

	model, _ = mustUpdate(t, model, "f")
	model, _ = mustUpdate(t, model, "x")
	defaultBoardIndex := -1
	for i, board := range model.boards {
		if board.ID == defaultView.Board.ID {
			defaultBoardIndex = i
			break
		}
	}
	if defaultBoardIndex < 0 {
		t.Fatal("default board missing from filter options")
	}
	model.masterFilterField = 2 + defaultBoardIndex
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if model.masterFilter.Search != "Default" {
		t.Fatalf("editing modal mutated applied search before Enter: %q", model.masterFilter.Search)
	}
	if len(model.masterFilter.BoardIDs) != 2 || model.masterFilter.BoardIDs[0] != defaultView.Board.ID || model.masterFilter.BoardIDs[1] != client.ID {
		t.Fatalf("editing modal mutated applied board selection before Enter: %v", model.masterFilter.BoardIDs)
	}
	if got := tuiTicketTitles(model); len(got) != 1 || got[0] != "Default task" {
		t.Fatalf("editing modal reloaded applied results before Enter: %v", got)
	}

	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.masterFilterOpen {
		t.Fatal("Escape should close filter modal")
	}
	if model.masterFilter.Search != "Default" {
		t.Fatalf("Escape should preserve applied search, got %q", model.masterFilter.Search)
	}
	if got := tuiTicketTitles(model); len(got) != 1 || got[0] != "Default task" {
		t.Fatalf("Escape should preserve applied results: %v", got)
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
	model.masterFilterDraft.BoardIDs = []int64{client.ID}
	model.masterFilterDraft.Harnesses = []string{"codex"}
	model.masterFilterDraft.Runtimes = []string{"running"}
	model.masterFilterDraft.IncludeArchived = true
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if got := tuiTicketTitles(model); len(got) != 1 || got[0] != "Active codex" {
		t.Fatalf("runtime filter should include active running only titles=%v\n%s", got, model.View())
	}
	if !strings.Contains(model.View(), "boards=Client B") || !strings.Contains(model.View(), "harness=codex") || !strings.Contains(model.View(), "runtime=running") || !strings.Contains(model.View(), "archived") {
		t.Fatalf("active filter summary missing:\n%s", model.View())
	}
}

func TestMasterFilterPresetSaveApplyDelete(t *testing.T) {
	store, ctx := newTestStore(t)
	client, _ := store.CreateBoard(ctx, "Client B")
	clientView, _ := store.BoardViewByID(ctx, client.ID)
	_, _ = store.CreateTicket(ctx, clientView.Columns[0].ID, "Codex handoff", "", "codex")

	model := New(ctx, NewService(store, nil))
	model.masterBoard = true
	model.reloadBoards()
	model.reload()
	model, _ = mustUpdate(t, model, "f")
	model.masterFilterDraft.Search = "codex"
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlS})
	if model.filterPresetMode != "save" {
		t.Fatalf("expected filter preset save mode, got %q status=%s", model.filterPresetMode, model.status)
	}
	model, _ = mustUpdate(t, model, "codex-only")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	presets, err := store.ListFilterPresets(ctx)
	if err != nil || len(presets) != 1 || presets[0].Name != "codex-only" {
		t.Fatalf("presets=%+v err=%v", presets, err)
	}
	if !model.masterFilter.Empty() || model.activePresetName != "" {
		t.Fatalf("saving draft should not apply it: filter=%+v preset=%q", model.masterFilter, model.activePresetName)
	}
	// Clear and re-apply.
	model, _ = mustUpdate(t, model, "C")
	model, _ = mustUpdate(t, model, "f")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlP})
	if model.filterPresetMode != "list" {
		t.Fatalf("expected preset list mode status=%s mode=%q", model.status, model.filterPresetMode)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.masterFilter.Search != "codex" {
		t.Fatalf("preset apply search=%q", model.masterFilter.Search)
	}
}

func TestBoardPickerArchiveAndSyncToggle(t *testing.T) {
	store, ctx := newTestStore(t)
	board, err := store.CreateBoard(ctx, "Ops")
	if err != nil {
		t.Fatal(err)
	}
	model := NewWithPicker(ctx, NewService(store, nil))
	model.firstRun = false
	// Startup picker: select real board index 1 (0 is Master).
	model.boardIndex = 1
	if len(model.boards) == 0 {
		model.reloadBoards()
	}
	// Find Ops index
	for i, b := range model.boards {
		if b.ID == board.ID {
			model.boardIndex = i + 1
		}
	}
	model, _ = mustUpdate(t, model, "s")
	reloaded, err := store.BoardByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	// toggled from default enabled
	if reloaded.SyncEnabled {
		// default was enabled; toggle should disable
		t.Fatalf("expected sync disabled after toggle: %+v status=%s", reloaded, model.status)
	}
	model, _ = mustUpdate(t, model, "a")
	reloaded, err = store.BoardByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.ArchivedAt.Valid {
		t.Fatalf("expected archive: %+v status=%s", reloaded, model.status)
	}
	if reloaded.SyncEnabled {
		t.Fatalf("archive must force sync off: %+v", reloaded)
	}
}

func TestBoardPickerArchivingCurrentBoardSwitchesToMaster(t *testing.T) {
	store, ctx := newTestStore(t)
	board, err := store.CreateBoard(ctx, "Ops")
	if err != nil {
		t.Fatal(err)
	}
	model := New(ctx, NewService(store, nil))
	model.firstRun = false

	model, _ = mustUpdate(t, model, "b")
	for i, candidate := range model.boards {
		if candidate.ID == board.ID {
			model.boardIndex = i + 1
			break
		}
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.masterBoard || model.boardID != board.ID || model.view.Board.ID != board.ID {
		t.Fatalf("did not select Ops board: master=%v boardID=%d view=%+v", model.masterBoard, model.boardID, model.view.Board)
	}

	model, _ = mustUpdate(t, model, "b")
	model, _ = mustUpdate(t, model, "a")
	reloaded, err := store.BoardByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.ArchivedAt.Valid {
		t.Fatalf("expected archived board: %+v", reloaded)
	}
	if !model.masterBoard || model.boardID != 0 || model.view.Board.Name != "Master" {
		t.Fatalf("archiving current board must switch to Master: master=%v boardID=%d view=%+v", model.masterBoard, model.boardID, model.view.Board)
	}

	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.boardPicker || model.view.Board.Name != "Master" {
		t.Fatalf("closing picker must retain Master after archiving current board:\n%s", model.View())
	}
}

func TestMasterCreateUsesWorkflowKey(t *testing.T) {
	store, ctx := newTestStore(t)
	board, err := store.CreateBoard(ctx, "Client")
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Rename display while keeping default key equal to original name of first default column Quiet.
	col := view.Columns[0]
	origKey := col.WorkflowKey
	if err := store.RenameColumn(ctx, col.ID, "Renamed Open"); err != nil {
		t.Fatal(err)
	}
	id, err := store.ColumnIDByBoardAndWorkflowKey(ctx, board.ID, origKey)
	if err != nil || id != col.ID {
		t.Fatalf("workflow key lookup failed id=%d err=%v", id, err)
	}
	// Direct API path used by Master create must hit this method.
	ticket, err := NewService(store, nil).CreateTicket(ctx, id, "From key", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.ColumnID != col.ID {
		t.Fatalf("ticket column=%d want %d", ticket.ColumnID, col.ID)
	}
}
