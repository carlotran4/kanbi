package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
)

type Store interface {
	BoardView(context.Context) (storage.BoardView, error)
	CreateTicket(context.Context, int64, string, string, string) (storage.Ticket, error)
	UpdateTicket(context.Context, int64, string, string, string) error
	ArchiveTicket(context.Context, int64) error
	MoveTicket(context.Context, int64, int64) error
	ReorderTicket(context.Context, int64, int) error
}

type TicketOpener interface {
	OpenTicket(context.Context, storage.Ticket, bool) error
}

type RuntimeRefresher interface {
	RefreshRuntime(context.Context) error
}

type StateMarker interface {
	MarkTicketState(context.Context, int64, string) error
}

type ColumnEditor interface {
	AddColumn(context.Context, int64, string) (storage.Column, error)
	RenameColumn(context.Context, int64, string) error
	DeleteColumn(context.Context, int64) error
	ReorderColumn(context.Context, int64, int) error
}

type PromptPaster interface {
	PastePromptNow(context.Context, string, string) error
}

type SessionCloser interface {
	CloseTicketSession(context.Context, storage.Ticket) error
}

type SessionRepairer interface {
	StartFreshTicket(context.Context, storage.Ticket, bool) error
	UpdateSessionRef(context.Context, storage.Ticket, string) error
}

type AllSessionKiller interface {
	KillAllSessions(context.Context) error
}

type Model struct {
	store            Store
	ctx              context.Context
	view             storage.BoardView
	col              int
	card             int
	status           string
	err              error
	editing          bool
	editField        int
	editTitle        string
	editBody         string
	editHard         string
	editCursor       int
	stateMenu        bool
	stateIndex       int
	columnEditing    bool
	columnAction     string
	columnName       string
	promptFallback   bool
	promptWindow     string
	promptText       string
	promptTicket     storage.Ticket
	repairing        bool
	repairEditingRef bool
	repairRef        string
	repairTicket     storage.Ticket
	repairReason     string
	editorTicketID   int64
}

func New(ctx context.Context, store Store) Model {
	m := Model{ctx: ctx, store: store}
	m.reload()
	return m
}

func (m Model) Init() tea.Cmd { return runtimeTickCmd() }

type runtimeTickMsg time.Time

type editorFinishedMsg struct {
	ticketID int64
	body     string
	err      error
}

func runtimeTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return runtimeTickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case runtimeTickMsg:
		if refresher, ok := m.store.(RuntimeRefresher); ok {
			if err := refresher.RefreshRuntime(m.ctx); err != nil {
				m.status = err.Error()
			} else {
				m.reload()
			}
		}
		return m, runtimeTickCmd()
	case editorFinishedMsg:
		m.applyEditorResult(msg)
		return m, tea.ClearScreen
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.promptFallback {
		return m.updatePromptFallback(key)
	}
	if m.repairing {
		return m.updateRepair(key), nil
	}
	if m.stateMenu {
		return m.updateStateMenu(key), nil
	}
	if m.columnEditing {
		return m.updateColumnEdit(key), nil
	}
	if m.editing {
		return m.updateEdit(key)
	}
	switch key.String() {
	case "q", "ctrl+c":
		if killer, ok := m.store.(AllSessionKiller); ok {
			_ = killer.KillAllSessions(m.ctx)
		}
		return m, tea.Quit
	case "tab":
		m.moveAttention(1)
	case "shift+tab", "backtab":
		m.moveAttention(-1)
	case "h", "left":
		m.moveColumn(-1)
	case "l", "right":
		m.moveColumn(1)
	case "j", "down":
		m.moveCard(1)
	case "k", "up":
		m.moveCard(-1)
	case "H":
		m.moveTicketColumn(-1)
	case "L":
		m.moveTicketColumn(1)
	case "J":
		m.reorderTicket(1)
	case "K":
		m.reorderTicket(-1)
	case "n":
		m.createTicket()
	case "c":
		m.startColumnEdit("add")
	case "r":
		m.startColumnEdit("rename")
	case "D":
		m.deleteColumn()
	case "a":
		m.archiveTicket()
	case "e":
		m.startEdit()
	case "E":
		return m, m.openBodyEditor()
	case "m":
		m.startStateMenu()
	case "s":
		m.openTicket(true)
	case "o", "enter":
		m.openTicket(false)
	case "x":
		m.closeSession()
	}
	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return "agent-kanban\n\n" + m.err.Error() + "\n\nq quit\n"
	}
	if m.editing {
		return m.editView()
	}
	if m.stateMenu {
		return m.stateMenuView()
	}
	if m.columnEditing {
		return m.columnEditView()
	}
	if m.promptFallback {
		return m.promptFallbackView()
	}
	if m.repairing {
		return m.repairView()
	}

	var b strings.Builder
	title := lipgloss.NewStyle().Bold(true).Render("Agent Kanban")
	fmt.Fprintf(&b, "%s  %s\n\n", title, m.view.Board.Name)
	b.WriteString(m.boardView())
	b.WriteString("\n\n")
	dim := lipgloss.NewStyle().Faint(true)
	b.WriteString("o:open  s:send  x:close  n:new  e:edit  a:archive  m:state  Tab:attention  q:quit\n")
	b.WriteString(dim.Render("cols: c:add  r:rename  D:delete  │  move col: Shift+H/L  │  move ticket: H/L:col  J/K:reorder") + "\n")
	if m.status != "" {
		b.WriteString(m.status + "\n")
	}
	return b.String()
}

