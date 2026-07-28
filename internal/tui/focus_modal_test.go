package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestResumeViewWrapsLongContentAndKeepsControlsAt80x24(t *testing.T) {
	long := strings.Repeat("long checkpoint content ", 20)
	m := Model{
		width:  80,
		height: 24,
		resumeTicket: storage.Ticket{
			DisplayID: "GH-334",
			Title:     long,
			Body:      long,
			LatestCheckpoint: &storage.PauseCheckpoint{
				Why: long, Completed: long, NextAction: long, PausedAt: time.Now(),
			},
		},
	}
	for _, scroll := range []int{0, 12, 1000} {
		m.modalScroll = scroll
		view := m.resumeView()
		lines := strings.Split(view, "\n")
		if len(lines) > 24 {
			t.Fatalf("scroll=%d rendered %d rows", scroll, len(lines))
		}
		for i, line := range lines {
			if displayWidth(line) > 78 {
				t.Fatalf("scroll=%d line %d width=%d: %q", scroll, i, displayWidth(line), line)
			}
		}
		if !strings.Contains(view, "Resume GH-334") || !strings.Contains(view, "r resume   s resume + send handoff   Esc cancel") {
			t.Fatalf("scroll=%d hid fixed title/actions:\n%s", scroll, view)
		}
	}
}
