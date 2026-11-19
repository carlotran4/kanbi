package tui

import (
	"errors"
	"testing"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/devreload"
)

func TestDevReloadDefersAndRestoresNavigation(t *testing.T) {
	store, ctx := newTestStore(t)
	v := defaultBoardView(t, ctx, store)
	ticket := createTicket(t, ctx, store, v.Columns[1].ID, "reload selection", "", "pi")
	m := New(ctx, &openingStore{Service: app.NewService(store, nil)})
	defer m.Close()
	m.boardID = v.Board.ID
	m.reload()
	m.selectSearchTicket(ticket.ID)
	if m.devTick() != nil {
		t.Fatal("reload active by default")
	}
	m.EnableDevReload("")
	m.updateDevReload(devreload.Result{Path: "/fixture/kanbi"})
	for name, block := range map[string]func(*Model){
		"external editor": func(m *Model) { m.devEditor = true },
		"draft":           func(m *Model) { m.editing = true },
		"fleet":           func(m *Model) { m.fleet.active = true },
		"fleet startup":   func(m *Model) { m.fleet.busy = true },
		"lifecycle":       func(m *Model) { m.devActions = 1 },
		"search":          func(m *Model) { m.search.active = true },
		"operator":        func(m *Model) { m.operators.open = true },
	} {
		t.Run(name, func(t *testing.T) {
			next := m
			block(&next)
			next.updateDevReload(devTickMsg{})
			if next.devRestart {
				t.Fatal("restarted during protected interaction")
			}
		})
	}
	m.updateDevReload(devTickMsg{})
	path, saved := m.DevRestart()
	if path != "/fixture/kanbi" {
		t.Fatal("safe build did not restart")
	}
	next := NewWithPicker(ctx, &openingStore{Service: app.NewService(store, nil)})
	defer next.Close()
	next.EnableDevReload(saved)
	selected, ok := next.selectedTicket()
	if !ok || selected.ID != ticket.ID || next.boardID != v.Board.ID || next.boardPicker {
		t.Fatal("navigation lost")
	}
	m.devRestart = false
	m.updateDevReload(devreload.Result{Err: errors.New("build failed"), Log: "fixture.log"})
	if m.devPending != "" {
		t.Fatal("failed build retained restart")
	}
}

func TestDevReloadWaitsForDispatchedLifecycleResult(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, view.Columns[0].ID, "dispatch", "", "pi")
	m := New(ctx, &openingStore{Service: app.NewService(store, nil)})
	defer m.Close()
	m.EnableDevReload("")
	m, _ = mustUpdate(t, m, "enter")
	if m.devActions != 1 {
		t.Fatalf("open not tracked: %d", m.devActions)
	}
	m.devPending = "/fixture/kanbi"
	m.updateDevReload(devTickMsg{})
	if m.devRestart {
		t.Fatal("restarted before session open completed")
	}
	updated, _ := m.update(openTicketMsg{})
	m = updated.(Model)
	if m.devActions != 0 {
		t.Fatal("completed action still blocks reload")
	}
}