const (
	boardColumnWidth = 30
	boardColumnGap   = 2
)

func (m Model) boardView() string {
	if len(m.view.Columns) == 0 {
		return boxLines([]string{"No columns"}, boardColumnWidth)
	}

	columns := make([]string, 0, len(m.view.Columns))
	for ci, col := range m.view.Columns {
		columns = append(columns, m.columnView(ci, col))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, columns...)
}

func (m Model) columnView(ci int, col storage.Column) string {
	focus := " "
	if ci == m.col {
		focus = ">"
	}

	header := fmt.Sprintf("%s %s", focus, col.Name)
	count := fmt.Sprintf("%d", len(col.Tickets))
	lines := []string{spaceBetween(header, count, boardColumnWidth), mutedBorder.Render(strings.Repeat("─", boardColumnWidth))}
	if len(col.Tickets) == 0 {
		lines = append(lines, "", padLine("(empty)", boardColumnWidth))
	}
	for ti, ticket := range col.Tickets {
		if ti > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, cardView(ci == m.col && ti == m.card, ticket, boardColumnWidth)...)
	}

	return lipgloss.NewStyle().MarginRight(boardColumnGap).Render(strings.Join(lines, "\n"))
}

func cardView(focused bool, ticket storage.Ticket, width int) []string {
	cardInnerWidth := width - 4
	cursor := " "
	if focused {
		cursor = ">"
	}

	titleLines := wrapText(ticket.DisplayID+" "+ticket.Title, cardInnerWidth-2, 3)
	if len(titleLines) == 0 {
		titleLines = []string{ticket.DisplayID}
	}
	content := make([]string, 0, len(titleLines)+1)
	for i, line := range titleLines {
		prefix := "  "
		if i == 0 {
			prefix = cursor + " "
		}
		content = append(content, padLine(prefix+line, cardInnerWidth))
	}

	elapsed := elapsedLabel(ticket)
	var meta string
	if elapsed != "" {
		meta = fmt.Sprintf("[%s] %s %s · %s", ticket.Harness, windowIndicator(ticket), runtimeLabel(ticket), elapsed)
	} else {
		meta = fmt.Sprintf("[%s] %s %s", ticket.Harness, windowIndicator(ticket), runtimeLabel(ticket))
	}
	content = append(content, padLine("  "+meta, cardInnerWidth))

	lines := roundedBoxLines(content, width)
	style := lipgloss.NewStyle()
	styled := false
	switch ticket.Runtime {
	case "needs_permission":
		style = style.Foreground(lipgloss.Color("203")).Bold(true)
		styled = true
	case "waiting_for_user":
		style = style.Foreground(lipgloss.Color("214"))
		styled = true
	case "error":
		style = style.Foreground(lipgloss.Color("196")).Bold(true)
		styled = true
	}
	if focused {
		style = style.Bold(true)
		styled = true
	}
	if styled {
		return strings.Split(style.Render(strings.Join(lines, "\n")), "\n")
	}
	return lines
}

