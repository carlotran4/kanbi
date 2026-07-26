package tui

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestActiveFileCompletionQueryRecognizesCursorBoundedAtReference(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		cursor int
		want   fileCompletionQuery
		found  bool
	}{
		{name: "at start", value: "@internal/tu", cursor: len([]rune("@internal/tu")), want: fileCompletionQuery{start: 0, end: 12, text: "internal/tu"}, found: true},
		{name: "after whitespace", value: "Please edit @cmd/ka", cursor: len([]rune("Please edit @cmd/ka")), want: fileCompletionQuery{start: 12, end: 19, text: "cmd/ka"}, found: true},
		{name: "current line only", value: "first\n@internal", cursor: len([]rune("first\n@internal")), want: fileCompletionQuery{start: 6, end: 15, text: "internal"}, found: true},
		{name: "email is not reference", value: "a@b", cursor: 3, found: false},
		{name: "reference ended by whitespace", value: "@cmd next", cursor: 9, found: false},
		{name: "cursor after plain text", value: "plain", cursor: 5, found: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := activeFileCompletionQuery(tt.value, tt.cursor)
			if found != tt.found {
				t.Fatalf("found = %v, want %v", found, tt.found)
			}
			if got != tt.want {
				t.Fatalf("query = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFilterFileCompletionCandidatesRanksDeterministically(t *testing.T) {
	paths := []string{
		"internal/tui/model.go",
		"cmd/kanbi/main.go",
		"internal/tui/edit_modal.go",
		"internal/storage/models.go",
	}
	got := filterFileCompletionCandidates(paths, "mod")
	want := []string{"internal/tui/model.go", "internal/storage/models.go", "internal/tui/edit_modal.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}

func TestScanProjectFilesIgnoresOnlyExplicitHeavyDirectories(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"cmd/kanbi/main.go",
		".config/keep.txt",
		".git/config",
		"node_modules/pkg/index.js",
		"dist/app.js",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanProjectFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".config/keep.txt", "cmd/kanbi/main.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestScanProjectFilesOmitsTerminalControlCharacters(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "safe.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad\x1b[2J.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := scanProjectFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"safe.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestFileCompletionPrefersExistingWorkspaceLaunchDirectory(t *testing.T) {
	workspace := t.TempDir()
	fallback := t.TempDir()
	ticket := storage.Ticket{
		WorkspaceLaunchCWD: sql.NullString{String: workspace, Valid: true},
		BoardWorkdir:       fallback,
	}
	if got := fileCompletionRoot(ticket); got != workspace {
		t.Fatalf("root = %q, want workspace %q", got, workspace)
	}
	ticket.WorkspaceLaunchCWD = sql.NullString{String: filepath.Join(workspace, "missing"), Valid: true}
	if got := fileCompletionRoot(ticket); got != fallback {
		t.Fatalf("fallback root = %q, want %q", got, fallback)
	}

	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(fallback, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ticket.BoardWorkdir = link
	if got := fileCompletionRoot(ticket); got != fallback {
		t.Fatalf("symlink root = %q, want resolved %q", got, fallback)
	}
}

func TestBodyTypingScansProjectAndInsertsSelectedRelativePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "internal", "tui", "model.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package tui"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if err := store.SetBoardWorkdir(ctx, view.Board.ID, root); err != nil {
		t.Fatal(err)
	}
	createTicket(t, ctx, store, view.Columns[0].ID, "Reference", "@model", "pi")

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	cmd := model.refreshFileCompletion(fileCompletionBody)
	if cmd == nil || !model.fileCompletion.loading {
		t.Fatal("typing a reference should begin an asynchronous project scan")
	}
	next, _ := model.Update(cmd())
	model = next.(Model)
	if got, want := model.fileCompletion.candidates, []string{"internal/tui/model.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if got, want := model.bodyTA.Value(), "@internal/tui/model.go"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestBodyFileCompletionInsertsSelectedRelativePathAndDismisses(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "Reference", "before @cmd/ka after", "pi")

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	setTextareaCursorOffset(&model.bodyTA, len([]rune("before @cmd/ka")))
	query, ok := activeFileCompletionQuery(model.bodyTA.Value(), textareaCursorOffset(model.bodyTA))
	if !ok {
		t.Fatal("expected active body reference")
	}
	model.fileCompletion = fileCompletionState{
		target: fileCompletionBody, ticketID: model.editTicket.ID, request: 1, query: query,
		candidates: []string{"cmd/kanbi/main.go"},
	}

	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if got, want := model.bodyTA.Value(), "before @cmd/kanbi/main.go after"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if model.fileCompletion.target != "" {
		t.Fatalf("completion should dismiss: %#v", model.fileCompletion)
	}
}

func TestFileCompletionNavigationAndDismissalDoNotEditText(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "Reference", "@cmd", "pi")
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	query, _ := activeFileCompletionQuery(model.bodyTA.Value(), textareaCursorOffset(model.bodyTA))
	model.fileCompletion = fileCompletionState{target: fileCompletionBody, ticketID: model.editTicket.ID, query: query, candidates: []string{"cmd/a.go", "cmd/b.go"}}

	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.fileCompletion.selected != 1 {
		t.Fatalf("selected = %d, want 1", model.fileCompletion.selected)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if !model.editing || model.fileCompletion.target != "" || model.bodyTA.Value() != "@cmd" {
		t.Fatalf("escape should only dismiss completion: editing=%v completion=%#v body=%q", model.editing, model.fileCompletion, model.bodyTA.Value())
	}
}

func TestNoteFileCompletionInsertsSelectedRelativePath(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "Reference", "", "pi")

	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	for range 3 {
		model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	}
	model, _ = mustUpdate(t, model, "a")
	model.noteTA.SetValue("@internal/tu")
	query, ok := activeFileCompletionQuery(model.noteTA.Value(), textareaCursorOffset(model.noteTA))
	if !ok {
		t.Fatal("expected active note reference")
	}
	model.fileCompletion = fileCompletionState{
		target: fileCompletionNote, ticketID: model.editTicket.ID, request: 1, query: query,
		candidates: []string{"internal/tui/model.go"},
	}

	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	if got, want := model.noteTA.Value(), "@internal/tui/model.go"; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
}

func TestFileCompletionIgnoresStaleResults(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "Reference", "@cmd", "pi")
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	query, ok := activeFileCompletionQuery(model.bodyTA.Value(), textareaCursorOffset(model.bodyTA))
	if !ok {
		t.Fatal("expected active reference")
	}
	model.fileCompletion = fileCompletionState{target: fileCompletionBody, ticketID: model.editTicket.ID, request: 2, query: query, loading: true}

	next, _ := model.Update(fileCompletionResultMsg{target: fileCompletionBody, ticketID: model.editTicket.ID, request: 1, paths: []string{"cmd/kanbi/main.go"}})
	model = next.(Model)
	if len(model.fileCompletion.candidates) != 0 || !model.fileCompletion.loading {
		t.Fatalf("stale result changed completion: %#v", model.fileCompletion)
	}
}

func TestNoteCompletionDismissRestoresTextareaHeight(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "Reference", "", "pi")
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	for range 3 {
		model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	}
	model, _ = mustUpdate(t, model, "a")
	model.noteTA.InsertString("@")
	_ = model.refreshFileCompletion(fileCompletionNote)
	if got := model.noteTA.Height(); got != 3 {
		t.Fatalf("completion note height = %d, want 3", got)
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if got := model.noteTA.Height(); got != 6 {
		t.Fatalf("dismissed note height = %d, want 6", got)
	}
}

func TestFileCompletionPathRenderingUsesTerminalCellWidth(t *testing.T) {
	got := trimFileCompletionPath("界界界.go", 5)
	if width := lipgloss.Width(got); width > 5 {
		t.Fatalf("rendered path %q is %d cells, want <= 5", got, width)
	}
}

func TestFileCompletionReusesOneScanWhileQueryChanges(t *testing.T) {
	root := t.TempDir()
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	if err := store.SetBoardWorkdir(ctx, view.Board.ID, root); err != nil {
		t.Fatal(err)
	}
	createTicket(t, ctx, store, view.Columns[0].ID, "Reference", "@", "pi")
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	if cmd := model.refreshFileCompletion(fileCompletionBody); cmd == nil {
		t.Fatal("initial query should start a scan")
	}
	request := model.fileCompletion.request
	model.fileCompletion.loading = false
	model.fileCompletion.paths = []string{"cmd/kanbi/main.go", "internal/tui/model.go"}
	model.bodyTA.InsertString("mod")
	if cmd := model.refreshFileCompletion(fileCompletionBody); cmd != nil {
		t.Fatal("query changes should filter the existing index without rescanning")
	}
	if model.fileCompletion.request != request {
		t.Fatalf("request changed from %d to %d", request, model.fileCompletion.request)
	}
	if got, want := model.fileCompletion.candidates, []string{"internal/tui/model.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}

func TestModelCtrlEOpensExternalEditorFromBody(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket := createTicket(t, ctx, store, view.Columns[0].ID, "Editor", "body", "pi")
	model := New(ctx, NewService(store, nil))
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model, cmd := mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlE})
	if cmd == nil || model.editorTicketID != ticket.ID {
		t.Fatalf("ctrl+e did not open body editor: cmd=%v ticket=%d", cmd != nil, model.editorTicketID)
	}
}
