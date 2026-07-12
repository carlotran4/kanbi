package ticketbackend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

const defaultJiraIssueType = "Task"

type JiraConfig struct {
	SiteURL     string `json:"site_url"`
	Email       string `json:"email,omitempty"`
	APIToken    string `json:"api_token,omitempty"`
	BearerToken string `json:"bearer_token,omitempty"`
	ProjectKey  string `json:"project_key"`
	IssueType   string `json:"issue_type,omitempty"`
	DoneColumn  string `json:"done_column,omitempty"`
	JQL         string `json:"jql,omitempty"`
}

type JiraIssue struct {
	ID          string
	Key         string
	Self        string
	BrowseURL   string
	Summary     string
	Description string
	Status      string
	UpdatedAt   time.Time
}

type JiraComment struct {
	ID        string
	Body      string
	UpdatedAt time.Time
}

type JiraIssueUpdate struct {
	Summary     string
	Description string
	Status      string
}

type JiraClient interface {
	SearchIssues(context.Context, JiraConfig) ([]JiraIssue, error)
	ListComments(context.Context, JiraConfig, string) ([]JiraComment, error)
	CreateIssue(context.Context, JiraConfig, JiraIssueUpdate) (JiraIssue, error)
	UpdateIssue(context.Context, JiraConfig, string, JiraIssueUpdate) (JiraIssue, error)
	CreateComment(context.Context, JiraConfig, string, string) (JiraComment, error)
	UpdateComment(context.Context, JiraConfig, string, string, string) (JiraComment, error)
}

type JiraBackend struct{ Client JiraClient }

func (JiraBackend) Kind() string { return KindAtlassian }