func runtimeLabel(ticket storage.Ticket) string {
	switch ticket.Runtime {
	case "not_started":
		return "new"
	case "running":
		return "running"
	case "waiting_for_user":
		return "waiting"
	case "needs_permission":
		return "permission!"
	case "idle_unknown":
		return "idle"
	case "closing":
		return "closing"
	case "closed":
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return "resumable"
		}
		return "closed"
	case "error":
		return "error"
	case "exited":
		return "exited"
	default:
		return ticket.Runtime
	}
}

func windowIndicator(ticket storage.Ticket) string {
	switch {
	case ticket.Runtime == "error":
		return "!"
	case ticket.SessionActive && ticket.WindowName.Valid:
		return "●"
	case ticket.SessionRef.Valid && ticket.SessionRef.String != "":
		return "○"
	default:
		return "-"
	}
}

func elapsedLabel(ticket storage.Ticket) string {
	base := ticket.LastStateChangeAt
	if !base.Valid {
		base = ticket.LastOutputAt
	}
	if !base.Valid {
		return ""
	}
	d := time.Since(base.Time)
	if d < time.Minute {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

var mutedBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

func roundedBoxLines(lines []string, width int) []string {
	innerWidth := width - 2
	top := mutedBorder.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	bot := mutedBorder.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	out := make([]string, 0, len(lines)+2)
	out = append(out, top)
	for _, line := range lines {
		out = append(out, mutedBorder.Render("│")+" "+padLine(line, innerWidth-2)+" "+mutedBorder.Render("│"))
	}
	out = append(out, bot)
	return out
}

func boxLines(lines []string, width int) string {
	innerWidth := width - 2
	border := mutedBorder.Render("+" + strings.Repeat("-", innerWidth) + "+")
	out := make([]string, 0, len(lines)+2)
	out = append(out, border)
	for _, line := range lines {
		out = append(out, mutedBorder.Render("|")+padLine(line, innerWidth)+mutedBorder.Render("|"))
	}
	out = append(out, border)
	return strings.Join(out, "\n")
}

func spaceBetween(left, right string, width int) string {
	left = trimToWidth(left, width)
	right = trimToWidth(right, width)
	leftWidth := runeLen(left)
	rightWidth := runeLen(right)
	if leftWidth+rightWidth+1 > width {
		return padLine(left, width)
	}
	return left + strings.Repeat(" ", width-leftWidth-rightWidth) + right
}

func wrapText(text string, width int, maxLines int) []string {
	if width <= 0 || maxLines <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var lines []string
	current := ""
	for _, word := range words {
		for runeLen(word) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
				if len(lines) == maxLines {
					return lines
				}
			}
			lines = append(lines, trimToWidth(word, width))
			word = string([]rune(word)[width:])
			if len(lines) == maxLines {
				return lines
			}
		}
		if current == "" {
			current = word
			continue
		}
		if runeLen(current)+1+runeLen(word) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		if len(lines) == maxLines {
			return lines
		}
		current = word
	}
	if current != "" && len(lines) < maxLines {
		lines = append(lines, current)
	}
	return lines
}

func padLine(s string, width int) string {
	s = trimToWidth(s, width)
	return s + strings.Repeat(" ", width-runeLen(s))
}

func trimToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "~"
}

func runeLen(s string) int {
	return len([]rune(s))
}

