package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/charmbracelet/lipgloss"
)

func TestModalBackdropPreservesBothMarginsAndCellWidths(t *testing.T) {
	for _, background := range []string{strings.Repeat("x", 20), strings.Repeat("界", 10)} {
		frame := overlayModal(strings.Repeat(background+"\n", 4), "POPUP", 20, 5)
		lines := strings.Split(frame, "\n")
		if len(lines) != 5 {
			t.Fatalf("overlay height=%d", len(lines))
		}
		for _, line := range lines {
			if lipgloss.Width(line) != 20 {
				t.Fatalf("overlay broke cell alignment: %q", line)
			}
		}
		runes := []rune(background)
		if !strings.Contains(ansiStrip(lines[2]), "POPUP") || !strings.HasSuffix(ansiStrip(lines[2]), string(runes[len(runes)-1])) {
			t.Fatalf("overlay discarded a margin: %q", lines[2])
		}
	}
}

func TestBoardPickerKeepsSelectedDetailsAndAllControlsVisible(t *testing.T) {
	for _, width := range []int{80, 160} {
		m := Model{width: width, height: 24, boardIndex: 25}
		for i := 0; i < 32; i++ {
			m.boards = append(m.boards, storage.Board{
				Name:    fmt.Sprintf("Project %02d", i),
				Workdir: "/very/long/" + strings.Repeat("parent/", 20) + "项目/checkout",
			})
		}
		m.boards[24].LastSyncError = sqlNullStr("temporary provider failure")
		frame := ansiStrip(m.boardPickerView())
		for _, want := range []string{"Project 24", "Directory: …", "项目/checkout", "temporary provider failure", "kanbi sync --board Project 24", "Enter select", "j/k move", "Esc cancel", "c create", "r rename", "w cwd", "t worktrees", "a archive", "s sync", "e export", "i import", "A archived", "d delete"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("width=%d lost %q:\n%s", width, want, frame)
			}
		}
		if strings.Count(frame, "Directory:") != 1 || strings.Contains(frame, "Project 00") || lipgloss.Height(frame) > 22 {
			t.Fatalf("picker failed to window its list: %s", frame)
		}
		for _, line := range strings.Split(frame, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("picker exceeded terminal width: %q", line)
			}
		}
	}
}

func TestMasterCardMovesOwnershipWithoutAddingRows(t *testing.T) {
	ticket := storage.Ticket{DisplayID: "T-001", Title: "Fix scrolling", BoardName: "agent-kanban", Harness: "pi", Runtime: kanban.StateNotStarted}
	frame := ansiStrip(strings.Join(cardView(false, ticket, 39, true), "\n"))
	if strings.Contains(frame, "[agent-kanban]") || !strings.Contains(frame, "not started · agent-kanban") {
		t.Fatalf("Master identity was not moved to metadata: %s", frame)
	}
	if got, want := len(cardView(false, ticket, 39, true)), len(cardView(false, ticket, 39, false)); got != want {
		t.Fatalf("Master added rows: %d/%d", got, want)
	}
	ticket.Harness = "copilot"
	ticket.FocusMember, ticket.FocusPaused = true, true
	frame = ansiStrip(strings.Join(cardView(false, ticket, 30, true), "\n"))
	if !strings.Contains(frame, "agent-kanban") || !strings.Contains(frame, "copilot") || !strings.Contains(frame, "⏸ paused") {
		t.Fatalf("crowded Master card lost ownership or runtime: %s", frame)
	}
}

func TestFocusMetadataPreservesRuntimeLabelsAndIndicators(t *testing.T) {
	for _, state := range []string{kanban.StateNotStarted, kanban.StateNeedsPermission, kanban.StateWaitingForUser, kanban.StateError} {
		ticket := storage.Ticket{Harness: "copilot", Runtime: state, FocusMember: true, FocusPaused: true}
		for _, width := range []int{24, 33} {
			line := cardStatusLine(ticket, width)
			if !strings.Contains(line, runtimeLabel(ticket)) || !strings.Contains(line, "paused") || strings.Contains(line, "· ·") || lipgloss.Width(line) > width {
				t.Fatalf("width=%d lost readable Focus/runtime state: %q", width, line)
			}
			if indicator := runtimeIndicator(ticket); indicator != "·" && !strings.Contains(line, indicator) {
				t.Fatalf("lost attention indicator %q: %q", indicator, line)
			}
		}
	}
}

func TestInspectorOutputEmptyStatesDoNotHideStaleContent(t *testing.T) {
	m, store, ctx := newTestModel(t)
	v := defaultBoardView(t, ctx, store)
	createTicket(t, ctx, store, v.Columns[0].ID, "Title", "Task", "pi")
	m.reload()
	m.inspector.open, m.inspector.section = true, 1
	for _, width := range []int{80, 160} {
		m.width, m.height = width, 24
		m.inspector.preview = multiplexer.TerminalPreview{}
		m.inspector.err = "no active terminal container"
		frame := ansiStrip(m.inspectorView(m.boardContentHeight() + 2))
		for _, want := range []string{"No terminal output", "No active agent session.", "Enter to open the agent."} {
			if !strings.Contains(frame, want) {
				t.Fatalf("width=%d empty state missing %q: %s", width, want, frame)
			}
		}
		if strings.Contains(frame, "Unavailable") || strings.Count(frame, "No terminal output") != 1 {
			t.Fatalf("duplicated empty state: %s", frame)
		}
		m.inspector.preview = multiplexer.TerminalPreview{Text: "retained screen", Source: "visible screen", CapturedAt: time.Now().Add(-time.Minute)}
		m.inspector.err = "capture failed"
		frame = ansiStrip(m.inspectorView(m.boardContentHeight() + 2))
		for _, want := range []string{"STALE", "capture failed", "retained screen"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("stale capture lost %q: %s", want, frame)
			}
		}
		m.inspector.preview = multiplexer.TerminalPreview{Source: "visible screen", CapturedAt: time.Now()}
		m.inspector.err = ""
		frame = ansiStrip(m.inspectorView(m.boardContentHeight() + 2))
		if !strings.Contains(frame, "Terminal screen is empty.") || strings.Contains(frame, "No active agent session") {
			t.Fatalf("empty captured screen mistaken for unavailable output: %s", frame)
		}
	}
}