func (b JiraBackend) Sync(ctx context.Context, store SyncRepository, board storage.Board) (Result, error) {
	cfg, err := ParseJiraConfig(board.BackendConfig, board.BackendQuery)
	if err != nil {
		return Result{}, err
	}
	client := b.Client
	if client == nil {
		client = NewJiraHTTPClient(nil)
	}
	issues, err := client.SearchIssues(ctx, cfg)
	if err != nil {
		return Result{}, err
	}
	columns := jiraColumns(cfg, issues)
	columnIDs, err := store.SyncBoardColumns(ctx, board.ID, columns)
	if err != nil {
		return Result{}, err
	}
	columnNamesByID := map[int64]string{}
	for name, id := range columnIDs {
		columnNamesByID[id] = name
	}
	locals, err := store.SyncTicketsForBoard(ctx, board.ID)
	if err != nil {
		return Result{}, err
	}
	byExternal := map[string]storage.Ticket{}
	for _, t := range locals {
		if t.ExternalID.Valid {
			byExternal[t.ExternalID.String] = t
		}
	}
	var res Result
	syncedLocal := map[int64]bool{}
	byID := map[int64]storage.Ticket{}
	for _, t := range locals {
		byID[t.ID] = t
	}
	for _, issue := range issues {
		col := strings.TrimSpace(issue.Status)
		if col == "" {
			col = "Open"
		}
		columnID := columnIDs[col]
		if columnID == 0 {
			continue
		}
		externalID := issue.ID
		if externalID == "" {
			externalID = issue.Key
		}
		if local, ok := byExternal[externalID]; ok {
			syncedLocal[local.ID] = true
			localNewer := !local.ExternalUpdatedAt.Valid || local.UpdatedAt.After(issue.UpdatedAt)
			remoteNewer := !local.ExternalUpdatedAt.Valid || issue.UpdatedAt.After(local.ExternalUpdatedAt.Time)
			if localNewer && remoteNewer {
				res.Conflicts++
			}
			if localNewer && local.UpdatedAt.After(issue.UpdatedAt) {
				localColumn := columnNamesByID[local.ColumnID]
				if localColumn == "" {
					localColumn = col
				}
				updated, err := client.UpdateIssue(ctx, cfg, issue.ID, jiraUpdateFromLocal(cfg, local, localColumn))
				if err != nil {
					return res, err
				}
				issue = updated
				if _, err := store.UpsertRemoteTicket(ctx, jiraRemoteTicket(cfg, board.ID, local.ColumnID, issue)); err != nil {
					return res, err
				}
				_ = store.ClearTicketRemotePush(ctx, local.ID)
				res.Pushed++
			} else if remoteNewer {
				if _, err := store.UpsertRemoteTicket(ctx, jiraRemoteTicket(cfg, board.ID, columnID, issue)); err != nil {
					return res, err
				}
				_ = store.ClearTicketRemotePush(ctx, local.ID)
				res.Pulled++
			}
			if err := b.syncComments(ctx, store, client, cfg, local.ID, issue.ID); err != nil {
				return res, err
			}
			continue
		}
		if marker, ok := parseJiraPushMarker(issue.Description); ok {
			if local, ok := byID[marker.TicketID]; ok && !local.ExternalID.Valid && !syncedLocal[local.ID] {
				have := ticketRemotePushToken(local)
				if have != "" && have == marker.Token {
					rt := jiraRemoteTicket(cfg, board.ID, local.ColumnID, issue)
					rt.SourceTicketID = local.ID
					if _, err := store.UpsertRemoteTicket(ctx, rt); err != nil {
						return res, err
					}
					_ = store.ClearTicketRemotePush(ctx, local.ID)
					syncedLocal[local.ID] = true
					res.Pulled++
					if err := b.syncComments(ctx, store, client, cfg, local.ID, issue.ID); err != nil {
						return res, err
					}
					continue
				}
			}
		}
		t, err := store.UpsertRemoteTicket(ctx, jiraRemoteTicket(cfg, board.ID, columnID, issue))
		if err != nil {
			return res, err
		}
		res.Pulled++
		if err := b.syncComments(ctx, store, client, cfg, t.ID, issue.ID); err != nil {
			return res, err
		}
	}
	locals, err = store.SyncTicketsForBoard(ctx, board.ID)
	if err != nil {
		return res, err
	}
	var pendingErrs []error
	for _, local := range locals {
		if local.ExternalID.Valid || syncedLocal[local.ID] {
			continue
		}
		if ticketRemotePushActive(local) {
			msg := fmt.Errorf("pending remote create for ticket %s may already exist in Jira; re-run sync after the issue with the Kanbi local-ticket marker is visible to the board JQL, or clear the pending push intentionally after verifying no remote duplicate", local.DisplayID)
			_ = store.MarkTicketRemotePushFailed(ctx, local.ID, msg.Error())
			pendingErrs = append(pendingErrs, msg)
			continue
		}
		localColumn := columnNamesByID[local.ColumnID]
		if localColumn == "" {
			localColumn = "Open"
		}
		token, err := mintPushToken()
		if err != nil {
			return res, err
		}
		if err := store.MarkTicketRemotePushPending(ctx, local.ID, token); err != nil {
			return res, err
		}
		update := jiraUpdateFromLocal(cfg, local, localColumn)
		update.Description = embedJiraPushMarker(update.Description, local.ID, token)
		created, err := client.CreateIssue(ctx, cfg, update)
		if err != nil {
			return res, err
		}
		rt := jiraRemoteTicket(cfg, board.ID, local.ColumnID, created)
		rt.SourceTicketID = local.ID
		if _, err := store.UpsertRemoteTicket(ctx, rt); err != nil {
			return res, err
		}
		_ = store.ClearTicketRemotePush(ctx, local.ID)
		res.Pushed++
		if err := b.syncComments(ctx, store, client, cfg, local.ID, created.ID); err != nil {
			return res, err
		}
	}
	return res, errors.Join(pendingErrs...)
}