func (m *Model) reload() {
	view, err := m.store.BoardView(m.ctx)
	m.view = view
	m.err = err
	m.clamp()
}

func (m *Model) moveColumn(delta int) {
	m.col += delta
	m.clamp()
}

func (m *Model) moveCard(delta int) {
	m.card += delta
	m.clamp()
}

func (m *Model) selectedTicket() (storage.Ticket, bool) {
	if m.col < 0 || m.col >= len(m.view.Columns) {
		return storage.Ticket{}, false
	}
	tickets := m.view.Columns[m.col].Tickets
	if m.card < 0 || m.card >= len(tickets) {
		return storage.Ticket{}, false
	}
	return tickets[m.card], true
}

func (m *Model) createTicket() {
	if len(m.view.Columns) == 0 {
		return
	}
	t, err := m.store.CreateTicket(m.ctx, m.view.Columns[m.col].ID, "New ticket", "", "pi")
	if err != nil {
		m.status = err.Error()
		return
	}
	m.status = "created " + t.DisplayID
	m.reload()
	// Position cursor on new ticket then open edit immediately.
	for ci, col := range m.view.Columns {
		for ti, ticket := range col.Tickets {
			if ticket.ID == t.ID {
				m.col = ci
				m.card = ti
			}
		}
	}
	m.startEdit()
}

func (m *Model) startColumnEdit(action string) {
	if action != "add" && (m.col < 0 || m.col >= len(m.view.Columns)) {
		return
	}
	m.columnEditing = true
	m.columnAction = action
	m.columnName = ""
	if action == "rename" {
		m.columnName = m.view.Columns[m.col].Name
	}
}

func (m Model) updateColumnEdit(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.columnEditing = false
	case "enter":
		editor, ok := m.store.(ColumnEditor)
		if !ok {
			m.status = "column editing unavailable"
			m.columnEditing = false
			return m
		}
		switch m.columnAction {
		case "add":
			boardID := int64(0)
			if len(m.view.Columns) > 0 {
				boardID = m.view.Columns[0].BoardID
			}
			if _, err := editor.AddColumn(m.ctx, boardID, m.columnName); err != nil {
				m.status = err.Error()
			} else {
				m.status = "added column"
			}
		case "rename":
			if err := editor.RenameColumn(m.ctx, m.view.Columns[m.col].ID, m.columnName); err != nil {
				m.status = err.Error()
			} else {
				m.status = "renamed column"
			}
		}
		m.columnEditing = false
		m.reload()
	case "backspace":
		m.columnName = popRune(m.columnName)
	default:
		if len(key.Runes) > 0 {
			m.columnName += string(key.Runes)
		}
	}
	return m
}

func (m *Model) reorderColumn(delta int) {
	if m.col < 0 || m.col >= len(m.view.Columns) {
		return
	}
	editor, ok := m.store.(ColumnEditor)
	if !ok {
		m.status = "column editing unavailable"
		return
	}
	if err := editor.ReorderColumn(m.ctx, m.view.Columns[m.col].ID, delta); err != nil {
		m.status = err.Error()
		return
	}
	m.col += delta
	m.reload()
}

func (m *Model) deleteColumn() {
	if m.col < 0 || m.col >= len(m.view.Columns) {
		return
	}
	editor, ok := m.store.(ColumnEditor)
	if !ok {
		m.status = "column editing unavailable"
		return
	}
	name := m.view.Columns[m.col].Name
	if err := editor.DeleteColumn(m.ctx, m.view.Columns[m.col].ID); err != nil {
		m.status = err.Error()
		return
	}
	m.status = "deleted column " + name
	m.reload()
}

func (m *Model) archiveTicket() {
	t, ok := m.selectedTicket()
	if !ok {
		return
	}
	if closer, ok := m.store.(SessionCloser); ok && t.SessionID.Valid {
		if err := closer.CloseTicketSession(m.ctx, t); err != nil {
			m.status = err.Error()
			return
		}
	}
	if err := m.store.ArchiveTicket(m.ctx, t.ID); err != nil {
		m.status = err.Error()
		return
	}
	m.status = "archived " + t.DisplayID
	m.reload()
}

