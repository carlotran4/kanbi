package tui

import (
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
)

func (m *Model) syncScrollDimensions() {
	n := len(m.view.Columns)
	for len(m.colScroll) < n {
		m.colScroll = append(m.colScroll, 0)
	}
	if len(m.colScroll) > n {
		m.colScroll = m.colScroll[:n]
	}
}

// boardContentHeight returns the terminal rows available below a column's
// two-line header. Global header/footer rows and the optional horizontal-scroll
// hint are outside this budget.
func (m *Model) boardContentHeight() int {
	fixed := 2 + 2 + 2 // app header + footer + column header
	if m.status != "" {
		fixed++
		if m.errOperation != "" {
			fixed += 2
		}
	}
	if m.refreshError != "" {
		fixed++
	}
	if m.integrationNotice != "" {
		fixed++
	}
	if m.hScrollHint() != "" {
		fixed++
	}
	h := m.height - fixed
	if h < 1 {
		h = 1
	}
	return h
}

type focusColumnSummary struct {
	enabled                                  bool
	focusedCount, pausedCount, archivedCount int
}

func (m *Model) summarizeFocusColumn(col storage.Column) focusColumnSummary {
	summary := focusColumnSummary{enabled: m.focus.Enabled && hasFocusKey(m.focus, col.WorkflowKey)}
	if !summary.enabled {
		return summary
	}
	for _, ticket := range col.Tickets {
		switch focusTicketSection(ticket) {
		case focusSectionPaused:
			summary.pausedCount++
		case focusSectionArchived:
			summary.archivedCount++
		default:
			summary.focusedCount++
		}
	}
	return summary
}

func (m *Model) cardViewportHeight(summary focusColumnSummary, scrollTop int) int {
	avail := m.boardContentHeight()
	if summary.enabled {
		// FOCUSED is always visible. An empty focused section adds its compact
		// message outside cardView.
		avail--
		if summary.focusedCount == 0 {
			avail--
		}
	}
	if scrollTop > 0 {
		avail-- // hidden-above hint
	}
	return maxInt(1, avail)
}

func (m *Model) cardHeightAt(ci int, col storage.Column, ti, scrollTop, columnWidth int, summary focusColumnSummary) int {
	h := m.cachedCardHeight(col.Tickets[ti], columnWidth, ci == m.col && ti == m.card)
	if !summary.enabled {
		return h
	}
	section := focusTicketSection(col.Tickets[ti])
	previousSection := -1
	if ti > scrollTop {
		previousSection = focusTicketSection(col.Tickets[ti-1])
	}
	if section == focusSectionPaused && (ti == scrollTop || previousSection != focusSectionPaused) {
		// Keep PAUSED authoritative even when scrolling begins inside it.
		h++
	}
	if section == focusSectionArchived && (ti == scrollTop || previousSection != focusSectionArchived) {
		if summary.pausedCount == 0 {
			h += 2 // empty PAUSED label/message
		}
		h++ // ARCHIVED label
	}
	if summary.pausedCount == 0 && summary.archivedCount == 0 && ti == len(col.Tickets)-1 {
		// Reserve the empty PAUSED label/message only when the final focused
		// card and that compact section can both fit.
		h += 2
	}
	return h
}

// visibleCardRange uses the same row accounting for cursor following and
// rendering. The vertical overflow hints consume rows from the card viewport.
func (m *Model) visibleCardRange(ci int, col storage.Column, scrollTop, columnWidth int) (end int, showAbove, showBelow bool) {
	return m.visibleCardRangeWithSummary(ci, col, scrollTop, columnWidth, m.summarizeFocusColumn(col))
}

func (m *Model) visibleCardRangeWithSummary(ci int, col storage.Column, scrollTop, columnWidth int, summary focusColumnSummary) (end int, showAbove, showBelow bool) {
	avail := m.cardViewportHeight(summary, scrollTop)
	showAbove = scrollTop > 0
	used := 0
	end = scrollTop - 1
	for ti := scrollTop; ti < len(col.Tickets); ti++ {
		h := m.cardHeightAt(ci, col, ti, scrollTop, columnWidth, summary)
		reserveBelowHint := 0
		if ti < len(col.Tickets)-1 {
			reserveBelowHint = 1
		}
		if used+h+reserveBelowHint > avail {
			break
		}
		used += h
		end = ti
	}
	if end < scrollTop {
		// Extremely short terminals still show the focused/top card. Omit the
		// lower hint if it cannot fit rather than scrolling the whole terminal.
		end = scrollTop
		used = m.cachedCardHeight(col.Tickets[scrollTop], columnWidth, ci == m.col && scrollTop == m.card)
	}
	showBelow = end < len(col.Tickets)-1 && used < avail
	return end, showAbove, showBelow
}

// scrollTopForCard finds the earliest retained viewport anchor that shows the
// selected card. Walking backward from the selection bounds work by visible
// rows; the old forward loop retried a full range for every intervening ticket.
func (m *Model) scrollTopForCard(ci int, col storage.Column, current, selected, columnWidth int, summary focusColumnSummary) int {
	best := selected
	for candidate := selected; candidate >= current; candidate-- {
		avail := m.cardViewportHeight(summary, candidate)
		used := 0
		for ti := candidate; ti <= selected; ti++ {
			used += m.cardHeightAt(ci, col, ti, candidate, columnWidth, summary)
			if used > avail {
				break
			}
		}
		if selected < len(col.Tickets)-1 {
			used++ // lower overflow hint
		}
		if used > avail {
			break
		}
		best = candidate
	}
	return best
}

