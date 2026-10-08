package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
)

type blockedRefreshActions struct {
	Actions
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (a *blockedRefreshActions) RefreshRuntime(ctx context.Context) error {
	if a.calls.Add(1) == 1 {
		close(a.started)
	}
	select {
	case <-a.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRuntimeRefreshDoesNotBlockNavigationAndPreservesEditor(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	for i := 0; i < 10; i++ {
		createTicket(t, ctx, store, view.Columns[0].ID, "ticket", "body", "pi")
	}
	actions := &blockedRefreshActions{Actions: NewService(store, nil), started: make(chan struct{}), release: make(chan struct{})}
	m := New(ctx, actions)
	defer m.Close()
	updated, cmd := m.Update(runtimeTickMsg(time.Now()))
	m = updated.(Model)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-actions.started
	start := time.Now()
	m, _ = mustUpdate(t, m, "j")
	_ = m.View()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("navigation waited for background work: %s", elapsed)
	}
	selected, _ := m.selectedTicket()
	updated, duplicate := m.Update(runtimeTickMsg(time.Now()))
	m = updated.(Model)
	if duplicate != nil {
		t.Fatal("duplicate tick launched overlapping work")
	}
	m, _ = mustUpdate(t, m, "e")
	m.editInputs[0].Set("unsaved title")
	m.bodyTA.SetValue("unsaved body")
	close(actions.release)
	updated, _ = m.Update(<-done)
	m = updated.(Model)
	if !m.editing || m.editTicket.ID != selected.ID || m.editInputs[0].Value() != "unsaved title" || m.bodyTA.Value() != "unsaved body" {
		t.Fatal("refresh replaced the editor or its unsaved text")
	}
	if actions.calls.Load() != 1 || m.refreshBusy {
		t.Fatal("refresh was not coalesced or did not complete")
	}
}

func TestRuntimeRefreshRejectsOldProjectionAndRetainsLastGoodBoard(t *testing.T) {
	m, store, ctx := newTestModel(t)
	defer m.Close()
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "current", "body", "pi")
	m.reload()
	generation := m.projectionGeneration
	snapshot, err := m.readSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	createTicket(t, ctx, store, view.Columns[0].ID, "new", "body", "pi")
	m.reload()
	m.applyRuntimeRefresh(runtimeRefreshedMsg{generation: generation, snapshot: snapshot})
	if len(m.view.Columns[0].Tickets) != 2 {
		t.Fatal("late refresh overwrote foreground mutation")
	}
	m.applyRuntimeRefresh(runtimeRefreshedMsg{generation: m.projectionGeneration, err: errors.New("offline")})
	if m.err != nil || !strings.Contains(m.View(), "runtime refresh stale") || len(m.view.Columns[0].Tickets) != 2 {
		t.Fatal("poll failure hid the last good board")
	}
	snapshot, err = m.readSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m.applyRuntimeRefresh(runtimeRefreshedMsg{generation: m.projectionGeneration, snapshot: snapshot})
	if m.refreshError != "" {
		t.Fatal("successful observation did not clear stale notice")
	}
}

func TestRuntimeRefreshShutdownCancelsAndJoins(t *testing.T) {
	store, ctx := newTestStore(t)
	actions := &blockedRefreshActions{Actions: NewService(store, nil), started: make(chan struct{}), release: make(chan struct{})}
	m := New(ctx, actions)
	_, cmd := m.Update(runtimeTickMsg(time.Now()))
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-actions.started
	m.Close()
	result := (<-done).(runtimeRefreshedMsg)
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("uncancelled worker: %v", result.err)
	}
	if msg := cmd(); msg != nil {
		t.Fatal("queued work started after shutdown")
	}
}

func TestPreviewCacheInvalidatesByContentWidthAndCheckpoint(t *testing.T) {
	m, _, _ := newTestModel(t)
	defer m.Close()
	ticket := storage.Ticket{ID: 1, Body: "original body"}
	first := m.bodyPreview(ticket, 20)
	ticket.Body = "changed body"
	if m.bodyPreview(ticket, 20) == first {
		t.Fatal("stale body preview")
	}
	ticket.FocusPaused = true
	ticket.LatestCheckpoint = &storage.PauseCheckpoint{NextAction: "fresh next action"}
	if !strings.Contains(ansiStrip(m.bodyPreview(ticket, 40)), "fresh next action") {
		t.Fatal("stale checkpoint preview")
	}
	if len(m.renderCache.previews) != 3 {
		t.Fatal("preview keys did not separate content and width")
	}
	for width := 10; width < 150; width++ {
		m.bodyPreview(ticket, width)
	}
	if len(m.renderCache.previews) > 128 {
		t.Fatal("unbounded preview cache")
	}
}
