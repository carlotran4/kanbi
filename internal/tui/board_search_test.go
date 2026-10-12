package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestBoardSearchTitleOnlyScopeAndSafeNavigation(t *testing.T) {
	for _, master := range []bool{false, true} {
		t.Run(fmt.Sprintf("master=%t", master), func(t *testing.T) {
			store, ctx := newTestStore(t)
			v := defaultBoardView(t, ctx, store)
			first := createTicket(t, ctx, store, v.Columns[0].ID, "Token first", "", "pi")
			createTicket(t, ctx, store, v.Columns[0].ID, "Unrelated", "token in body", "pi")
			second := createTicket(t, ctx, store, v.Columns[3].ID, "TOKEN last", "", "pi")
			other, err := store.CreateBoard(ctx, "Token board")
			if err != nil {
				t.Fatal(err)
			}
			ov, _ := store.BoardViewByID(ctx, other.ID)
			createTicket(t, ctx, store, ov.Columns[0].ID, "Token elsewhere", "", "pi")
			actions := &openingStore{Service: app.NewService(store, nil)}
			m := New(ctx, actions)
			defer m.Close()
			m.masterBoard = master
			if master {
				m.masterFilter = storage.MasterFilter{BoardIDs: []int64{v.Board.ID}, Harnesses: []string{"pi"}}
			}
			m.reload()
			m.width, m.height = 80, 24
			before, _ := store.ListTickets(ctx, true)
			m, _ = mustUpdate(t, m, "/")
			m, _ = mustUpdate(t, m, "token")
			if got := tuiTicketTitles(m); !reflect.DeepEqual(got, []string{"Token first", "TOKEN last"}) {
				t.Fatalf("title/scope search=%v", got)
			}
			if len(m.view.Columns) != len(v.Columns) {
				t.Fatal("search removed columns")
			}
			if tk, _ := m.selectedTicket(); tk.ID != first.ID {
				t.Fatal("initial match incorrect")
			}
			m, cmd := mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil {
				t.Fatal("match Enter dispatched a command")
			}
			if tk, _ := m.selectedTicket(); tk.ID != second.ID || m.colOffset == 0 {
				t.Fatal("next match did not reveal horizontal overflow")
			}
			m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if tk, _ := m.selectedTicket(); tk.ID != first.ID {
				t.Fatal("forward did not wrap")
			}
			m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
			if tk, _ := m.selectedTicket(); tk.ID != second.ID {
				t.Fatal("backward did not wrap")
			}
			m, _ = mustUpdate(t, m, "nxHLbfiq?")
			if !m.search.active || m.editing || m.boardPicker || m.masterFilterOpen || m.inspector.open {
				t.Fatal("query triggered a board shortcut")
			}
			if _, count := m.searchPosition(); count != 0 {
				t.Fatal("expected zero matches")
			}
			if _, ok := m.selectedTicket(); ok {
				t.Fatal("no matches retained hidden keyboard target")
			}
			if view := m.View(); !strings.Contains(view, "0 matches") || !strings.Contains(view, "No title matches") || strings.Contains(view, "Press n") {
				t.Fatalf("unclear no-match frame\n%s", view)
			}
			m, cmd = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil {
				t.Fatal("no-match Enter dispatched action")
			}
			m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.search.active {
				t.Fatal("Escape did not clear search")
			}
			for _, excluded := range []string{"T-001", "Default", "pi", "token in body"} {
				m, _ = mustUpdate(t, m, "/")
				m, _ = mustUpdate(t, m, excluded)
				if _, count := m.searchPosition(); count != 0 {
					t.Fatalf("matched a non-title field: %q", excluded)
				}
				m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			}
			after, _ := store.ListTickets(ctx, true)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("search mutated durable tickets")
			}
			if len(actions.opened) > 0 || actions.pastedPrompt != "" {
				t.Fatal("search opened/pasted to terminal")
			}
			for _, tk := range after {
				if _, ok, e := store.LatestSession(ctx, tk.ID); e != nil || ok {
					t.Fatal("search created a session")
				}
			}
		})
	}
}

