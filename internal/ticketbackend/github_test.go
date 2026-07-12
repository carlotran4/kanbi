package ticketbackend

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

type fakeGitHubClient struct {
	issues          []GitHubIssue
	comments        map[int][]GitHubComment
	createdIssues   []GitHubIssueUpdate
	updatedIssues   []GitHubIssueUpdate
	createdComments []string
	updatedComments []string
}

func (f *fakeGitHubClient) ListIssues(context.Context, GitHubConfig) ([]GitHubIssue, error) {
	return append([]GitHubIssue(nil), f.issues...), nil
}
func (f *fakeGitHubClient) ListComments(_ context.Context, _ GitHubConfig, n int) ([]GitHubComment, error) {
	return append([]GitHubComment(nil), f.comments[n]...), nil
}
func (f *fakeGitHubClient) CreateIssue(_ context.Context, _ GitHubConfig, u GitHubIssueUpdate) (GitHubIssue, error) {
	f.createdIssues = append(f.createdIssues, u)
	n := 1000 + len(f.createdIssues)
	issue := GitHubIssue{Number: n, HTMLURL: "url", UpdatedAt: time.Now().UTC()}
	if u.Title != nil {
		issue.Title = *u.Title
	}
	if u.Body != nil {
		issue.Body = *u.Body
	}
	if u.State != nil {
		issue.State = *u.State
	}
	for _, l := range u.Labels {
		issue.Labels = append(issue.Labels, GitHubLabel{Name: l})
	}
	f.issues = append(f.issues, issue)
	return issue, nil
}

func (f *fakeGitHubClient) UpdateIssue(_ context.Context, _ GitHubConfig, n int, u GitHubIssueUpdate) (GitHubIssue, error) {
	f.updatedIssues = append(f.updatedIssues, u)
	for i := range f.issues {
		if f.issues[i].Number == n {
			if u.Title != nil {
				f.issues[i].Title = *u.Title
			}
			if u.Body != nil {
				f.issues[i].Body = *u.Body
			}
			if u.State != nil {
				f.issues[i].State = *u.State
			}
			f.issues[i].Labels = nil
			for _, l := range u.Labels {
				f.issues[i].Labels = append(f.issues[i].Labels, GitHubLabel{Name: l})
			}
			f.issues[i].UpdatedAt = time.Now().UTC().Add(time.Hour)
			return f.issues[i], nil
		}
	}
	return GitHubIssue{}, nil
}
func (f *fakeGitHubClient) CreateComment(_ context.Context, _ GitHubConfig, n int, body string) (GitHubComment, error) {
	f.createdComments = append(f.createdComments, body)
	c := GitHubComment{ID: int64(100 + len(f.createdComments)), Body: body, UpdatedAt: time.Now().UTC()}
	f.comments[n] = append(f.comments[n], c)
	return c, nil
}
func (f *fakeGitHubClient) UpdateComment(_ context.Context, _ GitHubConfig, id int64, body string) (GitHubComment, error) {
	f.updatedComments = append(f.updatedComments, body)
	return GitHubComment{ID: id, Body: body, UpdatedAt: time.Now().UTC()}, nil
}