func (m *Model) moveTicketColumn(delta int) {
	t, ok := m.selectedTicket()
	to := m.col + delta
	if !ok || to < 0 || to >= len(m.view.Columns) {
		return
	}
	if err := m.store.MoveTicket(m.ctx, t.ID, m.view.Columns[to].ID); err != nil {
		m.status = err.Error()
		return
	}
	m.col = to
	m.reload()
}

func (m *Model) reorderTicket(delta int) {
	t, ok := m.selectedTicket()
	if !ok {
		return
	}
	if err := m.store.ReorderTicket(m.ctx, t.ID, delta); err != nil {
		m.status = err.Error()
		return
	}
	m.card += delta
	m.reload()
}

func (m *Model) startEdit() {
	t, ok := m.selectedTicket()
	if !ok {
		return
	}
	m.editing = true
	m.editField = 0
	m.editTitle = t.Title
	m.editBody = t.Body
	m.editHard = t.Harness
	m.editCursor = len([]rune(t.Title))
}

func (m *Model) openTicket(sendPrompt bool) {
	t, ok := m.selectedTicket()
	if !ok {
		return
	}
	opener, ok := m.store.(TicketOpener)
	if !ok {
		m.status = "open unavailable"
		return
	}
	if err := opener.OpenTicket(m.ctx, t, sendPrompt); err != nil {
		var promptErr tmux.PromptReadyError
		switch {
		case errors.As(err, &promptErr):
			m.promptFallback = true
			m.promptWindow = promptErr.WindowName
			m.promptText = promptErr.Prompt
			m.promptTicket = t
			m.status = err.Error()
		case errors.Is(err, tmux.ErrPromptAlreadySent):
			m.status = "Prompt already sent; open session instead?"
		case isRepairError(err):
			m.startRepair(t, err)
		default:
			m.status = err.Error()
		}
		return
	}
	if sendPrompt {
		m.status = "sent prompt " + t.DisplayID
	} else {
		m.status = "opened " + t.DisplayID
	}
	m.reload()
}

func (m *Model) closeSession() {
	t, ok := m.selectedTicket()
	if !ok {
		return
	}
	closer, ok := m.store.(SessionCloser)
	if !ok {
		m.status = "close unavailable"
		return
	}
	if err := closer.CloseTicketSession(m.ctx, t); err != nil {
		m.status = err.Error()
		return
	}
	m.status = "closed " + t.DisplayID
	m.reload()
}

func (m Model) updatePromptFallback(key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "p":
		paster, ok := m.store.(PromptPaster)
		if !ok {
			m.status = "paste unavailable"
			m.promptFallback = false
			return m, nil
		}
		if err := paster.PastePromptNow(m.ctx, m.promptWindow, m.promptText); err != nil {
			m.status = err.Error()
		} else {
			m.status = "pasted prompt " + m.promptTicket.DisplayID
		}
		m.promptFallback = false
		m.reload()
	case "o":
		m.status = "opened without prompt " + m.promptTicket.DisplayID
		m.promptFallback = false
		m.reload()
	case "c", "esc":
		m.status = "cancelled prompt send"
		m.promptFallback = false
	}
	return m, nil
}

func isRepairError(err error) bool {
	var repair tmux.RepairNeededError
	var resume tmux.ResumeFailedError
	return errors.As(err, &repair) || errors.As(err, &resume)
}

func (m *Model) startRepair(ticket storage.Ticket, err error) {
	m.repairing = true
	m.repairEditingRef = false
	m.repairTicket = ticket
	m.repairRef = ""
	m.repairReason = err.Error()
	m.status = err.Error()
}

