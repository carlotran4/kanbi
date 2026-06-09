package ticketbackend

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"kanbi/internal/storage"
)

type fakeJiraClient struct {
	issues          []JiraIssue
	comments        map[string][]JiraComment
	createdIssues   []JiraIssueUpdate
	updatedIssues   []JiraIssueUpdate
	createdComments []string
	lastSearchJQL   string
}

func (f *fakeJiraClient) SearchIssues(_ context.Context, cfg JiraConfig) ([]JiraIssue, error) {
	f.lastSearchJQL = cfg.JQL
	return append([]JiraIssue(nil), f.issues...), nil
}
func (f *fakeJiraClient) ListComments(_ context.Context, _ JiraConfig, issueID string) ([]JiraComment, error) {
	return append([]JiraComment(nil), f.comments[issueID]...), nil
}
func (f *fakeJiraClient) CreateIssue(_ context.Context, _ JiraConfig, u JiraIssueUpdate) (JiraIssue, error) {
	f.createdIssues = append(f.createdIssues, u)
	n := 1000 + len(f.createdIssues)
	issue := JiraIssue{ID: strconvItoa(n), Key: "AK-" + strconvItoa(n), BrowseURL: "url", Summary: u.Summary, Description: u.Description, Status: u.Status, UpdatedAt: time.Now().UTC()}
	f.issues = append(f.issues, issue)
	return issue, nil
}
func (f *fakeJiraClient) UpdateIssue(_ context.Context, _ JiraConfig, issueID string, u JiraIssueUpdate) (JiraIssue, error) {
	f.updatedIssues = append(f.updatedIssues, u)
	for i := range f.issues {
		if f.issues[i].ID == issueID {
			f.issues[i].Summary = u.Summary
			f.issues[i].Description = u.Description
			f.issues[i].Status = u.Status
			f.issues[i].UpdatedAt = time.Now().UTC().Add(time.Hour)
			return f.issues[i], nil
		}
	}
	return JiraIssue{}, nil
}
func (f *fakeJiraClient) CreateComment(_ context.Context, _ JiraConfig, issueID, body string) (JiraComment, error) {
	f.createdComments = append(f.createdComments, body)
	c := JiraComment{ID: strconvItoa(100 + len(f.createdComments)), Body: body, UpdatedAt: time.Now().UTC()}
	f.comments[issueID] = append(f.comments[issueID], c)
	return c, nil
}
func (f *fakeJiraClient) UpdateComment(_ context.Context, _ JiraConfig, issueID, commentID, body string) (JiraComment, error) {
	return JiraComment{ID: commentID, Body: body, UpdatedAt: time.Now().UTC()}, nil
}

func TestJiraSyncPullsIssuesColumnsAndComments(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Jira", Workdir: t.TempDir(), TicketBackend: KindAtlassian, BackendConfig: `{"site_url":"https://acme.atlassian.net","project_key":"AK","email":"me@example.com","api_token":"tok"}`, BackendQuery: "project = AK AND labels = kanbi"})
	if err != nil {
		t.Fatal(err)
	}
	remoteUpdated := time.Now().UTC().Add(-time.Hour)
	client := &fakeJiraClient{issues: []JiraIssue{{ID: "10042", Key: "AK-42", BrowseURL: "https://acme.atlassian.net/browse/AK-42", Summary: "Remote title", Description: "Remote body", Status: "In Progress", UpdatedAt: remoteUpdated}}, comments: map[string][]JiraComment{"10042": {{ID: "9", Body: "remote comment", UpdatedAt: remoteUpdated}}}}
	res, err := (JiraBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 1 || client.lastSearchJQL != board.BackendQuery {
		t.Fatalf("pulled=%d jql=%q", res.Pulled, client.lastSearchJQL)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, col := range view.Columns {
		if col.Name == "In Progress" && len(col.Tickets) == 1 {
			found = true
			if col.Tickets[0].DisplayID != "AK-42" || col.Tickets[0].ExternalID.String != "10042" {
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
		t.Fatalf("remote issue not projected in In Progress column: %+v", view.Columns)
	}
}

func TestJiraSyncPushesLocalNewerTicketAndNotes(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Jira", Workdir: t.TempDir(), TicketBackend: KindAtlassian, BackendConfig: `{"site_url":"https://acme.atlassian.net","project_key":"AK","email":"me@example.com","api_token":"tok"}`})
	if err != nil {
		t.Fatal(err)
	}
	oldRemote := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeJiraClient{issues: []JiraIssue{{ID: "1007", Key: "AK-7", BrowseURL: "url", Summary: "old", Description: "old", Status: "Open", UpdatedAt: oldRemote}}, comments: map[string][]JiraComment{}}
	if _, err := (JiraBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, err := store.TicketByDisplayIDInBoard(ctx, "AK-7", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTicket(ctx, ticket.ID, "local title", "local body", ticket.Harness); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddNote(ctx, ticket.ID, "local note"); err != nil {
		t.Fatal(err)
	}
	res, err := (JiraBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 1 || len(client.updatedIssues) != 1 {
		t.Fatalf("pushed=%d updated=%d", res.Pushed, len(client.updatedIssues))
	}
	if got := client.updatedIssues[0].Summary; got != "local title" {
		t.Fatalf("pushed summary=%q", got)
	}
	if len(client.createdComments) != 1 || client.createdComments[0] != "local note" {
		t.Fatalf("created comments: %#v", client.createdComments)
	}
	updated, err := store.TicketByDisplayIDInBoard(ctx, "AK-7", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ExternalUpdatedAt.Time.Before(oldRemote) || strings.TrimSpace(updated.ExternalID.String) == "" {
		t.Fatalf("external sync fields not refreshed: %+v", updated)
	}
}

func TestJiraSyncCreatesRemoteIssueForLocalTicket(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Jira", Workdir: t.TempDir(), TicketBackend: KindAtlassian, BackendConfig: `{"site_url":"https://acme.atlassian.net","project_key":"AK","email":"me@example.com","api_token":"tok"}`})
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
	client := &fakeJiraClient{comments: map[string][]JiraComment{}}
	res, err := (JiraBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 1 || len(client.createdIssues) != 1 {
		t.Fatalf("pushed=%d created=%d", res.Pushed, len(client.createdIssues))
	}
	if got := client.createdIssues[0].Summary; got != "local only" {
		t.Fatalf("created summary=%q", got)
	}
	if _, err := store.TicketByDisplayIDInBoard(ctx, "AK-1001", board.ID); err != nil {
		t.Fatal(err)
	}
}

func TestParseJiraConfigUsesBoardQueryAsJQL(t *testing.T) {
	cfg, err := ParseJiraConfig(`{"site_url":"https://acme.atlassian.net","project_key":"AK","email":"me@example.com","api_token":"tok","issue_type":"Story"}`, "project = AK ORDER BY updated DESC")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SiteURL != "https://acme.atlassian.net" || cfg.ProjectKey != "AK" || cfg.IssueType != "Story" || cfg.JQL != "project = AK ORDER BY updated DESC" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func strconvItoa(n int) string { return fmt.Sprintf("%d", n) }
