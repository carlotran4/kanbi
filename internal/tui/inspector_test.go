package tui

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func inspectorPress(m Model, key string) Model {
	var msg tea.KeyMsg
	switch key {
	case "tab":
		msg.Type = tea.KeyTab
	case "esc":
		msg.Type = tea.KeyEsc
	default:
		msg.Type = tea.KeyRunes
		msg.Runes = []rune(key)
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestInspectorStableCardsAndNarrowReturn(t *testing.T) {
	m, store, ctx := newTestModel(t)
	v := defaultBoardView(t, ctx, store)
	for i := 0; i < 12; i++ {
		createTicket(t, ctx, store, v.Columns[0].ID, "Card", strings.Repeat("body preview ", 30), "pi")
	}
	m.reload()
	m.width, m.height = 160, 45
	m.card = 5
	m.colScroll[0] = 4
	selected, _ := m.selectedTicket()
	m = inspectorPress(m, "i")
	anchor := m.colScroll[0]
	width, _ := m.boardColumnLayout()
	first := m.cachedCardHeight(selected, width, true)
	if m.cardPreview(selected, width, true) != "" {
		t.Fatal("inline preview visible")
	}
	m = inspectorPress(m, "j")
	if h := m.cachedCardHeight(selected, width, false); h != first {
		t.Fatalf("selection shifts card height: %d/%d", h, first)
	}
	m = inspectorPress(m, "k")
	if got, _ := m.selectedTicket(); got.ID != selected.ID {
		t.Fatal("selection changed")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	if frame := m.View(); !strings.Contains(frame, "INSPECTOR") || lipgloss.Height(frame) > 24 {
		t.Fatalf("narrow frame height=%d\n%s", lipgloss.Height(frame), frame)
	}
	m = inspectorPress(m, "j")
	if got, _ := m.selectedTicket(); got.ID != selected.ID {
		t.Fatal("detail scroll moved card")
	}
	m = inspectorPress(m, "esc")
	if m.inspector.open || m.colScroll[0] != anchor {
		t.Fatalf("return lost anchor: %v %d/%d", m.inspector.open, m.colScroll[0], anchor)
	}
	if m.cardPreview(selected, width, true) == "" {
		t.Fatal("normal preview not restored")
	}
}

func TestInspectorBoundsAndReadOnlyFocus(t *testing.T) {
	m, store, ctx := newTestModel(t)
	v := defaultBoardView(t, ctx, store)
	ticket := createTicket(t, ctx, store, v.Columns[0].ID, "Title", strings.Repeat("long line\n", 200), "pi")
	m.reload()
	m.inspector.open = true
	for _, size := range [][2]int{{160, 45}, {120, 24}, {80, 24}} {
		m.width, m.height = size[0], size[1]
		for section := 0; section < 3; section++ {
			m.inspector.section = section
			m.inspector.preview = multiplexer.TerminalPreview{Text: "\x1b[31m" + strings.Repeat("XX", 300) + "\x1b[2J\n" + strings.Repeat("line\n", 200), CapturedAt: time.Now().Add(-time.Minute), Source: "visible screen"}
			m.inspector.scroll = 10000
			m.inspector.horizontal = 10000
			frame := m.View()
			if lipgloss.Height(frame) > m.height {
				t.Fatalf("%v section %d height %d", size, section, lipgloss.Height(frame))
			}
			for _, line := range strings.Split(frame, "\n") {
				if lipgloss.Width(line) > m.width {
					t.Fatalf("%v line width %d: %q", size, lipgloss.Width(line), line)
				}
			}
			if !strings.Contains(frame, "Enter:open") && !strings.Contains(frame, "Enter:send/open") {
				t.Fatal("footer missing")
			}
		}
	}
	latest, err := store.TicketByID(ctx, ticket.ID)
	if err != nil || latest.SessionID.Valid {
		t.Fatal("inspection created session")
	}
}

func TestPreviewSanitizer(t *testing.T) {
	input := "\x1b[31mred\x1b[0m\x1b[2J\x1b[H\x1b]52;c;clipboard\x07\x1b]8;;url\x1b\\link\x1b]8;;\x1b\\\x1b_Gpayload\x1b\\\x1bPbad\x1b\\\r\x07\u009b2J"
	clean := safePreviewANSI(input)
	if strings.Contains(clean, "clipboard") || strings.Contains(clean, "payload") || strings.Contains(clean, "bad") || strings.Contains(clean, "\x1b[2J") || strings.Contains(clean, "\u009b") {
		t.Fatalf("unsafe output %q", clean)
	}
	if !strings.Contains(clean, "\x1b[31mred\x1b[0m") || !strings.Contains(clean, "link") {
		t.Fatalf("lost supported styling %q", clean)
	}
}

type previewTestActions struct {
	Actions
	calls int
}

func (a *previewTestActions) PreviewTicket(context.Context, storage.Ticket) (multiplexer.TerminalPreview, error) {
	a.calls++
	return multiplexer.TerminalPreview{Text: "screen", Source: "visible screen", CapturedAt: time.Now()}, nil
}
func TestInspectorCaptureSelectionAndFailure(t *testing.T) {
	m, store, ctx := newTestModel(t)
	v := defaultBoardView(t, ctx, store)
	for i := 0; i < 2; i++ {
		ticket := createTicket(t, ctx, store, v.Columns[0].ID, "active", "body", "pi")
		_, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", Multiplexer: "herdr", MuxContainerID: sql.NullString{String: "pane", Valid: true}, Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
	}
	m.reload()
	actions := &previewTestActions{Actions: app.NewService(store, nil)}
	m.actions = actions
	m.inspector.open = true
	m.inspector.section = 1
	capture := m.syncInspector()
	if capture == nil {
		t.Fatal("capture absent")
	}
	m.moveCard(1)
	m.syncInspector()
	old := capture().(inspectorPreviewMsg)
	next, _ := m.update(old)
	m = next.(Model)
	if m.inspector.preview.Text != "" {
		t.Fatal("old selection applied")
	}
	cmd := m.syncInspector()
	if cmd == nil {
		t.Fatal("new selection not scheduled")
	}
	next, _ = m.update(cmd())
	m = next.(Model)
	next, _ = m.update(inspectorPreviewMsg{generation: m.inspector.generation, err: errors.New("read failed")})
	m = next.(Model)
	if m.inspector.preview.Text != "screen" || !strings.Contains(m.inspectorView(20), "STALE") {
		t.Fatal("failure discarded last observation or hid staleness")
	}
	m.inspector.open = false
	if m.syncInspector() != nil {
		t.Fatal("closed capture")
	}
	if actions.calls != 2 {
		t.Fatal(actions.calls)
	}
}

func TestInspectorCheckpointWorktreeAndColorCarry(t *testing.T) {
	m, store, ctx := newTestModel(t)
	v := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, v.Columns[0].ID, "Title", "Task", "pi")
	m.reload()
	ticket := &m.view.Columns[0].Tickets[0]
	ticket.ColumnName = "Real owning column"
	ticket.LatestCheckpoint = &storage.PauseCheckpoint{Why: "Pause reason", Completed: "Completed work", NextAction: "Exact next action", PausedAt: time.Now()}
	ticket.WorkspaceID = sql.NullInt64{Int64: 1, Valid: true}
	ticket.WorkspaceBranch = sql.NullString{String: "feature/inspector-branch", Valid: true}
	ticket.WorkspaceStatusJSON = sql.NullString{String: `{"dirty":true,"changed_files":3}`, Valid: true}
	m.inspector.open = true
	frame := m.inspectorView(35)
	for _, want := range []string{"Real owning column", "LATEST PAUSE CHECKPOINT", "Pause reason", "Completed work", "Exact next action"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("missing %q\n%s", want, frame)
		}
	}
	ticket.Body = strings.Repeat("long task body ", 20000)
	m.inspector.scroll = 1 << 20
	if tail := m.inspectorView(35); !strings.Contains(tail, "Exact next action") {
		t.Fatal("long task hid checkpoint")
	}
	m.inspector.scroll = 0
	m.inspector.section = 2
	frame = m.inspectorView(35)
	for _, want := range []string{"feature/inspector-branch", "3 files", "age unknown"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("missing %q\n%s", want, frame)
		}
	}
	lines := styledPreviewLines("\x1b[31mfirst\nsecond\n\x1b[0mthird\nfourth")
	if !strings.HasPrefix(lines[1], "\x1b[31m") || lines[3] != "fourth" {
		t.Fatalf("color carry %q", lines)
	}
}