// cardHeight returns the exact number of lines rendered by cardView at the
// supplied outer card width.
func cardHeight(ticket storage.Ticket, width int) int {
	return cardHeightEx(ticket, width, false, false)
}

func cardHeightEx(ticket storage.Ticket, width int, focused, showBoard bool) int {
	preview := ""
	if focused {
		body := ticket.Body
		if ticket.FocusPaused && ticket.LatestCheckpoint != nil {
			body = "Next: " + ticket.LatestCheckpoint.NextAction
		}
		preview = renderBodyPreview(body, maxInt(10, width-8))
	}
	return cardHeightWithPreview(ticket, width, showBoard, preview)
}

func cardHeightWithPreview(ticket storage.Ticket, width int, showBoard bool, preview string) int {
	cardInnerWidth := width - 4
	titleLines := wrapText(cardTitle(ticket, showBoard), cardInnerWidth-2, 3)
	if len(titleLines) == 0 {
		titleLines = []string{ticket.DisplayID}
	}
	previewLines := 0
	for line := range strings.SplitSeq(preview, "\n") {
		if strings.TrimSpace(line) != "" {
			previewLines++
		}
	}
	workspaceLines := 0
	if ticketWorkspaceCardLine(ticket, cardInnerWidth-2) != "" {
		workspaceLines = 1
	}
	// Top/bottom borders + title + compact status + optional workspace + preview.
	return 2 + len(titleLines) + 1 + workspaceLines + previewLines
}

// vScrollFollow adjusts the scroll offset for the focused column so the
// focused card is always within the visible window.
func (m *Model) vScrollFollow() {
	if m.col < 0 || m.col >= len(m.view.Columns) {
		return
	}
	col := m.view.Columns[m.col]
	if len(col.Tickets) == 0 {
		return
	}
	if len(m.colScroll) <= m.col {
		return
	}

	// Clamp scroll offset first.
	if m.colScroll[m.col] > len(col.Tickets)-1 {
		m.colScroll[m.col] = len(col.Tickets) - 1
	}
	if m.colScroll[m.col] < 0 {
		m.colScroll[m.col] = 0
	}

	columnWidth, _ := m.boardColumnLayout()
	if m.card < m.colScroll[m.col] {
		// Preserve ordinary no-backfill semantics: moving above the viewport
		// anchors the selected card at the top rather than pulling older cards in.
		m.colScroll[m.col] = m.card
		return
	}
	summary := m.summarizeFocusColumn(col)
	m.colScroll[m.col] = m.scrollTopForCard(m.col, col, m.colScroll[m.col], m.card, columnWidth, summary)
}

// Backfill is a resize operation. Ordinary navigation and refresh preserve
// their viewport anchor rather than pulling earlier cards back onto the screen.
func (m *Model) vScrollBackfill() {
	if m.col < 0 || m.col >= len(m.view.Columns) || m.col >= len(m.colScroll) {
		return
	}
	col := m.view.Columns[m.col]
	if len(col.Tickets) == 0 {
		return
	}
	columnWidth, _ := m.boardColumnLayout()
	summary := m.summarizeFocusColumn(col)
	// Backfill newly available height after a resize while keeping the selected
	// card visible. This prevents stale (+N more ▲) hints after widening.
	for m.colScroll[m.col] > 0 {
		candidate := m.colScroll[m.col] - 1
		visibleEnd, _, _ := m.visibleCardRangeWithSummary(m.col, col, candidate, columnWidth, summary)
		if m.card > visibleEnd {
			break
		}
		m.colScroll[m.col] = candidate
	}
}

// hScrollFollow adjusts colOffset so the focused column is always visible.
func (m *Model) hScrollFollow() {
	if len(m.view.Columns) == 0 {
		return
	}
	// Retreat to the earliest viewport that still contains the focused column.
	// This also reveals newly available columns after a narrow-to-wide resize.
	for m.col < m.colOffset {
		m.colOffset--
	}
	for m.colOffset > 0 {
		previous := m.colOffset
		m.colOffset--
		_, visibleCount := m.boardColumnLayout()
		if m.col > m.colOffset+visibleCount-1 {
			m.colOffset = previous
			break
		}
	}
	// Scroll right: advance offset until the focused column is visible.
	for {
		_, visibleCount := m.boardColumnLayout()
		lastVisible := m.colOffset + visibleCount - 1
		if m.col <= lastVisible {
			break
		}
		m.colOffset++
	}
}

func (m *Model) clamp() {
	if len(m.view.Columns) == 0 {
		m.col = 0
		m.card = 0
		return
	}
	if m.col < 0 {
		m.col = 0
	}
	if m.col >= len(m.view.Columns) {
		m.col = len(m.view.Columns) - 1
	}
	maxCard := len(m.view.Columns[m.col].Tickets) - 1
	if maxCard < 0 {
		m.card = 0
		return
	}
	if m.card < 0 {
		m.card = 0
	}
	if m.card > maxCard {
		m.card = maxCard
	}
}

// Centered inspectors leave the app header and every current footer row visible.
// fitModal takes two additional rows for its own outer clipping margin.
func (m Model) inspectorViewportHeight() int {
	footer := 2
	if m.status != "" {
		if m.errOperation != "" {
			footer += 3
		} else {
			footer++
		}
	}
	if m.refreshError != "" {
		footer++
	}
	if m.integrationNotice != "" {
		footer++
	}
	return maxInt(1, m.height-2*maxInt(2, footer))
}