func TestGitHubSyncPullsIssuesColumnsAndComments(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	remoteUpdated := time.Now().UTC().Add(-time.Hour)
	client := &fakeGitHubClient{
		issues:   []GitHubIssue{{Number: 42, HTMLURL: "https://github.com/acme/proj/issues/42", Title: "Remote title", Body: "Remote body", State: "open", Labels: []GitHubLabel{{Name: "needs-review"}}, UpdatedAt: remoteUpdated}},
		comments: map[int][]GitHubComment{42: {{ID: 9, Body: "remote comment", UpdatedAt: remoteUpdated}}},
	}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 1 {
		t.Fatalf("pulled=%d, want 1", res.Pulled)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, col := range view.Columns {
		if col.Name == "Review" && len(col.Tickets) == 1 {
			found = true
			if col.Tickets[0].DisplayID != "GH-42" || col.Tickets[0].ExternalID.String != "42" {
				t.Fatalf("unexpected ticket: %+v", col.Tickets[0])
			}
			notes, err := store.ListNotes(ctx, col.Tickets[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(notes) != 1 || notes[0].Body != "remote comment" || notes[0].ExternalID.String != "9" {
				t.Fatalf("unexpected notes: %+v", notes)
			}
		}
	}
	if !found {
		t.Fatalf("remote issue not projected in Review column: %+v", view.Columns)
	}
}

func TestGitHubSyncPushesLocalNewerTicketAndNotes(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	oldRemote := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeGitHubClient{
		issues:   []GitHubIssue{{Number: 7, HTMLURL: "url", Title: "old", Body: "old", State: "open", Labels: []GitHubLabel{{Name: "kanbi"}, {Name: "status:Open"}}, UpdatedAt: oldRemote}},
		comments: map[int][]GitHubComment{},
	}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, err := store.TicketByDisplayIDInBoard(ctx, "GH-7", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTicket(ctx, ticket.ID, "local title", "local body", ticket.Harness); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddNote(ctx, ticket.ID, "local note"); err != nil {
		t.Fatal(err)
	}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 1 || len(client.updatedIssues) != 1 {
		t.Fatalf("pushed=%d updated=%d", res.Pushed, len(client.updatedIssues))
	}
	if got := *client.updatedIssues[0].Title; got != "local title" {
		t.Fatalf("pushed title=%q", got)
	}
	if len(client.createdComments) != 1 || client.createdComments[0] != "local note" {
		t.Fatalf("created comments: %#v", client.createdComments)
	}
	updated, err := store.TicketByDisplayIDInBoard(ctx, "GH-7", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ExternalUpdatedAt.Time.Before(oldRemote) || strings.TrimSpace(updated.ExternalID.String) == "" {
		t.Fatalf("external sync fields not refreshed: %+v", updated)
	}
}

func TestGitHubUpdateFromLocalUsesNativeWorkflowLabels(t *testing.T) {
	cfg, err := ParseGitHubConfig(`{"owner":"acme","repo":"proj"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	update := githubUpdateFromLocal(cfg, storage.Ticket{Title: "t", Body: "b"}, "Review", []GitHubLabel{{Name: "kanbi"}, {Name: "status:Open"}, {Name: "bug"}})
	if *update.State != "open" {
		t.Fatalf("state=%s", *update.State)
	}
	joined := strings.Join(update.Labels, ",")
	if strings.Contains(joined, "status:") || !strings.Contains(joined, "needs-review") || !strings.Contains(joined, "bug") || !strings.Contains(joined, "kanbi") {
		t.Fatalf("labels=%v", update.Labels)
	}
}

func TestGitHubUpdateFromLocalClosesTerminalColumns(t *testing.T) {
	cfg, err := ParseGitHubConfig(`{"owner":"acme","repo":"proj"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	update := githubUpdateFromLocal(cfg, storage.Ticket{Title: "t", Body: "b"}, "Done", []GitHubLabel{{Name: "kanbi"}, {Name: "needs-review"}, {Name: "status:Done"}})
	if *update.State != "closed" {
		t.Fatalf("state=%s", *update.State)
	}
	joined := strings.Join(update.Labels, ",")
	if strings.Contains(joined, "needs-review") || strings.Contains(joined, "status:") || !strings.Contains(joined, "kanbi") {
		t.Fatalf("labels=%v", update.Labels)
	}
}

func TestGitHubSyncCreatesRemoteIssueForLocalTicket(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	local, err := store.CreateTicket(ctx, view.Columns[0].ID, "local only", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeGitHubClient{comments: map[int][]GitHubComment{}}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 1 || len(client.createdIssues) != 1 {
		t.Fatalf("pushed=%d created=%d", res.Pushed, len(client.createdIssues))
	}
	if got := *client.createdIssues[0].Title; got != "local only" {
		t.Fatalf("created title=%q", got)
	}
	created, err := store.TicketByDisplayIDInBoard(ctx, "GH-1001", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != local.ID || !created.ExternalID.Valid || created.ExternalID.String != "1001" {
		t.Fatalf("created ticket not linked to local placeholder: id=%d external=%+v local_id=%d", created.ID, created.ExternalID, local.ID)
	}
	if _, err := store.TicketByDisplayIDInBoard(ctx, local.DisplayID, board.ID); err == nil {
		t.Fatalf("local placeholder %s should have been converted to GH display id", local.DisplayID)
	}

	res, err = (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.createdIssues) != 1 || res.Pushed != 0 {
		t.Fatalf("second sync created duplicate issue: pushed=%d created=%d", res.Pushed, len(client.createdIssues))
	}
	view, err = store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Columns[0].Tickets) != 1 || view.Columns[0].Tickets[0].DisplayID != "GH-1001" {
		t.Fatalf("tickets after sync = %+v", view.Columns[0].Tickets)
	}
}

func TestParseGitHubConfigFallsBackToGHToken(t *testing.T) {
	t.Setenv("KANBI_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	binDir := t.TempDir()
	gh := filepath.Join(binDir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nif [ \"$1\" = auth ] && [ \"$2\" = token ]; then echo gh-token; exit 0; fi\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	cfg, err := ParseGitHubConfig(`{"owner":"o","repo":"r"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "gh-token" {
		t.Fatalf("token=%q", cfg.Token)
	}
}

func TestParseGitHubConfigAppliesQuerySurface(t *testing.T) {
	cfg, err := ParseGitHubConfig(`{"owner":"o","repo":"r","column_label_prefix":"flow:"}`, "state=open&labels=bug,triage&assignee=octo&since=2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Owner != "o" || cfg.Repo != "r" || cfg.ColumnLabelPrefix != "flow:" || cfg.Assignee != "octo" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if len(cfg.States) != 1 || cfg.States[0] != "open" || len(cfg.Labels) != 2 || cfg.Labels[1] != "triage" || cfg.ClosedColumn != "Done" {
		t.Fatalf("query not applied: %+v", cfg)
	}
}

func newTestStore(t *testing.T, ctx context.Context) *storage.Store {
	t.Helper()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestGitHubSyncPullsClosedIssueIntoDoneWithoutArchiving(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	closedAt := time.Now().UTC().Add(-time.Hour)
	client := &fakeGitHubClient{issues: []GitHubIssue{{Number: 13, HTMLURL: "url", Title: "closed", Body: "body", State: "closed", UpdatedAt: closedAt, ClosedAt: &closedAt}}, comments: map[int][]GitHubComment{}}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 1 {
		t.Fatalf("pulled=%d", res.Pulled)
	}
	ticket, err := store.TicketByDisplayIDInBoard(ctx, "GH-13", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.ArchivedAt.Valid {
		t.Fatalf("closed GitHub issue should remain visible, got archived_at=%v", ticket.ArchivedAt.Time)
	}
	view, _ := store.BoardViewByID(ctx, board.ID)
	if !columnHasTicket(view, "Done", "GH-13") {
		t.Fatalf("closed issue not visible in Done: %+v", view.Columns)
	}
}

func TestGitHubSyncRepairsPreviouslyArchivedClosedDoneIssue(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	closedAt := time.Now().UTC().Add(-time.Hour)
	client := &fakeGitHubClient{issues: []GitHubIssue{{Number: 14, HTMLURL: "url", Title: "closed", Body: "body", State: "closed", UpdatedAt: closedAt, ClosedAt: &closedAt}}, comments: map[int][]GitHubComment{}}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, err := store.TicketByDisplayIDInBoard(ctx, "GH-14", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRemoteTicket(ctx, storage.RemoteTicket{BoardID: board.ID, ColumnID: ticket.ColumnID, ExternalID: "14", ExternalURL: "url", ExternalUpdatedAt: closedAt, DisplayID: "GH-14", DisplayNumber: 14, Title: "closed", Body: "body", ArchivedAt: &closedAt}); err != nil {
		t.Fatal(err)
	}
	ticket, _ = store.TicketByDisplayIDInBoard(ctx, "GH-14", board.ID)
	if !ticket.ArchivedAt.Valid {
		t.Fatal("test setup did not archive ticket")
	}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 1 {
		t.Fatalf("repair pulled=%d", res.Pulled)
	}
	ticket, _ = store.TicketByDisplayIDInBoard(ctx, "GH-14", board.ID)
	if ticket.ArchivedAt.Valid {
		t.Fatalf("closed Done ticket should have been unarchived: %+v", ticket)
	}
}

func TestGitHubSyncPullsRemoteNewerExistingIssueAndReopens(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeGitHubClient{issues: []GitHubIssue{{Number: 8, HTMLURL: "url", Title: "old", Body: "old", State: "closed", UpdatedAt: base, ClosedAt: &base}}, comments: map[int][]GitHubComment{}}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	remoteNewer := time.Now().UTC().Add(time.Hour)
	client.issues[0] = GitHubIssue{Number: 8, HTMLURL: "url", Title: "remote new", Body: "remote body", State: "open", Labels: []GitHubLabel{{Name: "blocked"}}, UpdatedAt: remoteNewer}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 1 {
		t.Fatalf("pulled=%d", res.Pulled)
	}
	ticket, err := store.TicketByDisplayIDInBoard(ctx, "GH-8", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "remote new" || ticket.ArchivedAt.Valid {
		t.Fatalf("remote reopen/title not projected: %+v", ticket)
	}
	view, _ := store.BoardViewByID(ctx, board.ID)
	if !columnHasTicket(view, "Blocked", "GH-8") {
		t.Fatalf("reopened issue not in Blocked column: %+v", view.Columns)
	}
}

func TestGitHubSyncUpdatesExistingCommentWithoutDuplicate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	oldRemote := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeGitHubClient{issues: []GitHubIssue{{Number: 9, HTMLURL: "url", Title: "t", Body: "b", State: "open", UpdatedAt: oldRemote}}, comments: map[int][]GitHubComment{9: {{ID: 99, Body: "old comment", UpdatedAt: oldRemote}}}}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, _ := store.TicketByDisplayIDInBoard(ctx, "GH-9", board.ID)
	notes, _ := store.ListNotes(ctx, ticket.ID)
	if err := store.UpdateNote(ctx, notes[0].ID, "local edited comment"); err != nil {
		t.Fatal(err)
	}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 0 || len(client.updatedComments) != 1 || len(client.createdComments) != 0 {
		t.Fatalf("updated=%v created=%v res=%+v", client.updatedComments, client.createdComments, res)
	}
	notes, _ = store.ListNotes(ctx, ticket.ID)
	if len(notes) != 1 || notes[0].Body != "local edited comment" {
		t.Fatalf("duplicate or stale notes: %+v", notes)
	}
}

func TestGitHubSyncLocalArchiveDoesNotPushClosedState(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	oldRemote := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeGitHubClient{issues: []GitHubIssue{{Number: 12, HTMLURL: "url", Title: "t", Body: "b", State: "open", UpdatedAt: oldRemote}}, comments: map[int][]GitHubComment{}}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, _ := store.TicketByDisplayIDInBoard(ctx, "GH-12", board.ID)
	if err := store.ArchiveTicket(ctx, ticket.ID); err != nil {
		t.Fatal(err)
	}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	// Local archive is hide-only; it must not close the remote GitHub issue.
	for _, update := range client.updatedIssues {
		if update.State != nil && *update.State == "closed" {
			t.Fatalf("archive should not push closed state: res=%+v updates=%+v", res, client.updatedIssues)
		}
	}
}

func TestGitHubSyncLocalMoveUpdatesStatusLabelsAndCloses(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	oldRemote := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeGitHubClient{issues: []GitHubIssue{{Number: 10, HTMLURL: "url", Title: "t", Body: "b", State: "open", Labels: []GitHubLabel{{Name: "kanbi"}, {Name: "needs-review"}, {Name: "status:Review"}}, UpdatedAt: oldRemote}}, comments: map[int][]GitHubComment{}}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, _ := store.TicketByDisplayIDInBoard(ctx, "GH-10", board.ID)
	doneID, err := store.ColumnIDByBoardAndName(ctx, board.ID, "Done")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTicket(ctx, ticket.ID, doneID); err != nil {
		t.Fatal(err)
	}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 1 || len(client.updatedIssues) != 1 {
		t.Fatalf("pushed=%d updates=%d", res.Pushed, len(client.updatedIssues))
	}
	update := client.updatedIssues[0]
	if *update.State != "closed" {
		t.Fatalf("state=%s", *update.State)
	}
	labels := strings.Join(update.Labels, ",")
	if strings.Contains(labels, "needs-review") || strings.Contains(labels, "status:") || !strings.Contains(labels, "kanbi") {
		t.Fatalf("unsafe labels: %v", update.Labels)
	}
}

func TestGitHubConflictNewestUpdatedAtWinsForTicketAndComment(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "GitHub", Workdir: t.TempDir(), TicketBackend: KindGitHub, BackendConfig: `{"owner":"acme","repo":"proj"}`})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeGitHubClient{issues: []GitHubIssue{{Number: 11, HTMLURL: "url", Title: "base", Body: "base", State: "open", UpdatedAt: base}}, comments: map[int][]GitHubComment{11: {{ID: 111, Body: "base comment", UpdatedAt: base}}}}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, _ := store.TicketByDisplayIDInBoard(ctx, "GH-11", board.ID)
	notes, _ := store.ListNotes(ctx, ticket.ID)
	if err := store.UpdateTicket(ctx, ticket.ID, "local title", "local body", ticket.Harness); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateNote(ctx, notes[0].ID, "local comment"); err != nil {
		t.Fatal(err)
	}
	client.issues[0].Title = "remote title"
	client.issues[0].Body = "remote body"
	client.issues[0].UpdatedAt = time.Now().UTC().Add(time.Hour)
	client.comments[11][0].Body = "remote comment"
	client.comments[11][0].UpdatedAt = time.Now().UTC().Add(time.Hour)
	res, err := (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflicts < 2 || res.Pulled != 1 {
		t.Fatalf("expected ticket+comment conflicts with remote wins: %+v", res)
	}
	ticket, _ = store.TicketByDisplayIDInBoard(ctx, "GH-11", board.ID)
	notes, _ = store.ListNotes(ctx, ticket.ID)
	if ticket.Title != "remote title" || notes[0].Body != "remote comment" || len(client.updatedIssues) != 0 || len(client.updatedComments) != 0 {
		t.Fatalf("newest remote did not win: ticket=%+v notes=%+v updates=%d/%d", ticket, notes, len(client.updatedIssues), len(client.updatedComments))
	}
}

func TestGitHubHTTPClientPaginatesIssuesAndComments(t *testing.T) {
	var issuePages, commentPages int
	var baseURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/1/comments"):
			commentPages++
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"id":2,"body":"c2","updated_at":"2026-01-01T00:00:00Z"}]`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/issues/1/comments?per_page=100&page=2>; rel="next"`, baseURL))
			fmt.Fprint(w, `[{"id":1,"body":"c1","updated_at":"2026-01-01T00:00:00Z"}]`)
		case r.URL.Path == "/repos/o/r/issues":
			issuePages++
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"number":2,"title":"i2","state":"open","updated_at":"2026-01-01T00:00:00Z"}]`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/issues?state=open&per_page=100&page=2>; rel="next"`, baseURL))
			fmt.Fprint(w, `[{"number":1,"title":"i1","state":"open","updated_at":"2026-01-01T00:00:00Z"}]`)
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	}))
	defer server.Close()
	baseURL = server.URL
	client := NewGitHubHTTPClient(server.Client())
	cfg := GitHubConfig{Owner: "o", Repo: "r", APIBaseURL: server.URL, States: []string{"open"}}
	issues, err := client.ListIssues(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := client.ListComments(context.Background(), cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issuePages != 2 || len(comments) != 2 || commentPages != 2 {
		t.Fatalf("issues=%d pages=%d comments=%d pages=%d", len(issues), issuePages, len(comments), commentPages)
	}
}

func TestGitHubHTTPClientReportsRateLimitHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1760000000")
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()
	client := NewGitHubHTTPClient(server.Client())
	_, err := client.ListIssues(context.Background(), GitHubConfig{Owner: "o", Repo: "r", APIBaseURL: server.URL, States: []string{"open"}})
	if err == nil || !strings.Contains(err.Error(), "rate_limit_reset=1760000000") || !strings.Contains(err.Error(), "rate_limit_remaining=0") {
		t.Fatalf("rate limit error missing context: %v", err)
	}
}

func columnHasTicket(view storage.BoardView, columnName, displayID string) bool {
	for _, col := range view.Columns {
		if col.Name != columnName {
			continue
		}
		for _, ticket := range col.Tickets {
			if ticket.DisplayID == displayID {
				return true
			}
		}
	}
	return false
}
