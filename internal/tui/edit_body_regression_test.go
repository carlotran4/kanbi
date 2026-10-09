package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLongBodyEditPreservesContentsAndImageNumbering(t *testing.T) {
	store, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, store)
	body := "Unicode café 界\n![](/tmp/performance-missing.png)\n" + strings.Repeat("markdown **body** ", 3800)
	ticket := createTicket(t, ctx, store, view.Columns[0].ID, "Long body", body, "pi")
	model := New(ctx, NewService(store, nil))
	defer model.Close()
	model.width, model.height = 160, 40
	model, _ = mustUpdate(t, model, "e")
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	for _, r := range "PERF_BODY_EDIT_" {
		model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		_ = model.View()
	}
	model, _ = mustUpdateKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlS})
	got, err := store.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(body, "![](", "![image 1](", 1) + "PERF_BODY_EDIT_"
	if got.Body != want {
		t.Fatalf("saved body differs: got %d bytes, want %d", len(got.Body), len(want))
	}
}