func (m Model) updateRepair(key tea.KeyMsg) Model {
	repairer, ok := m.store.(SessionRepairer)
	if !ok {
		m.status = "repair unavailable"
		m.repairing = false
		return m
	}
	if m.repairEditingRef {
		switch key.String() {
		case "esc":
			m.repairEditingRef = false
		case "enter":
			if err := repairer.UpdateSessionRef(m.ctx, m.repairTicket, strings.TrimSpace(m.repairRef)); err != nil {
				m.status = err.Error()
				return m
			}
			updated := m.repairTicket
			updated.SessionRef.Valid = strings.TrimSpace(m.repairRef) != ""
			updated.SessionRef.String = strings.TrimSpace(m.repairRef)
			if opener, ok := m.store.(TicketOpener); ok && updated.SessionRef.Valid {
				if err := opener.OpenTicket(m.ctx, updated, false); err != nil {
					m.status = err.Error()
					return m
				}
			}
			m.repairing = false
			m.status = "updated session ref " + m.repairTicket.DisplayID
			m.reload()
		case "backspace":
			m.repairRef = popRune(m.repairRef)
		default:
			if len(key.Runes) > 0 {
				m.repairRef += string(key.Runes)
			}
		}
		return m
	}
	switch key.String() {
	case "esc", "c":
		m.repairing = false
		m.status = "cancelled repair"
	case "e":
		m.repairEditingRef = true
		m.repairRef = ""
	case "f":
		if err := repairer.StartFreshTicket(m.ctx, m.repairTicket, false); err != nil {
			m.status = err.Error()
			return m
		}
		m.repairing = false
		m.status = "started fresh " + m.repairTicket.DisplayID
		m.reload()
	case "r":
		opener, ok := m.store.(TicketOpener)
		if !ok {
			m.status = "open unavailable"
			return m
		}
		if err := opener.OpenTicket(m.ctx, m.repairTicket, false); err != nil {
			m.status = err.Error()
			return m
		}
		m.repairing = false
		m.status = "retried " + m.repairTicket.DisplayID
		m.reload()
	}
	return m
}

func (m *Model) startStateMenu() {
	if _, ok := m.selectedTicket(); !ok {
		return
	}
	m.stateMenu = true
	m.stateIndex = 0
}

var manualStates = []string{"running", "waiting_for_user", "idle_unknown", "error"}

func (m Model) updateStateMenu(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.stateMenu = false
	case "j", "down":
		m.stateIndex = (m.stateIndex + 1) % len(manualStates)
	case "k", "up":
		m.stateIndex--
		if m.stateIndex < 0 {
			m.stateIndex = len(manualStates) - 1
		}
	case "enter":
		t, ok := m.selectedTicket()
		if !ok {
			m.stateMenu = false
			return m
		}
		marker, ok := m.store.(StateMarker)
		if !ok {
			m.status = "manual state unavailable"
			m.stateMenu = false
			return m
		}
		state := manualStates[m.stateIndex]
		if err := marker.MarkTicketState(m.ctx, t.ID, state); err != nil {
			m.status = err.Error()
		} else {
			m.status = "marked " + t.DisplayID + " " + state
		}
		m.stateMenu = false
		m.reload()
	}
	return m
}

func (m *Model) moveAttention(delta int) {
	type pos struct{ col, card int }
	var positions []pos
	for ci, col := range m.view.Columns {
		for ti, ticket := range col.Tickets {
			if isAttention(ticket.Runtime) {
				positions = append(positions, pos{ci, ti})
			}
		}
	}
	if len(positions) == 0 {
		m.status = "no attention tickets"
		return
	}
	current := 0
	for i, p := range positions {
		if p.col == m.col && p.card == m.card {
			current = i
			break
		}
	}
	next := (current + delta) % len(positions)
	if next < 0 {
		next += len(positions)
	}
	m.col = positions[next].col
	m.card = positions[next].card
	m.status = "attention " + m.view.Columns[m.col].Tickets[m.card].DisplayID
}

