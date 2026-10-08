package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/glamour"

	"github.com/carlotran4/kanbi/internal/storage"
)

type textRenderKey struct {
	body  string
	width int
}
type baseRenderKey struct {
	version                                            uint64
	width, height, col, card, colOffset, contentHeight int
	second                                             int64
	status, operation, next, notice, refreshError      string
}
type renderCache struct {
	previews     map[textRenderKey]string
	notes        map[textRenderKey]string
	noteRenderer *glamour.TermRenderer
	noteWidth    int
	baseKey      baseRenderKey
	base         string
}

func newRenderCache() *renderCache {
	return &renderCache{previews: make(map[textRenderKey]string), notes: make(map[textRenderKey]string)}
}

func (m *Model) bodyPreview(ticket storage.Ticket, width int) string {
	body := ticket.Body
	if ticket.FocusPaused && ticket.LatestCheckpoint != nil {
		body = "Next: " + ticket.LatestCheckpoint.NextAction
	}
	if m.renderCache == nil {
		return renderBodyPreview(body, width)
	}
	key := textRenderKey{body: body, width: width}
	if value, ok := m.renderCache.previews[key]; ok {
		return value
	}
	value := renderBodyPreview(body, width)
	if len(m.renderCache.previews) >= 128 {
		clear(m.renderCache.previews)
	}
	m.renderCache.previews[key] = value
	return value
}

func (m *Model) cardPreview(ticket storage.Ticket, width int, focused bool) string {
	if !focused {
		return ""
	}
	return m.bodyPreview(ticket, maxInt(10, width-8))
}

func (m *Model) cachedCardHeight(ticket storage.Ticket, width int, focused bool) int {
	return cardHeightWithPreview(ticket, width, m.masterBoard, m.cardPreview(ticket, width, focused))
}

func (m *Model) cachedCardView(focused bool, ticket storage.Ticket, width int, showBoard bool) []string {
	return cardViewWithPreview(focused, ticket, width, showBoard, m.cardPreview(ticket, width, focused))
}

func (m *Model) cachedBaseView() string {
	if m.renderCache == nil || m.activeModalKind() == modalNone {
		return m.baseView()
	}
	key := baseRenderKey{version: m.renderVersion, width: m.width, height: m.height, col: m.col, card: m.card, colOffset: m.colOffset,
		contentHeight: m.boardContentHeight(), second: time.Now().Unix(), status: m.status, operation: m.errOperation, next: m.errNext, notice: m.integrationNotice, refreshError: m.refreshError}
	if m.renderCache.base != "" && m.renderCache.baseKey == key {
		return m.renderCache.base
	}
	m.renderCache.base = m.baseView()
	m.renderCache.baseKey = key
	return m.renderCache.base
}

func (m *Model) renderNote(body string, width int) string {
	body = strings.TrimSpace(body)
	if m.renderCache == nil {
		m.renderCache = newRenderCache()
	}
	key := textRenderKey{body: body, width: width}
	if value, ok := m.renderCache.notes[key]; ok {
		return value
	}
	cache := m.renderCache
	if cache.noteRenderer == nil || cache.noteWidth != width {
		cache.noteRenderer, _ = glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width))
		cache.noteWidth = width
	}
	value := body
	if cache.noteRenderer != nil {
		if rendered, err := cache.noteRenderer.Render(body); err == nil && strings.TrimSpace(rendered) != "" {
			value = strings.TrimSpace(rendered)
		}
	}
	if len(cache.notes) >= 64 {
		clear(cache.notes)
	}
	cache.notes[key] = value
	return value
}
