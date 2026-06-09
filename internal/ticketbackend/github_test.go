package ticketbackend

import (
	"context"
	"strings"
	"testing"
	"time"

	"kanbi/internal/storage"
)

type fakeGitHubClient struct {
	issues          []GitHubIssue
	comments        map[int][]GitHubComment
	createdIssues   []GitHubIssueUpdate
	updatedIssues   []GitHubIssueUpdate
	createdComments []string
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
		issues:   []GitHubIssue{{Number: 42, HTMLURL: "https://github.com/acme/proj/issues/42", Title: "Remote title", Body: "Remote body", State: "open", Labels: []GitHubLabel{{Name: "status:Review"}}, UpdatedAt: remoteUpdated}},
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
		issues:   []GitHubIssue{{Number: 7, HTMLURL: "url", Title: "old", Body: "old", State: "open", Labels: []GitHubLabel{{Name: "status:Open"}}, UpdatedAt: oldRemote}},
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
	if _, err := store.CreateTicket(ctx, view.Columns[0].ID, "local only", "body", "pi"); err != nil {
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
	if _, err := store.TicketByDisplayIDInBoard(ctx, "GH-1001", board.ID); err != nil {
		t.Fatal(err)
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
	if len(cfg.States) != 1 || cfg.States[0] != "open" || len(cfg.Labels) != 2 || cfg.Labels[1] != "triage" {
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
