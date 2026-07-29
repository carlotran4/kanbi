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

// visibleCardRange uses the same row accounting for cursor following and
// rendering. The vertical overflow hints consume rows from the card viewport.
func (m *Model) visibleCardRange(ci int, col storage.Column, scrollTop, columnWidth int) (end int, showAbove, showBelow bool) {
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
		// FOCUSED is always visible. Empty focused and paused sections add
		// their compact messages outside cardView.
		avail--
		if focusedCount == 0 {
			avail--
		}
	}
	if avail < 1 {
		avail = 1
	}
	showAbove = scrollTop > 0
	if showAbove {
		avail--
	}
	if avail < 1 {
		avail = 1
	}

	used := 0
	end = scrollTop - 1
	for ti := scrollTop; ti < len(col.Tickets); ti++ {
		h := cardHeightEx(col.Tickets[ti], columnWidth, ci == m.col && ti == m.card, m.masterBoard)
		section := focusTicketSection(col.Tickets[ti])
		previousSection := -1
		if ti > scrollTop {
			previousSection = focusTicketSection(col.Tickets[ti-1])
		}
		if isFocusColumn && section == focusSectionPaused && (ti == scrollTop || previousSection != focusSectionPaused) {
			// Keep PAUSED authoritative even when scrolling begins inside the
			// paused partition.
			h++
		}
		if isFocusColumn && section == focusSectionArchived && (ti == scrollTop || previousSection != focusSectionArchived) {
			if pausedCount == 0 {
				h += 2 // empty PAUSED label/message
			}
			h++ // ARCHIVED label
		}
		if isFocusColumn && pausedCount == 0 && archivedCount == 0 && ti == len(col.Tickets)-1 {
			// Reserve the empty PAUSED label/message only when the final focused
			// card and that compact section can both fit.
			h += 2
		}
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
		used = cardHeightEx(col.Tickets[scrollTop], columnWidth, ci == m.col && scrollTop == m.card, m.masterBoard)
	}
	showBelow = end < len(col.Tickets)-1 && used < avail
	return end, showAbove, showBelow
}

// cardHeight returns the exact number of lines rendered by cardView at the
// supplied outer card width.
func cardHeight(ticket storage.Ticket, width int) int {
	return cardHeightEx(ticket, width, false, false)
}

func cardHeightEx(ticket storage.Ticket, width int, focused, showBoard bool) int {
	cardInnerWidth := width - 4
	titleLines := wrapText(cardTitle(ticket, showBoard), cardInnerWidth-2, 3)
	if len(titleLines) == 0 {
		titleLines = []string{ticket.DisplayID}
	}
	previewLines := 0
	if focused {
		previewWidth := cardInnerWidth - 4
		if previewWidth < 10 {
			previewWidth = 10
		}
		previewBody := ticket.Body
		if ticket.FocusPaused && ticket.LatestCheckpoint != nil {
			previewBody = "Next: " + ticket.LatestCheckpoint.NextAction
		}
		preview := renderBodyPreview(previewBody, previewWidth)
		for _, pl := range strings.Split(preview, "\n") {
			if strings.TrimSpace(pl) != "" {
				previewLines++
			}
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
	// Scroll down: advance offset until focused card is visible.
	for {
		visibleEnd, _, _ := m.visibleCardRange(m.col, col, m.colScroll[m.col], columnWidth)
		if m.card <= visibleEnd {
			break
		}
		m.colScroll[m.col]++
	}
	// Backfill newly available height after a resize while keeping the selected
	// card visible. This prevents stale (+N more ▲) hints after widening.
	for m.colScroll[m.col] > 0 {
		candidate := m.colScroll[m.col] - 1
		visibleEnd, _, _ := m.visibleCardRange(m.col, col, candidate, columnWidth)
		if m.card > visibleEnd {
			break
		}
		m.colScroll[m.col] = candidate
	}
	// Scroll up: retreat offset if focused card is above visible window.
	for m.card < m.colScroll[m.col] {
		m.colScroll[m.col]--
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