func (m Model) updateEdit(key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.editing = false
	case "tab":
		newField := m.editField + 1
		if newField > 2 {
			m.saveEdit()
		} else {
			m.editField = newField
			m.editCursor = len([]rune(m.currentEditField()))
		}
	case "enter":
		if m.editField == 1 {
			// newline in body
			m.insertEdit("\n")
		} else {
			newField := m.editField + 1
			if newField > 2 {
				m.saveEdit()
			} else {
				m.editField = newField
				m.editCursor = len([]rune(m.currentEditField()))
			}
		}
	case "backspace":
		m.backspaceEdit()
	case "delete":
		m.deleteEdit()
	case "left":
		if m.editCursor > 0 {
			m.editCursor--
		}
	case "right":
		if m.editCursor < len([]rune(m.currentEditField())) {
			m.editCursor++
		}
	case "home", "ctrl+a":
		m.editCursor = 0
	case "end", "ctrl+e":
		m.editCursor = len([]rune(m.currentEditField()))
	case "ctrl+E":
		return m, m.openBodyEditor()
	default:
		if len(key.Runes) > 0 {
			m.insertEdit(string(key.Runes))
		}
	}
	return m, nil
}

func (m *Model) saveEdit() {
	t, ok := m.selectedTicket()
	if !ok {
		m.editing = false
		return
	}
	if strings.TrimSpace(m.editHard) == "" {
		m.editHard = "pi"
	}
	if err := m.store.UpdateTicket(m.ctx, t.ID, strings.TrimSpace(m.editTitle), m.editBody, strings.TrimSpace(m.editHard)); err != nil {
		m.status = err.Error()
	} else {
		m.status = "updated " + t.DisplayID
	}
	m.editing = false
	m.reload()
}

// currentEditField returns a pointer to the rune slice of the active field.
func (m *Model) currentEditField() string {
	switch m.editField {
	case 0:
		return m.editTitle
	case 1:
		return m.editBody
	case 2:
		return m.editHard
	}
	return ""
}

func (m *Model) setEditField(s string) {
	switch m.editField {
	case 0:
		m.editTitle = s
	case 1:
		m.editBody = s
	case 2:
		m.editHard = s
	}
}

func (m *Model) insertEdit(s string) {
	r := []rune(m.currentEditField())
	ins := []rune(s)
	new := make([]rune, 0, len(r)+len(ins))
	new = append(new, r[:m.editCursor]...)
	new = append(new, ins...)
	new = append(new, r[m.editCursor:]...)
	m.setEditField(string(new))
	m.editCursor += len(ins)
}

func (m *Model) backspaceEdit() {
	r := []rune(m.currentEditField())
	if m.editCursor == 0 || len(r) == 0 {
		return
	}
	new := make([]rune, 0, len(r)-1)
	new = append(new, r[:m.editCursor-1]...)
	new = append(new, r[m.editCursor:]...)
	m.setEditField(string(new))
	m.editCursor--
}

func (m *Model) deleteEdit() {
	r := []rune(m.currentEditField())
	if m.editCursor >= len(r) {
		return
	}
	new := make([]rune, 0, len(r)-1)
	new = append(new, r[:m.editCursor]...)
	new = append(new, r[m.editCursor+1:]...)
	m.setEditField(string(new))
}

func renderWithCursor(value string, cursor int) string {
	r := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(r) {
		cursor = len(r)
	}
	var b strings.Builder
	b.WriteString(string(r[:cursor]))
	if cursor < len(r) {
		// highlight char under cursor
		b.WriteString(lipgloss.NewStyle().Reverse(true).Render(string(r[cursor : cursor+1])))
		b.WriteString(string(r[cursor+1:]))
	} else {
		// cursor at end: show block
		b.WriteString(lipgloss.NewStyle().Reverse(true).Render(" "))
	}
	return b.String()
}