func TestBoardSearchUnicodeLiteralEditingAndLongQuery(t *testing.T) {
	m, s, ctx := newTestModel(t)
	defer m.Close()
	v := defaultBoardView(t, ctx, s)
	createTicket(t, ctx, s, v.Columns[0].ID, "ÉCOLE κόσμος 日本語 c++ [fix] 🐱", "", "pi")
	m.reload()
	m.width, m.height = 80, 24
	for _, query := range []string{"école", "ΚΌΣΜΟΣ", "日本語", "c++ [fix]", "🐱", strings.Repeat("日本語🐱", 200)} {
		m, _ = mustUpdate(t, m, "/")
		m, _ = mustUpdate(t, m, query)
		_, count := m.searchPosition()
		want := 1
		if len(query) > 100 {
			want = 0
		}
		if count != want {
			t.Fatalf("query %q matches=%d", query, count)
		}
		frame := m.View()
		if lipgloss.Height(frame) > 24 {
			t.Fatalf("query overflowed height %d", lipgloss.Height(frame))
		}
		for _, line := range strings.Split(frame, "\n") {
			if lipgloss.Width(line) > 80 {
				t.Fatalf("query overflowed width: %q", line)
			}
		}
		m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyHome})
		m, _ = mustUpdate(t, m, "x")
		m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
		if m.search.query.Value() != query {
			t.Fatal("cursor insertion/backspace failed")
		}
		m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyHome})
		m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyDelete})
		if m.search.query.Value() == query {
			t.Fatal("delete failed")
		}
		m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	}
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q\n日"), Paste: true})
	if m.search.query.Value() != "q 日" || !m.search.active {
		t.Fatal("paste activated shortcuts or inserted newlines")
	}
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = mustUpdate(t, m, "/")
	for _, literal := range []string{"enter", "esc", "ctrl+p", "left", "home", "ctrl+c"} {
		m, _ = mustUpdate(t, m, literal)
	}
	if m.search.query.Value() != "enterescctrl+plefthomectrl+c" || !m.search.active {
		t.Fatal("coalesced printable input was interpreted as control keys")
	}
}

func TestBoardSearchMissingMatchAndRemovedColumns(t *testing.T) {
	m, store, ctx := newTestModel(t)
	defer m.Close()
	v := defaultBoardView(t, ctx, store)
	for i := 0; i < 3; i++ {
		createTicket(t, ctx, store, v.Columns[0].ID, fmt.Sprintf("find %d", i), "", "pi")
	}
	m.reload()
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "find")
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// Model an externally deleted row without destroying durable test history.
	snapshot, err := m.readSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	removed := snapshot.view.Columns[0].Tickets[1].ID
	last := snapshot.view.Columns[0].Tickets[2].ID
	snapshot.view.Columns[0].Tickets = append(snapshot.view.Columns[0].Tickets[:1:1], snapshot.view.Columns[0].Tickets[2:]...)
	m.applySnapshot(snapshot)
	if selected, _ := m.selectedTicket(); selected.ID == removed || selected.ID != last {
		t.Fatal("lost match did not advance to the next surviving ordinal")
	}
	m.applySnapshot(boardSnapshot{view: storage.BoardView{Board: snapshot.view.Board}})
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.search.active || m.col != 0 || m.card != 0 || m.colOffset != 0 {
		t.Fatal("removed columns prevented safe cancellation")
	}
}

func TestBoardSearchRestoresAnchorsAndTracksRefresh(t *testing.T) {
	m, s, ctx := newTestModel(t)
	defer m.Close()
	v := defaultBoardView(t, ctx, s)
	for i := 0; i < 20; i++ {
		createTicket(t, ctx, s, v.Columns[0].ID, fmt.Sprintf("original %d", i), "preview", "pi")
	}
	target := createTicket(t, ctx, s, v.Columns[3].ID, "find target", "", "pi")
	m.reload()
	m.width, m.height = 80, 24
	m.col = 0
	m.card = 7
	m.colScroll[0] = 6
	original, _ := m.selectedTicket()
	savedScroll := append([]int(nil), m.colScroll...)
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "find")
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got, _ := m.selectedTicket(); got.ID != original.ID || !reflect.DeepEqual(m.colScroll, savedScroll) || m.colOffset != 0 {
		t.Fatalf("did not restore selection/viewport: %d %v", got.ID, m.colScroll)
	}
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "find")
	if err := s.MoveTicket(ctx, target.ID, v.Columns[2].ID); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if got, _ := m.selectedTicket(); got.ID != target.ID || m.col != 2 {
		t.Fatal("refresh lost moved match")
	}
	if err := s.MoveTicket(ctx, original.ID, v.Columns[1].ID); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got, _ := m.selectedTicket(); got.ID != original.ID || m.col != 1 {
		t.Fatal("Escape failed to restore moved original")
	}
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "find")
	if err := s.ArchiveTicket(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got, ok := m.selectedTicket(); !ok || got.ID == original.ID {
		t.Fatal("Escape did not select surviving neighbor")
	}
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "find")
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if got, _ := m.selectedTicket(); got.ID != target.ID || m.search.active {
		t.Fatal("keep found selection failed")
	}
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "find")
	if err := s.UpdateTicket(ctx, target.ID, "renamed", "", "pi"); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if _, count := m.searchPosition(); count != 0 {
		t.Fatal("refresh failed to remove renamed match")
	}
	// Clearing the query restores the latest full board without ending search.
	for range len("find") {
		m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if len(tuiTicketTitles(m)) != 20 || !m.search.active {
		t.Fatal("empty query did not restore full board")
	}
}