func (b JiraBackend) syncComments(ctx context.Context, store SyncRepository, client JiraClient, cfg JiraConfig, ticketID int64, issueID string) error {
	comments, err := client.ListComments(ctx, cfg, issueID)
	if err != nil {
		return err
	}
	locals, err := store.ListNotesForSync(ctx, ticketID)
	if err != nil {
		return err
	}
	byExternal := map[string]storage.Note{}
	for _, n := range locals {
		if n.ExternalID.Valid {
			byExternal[n.ExternalID.String] = n
		}
	}
	for _, c := range comments {
		if n, ok := byExternal[c.ID]; ok {
			if n.DeletedAt.Valid {
				continue
			}
			if n.UpdatedAt.After(c.UpdatedAt) {
				updated, err := client.UpdateComment(ctx, cfg, issueID, c.ID, n.Body)
				if err != nil {
					return err
				}
				if err := store.UpsertRemoteNote(ctx, ticketID, c.ID, updated.Body, updated.UpdatedAt); err != nil {
					return err
				}
			} else if !n.ExternalUpdatedAt.Valid || c.UpdatedAt.After(n.ExternalUpdatedAt.Time) {
				if err := store.UpsertRemoteNote(ctx, ticketID, c.ID, c.Body, c.UpdatedAt); err != nil {
					return err
				}
			}
			continue
		}
		if err := store.UpsertRemoteNote(ctx, ticketID, c.ID, c.Body, c.UpdatedAt); err != nil {
			return err
		}
	}
	for _, n := range locals {
		if n.DeletedAt.Valid || n.ExternalID.Valid || strings.TrimSpace(n.Body) == "" {
			continue
		}
		created, err := client.CreateComment(ctx, cfg, issueID, n.Body)
		if err != nil {
			return err
		}
		if err := store.LinkLocalNoteToRemote(ctx, n.ID, created.ID, created.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func ParseJiraConfig(configJSON, query string) (JiraConfig, error) {
	var cfg JiraConfig
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return cfg, fmt.Errorf("atlassian backend_config: %w", err)
		}
	}
	if cfg.SiteURL == "" {
		cfg.SiteURL = os.Getenv("KANBI_JIRA_SITE_URL")
	}
	if cfg.Email == "" {
		cfg.Email = os.Getenv("KANBI_JIRA_EMAIL")
	}
	if cfg.APIToken == "" {
		cfg.APIToken = os.Getenv("KANBI_JIRA_API_TOKEN")
	}
	if cfg.BearerToken == "" {
		cfg.BearerToken = os.Getenv("KANBI_JIRA_BEARER_TOKEN")
	}
	if cfg.ProjectKey == "" {
		cfg.ProjectKey = os.Getenv("KANBI_JIRA_PROJECT_KEY")
	}
	if strings.TrimSpace(query) != "" {
		cfg.JQL = strings.TrimSpace(query)
	}
	if cfg.SiteURL == "" {
		return cfg, errors.New("atlassian backend requires site_url or KANBI_JIRA_SITE_URL")
	}
	if cfg.ProjectKey == "" {
		return cfg, errors.New("atlassian backend requires project_key or KANBI_JIRA_PROJECT_KEY")
	}
	if cfg.Email == "" && cfg.BearerToken == "" {
		return cfg, errors.New("atlassian backend requires email/api_token or bearer_token")
	}
	if cfg.Email != "" && cfg.APIToken == "" {
		return cfg, errors.New("atlassian backend requires api_token with email")
	}
	if cfg.IssueType == "" {
		cfg.IssueType = defaultJiraIssueType
	}
	if cfg.DoneColumn == "" {
		cfg.DoneColumn = "Done"
	}
	return cfg, nil
}

func jiraColumns(cfg JiraConfig, issues []JiraIssue) []string {
	seen := map[string]bool{}
	var cols []string
	for _, issue := range issues {
		name := strings.TrimSpace(issue.Status)
		if name == "" {
			name = "Open"
		}
		if !seen[name] {
			seen[name] = true
			cols = append(cols, name)
		}
	}
	if !seen["Open"] {
		cols = append([]string{"Open"}, cols...)
	}
	if !seen[cfg.DoneColumn] {
		cols = append(cols, cfg.DoneColumn)
	}
	return cols
}

func jiraUpdateFromLocal(cfg JiraConfig, t storage.Ticket, column string) JiraIssueUpdate {
	status := column
	if t.ArchivedAt.Valid {
		status = cfg.DoneColumn
	}
	return JiraIssueUpdate{Summary: t.Title, Description: t.Body, Status: status}
}

func jiraRemoteTicket(cfg JiraConfig, boardID, columnID int64, issue JiraIssue) storage.RemoteTicket {
	externalID := issue.ID
	if externalID == "" {
		externalID = issue.Key
	}
	display := issue.Key
	if display == "" {
		display = externalID
	}
	var number int
	if idx := strings.LastIndex(display, "-"); idx >= 0 && idx+1 < len(display) {
		number, _ = strconv.Atoi(display[idx+1:])
	}
	var archivedAt *time.Time
	if strings.EqualFold(issue.Status, cfg.DoneColumn) && !issue.UpdatedAt.IsZero() {
		archivedAt = &issue.UpdatedAt
	}
	return storage.RemoteTicket{BoardID: boardID, ColumnID: columnID, ExternalID: externalID, ExternalURL: issue.BrowseURL, ExternalUpdatedAt: issue.UpdatedAt, DisplayID: display, DisplayNumber: number, Title: issue.Summary, Body: stripJiraPushMarker(issue.Description), ArchivedAt: archivedAt}
}

type JiraHTTPClient struct{ HTTP *http.Client }

func NewJiraHTTPClient(httpClient *http.Client) JiraHTTPClient {
	if httpClient == nil {
		httpClient = NewProviderHTTPClient()
	}
	return JiraHTTPClient{HTTP: httpClient}
}

func (c JiraHTTPClient) SearchIssues(ctx context.Context, cfg JiraConfig) ([]JiraIssue, error) {
	jql := cfg.JQL
	if jql == "" {
		jql = "project = " + cfg.ProjectKey + " ORDER BY updated DESC"
	}
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("maxResults", "100")
	q.Set("fields", "summary,description,status,updated")
	var out jiraSearchResponse
	if err := c.do(ctx, cfg, http.MethodGet, "/rest/api/3/search/jql?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	issues := make([]JiraIssue, 0, len(out.Issues))
	for _, raw := range out.Issues {
		issues = append(issues, raw.toIssue(cfg))
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].UpdatedAt.Before(issues[j].UpdatedAt) })
	return issues, nil
}

