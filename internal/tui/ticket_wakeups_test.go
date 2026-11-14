package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/app"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestTicketWakeDialogKeepsTicketIdentityAndValidatesDraft(t *testing.T) {
	s, ctx := newTestStore(t)
	v := defaultBoardView(t, ctx, s)
	ticket := createTicket(t, ctx, s, v.Columns[0].ID, "Check deployment", "", "pi")
	other := createTicket(t, ctx, s, v.Columns[0].ID, "Other ticket", "", "pi")
	m := New(ctx, app.NewService(s, nil))
	m.firstRun = false
	m.width = 160
	m.height = 45
	m.openTicketWakes(ticket)
	m, _ = mustUpdate(t, m, "n")
	m.ticketWakes.at = NewInputBuffer("bad time")
	m.ticketWakes.prompt.SetValue("Check the rollout status")
	m, _ = mustUpdate(t, m, "ctrl+s")
	if !m.ticketWakes.editing || m.ticketWakes.error == "" {
		t.Fatal("invalid time saved")
	}
	m.ticketWakes.at = NewInputBuffer(time.Now().AddDate(0, 0, 1).Format("2006-01-02 15:04"))
	// Simulate a projection/selection change while the draft is open.
	m.col = 0
	m.card = 1
	m, _ = mustUpdate(t, m, "ctrl+s")
	wakes, err := s.TicketWakeups(ctx, ticket.ID)
	if err != nil || len(wakes) != 1 || wakes[0].Prompt != "Check the rollout status" {
		t.Fatal(wakes, err)
	}
	others, _ := s.TicketWakeups(ctx, other.ID)
	if len(others) != 0 {
		t.Fatal("saved to selected ticket instead of draft owner")
	}
	for _, size := range []struct{ w, h int }{{160, 45}, {80, 24}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = next.(Model)
		view := m.ticketWakesView()
		if lipgloss.Width(view) > size.w || lipgloss.Height(view) > size.h {
			t.Fatalf("%dx%d: view %dx%d", size.w, size.h, lipgloss.Width(view), lipgloss.Height(view))
		}
		m, _ = mustUpdate(t, m, "n")
		m.ticketWakes.prompt.SetValue(strings.Repeat("long prompt ", 100))
		view = m.ticketWakesView()
		if lipgloss.Width(view) > size.w || lipgloss.Height(view) > size.h {
			t.Fatalf("editor %dx%d: view %dx%d", size.w, size.h, lipgloss.Width(view), lipgloss.Height(view))
		}
		m, _ = mustUpdate(t, m, "esc")
	}
	m, _ = mustUpdate(t, m, "x")
	wakes, _ = s.TicketWakeups(ctx, ticket.ID)
	if wakes[0].State != "cancelled" {
		t.Fatal(wakes)
	}
	m, _ = mustUpdate(t, m, "esc")
	if m.ticketWakes.open {
		t.Fatal("dialog did not close")
	}
}
