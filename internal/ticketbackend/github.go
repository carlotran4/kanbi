package ticketbackend

import (
	"bytes"
	"context"
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

	"kanbi/internal/storage"
)

const defaultGitHubColumnLabelPrefix = "status:"

type GitHubConfig struct {
	Owner             string   `json:"owner"`
	Repo              string   `json:"repo"`
	Token             string   `json:"token,omitempty"`
	APIBaseURL        string   `json:"api_base_url,omitempty"`
	States            []string `json:"states,omitempty"`
	Labels            []string `json:"labels,omitempty"`
	Assignee          string   `json:"assignee,omitempty"`
	Mentioned         string   `json:"mentioned,omitempty"`
	Milestone         string   `json:"milestone,omitempty"`
	Since             string   `json:"since,omitempty"`
	ColumnLabelPrefix string   `json:"column_label_prefix,omitempty"`
	DefaultOpenColumn string   `json:"default_open_column,omitempty"`
	ClosedColumn      string   `json:"closed_column,omitempty"`
}

type GitHubIssue struct {
	ID        int64         `json:"id"`
	Number    int           `json:"number"`
	HTMLURL   string        `json:"html_url"`
	Title     string        `json:"title"`
	Body      string        `json:"body"`
	State     string        `json:"state"`
	Labels    []GitHubLabel `json:"labels"`
	UpdatedAt time.Time     `json:"updated_at"`
	ClosedAt  *time.Time    `json:"closed_at"`
}

type GitHubLabel struct {
	Name string `json:"name"`
}

type GitHubComment struct {
	ID        int64     `json:"id"`
	HTMLURL   string    `json:"html_url"`
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updated_at"`
}