func (m Model) editView() string {
	rowCursor := func(i int) string {
		if m.editField == i {
			return ">"
		}
		return " "
	}
	render := func(i int, value string) string {
		if m.editField == i {
			return renderWithCursor(value, m.editCursor)
		}
		return value
	}
	// For body, show it inline but with newlines rendered visibly for multiline.
	return fmt.Sprintf("Edit ticket\n\n%s title:   %s\n%s body:    %s\n%s harness: %s\n\nTab next field · Enter newline in body · ←/→ move · Home/End · Ctrl+E full editor · Esc cancel\n",
		rowCursor(0), render(0, m.editTitle),
		rowCursor(1), render(1, m.editBody),
		rowCursor(2), render(2, m.editHard))
}

func (m Model) stateMenuView() string {
	var b strings.Builder
	b.WriteString("Mark runtime state\n\n")
	for i, state := range manualStates {
		cursor := " "
		if i == m.stateIndex {
			cursor = ">"
		}
		fmt.Fprintf(&b, "%s %s\n", cursor, state)
	}
	b.WriteString("\nEnter mark, Esc cancel\n")
	if m.status != "" {
		b.WriteString(m.status + "\n")
	}
	return b.String()
}

func (m Model) columnEditView() string {
	title := "Add column"
	if m.columnAction == "rename" {
		title = "Rename column"
	}
	return fmt.Sprintf("%s\n\n> name: %s\n\nEnter save, Esc cancel\n", title, m.columnName)
}

func (m Model) promptFallbackView() string {
	return fmt.Sprintf("Prompt readiness was not detected for %s.\n\np paste now\no open without sending\nc cancel\n\n%s\n", m.promptTicket.DisplayID, m.status)
}

func (m Model) repairView() string {
	if m.repairEditingRef {
		return fmt.Sprintf("Edit session ref for %s\n\n> ref: %s\n\nEnter save, Esc back\n", m.repairTicket.DisplayID, m.repairRef)
	}
	return fmt.Sprintf("Session repair needed for %s\n\n%s\nr retry  e edit ref  f start fresh  c cancel\n",
		m.repairTicket.DisplayID, m.repairReason)
}

func (m Model) openBodyEditor() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	body := t.Body
	ticketID := t.ID
	if m.editing {
		body = m.editBody
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if _, err := exec.LookPath("nano"); err == nil {
			editor = "nano"
		} else {
			editor = "vi"
		}
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("agent-kanban-%s.md", t.DisplayID))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return func() tea.Msg { return editorFinishedMsg{ticketID: ticketID, err: err} }
	}
	cmd := exec.Command(editor, path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return editorFinishedMsg{ticketID: ticketID, err: err}
		}
		data, readErr := os.ReadFile(path)
		_ = os.Remove(path)
		if readErr != nil {
			return editorFinishedMsg{ticketID: ticketID, err: readErr}
		}
		return editorFinishedMsg{ticketID: ticketID, body: string(data)}
	})
}

func (m *Model) applyEditorResult(msg editorFinishedMsg) {
	if msg.err != nil {
		m.status = msg.err.Error()
		return
	}
	if m.editing {
		m.editBody = msg.body
		m.status = "body loaded from editor"
		return
	}
	t, err := findTicketByID(m.view, msg.ticketID)
	if err != nil {
		m.status = err.Error()
		return
	}
	if err := m.store.UpdateTicket(m.ctx, t.ID, t.Title, msg.body, t.Harness); err != nil {
		m.status = err.Error()
		return
	}
	m.status = "updated body " + t.DisplayID
	m.reload()
}

func findTicketByID(view storage.BoardView, id int64) (storage.Ticket, error) {
	for _, col := range view.Columns {
		for _, ticket := range col.Tickets {
			if ticket.ID == id {
				return ticket, nil
			}
		}
	}
	return storage.Ticket{}, errors.New("ticket not found")
}

func popRune(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(r[:len(r)-1])
}

func isAttention(state string) bool {
	return state == "waiting_for_user" || state == "needs_permission" || state == "error"
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
