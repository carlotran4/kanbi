package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
	"github.com/carlotran4/kanbi/internal/storage"
)

type integrationActions struct {
	*Service
	candidates []integrationpkg.Candidate
	runs       []storage.IntegrationRun
	created    integrationpkg.CreateOptions
	promoted   string
	focused    string
	cancelled  string
}

func (a *integrationActions) IntegrationCandidates(context.Context, int64) ([]integrationpkg.Candidate, error) {
	return a.candidates, nil
}
func (a *integrationActions) ListIntegrationRuns(context.Context, int64) ([]storage.IntegrationRun, error) {
	return a.runs, nil
}
func (a *integrationActions) CreateIntegration(_ context.Context, opts integrationpkg.CreateOptions) (integrationpkg.CreateResult, error) {
	a.created = opts
	run := storage.IntegrationRun{PublicID: "run-12345678", BoardID: opts.BoardID, State: storage.IntegrationStateRunning}
	a.runs = []storage.IntegrationRun{run}
	return integrationpkg.CreateResult{Run: run}, nil
}
func (a *integrationActions) CancelIntegration(_ context.Context, id string) error {
	a.cancelled = id
	return nil
}
func (a *integrationActions) PromoteIntegration(_ context.Context, id string) error {
	a.promoted = id
	return nil
}
func (a *integrationActions) FocusIntegration(_ context.Context, run storage.IntegrationRun) error {
	a.focused = run.PublicID
	return nil
}

func TestIntegrationSelectionLaunchesSelectedWorkspaces(t *testing.T) {
	store, ctx := newTestStore(t)
	board, _ := store.DefaultBoard(ctx)
	if err := store.SetBoardWorktreeMode(ctx, board.ID, storage.WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	a := &integrationActions{Service: NewService(store, nil), candidates: []integrationpkg.Candidate{
		{Workspace: storage.Workspace{ID: 11, BranchName: "feat/one"}, Ticket: storage.Ticket{DisplayID: "T-001"}, HeadSHA: "111111111111", Eligible: true},
		{Workspace: storage.Workspace{ID: 12, BranchName: "feat/two"}, Ticket: storage.Ticket{DisplayID: "T-002"}, Reason: "dirty; commit work first"},
	}}
	m := New(ctx, a)
	if cmd := m.openIntegration(); cmd != nil {
		t.Fatal("open should be synchronous")
	}
	if !m.integrationOpen || len(m.integrationCandidates) != 2 {
		t.Fatalf("modal=%v candidates=%d", m.integrationOpen, len(m.integrationCandidates))
	}
	updated, _ := m.updateIntegration(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	updated, cmd := m.updateIntegration(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected launch command")
	}
	msg := cmd()
	if _, ok := msg.(integrationActionMsg); !ok {
		t.Fatalf("message=%T", msg)
	}
	if len(a.created.WorkspaceIDs) != 1 || a.created.WorkspaceIDs[0] != 11 {
		t.Fatalf("created=%+v", a.created)
	}
}

func TestIntegrationReadyRequiresPromotionConfirmation(t *testing.T) {
	store, ctx := newTestStore(t)
	board, _ := store.DefaultBoard(ctx)
	if err := store.SetBoardWorktreeMode(ctx, board.ID, storage.WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	run := storage.IntegrationRun{PublicID: "run-ready", BoardID: board.ID, State: storage.IntegrationStateReady, SourceBranch: "main", SourceSHA: "aaaaaaaa", CandidateSHA: sqlNullStr("bbbbbbbb")}
	a := &integrationActions{Service: NewService(store, nil), runs: []storage.IntegrationRun{run}}
	m := New(ctx, a)
	m.openIntegration()
	updated, _ := m.updateIntegration(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = updated.(Model)
	if !m.integrationPromoting {
		t.Fatal("expected promotion confirmation")
	}
	updated, cmd := m.updateIntegration(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated
	if cmd == nil {
		t.Fatal("expected promote command")
	}
	_ = cmd()
	if a.promoted != run.PublicID {
		t.Fatalf("promoted=%q", a.promoted)
	}
}
