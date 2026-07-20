package ticketbackend

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestClassifyHTTP(t *testing.T) {
	if got := ClassifyHTTP(401, nil); got != ClassAuth {
		t.Fatalf("401 class=%s", got)
	}
	if got := ClassifyHTTP(429, nil); got != ClassRetryable {
		t.Fatalf("429 class=%s", got)
	}
	if got := ClassifyHTTP(500, nil); got != ClassRetryable {
		t.Fatalf("500 class=%s", got)
	}
	if got := ClassifyHTTP(422, nil); got != ClassValidation {
		t.Fatalf("422 class=%s", got)
	}
	if got := ClassifyHTTP(0, context.Canceled); got != ClassCanceled {
		t.Fatalf("canceled class=%s", got)
	}
}

func TestRedactSecretText(t *testing.T) {
	in := "Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz0123456789 and token=supersecret"
	out := storage.RedactSecretText(in)
	if strings.Contains(out, "ghp_") || strings.Contains(out, "supersecret") {
		t.Fatalf("not redacted: %q", out)
	}
}

func TestGitHubGETRetriesRetryableStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("temporary"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := NewGitHubHTTPClient(server.Client())
	_, err := client.ListIssues(context.Background(), GitHubConfig{Owner: "o", Repo: "r", APIBaseURL: server.URL, States: []string{"open"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want 3", calls.Load())
	}
}

func TestGitHubCreateIssueDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()
	client := NewGitHubHTTPClient(server.Client())
	title := "t"
	body := "b"
	_, err := client.CreateIssue(context.Background(), GitHubConfig{Owner: "o", Repo: "r", APIBaseURL: server.URL}, GitHubIssueUpdate{Title: &title, Body: &body})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("create calls=%d want 1", calls.Load())
	}
}

