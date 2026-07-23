package ticketbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

type fakeJiraClient struct {
	issues            []JiraIssue
	comments          map[string][]JiraComment
	createdIssues     []JiraIssueUpdate
	updatedIssues     []JiraIssueUpdate
	updateIssueErrors []error
	createdComments   []string
	lastSearchJQL     string
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
	if len(f.updateIssueErrors) > 0 {
		err := f.updateIssueErrors[0]
		f.updateIssueErrors = f.updateIssueErrors[1:]
		if err != nil {
			return JiraIssue{}, err
		}
	}
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

func TestJiraSyncPullsDoneIssueWithActiveSessionWithoutArchiving(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Jira", Workdir: t.TempDir(), TicketBackend: KindAtlassian, BackendConfig: `{"site_url":"https://acme.atlassian.net","project_key":"AK","email":"me@example.com","api_token":"tok"}`})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	client := &fakeJiraClient{issues: []JiraIssue{{ID: "1007", Key: "AK-7", BrowseURL: "url", Summary: "active", Description: "body", Status: "Open", UpdatedAt: base}}, comments: map[string][]JiraComment{}}
	backend := JiraBackend{Client: client}
	if _, err := backend.Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, err := store.TicketByDisplayIDInBoard(ctx, "AK-7", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "test", TmuxWindowName: "ticket", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	remoteUpdated := time.Now().UTC()
	client.issues = []JiraIssue{
		{ID: "1007", Key: "AK-7", BrowseURL: "url", Summary: "active", Description: "body", Status: "Done", UpdatedAt: remoteUpdated},
		{ID: "1008", Key: "AK-8", BrowseURL: "url", Summary: "later issue", Description: "body", Status: "Review", UpdatedAt: remoteUpdated},
	}
	res, err := backend.Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 2 {
		t.Fatalf("pulled=%d, want 2", res.Pulled)
	}
	ticket, err = store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.ArchivedAt.Valid {
		t.Fatalf("Done Jira ticket should remain visible, got archived_at=%v", ticket.ArchivedAt.Time)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !columnHasTicket(view, "Done", "AK-7") || !columnHasTicket(view, "Review", "AK-8") {
		t.Fatalf("Done ticket or later issue missing from board view: %+v", view.Columns)
	}

	active, _, err := store.ActiveSession(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSessionClosed(ctx, active.ID, "closed", "test", "done"); err != nil {
		t.Fatal(err)
	}
	doneID, err := store.ColumnIDByBoardAndName(ctx, board.ID, "Done")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRemoteTicket(ctx, storage.RemoteTicket{BoardID: board.ID, ColumnID: doneID, ExternalID: "1007", ExternalURL: "url", ExternalUpdatedAt: remoteUpdated, DisplayID: "AK-7", DisplayNumber: 7, Title: "active", Body: "body", ArchivedAt: &remoteUpdated}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "test", TmuxWindowName: "ticket", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	res, err = backend.Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 1 {
		t.Fatalf("legacy Done repair pulled=%d, want 1", res.Pulled)
	}
	ticket, err = store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.ArchivedAt.Valid {
		t.Fatalf("legacy Done ticket was not restored: %+v", ticket)
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

func TestJiraSyncDoesNotCountPartialUpdateAndRetries(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Jira", Workdir: t.TempDir(), TicketBackend: KindAtlassian, BackendConfig: `{"site_url":"https://acme.atlassian.net","project_key":"AK","email":"me@example.com","api_token":"tok"}`})
	if err != nil {
		t.Fatal(err)
	}
	remoteUpdated := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeJiraClient{issues: []JiraIssue{{ID: "1007", Key: "AK-7", BrowseURL: "url", Summary: "old", Description: "old", Status: "Open", UpdatedAt: remoteUpdated}}, comments: map[string][]JiraComment{}}
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
	client.updateIssueErrors = []error{errors.New("transition failed after metadata update")}

	res, err := (JiraBackend{Client: client}).Sync(ctx, store, board)
	if err == nil || !strings.Contains(err.Error(), "transition failed") {
		t.Fatalf("first sync error=%v", err)
	}
	if res.Pushed != 0 {
		t.Fatalf("partial update counted as pushed: %+v", res)
	}
	unsynced, err := store.TicketByDisplayIDInBoard(ctx, "AK-7", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unsynced.ExternalUpdatedAt.Time.Equal(remoteUpdated) {
		t.Fatalf("partial update advanced local sync marker: got %v want %v", unsynced.ExternalUpdatedAt.Time, remoteUpdated)
	}

	res, err = (JiraBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 1 || len(client.updatedIssues) != 2 {
		t.Fatalf("retry did not push update: result=%+v attempts=%d", res, len(client.updatedIssues))
	}
}

func TestJiraSyncDoesNotOverwriteMoveMadeAfterLocalSnapshot(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Jira", Workdir: t.TempDir(), TicketBackend: KindAtlassian, BackendConfig: `{"site_url":"https://acme.atlassian.net","project_key":"AK","email":"me@example.com","api_token":"tok"}`})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour)
	client := &fakeJiraClient{issues: []JiraIssue{
		{ID: "10330", Key: "AK-330", BrowseURL: "url", Summary: "ticket", Description: "body", Status: "In Progress", UpdatedAt: base},
		{ID: "10331", Key: "AK-331", BrowseURL: "url", Summary: "review occupant", Description: "body", Status: "Review", UpdatedAt: base},
	}, comments: map[string][]JiraComment{}}
	backend := JiraBackend{Client: client}
	if _, err := backend.Sync(ctx, store, board); err != nil {
		t.Fatal(err)
	}
	ticket, err := store.TicketByDisplayIDInBoard(ctx, "AK-330", board.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewID, err := store.ColumnIDByBoardAndName(ctx, board.ID, "Review")
	if err != nil {
		t.Fatal(err)
	}

	client.issues[0].UpdatedAt = time.Now().UTC()
	gated := &gatedTicketSnapshotRepository{SyncRepository: store, snapshotTaken: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := backend.Sync(ctx, gated, board)
		done <- err
	}()
	<-gated.snapshotTaken
	if err := store.MoveTicket(ctx, ticket.ID, reviewID); err != nil {
		t.Fatal(err)
	}
	close(gated.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	updated, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ColumnID != reviewID {
		t.Fatalf("stale sync reverted concurrent move: column=%d want review=%d", updated.ColumnID, reviewID)
	}
	res, err := backend.Sync(ctx, store, board)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed < 1 || len(client.updatedIssues) < 1 || client.updatedIssues[0].Status != "Review" {
		t.Fatalf("newer move did not converge to Jira: result=%+v updates=%+v", res, client.updatedIssues)
	}
}

func TestJiraHTTPClientPaginatesIssuesAndComments(t *testing.T) {
	var issuePages, commentPages int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			issuePages++
			if got := r.URL.Query().Get("maxResults"); got != "100" {
				t.Fatalf("maxResults=%q, want 100", got)
			}
			page := r.URL.Query().Get("nextPageToken")
			if page != "" && page != "issues-page-2" {
				t.Fatalf("nextPageToken=%q", page)
			}
			start, count := 0, 100
			if page == "issues-page-2" {
				start, count = 100, 1
			}
			issues := make([]map[string]any, count)
			for i := range issues {
				issues[i] = map[string]any{
					"id":  strconvItoa(start + i + 1),
					"key": "AK-" + strconvItoa(start+i+1),
					"fields": map[string]any{
						"summary": "issue",
						"status":  map[string]any{"name": "Open"},
						"updated": "2026-01-01T00:00:00Z",
					},
				}
			}
			response := map[string]any{"issues": issues, "isLast": page == "issues-page-2"}
			if page == "" {
				response["nextPageToken"] = "issues-page-2"
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Fatal(err)
			}
		case "/rest/api/3/issue/1001/comment":
			commentPages++
			if got := r.URL.Query().Get("maxResults"); got != "100" {
				t.Fatalf("maxResults=%q, want 100", got)
			}
			startAt, err := strconv.Atoi(r.URL.Query().Get("startAt"))
			if err != nil {
				t.Fatal(err)
			}
			if startAt != 0 && startAt != 100 {
				t.Fatalf("startAt=%d", startAt)
			}
			count := 100
			if startAt == 100 {
				count = 1
			}
			comments := make([]map[string]any, count)
			for i := range comments {
				comments[i] = map[string]any{
					"id":      strconvItoa(startAt + i + 1),
					"body":    "comment",
					"updated": "2026-01-01T00:00:00Z",
				}
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"comments": comments, "startAt": startAt, "maxResults": 100, "total": 101}); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	}))
	defer server.Close()

	client := NewJiraHTTPClient(server.Client())
	cfg := JiraConfig{SiteURL: server.URL, ProjectKey: "AK", BearerToken: "token"}
	issues, err := client.SearchIssues(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := client.ListComments(context.Background(), cfg, "1001")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 101 || issuePages != 2 || len(comments) != 101 || commentPages != 2 {
		t.Fatalf("issues=%d pages=%d comments=%d pages=%d", len(issues), issuePages, len(comments), commentPages)
	}
}

func TestJiraHTTPUpdateIssueTransitionFailures(t *testing.T) {
	tests := []struct {
		name               string
		transitionGetCode  int
		transitionsBody    string
		transitionPostCode int
		want               string
	}{
		{name: "lookup fails", transitionGetCode: http.StatusBadGateway, want: "GET /rest/api/3/issue/1007/transitions"},
		{name: "requested transition unavailable", transitionGetCode: http.StatusOK, transitionsBody: `{"transitions":[{"id":"2","to":{"name":"Review"}}]}`, want: `no available transition to "Done"`},
		{name: "transition post fails", transitionGetCode: http.StatusOK, transitionsBody: `{"transitions":[{"id":"3","to":{"name":"Done"}}]}`, transitionPostCode: http.StatusConflict, want: "POST /rest/api/3/issue/1007/transitions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := "Open"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/1007":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/1007":
					fmt.Fprintf(w, `{"id":"1007","key":"AK-7","fields":{"summary":"local","description":"body","status":{"name":%q},"updated":"2026-01-01T00:00:00Z"}}`, status)
				case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/1007/transitions":
					w.WriteHeader(tt.transitionGetCode)
					if tt.transitionsBody != "" {
						_, _ = w.Write([]byte(tt.transitionsBody))
					}
				case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/1007/transitions":
					code := tt.transitionPostCode
					if code == 0 {
						code = http.StatusNoContent
					}
					w.WriteHeader(code)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client := NewJiraHTTPClient(server.Client())
			_, err := client.UpdateIssue(context.Background(), JiraConfig{SiteURL: server.URL, ProjectKey: "AK", BearerToken: "token"}, "1007", JiraIssueUpdate{Summary: "local", Description: "body", Status: "Done"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want substring %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "after updating summary/description") {
				t.Fatalf("error does not explain partial metadata update: %v", err)
			}
		})
	}
}

func TestJiraHTTPUpdateIssueSkipsTransitionWhenStatusAlreadyMatches(t *testing.T) {
	transitionRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/1007":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/1007":
			_, _ = w.Write([]byte(`{"id":"1007","key":"AK-7","fields":{"summary":"local","description":"body","status":{"name":"Open"},"updated":"2026-01-01T00:00:00Z"}}`))
		case strings.HasSuffix(r.URL.Path, "/transitions"):
			transitionRequests++
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewJiraHTTPClient(server.Client())
	issue, err := client.UpdateIssue(context.Background(), JiraConfig{SiteURL: server.URL, ProjectKey: "AK", BearerToken: "token"}, "1007", JiraIssueUpdate{Summary: "local", Description: "body", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != "Open" || transitionRequests != 0 {
		t.Fatalf("issue=%+v transitionRequests=%d", issue, transitionRequests)
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
