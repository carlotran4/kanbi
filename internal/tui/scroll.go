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

// boardContentHeight returns the number of terminal rows available for card
// rendering (total height minus header and footer rows).
func (m *Model) boardContentHeight() int {
	// 2 header lines (bar + blank) + 1 rule + 1 hints line = 4 fixed.
	// Status line is conditional.
	headerFooter := 4
	if m.status != "" {
		headerFooter++
	}
	h := m.height - headerFooter
	if h < 4 {
		h = 4
	}
	return h
}

// cardHeight returns the number of rendered lines a single card occupies inside
// a column (box top + title lines + meta line + preview lines + box bottom).
func cardHeight(ticket storage.Ticket, innerWidth int) int {
	return cardHeightEx(ticket, innerWidth, false)
}

func cardHeightEx(ticket storage.Ticket, innerWidth int, focused bool) int {
	cardInnerWidth := innerWidth - 4
	titleLines := wrapText(ticket.DisplayID+" "+ticket.Title, cardInnerWidth-2, 3)
	if len(titleLines) == 0 {
		titleLines = []string{ticket.DisplayID}
	}
	previewLines := 0
	if focused {
		previewWidth := cardInnerWidth - 4
		if previewWidth < 10 {
			previewWidth = 10
		}
		preview := renderBodyPreview(ticket.Body, previewWidth)
		for _, pl := range strings.Split(preview, "\n") {
			if strings.TrimSpace(pl) != "" {
				previewLines++
			}
		}
		if previewLines > 4 {
			previewLines = 4
		}
	}
	stateLines := len(wrapText("["+ticket.Harness+"] "+runtimeLabel(ticket), cardInnerWidth-2, 2))
	if stateLines == 0 {
		stateLines = 1
	}
	sessionLines := len(wrapText("session: "+windowIndicator(ticket), cardInnerWidth-2, 2))
	if sessionLines == 0 {
		sessionLines = 1
	}
	// top/bottom borders + title + textual state/session lines + preview.
	return 2 + len(titleLines) + stateLines + sessionLines + previewLines
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
	inner := boardColumnWidth - 2
	avail := m.boardContentHeight()

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

	// Scroll down: advance offset until focused card is visible.
	for {
		usedLines := 0
		visibleEnd := -1
		for ti := m.colScroll[m.col]; ti < len(col.Tickets); ti++ {
			h := cardHeightEx(col.Tickets[ti], inner, ti == m.card && m.col == m.col)
			if usedLines+h > avail {
				break
			}
			usedLines += h
			visibleEnd = ti
		}
		if visibleEnd < 0 {
			visibleEnd = m.colScroll[m.col]
		}
		if m.card <= visibleEnd {
			break
		}
		m.colScroll[m.col]++
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
	colW := boardColumnWidth + boardColumnGap
	// Scroll left: retreat offset if focused column is left of window.
	for m.col < m.colOffset {
		m.colOffset--
	}
	// Scroll right: advance offset until focused column is visible.
	for {
		used := 0
		lastVisible := m.colOffset - 1
		for ci := m.colOffset; ci < len(m.view.Columns); ci++ {
			if used+colW > m.width {
				break
			}
			used += colW
			lastVisible = ci
		}
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