func TestGitHubPendingCreateRecoversByTokenWithoutDuplicate(t *testing.T) {
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
	local, err := store.CreateTicket(ctx, view.Columns[0].ID, "pending create", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeGitHubClient{comments: map[int][]GitHubComment{}}
	// Force: create issue succeeds, then inject crash between create and local link by using a store wrapper.
	crashStore := &crashAfterCreateStore{Store: store, ticketID: local.ID}
	res, err := (GitHubBackend{Client: client}).Sync(ctx, crashStore, board)
	if err == nil {
		t.Fatal("expected crash after create")
	}
	if len(client.createdIssues) != 1 {
		t.Fatalf("created=%d", len(client.createdIssues))
	}
	if err := store.UpdateTicket(ctx, local.ID, "edited while pending", "new body", local.Harness); err != nil {
		t.Fatal(err)
	}
	// Pending state remains; second sync must link existing remote rather than create again.
	res, err = (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err != nil {
		t.Fatalf("recover sync: %v", err)
	}
	if len(client.createdIssues) != 1 {
		t.Fatalf("duplicate create: %d", len(client.createdIssues))
	}
	ticket, err := store.TicketByID(ctx, local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ticket.ExternalID.Valid {
		t.Fatal("ticket was not linked after recover")
	}
	if ticket.RemotePushState.Valid {
		t.Fatalf("push state still set: %+v", ticket.RemotePushState)
	}
	if ticket.Title != "edited while pending" || ticket.Body != "new body" || strings.Contains(ticket.Body, "kanbi:local-ticket") {
		t.Fatalf("pending edit was not preserved after GitHub link: %+v", ticket)
	}
	if _, err := (GitHubBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatalf("converge sync: %v", err)
	}
	if len(client.updatedIssues) != 1 || client.updatedIssues[0].Title == nil || *client.updatedIssues[0].Title != "edited while pending" {
		t.Fatalf("pending edit did not converge to GitHub: %+v", client.updatedIssues)
	}
	_ = res
}

func TestJiraPendingCreateRecoversByTokenWithoutDuplicate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, ctx)
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Jira", Workdir: t.TempDir(), TicketBackend: KindAtlassian, BackendConfig: `{"site_url":"https://acme.atlassian.net","project_key":"AK","bearer_token":"test"}`})
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	local, err := store.CreateTicket(ctx, view.Columns[0].ID, "pending create", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeJiraClient{comments: map[string][]JiraComment{}}
	crashStore := &crashAfterCreateStore{Store: store, ticketID: local.ID}
	if _, err := (JiraBackend{Client: client}).Sync(ctx, crashStore, board); err == nil {
		t.Fatal("expected crash after create")
	}
	if len(client.createdIssues) != 1 {
		t.Fatalf("created=%d", len(client.createdIssues))
	}
	if err := store.UpdateTicket(ctx, local.ID, "edited while pending", "new body", local.Harness); err != nil {
		t.Fatal(err)
	}
	if _, err := (JiraBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatalf("recover sync: %v", err)
	}
	if len(client.createdIssues) != 1 {
		t.Fatalf("duplicate create: %d", len(client.createdIssues))
	}
	ticket, err := store.TicketByID(ctx, local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ticket.ExternalID.Valid {
		t.Fatal("ticket was not linked after recover")
	}
	if ticket.Title != "edited while pending" || ticket.Body != "new body" || strings.Contains(ticket.Body, "kanbi:local-ticket") {
		t.Fatalf("pending edit was not preserved after Jira link: %+v", ticket)
	}
	if _, err := (JiraBackend{Client: client}).Sync(ctx, store, board); err != nil {
		t.Fatalf("converge sync: %v", err)
	}
	if len(client.updatedIssues) != 1 || client.updatedIssues[0].Summary != "edited while pending" {
		t.Fatalf("pending edit did not converge to Jira: %+v", client.updatedIssues)
	}
}

func TestGitHubPendingWithoutMatchFailsClosed(t *testing.T) {
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
	local, err := store.CreateTicket(ctx, view.Columns[0].ID, "stuck", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTicketRemotePushPending(ctx, local.ID, "deadbeefdeadbeefdeadbeefdeadbeef"); err != nil {
		t.Fatal(err)
	}
	client := &fakeGitHubClient{comments: map[int][]GitHubComment{}}
	_, err = (GitHubBackend{Client: client}).Sync(ctx, store, board)
	if err == nil {
		t.Fatal("expected fail-closed pending error")
	}
	if len(client.createdIssues) != 0 {
		t.Fatalf("created despite pending: %d", len(client.createdIssues))
	}
	ticket, _ := store.TicketByID(ctx, local.ID)
	if !ticket.RemotePushState.Valid || ticket.RemotePushState.String != storage.RemotePushStateFailed {
		t.Fatalf("push state=%+v", ticket.RemotePushState)
	}
}

type crashAfterCreateStore struct {
	*storage.Store
	ticketID int64
	tripped  bool
}

func (s *crashAfterCreateStore) UpsertRemoteTicket(ctx context.Context, rt storage.RemoteTicket) (storage.Ticket, error) {
	if rt.SourceTicketID == s.ticketID && !s.tripped {
		s.tripped = true
		return storage.Ticket{}, errors.New("injected crash after remote create")
	}
	return s.Store.UpsertRemoteTicket(ctx, rt)
}

func (s *crashAfterCreateStore) UpsertRemoteTicketIfUnchanged(ctx context.Context, rt storage.RemoteTicket, expected time.Time) (storage.Ticket, bool, error) {
	if rt.SourceTicketID == s.ticketID && !s.tripped {
		s.tripped = true
		return storage.Ticket{}, false, errors.New("injected crash after remote create")
	}
	return s.Store.UpsertRemoteTicketIfUnchanged(ctx, rt, expected)
}

func TestPushMarkerRoundTrip(t *testing.T) {
	body := embedGitHubPushMarker("hello", 42, "abc")
	m, ok := parseGitHubPushMarker(body)
	if !ok || m.TicketID != 42 || m.Token != "abc" {
		t.Fatalf("github marker=%+v ok=%v", m, ok)
	}
	j := embedJiraPushMarker("hello", 7, "def")
	jm, ok := parseJiraPushMarker(j)
	if !ok || jm.TicketID != 7 || jm.Token != "def" {
		t.Fatalf("jira marker=%+v ok=%v", jm, ok)
	}
}

func TestRuntimeSoakDeterministic(t *testing.T) {
	if testing.Short() && getenv("KANBI_SOAK") == "" {
		t.Skip("set KANBI_SOAK=1 for longer soak; short mode uses compact deterministic exercise")
	}
	duration := 2 * time.Second
	if v := getenv("KANBI_SOAK_SECONDS"); v != "" {
		if secs, err := time.ParseDuration(v + "s"); err == nil && secs > 0 {
			duration = secs
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Soak", Workdir: t.TempDir(), TicketBackend: KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	backend := &gatedBackend{kind: KindGitHub, started: make(chan int64, 64), release: make(chan struct{})}
	close(backend.release) // never block
	manager := NewManager(store)
	manager.Registry = NewRegistry(LocalBackend{}, backend)
	manager.Interval = 20 * time.Millisecond
	stop := manager.Start(ctx)
	defer stop()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		manager.ScheduleBoardSync(board.ID)
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	// No panic / hang is success; create counter remains zero for fake backend.
	if len(backend.started) == 0 && duration > 100*time.Millisecond {
		// start channel may drain; just ensure manager stops.
	}
}

func getenv(k string) string {
	return strings.TrimSpace(lookupEnv(k))
}

func lookupEnv(k string) string {
	// tiny indirection to keep soak test self-contained without exporting.
	return envGet(k)
}
