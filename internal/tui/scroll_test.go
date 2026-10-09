package tui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

func TestFocusScrollTopMatchesIncrementalViewportSemantics(t *testing.T) {
	rng := rand.New(rand.NewSource(362))
	for iteration := 0; iteration < 1000; iteration++ {
		count := 1 + rng.Intn(80)
		focused := rng.Intn(count + 1)
		paused := rng.Intn(count - focused + 1)
		tickets := make([]storage.Ticket, count)
		for i := range tickets {
			tickets[i] = storage.Ticket{
				ID: int64(i + 1), DisplayID: fmt.Sprintf("T-%03d", i+1),
				Title: strings.Repeat("ticket ", 1+rng.Intn(8)), Body: strings.Repeat("body ", 1+rng.Intn(20)), FocusMember: true,
			}
			switch {
			case i < focused:
			case i < focused+paused:
				tickets[i].FocusPaused = true
				tickets[i].LatestCheckpoint = &storage.PauseCheckpoint{NextAction: "continue"}
			default:
				tickets[i].ArchivedAt.Valid = true
			}
		}
		selected := rng.Intn(count)
		current := rng.Intn(selected + 1)
		focusEnabled := rng.Intn(2) == 0
		m := Model{
			renderCache: newRenderCache(),
			width:       []int{80, 160}[rng.Intn(2)],
			height:      8 + rng.Intn(38),
			col:         0,
			card:        selected,
			focus:       storage.FocusStatus{Enabled: focusEnabled, WorkflowKeys: []string{"focus"}},
		}
		col := storage.Column{WorkflowKey: "focus", Tickets: tickets}
		columnWidth, _ := m.boardColumnLayout()
		summary := m.summarizeFocusColumn(col)

		want := current
		for {
			end := legacyVisibleCardEnd(&m, 0, col, want, columnWidth)
			if selected <= end {
				break
			}
			want++
		}
		if got := m.scrollTopForCard(0, col, current, selected, columnWidth, summary); got != want {
			t.Fatalf("iteration=%d count=%d focused=%d paused=%d current=%d selected=%d size=%dx%d: scrollTop=%d want %d", iteration, count, focused, paused, current, selected, m.width, m.height, got, want)
		}
	}
}

// legacyVisibleCardEnd is the former incremental range calculation retained as
// a test-only oracle. Keep it independent of the optimized summary helpers.
func legacyVisibleCardEnd(m *Model, ci int, col storage.Column, scrollTop, columnWidth int) int {
	avail := m.boardContentHeight()
	isFocusColumn := m.focus.Enabled && hasFocusKey(m.focus, col.WorkflowKey)
	focusedCount, pausedCount, archivedCount := 0, 0, 0
	if isFocusColumn {
		for _, ticket := range col.Tickets {
			switch focusTicketSection(ticket) {
			case focusSectionPaused:
				pausedCount++
			case focusSectionArchived:
				archivedCount++
			default:
				focusedCount++
			}
		}
		avail--
		if focusedCount == 0 {
			avail--
		}
	}
	if scrollTop > 0 {
		avail--
	}
	avail = maxInt(1, avail)
	used, end := 0, scrollTop-1
	for ti := scrollTop; ti < len(col.Tickets); ti++ {
		h := m.cachedCardHeight(col.Tickets[ti], columnWidth, ci == m.col && ti == m.card)
		section, previousSection := focusTicketSection(col.Tickets[ti]), -1
		if ti > scrollTop {
			previousSection = focusTicketSection(col.Tickets[ti-1])
		}
		if isFocusColumn && section == focusSectionPaused && (ti == scrollTop || previousSection != focusSectionPaused) {
			h++
		}
		if isFocusColumn && section == focusSectionArchived && (ti == scrollTop || previousSection != focusSectionArchived) {
			if pausedCount == 0 {
				h += 2
			}
			h++
		}
		if isFocusColumn && pausedCount == 0 && archivedCount == 0 && ti == len(col.Tickets)-1 {
			h += 2
		}
		reserveBelow := 0
		if ti < len(col.Tickets)-1 {
			reserveBelow = 1
		}
		if used+h+reserveBelow > avail {
			break
		}
		used += h
		end = ti
	}
	if end < scrollTop {
		return scrollTop
	}
	return end
}

func TestFocusHorizontalNavigationKeepsDeepSelectionAndViewport(t *testing.T) {
	m := focusPerformanceModel(true, 80, 24)
	m.moveColumn(1)
	if m.col != 1 || m.card != focusPerformanceCard {
		t.Fatalf("selection changed during horizontal navigation: col=%d card=%d", m.col, m.card)
	}
	if m.colScroll[1] == 0 || m.colScroll[1] > m.card {
		t.Fatalf("destination viewport did not follow deep selection: scroll=%d card=%d", m.colScroll[1], m.card)
	}
	columnWidth, _ := m.boardColumnLayout()
	end, _, _ := m.visibleCardRange(1, m.view.Columns[1], m.colScroll[1], columnWidth)
	if m.card > end {
		t.Fatalf("selected card is outside destination viewport: card=%d end=%d", m.card, end)
	}
}