func (c JiraHTTPClient) ListComments(ctx context.Context, cfg JiraConfig, issueID string) ([]JiraComment, error) {
	var out struct {
		Comments []jiraCommentJSON `json:"comments"`
	}
	if err := c.do(ctx, cfg, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(issueID)+"/comment?maxResults=100", nil, &out); err != nil {
		return nil, err
	}
	comments := make([]JiraComment, 0, len(out.Comments))
	for _, raw := range out.Comments {
		comments = append(comments, raw.toComment())
	}
	return comments, nil
}

func (c JiraHTTPClient) CreateIssue(ctx context.Context, cfg JiraConfig, u JiraIssueUpdate) (JiraIssue, error) {
	in := map[string]any{"fields": map[string]any{"project": map[string]string{"key": cfg.ProjectKey}, "issuetype": map[string]string{"name": cfg.IssueType}, "summary": u.Summary, "description": jiraADF(u.Description)}}
	var out struct{ ID, Key, Self string }
	if err := c.do(ctx, cfg, http.MethodPost, "/rest/api/3/issue", in, &out); err != nil {
		return JiraIssue{}, err
	}
	issue, err := c.GetIssue(ctx, cfg, out.ID)
	if err != nil {
		issue = JiraIssue{ID: out.ID, Key: out.Key, Self: out.Self, BrowseURL: jiraBrowseURL(cfg, out.Key), Summary: u.Summary, Description: u.Description, Status: u.Status, UpdatedAt: time.Now().UTC()}
	}
	return issue, nil
}

func (c JiraHTTPClient) UpdateIssue(ctx context.Context, cfg JiraConfig, issueID string, u JiraIssueUpdate) (JiraIssue, error) {
	in := map[string]any{"fields": map[string]any{"summary": u.Summary, "description": jiraADF(u.Description)}}
	if err := c.do(ctx, cfg, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(issueID), in, nil); err != nil {
		return JiraIssue{}, err
	}
	issue, err := c.GetIssue(ctx, cfg, issueID)
	if err != nil {
		return JiraIssue{}, err
	}
	desiredStatus := strings.TrimSpace(u.Status)
	if desiredStatus == "" || strings.EqualFold(issue.Status, desiredStatus) {
		return issue, nil
	}
	if err := c.transitionIssue(ctx, cfg, issueID, desiredStatus); err != nil {
		return JiraIssue{}, fmt.Errorf("transition Jira issue %s to %q after updating summary/description: %w", issueID, desiredStatus, err)
	}
	return c.GetIssue(ctx, cfg, issueID)
}

func (c JiraHTTPClient) GetIssue(ctx context.Context, cfg JiraConfig, issueID string) (JiraIssue, error) {
	var out jiraIssueJSON
	err := c.do(ctx, cfg, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(issueID)+"?fields=summary,description,status,updated", nil, &out)
	return out.toIssue(cfg), err
}

func (c JiraHTTPClient) CreateComment(ctx context.Context, cfg JiraConfig, issueID, body string) (JiraComment, error) {
	var out jiraCommentJSON
	err := c.do(ctx, cfg, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(issueID)+"/comment", map[string]any{"body": jiraADF(body)}, &out)
	return out.toComment(), err
}