type GitHubIssueUpdate struct {
	Title  *string  `json:"title,omitempty"`
	Body   *string  `json:"body,omitempty"`
	State  *string  `json:"state,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

type GitHubClient interface {
	ListIssues(context.Context, GitHubConfig) ([]GitHubIssue, error)
	ListComments(context.Context, GitHubConfig, int) ([]GitHubComment, error)
	CreateIssue(context.Context, GitHubConfig, GitHubIssueUpdate) (GitHubIssue, error)
	UpdateIssue(context.Context, GitHubConfig, int, GitHubIssueUpdate) (GitHubIssue, error)
	CreateComment(context.Context, GitHubConfig, int, string) (GitHubComment, error)
	UpdateComment(context.Context, GitHubConfig, int64, string) (GitHubComment, error)
}

type GitHubBackend struct{ Client GitHubClient }

func (GitHubBackend) Kind() string { return KindGitHub }

func (b GitHubBackend) Sync(ctx context.Context, store *storage.Store, board storage.Board) (Result, error) {
	cfg, err := ParseGitHubConfig(board.BackendConfig, board.BackendQuery)
	if err != nil {
		return Result{}, err
	}
	client := b.Client
	if client == nil {
		client = NewGitHubHTTPClient(nil)
	}
	issues, err := client.ListIssues(ctx, cfg)
	if err != nil {
		return Result{}, err
	}
	columns := githubColumns(cfg, issues)
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
	for _, issue := range issues {
		col := githubIssueColumn(cfg, issue)
		columnID := columnIDs[col]
		if columnID == 0 {
			continue
		}
		externalID := strconv.Itoa(issue.Number)
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
				updated, err := client.UpdateIssue(ctx, cfg, issue.Number, githubUpdateFromLocal(cfg, local, localColumn, issue.Labels))
				if err != nil {
					return res, err
				}
				issue = updated
				if _, err := store.UpsertRemoteTicket(ctx, storage.RemoteTicket{BoardID: board.ID, ColumnID: local.ColumnID, ExternalID: externalID, ExternalURL: issue.HTMLURL, ExternalUpdatedAt: issue.UpdatedAt, DisplayID: fmt.Sprintf("GH-%d", issue.Number), DisplayNumber: issue.Number, Title: issue.Title, Body: issue.Body, ArchivedAt: issue.ClosedAt}); err != nil {
					return res, err
				}
				res.Pushed++
			} else if remoteNewer {
				if _, err := store.UpsertRemoteTicket(ctx, storage.RemoteTicket{BoardID: board.ID, ColumnID: columnID, ExternalID: externalID, ExternalURL: issue.HTMLURL, ExternalUpdatedAt: issue.UpdatedAt, DisplayID: fmt.Sprintf("GH-%d", issue.Number), DisplayNumber: issue.Number, Title: issue.Title, Body: issue.Body, ArchivedAt: issue.ClosedAt}); err != nil {
					return res, err
				}
				res.Pulled++
			}
			if err := b.syncComments(ctx, store, client, cfg, local.ID, issue.Number); err != nil {
				return res, err
			}
			continue
		}
		t, err := store.UpsertRemoteTicket(ctx, storage.RemoteTicket{BoardID: board.ID, ColumnID: columnID, ExternalID: externalID, ExternalURL: issue.HTMLURL, ExternalUpdatedAt: issue.UpdatedAt, DisplayID: fmt.Sprintf("GH-%d", issue.Number), DisplayNumber: issue.Number, Title: issue.Title, Body: issue.Body, ArchivedAt: issue.ClosedAt})
		if err != nil {
			return res, err
		}
		res.Pulled++
		if err := b.syncComments(ctx, store, client, cfg, t.ID, issue.Number); err != nil {
			return res, err
		}
	}
	for _, local := range locals {
		if local.ExternalID.Valid || syncedLocal[local.ID] {
			continue
		}
		localColumn := columnNamesByID[local.ColumnID]
		if localColumn == "" {
			localColumn = cfg.DefaultOpenColumn
		}
		created, err := client.CreateIssue(ctx, cfg, githubUpdateFromLocal(cfg, local, localColumn, nil))
		if err != nil {
			return res, err
		}
		externalID := strconv.Itoa(created.Number)
		if _, err := store.UpsertRemoteTicket(ctx, storage.RemoteTicket{BoardID: board.ID, ColumnID: local.ColumnID, ExternalID: externalID, ExternalURL: created.HTMLURL, ExternalUpdatedAt: created.UpdatedAt, DisplayID: fmt.Sprintf("GH-%d", created.Number), DisplayNumber: created.Number, Title: created.Title, Body: created.Body, ArchivedAt: created.ClosedAt}); err != nil {
			return res, err
		}
		res.Pushed++
		if err := b.syncComments(ctx, store, client, cfg, local.ID, created.Number); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (b GitHubBackend) syncComments(ctx context.Context, store *storage.Store, client GitHubClient, cfg GitHubConfig, ticketID int64, issueNumber int) error {
	comments, err := client.ListComments(ctx, cfg, issueNumber)
	if err != nil {
		return err
	}
	locals, err := store.ListNotes(ctx, ticketID)
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
		ext := strconv.FormatInt(c.ID, 10)
		if n, ok := byExternal[ext]; ok {
			if n.UpdatedAt.After(c.UpdatedAt) {
				updated, err := client.UpdateComment(ctx, cfg, c.ID, n.Body)
				if err != nil {
					return err
				}
				if err := store.UpsertRemoteNote(ctx, ticketID, ext, updated.Body, updated.UpdatedAt); err != nil {
					return err
				}
			} else if !n.ExternalUpdatedAt.Valid || c.UpdatedAt.After(n.ExternalUpdatedAt.Time) {
				if err := store.UpsertRemoteNote(ctx, ticketID, ext, c.Body, c.UpdatedAt); err != nil {
					return err
				}
			}
			continue
		}
		if err := store.UpsertRemoteNote(ctx, ticketID, ext, c.Body, c.UpdatedAt); err != nil {
			return err
		}
	}
	for _, n := range locals {
		if n.ExternalID.Valid || strings.TrimSpace(n.Body) == "" {
			continue
		}
		created, err := client.CreateComment(ctx, cfg, issueNumber, n.Body)
		if err != nil {
			return err
		}
		if err := store.LinkLocalNoteToRemote(ctx, n.ID, strconv.FormatInt(created.ID, 10), created.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func ParseGitHubConfig(configJSON, query string) (GitHubConfig, error) {
	var cfg GitHubConfig
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return cfg, fmt.Errorf("github backend_config: %w", err)
		}
	}
	if cfg.Owner == "" {
		cfg.Owner = os.Getenv("KANBI_GITHUB_OWNER")
	}
	if cfg.Repo == "" {
		cfg.Repo = os.Getenv("KANBI_GITHUB_REPO")
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("KANBI_GITHUB_TOKEN")
		if cfg.Token == "" {
			cfg.Token = os.Getenv("GITHUB_TOKEN")
		}
	}
	applyGitHubQuery(&cfg, query)
	if cfg.Owner == "" || cfg.Repo == "" {
		return cfg, errors.New("github backend requires backend_config owner/repo or KANBI_GITHUB_OWNER/REPO")
	}
	if len(cfg.States) == 0 {
		cfg.States = []string{"open", "closed"}
	}
	if cfg.ColumnLabelPrefix == "" {
		cfg.ColumnLabelPrefix = defaultGitHubColumnLabelPrefix
	}
	if cfg.DefaultOpenColumn == "" {
		cfg.DefaultOpenColumn = "Open"
	}
	if cfg.ClosedColumn == "" {
		cfg.ClosedColumn = "Closed"
	}
	return cfg, nil
}

func applyGitHubQuery(cfg *GitHubConfig, query string) {
	vals, _ := url.ParseQuery(strings.TrimSpace(query))
	if v := vals["state"]; len(v) > 0 {
		cfg.States = splitCSV(v[len(v)-1])
	}
	if v := vals["labels"]; len(v) > 0 {
		cfg.Labels = splitCSV(v[len(v)-1])
	}
	if v := vals.Get("assignee"); v != "" {
		cfg.Assignee = v
	}
	if v := vals.Get("mentioned"); v != "" {
		cfg.Mentioned = v
	}
	if v := vals.Get("milestone"); v != "" {
		cfg.Milestone = v
	}
	if v := vals.Get("since"); v != "" {
		cfg.Since = v
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func githubColumns(cfg GitHubConfig, issues []GitHubIssue) []string {
	seen := map[string]bool{}
	var cols []string
	for _, issue := range issues {
		name := githubIssueColumn(cfg, issue)
		if !seen[name] {
			seen[name] = true
			cols = append(cols, name)
		}
	}
	if !seen[cfg.DefaultOpenColumn] {
		cols = append([]string{cfg.DefaultOpenColumn}, cols...)
	}
	if !seen[cfg.ClosedColumn] {
		cols = append(cols, cfg.ClosedColumn)
	}
	return cols
}

func githubIssueColumn(cfg GitHubConfig, issue GitHubIssue) string {
	if issue.State == "closed" {
		return cfg.ClosedColumn
	}
	for _, l := range issue.Labels {
		if strings.HasPrefix(strings.ToLower(l.Name), strings.ToLower(cfg.ColumnLabelPrefix)) {
			return strings.TrimSpace(l.Name[len(cfg.ColumnLabelPrefix):])
		}
	}
	return cfg.DefaultOpenColumn
}

func githubUpdateFromLocal(cfg GitHubConfig, t storage.Ticket, column string, oldLabels []GitHubLabel) GitHubIssueUpdate {
	title, body := t.Title, t.Body
	state := "open"
	if t.ArchivedAt.Valid || strings.EqualFold(column, cfg.ClosedColumn) {
		state = "closed"
	}
	labels := make([]string, 0, len(oldLabels)+1)
	for _, l := range oldLabels {
		if !strings.HasPrefix(strings.ToLower(l.Name), strings.ToLower(cfg.ColumnLabelPrefix)) {
			labels = append(labels, l.Name)
		}
	}
	if state != "closed" {
		labels = append(labels, cfg.ColumnLabelPrefix+column)
	}
	return GitHubIssueUpdate{Title: &title, Body: &body, State: &state, Labels: labels}
}

type GitHubHTTPClient struct{ HTTP *http.Client }

func NewGitHubHTTPClient(httpClient *http.Client) GitHubHTTPClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return GitHubHTTPClient{HTTP: httpClient}
}

func (c GitHubHTTPClient) ListIssues(ctx context.Context, cfg GitHubConfig) ([]GitHubIssue, error) {
	var all []GitHubIssue
	for _, state := range cfg.States {
		params := url.Values{"state": {state}, "per_page": {"100"}}
		if len(cfg.Labels) > 0 {
			params.Set("labels", strings.Join(cfg.Labels, ","))
		}
		if cfg.Assignee != "" {
			params.Set("assignee", cfg.Assignee)
		}
		if cfg.Mentioned != "" {
			params.Set("mentioned", cfg.Mentioned)
		}
		if cfg.Milestone != "" {
			params.Set("milestone", cfg.Milestone)
		}
		if cfg.Since != "" {
			params.Set("since", cfg.Since)
		}
		var issues []GitHubIssue
		if err := c.do(ctx, cfg, http.MethodGet, fmt.Sprintf("/repos/%s/%s/issues?%s", url.PathEscape(cfg.Owner), url.PathEscape(cfg.Repo), params.Encode()), nil, &issues); err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if issue.Number > 0 {
				all = append(all, issue)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].UpdatedAt.Before(all[j].UpdatedAt) })
	return all, nil
}

func (c GitHubHTTPClient) ListComments(ctx context.Context, cfg GitHubConfig, n int) ([]GitHubComment, error) {
	var comments []GitHubComment
	err := c.do(ctx, cfg, http.MethodGet, fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", url.PathEscape(cfg.Owner), url.PathEscape(cfg.Repo), n), nil, &comments)
	return comments, err
}
func (c GitHubHTTPClient) CreateIssue(ctx context.Context, cfg GitHubConfig, u GitHubIssueUpdate) (GitHubIssue, error) {
	var out GitHubIssue
	err := c.do(ctx, cfg, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(cfg.Owner), url.PathEscape(cfg.Repo)), u, &out)
	return out, err
}

func (c GitHubHTTPClient) UpdateIssue(ctx context.Context, cfg GitHubConfig, n int, u GitHubIssueUpdate) (GitHubIssue, error) {
	var out GitHubIssue
	err := c.do(ctx, cfg, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(cfg.Owner), url.PathEscape(cfg.Repo), n), u, &out)
	return out, err
}
func (c GitHubHTTPClient) CreateComment(ctx context.Context, cfg GitHubConfig, n int, body string) (GitHubComment, error) {
	var out GitHubComment
	err := c.do(ctx, cfg, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", url.PathEscape(cfg.Owner), url.PathEscape(cfg.Repo), n), map[string]string{"body": body}, &out)
	return out, err
}
func (c GitHubHTTPClient) UpdateComment(ctx context.Context, cfg GitHubConfig, id int64, body string) (GitHubComment, error) {
	var out GitHubComment
	err := c.do(ctx, cfg, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/issues/comments/%d", url.PathEscape(cfg.Owner), url.PathEscape(cfg.Repo), id), map[string]string{"body": body}, &out)
	return out, err
}

func (c GitHubHTTPClient) do(ctx context.Context, cfg GitHubConfig, method, path string, in, out any) error {
	base := strings.TrimRight(cfg.APIBaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