func TestBoardSearchInspectorResizeAndVerticalOverflow(t *testing.T) {
	m, s, ctx := newTestModel(t)
	defer m.Close()
	v := defaultBoardView(t, ctx, s)
	for i := 0; i < 30; i++ {
		createTicket(t, ctx, s, v.Columns[0].ID, fmt.Sprintf("match %02d", i), "INLINE BODY SHOULD STAY HIDDEN", "pi")
	}
	m.reload()
	m.width, m.height = 160, 45
	m, _ = mustUpdate(t, m, "i")
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "match")
	for range 12 {
		m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	selected, _ := m.selectedTicket()
	if m.inspector.ticketID != selected.ID || m.colScroll[0] == 0 {
		t.Fatal("match navigation did not scroll/follow inspector")
	}
	if m.cardPreview(selected, 39, true) != "" {
		t.Fatal("search enabled inline preview")
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 160, Height: 45}} {
		next, _ := m.Update(size)
		m = next.(Model)
		frame := m.View()
		if lipgloss.Height(frame) > size.Height {
			t.Fatalf("frame too tall %d", lipgloss.Height(frame))
		}
		for _, line := range strings.Split(frame, "\n") {
			if lipgloss.Width(line) > size.Width {
				t.Fatalf("frame too wide %d", lipgloss.Width(line))
			}
		}
		if size.Width == 80 && strings.Contains(frame, "INSPECTOR") {
			t.Fatal("narrow search hid board behind inspector")
		}
		if size.Width == 160 && !strings.Contains(frame, "INSPECTOR") {
			t.Fatal("wide inspector disappeared")
		}
		width, _ := m.boardColumnLayout()
		end, _, _ := m.visibleCardRange(m.col, m.view.Columns[m.col], m.colScroll[m.col], width)
		if m.card < m.colScroll[m.col] || m.card > end {
			t.Fatal("resize hid match")
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.inspector.open || !strings.Contains(m.View(), "INSPECTOR") {
		t.Fatal("exit failed to restore narrow inspector")
	}
}

func TestBoardSearchMasterCancellationUsesWorkflowAnchors(t *testing.T) {
	m, _, _ := newTestModel(t)
	defer m.Close()
	m.masterBoard = true
	m.width, m.height = 80, 24
	m.view = storage.BoardView{Board: storage.Board{Name: "Master"}, Columns: []storage.Column{
		{ID: -1, Name: "Open", WorkflowKey: "open", Tickets: []storage.Ticket{{ID: 1, Title: "other"}}},
		{ID: -2, Name: "Review", WorkflowKey: "review", Tickets: []storage.Ticket{{ID: 10, Title: "old"}, {ID: 11, Title: "anchor"}, {ID: 12, Title: "original"}}},
		{ID: -3, Name: "Done", WorkflowKey: "done", Tickets: []storage.Ticket{{ID: 20, Title: "find"}}},
	}}
	m.col, m.card, m.colOffset = 1, 2, 0
	m.colScroll = []int{0, 1, 0}
	m, _ = mustUpdate(t, m, "/")
	m, _ = mustUpdate(t, m, "find")
	// Removing a Master workflow renumbers synthetic IDs for surviving columns.
	remaining := append([]storage.Column(nil), m.search.source.Columns[1:]...)
	remaining[0].ID, remaining[1].ID = -1, -2
	m.applySnapshot(boardSnapshot{view: storage.BoardView{Board: m.view.Board, Columns: remaining}})
	m, _ = mustUpdateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if selected, _ := m.selectedTicket(); selected.ID != 12 || m.col != 0 || m.colScroll[0] != 1 {
		t.Fatalf("Master restored positional ID instead of workflow: ticket=%d col=%d scroll=%v", selected.ID, m.col, m.colScroll)
	}
}