func (c JiraHTTPClient) UpdateComment(ctx context.Context, cfg JiraConfig, issueID, commentID, body string) (JiraComment, error) {
	var out jiraCommentJSON
	err := c.do(ctx, cfg, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(issueID)+"/comment/"+url.PathEscape(commentID), map[string]any{"body": jiraADF(body)}, &out)
	return out.toComment(), err
}

func (c JiraHTTPClient) transitionIssue(ctx context.Context, cfg JiraConfig, issueID, status string) error {
	var out struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := c.do(ctx, cfg, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(issueID)+"/transitions", nil, &out); err != nil {
		return err
	}
	for _, tr := range out.Transitions {
		if strings.EqualFold(tr.To.Name, status) {
			return c.do(ctx, cfg, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(issueID)+"/transitions", map[string]any{"transition": map[string]string{"id": tr.ID}}, nil)
		}
	}
	return fmt.Errorf("no available transition to %q", status)
}

func (c JiraHTTPClient) do(ctx context.Context, cfg JiraConfig, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}
	maxAttempts := 1
	if methodIsIdempotentGET(method) {
		maxAttempts = defaultRetryMaxAttempts
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := c.doOnce(ctx, cfg, method, path, payload, out)
		if err == nil {
			return nil
		}
		lastErr = err
		var ce *ClassifiedError
		if !errors.As(err, &ce) || !shouldRetryClass(method, ce.Class) || attempt == maxAttempts {
			return err
		}
		if waitErr := waitRetry(ctx, retryDelay(attempt)); waitErr != nil {
			return classifyAndWrap("atlassian "+method, method, 0, attempt, waitErr)
		}
	}
	return lastErr
}

func (c JiraHTTPClient) doOnce(ctx context.Context, cfg JiraConfig, method, path string, payload []byte, out any) error {
	base := strings.TrimRight(cfg.SiteURL, "/")
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	} else if cfg.Email != "" && cfg.APIToken != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cfg.Email+":"+cfg.APIToken)))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return classifyAndWrap("atlassian "+method+" "+path, method, 0, 1, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText := readLimitedBody(resp.Body)
		cause := fmt.Errorf("atlassian %s %s: %s: %s", method, path, resp.Status, bodyText)
		return classifyAndWrap("atlassian "+method+" "+path, method, resp.StatusCode, 1, cause)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return classifyAndWrap("atlassian decode "+path, method, resp.StatusCode, 1, err)
	}
	return nil
}

type jiraSearchResponse struct {
	Issues []jiraIssueJSON `json:"issues"`
}

type jiraIssueJSON struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Self   string `json:"self"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Updated     string          `json:"updated"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
	} `json:"fields"`
}

func (j jiraIssueJSON) toIssue(cfg JiraConfig) JiraIssue {
	return JiraIssue{ID: j.ID, Key: j.Key, Self: j.Self, BrowseURL: jiraBrowseURL(cfg, j.Key), Summary: j.Fields.Summary, Description: jiraADFText(j.Fields.Description), Status: j.Fields.Status.Name, UpdatedAt: parseJiraTime(j.Fields.Updated)}
}

type jiraCommentJSON struct {
	ID      string          `json:"id"`
	Body    json.RawMessage `json:"body"`
	Updated string          `json:"updated"`
}

func (j jiraCommentJSON) toComment() JiraComment {
	return JiraComment{ID: j.ID, Body: jiraADFText(j.Body), UpdatedAt: parseJiraTime(j.Updated)}
}

func jiraBrowseURL(cfg JiraConfig, key string) string {
	if key == "" {
		return ""
	}
	return strings.TrimRight(cfg.SiteURL, "/") + "/browse/" + url.PathEscape(key)
}

func parseJiraTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05.000Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func jiraADF(text string) any {
	return map[string]any{"type": "doc", "version": 1, "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]string{"type": "text", "text": text}}}}}
}

func jiraADFText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	var parts []string
	collectADFText(root, &parts)
	return strings.Join(parts, "")
}

func collectADFText(v any, parts *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if typ, _ := x["type"].(string); typ == "text" {
			if text, _ := x["text"].(string); text != "" {
				*parts = append(*parts, text)
			}
		}
		if typ, _ := x["type"].(string); typ == "paragraph" || typ == "hardBreak" {
			if len(*parts) > 0 {
				*parts = append(*parts, "\n")
			}
		}
		for _, child := range x {
			collectADFText(child, parts)
		}
	case []any:
		for _, child := range x {
			collectADFText(child, parts)
		}
	}
}
