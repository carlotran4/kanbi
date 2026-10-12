package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// source is the latest loaded board projection before transient title filtering.
// Its slices are never mutated by filtering. origin remains fixed across refresh.
type boardSearchState struct {
	active           bool
	query            InputBuffer
	pattern          *regexp.Regexp
	source           storage.BoardView
	origin           boardSearchAnchor
	lastSelectedID   int64
	inspectorFocused bool
}

type searchColumnAnchor struct {
	topID int64
	index int
}

type boardSearchAnchor struct {
	selectedID                    int64
	columnKey, offsetKey          string
	column, offset, selectedIndex int
	ticketIDs                     []int64
	columns                       map[string]searchColumnAnchor
}

type searchMatch struct{ col, card int }

func (m Model) searchColumnKey(col storage.Column) string {
	// Master IDs are negative positional indices, not durable column IDs.
	if m.masterBoard {
		return col.WorkflowKey
	}
	return fmt.Sprint(col.ID)
}

func (m *Model) startBoardSearch() {
	anchor := boardSearchAnchor{column: m.col, offset: m.colOffset, columns: make(map[string]searchColumnAnchor)}
	if ticket, ok := m.selectedTicket(); ok {
		anchor.selectedID = ticket.ID
	}
	for ci, col := range m.view.Columns {
		key := m.searchColumnKey(col)
		if ci == m.col {
			anchor.columnKey = key
		}
		if ci == m.colOffset {
			anchor.offsetKey = key
		}
		top := 0
		if ci < len(m.colScroll) {
			top = m.colScroll[ci]
		}
		ca := searchColumnAnchor{index: top}
		if top >= 0 && top < len(col.Tickets) {
			ca.topID = col.Tickets[top].ID
		}
		anchor.columns[key] = ca
		for _, ticket := range col.Tickets {
			if ticket.ID == anchor.selectedID {
				anchor.selectedIndex = len(anchor.ticketIDs)
			}
			anchor.ticketIDs = append(anchor.ticketIDs, ticket.ID)
		}
	}
	m.search = boardSearchState{active: true, source: m.view, origin: anchor,
		lastSelectedID: anchor.selectedID, inspectorFocused: m.inspector.focused}
	m.inspector.focused = false
	m.status, m.errOperation, m.errNext = "", "", ""
	m.renderVersion++
	m.hScrollFollow()
	m.vScrollFollow()
}

func (m *Model) updateBoardSearch(key tea.KeyMsg) {
	// Bracketed paste is literal input, even when it contains shortcut names.
	if !key.Paste {
		switch key.Type {
		case tea.KeyEsc:
			m.endBoardSearch(false)
			return
		case tea.KeyCtrlG:
			m.endBoardSearch(true)
			return
		case tea.KeyEnter, tea.KeyCtrlN:
			m.moveSearchMatch(1)
			return
		case tea.KeyCtrlP:
			m.moveSearchMatch(-1)
			return
		}
	}
	previous := m.search.query.Value()
	if key.Type == tea.KeyRunes {
		// Single-line search accepts pasted titles without control characters.
		text := strings.Map(func(r rune) rune {
			if r < ' ' || r == 127 {
				return ' '
			}
			return r
		}, string(key.Runes))
		m.search.query.Insert(text)
	} else {
		m.search.query.HandleKey(key.String(), nil)
	}
	if previous != m.search.query.Value() {
		query := m.search.query.Value()
		m.search.pattern = nil
		if query != "" {
			m.search.pattern = regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
		}
		m.filterSearchView()
		m.colScroll = make([]int, len(m.view.Columns))
		if m.search.pattern == nil {
			m.restoreSearchAnchor()
		} else {
			m.selectSearchMatch(0, 0)
			m.clamp()
			m.hScrollFollow()
			m.vScrollFollow()
		}
	}
	m.renderVersion++
}

func (m *Model) filterSearchView() {
	m.view = m.search.source
	if m.search.pattern == nil {
		return
	}
	m.view.Columns = append([]storage.Column(nil), m.view.Columns...)
	for ci := range m.view.Columns {
		var matches []storage.Ticket
		for _, ticket := range m.search.source.Columns[ci].Tickets {
			if m.search.pattern.MatchString(ticket.Title) {
				matches = append(matches, ticket)
			}
		}
		m.view.Columns[ci].Tickets = matches
	}
}

func (m Model) searchMatches() []searchMatch {
	if !m.search.active || m.search.pattern == nil {
		return nil
	}
	var matches []searchMatch
	for ci, col := range m.view.Columns {
		for ti := range col.Tickets {
			matches = append(matches, searchMatch{ci, ti})
		}
	}
	return matches
}

func (m Model) searchPosition() (int, int) {
	matches := m.searchMatches()
	for i, match := range matches {
		if match.col == m.col && match.card == m.card {
			return i, len(matches)
		}
	}
	return 0, len(matches)
}

func (m *Model) selectSearchMatch(id int64, fallback int) {
	matches := m.searchMatches()
	if len(matches) == 0 {
		return
	}
	index := maxInt(0, fallback) % len(matches)
	for i, match := range matches {
		if m.view.Columns[match.col].Tickets[match.card].ID == id {
			index = i
			break
		}
	}
	m.col, m.card = matches[index].col, matches[index].card
	m.search.lastSelectedID = m.view.Columns[m.col].Tickets[m.card].ID
}

func (m *Model) moveSearchMatch(delta int) {
	position, count := m.searchPosition()
	if count == 0 {
		return
	}
	m.selectSearchMatch(0, (position+delta+count)%count)
	m.hScrollFollow()
	m.vScrollFollow()
	m.renderVersion++
}

func (m *Model) selectSearchTicket(id int64) bool {
	if id == 0 {
		return false
	}
	for ci, col := range m.view.Columns {
		for ti, ticket := range col.Tickets {
			if ticket.ID == id {
				m.col, m.card = ci, ti
				return true
			}
		}
	}
	return false
}

func (m *Model) restoreSearchAnchor() {
	anchor := m.search.origin
	m.view = m.search.source
	m.col, m.card, m.colOffset = anchor.column, 0, anchor.offset
	m.colScroll = make([]int, len(m.view.Columns))
	for ci, col := range m.view.Columns {
		key := m.searchColumnKey(col)
		if key == anchor.columnKey {
			m.col = ci
		}
		if key == anchor.offsetKey {
			m.colOffset = ci
		}
		ca := anchor.columns[key]
		m.colScroll[ci] = maxInt(0, minInt(ca.index, len(col.Tickets)-1))
		for ti, ticket := range col.Tickets {
			if ticket.ID == ca.topID {
				m.colScroll[ci] = ti
				break
			}
		}
	}
	if !m.selectSearchTicket(anchor.selectedID) {
		// Try nearest original neighbors, preferring the following ticket.
		for distance := 1; distance <= len(anchor.ticketIDs); distance++ {
			found := false
			for _, index := range []int{anchor.selectedIndex + distance, anchor.selectedIndex - distance} {
				if index >= 0 && index < len(anchor.ticketIDs) && m.selectSearchTicket(anchor.ticketIDs[index]) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	m.clamp()
	m.colOffset = maxInt(0, minInt(m.colOffset, len(m.view.Columns)-1))
	m.hScrollFollow()
	m.vScrollFollow()
}

func (m *Model) endBoardSearch(keep bool) {
	selectedID := m.search.lastSelectedID
	if ticket, ok := m.selectedTicket(); ok {
		selectedID = ticket.ID
	}
	// Filtered scroll indices cannot be applied to the full board on exit.
	m.restoreSearchAnchor()
	if keep && m.selectSearchTicket(selectedID) {
		m.hScrollFollow()
		m.vScrollFollow()
	}
	m.inspector.focused = m.search.inspectorFocused
	m.search = boardSearchState{}
	m.renderVersion++
	m.hScrollFollow()
	m.vScrollFollow()
}

func (m Model) searchQueryLine() string {
	position, count := m.searchPosition()
	summary := "Type to search titles"
	if m.search.pattern != nil {
		summary = "0 matches"
		if count > 0 {
			summary = fmt.Sprintf("%d/%d matches", position+1, count)
		}
	}
	summary = trimToWidth(summary, maxInt(1, m.width-4))
	queryWidth := maxInt(1, m.width-lipgloss.Width(summary)-5)
	return trimToWidth("/ "+m.search.query.Viewport(queryWidth)+"  "+summary, maxInt(1, m.width))
}

func (m Model) searchControls() string {
	return trimToWidth("Enter next · Ctrl+P prev · wraps · Esc restore · Ctrl+G keep", maxInt(1, m.width))
}
