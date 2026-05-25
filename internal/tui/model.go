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

type BoardSelector interface {
	ListBoards(context.Context) ([]storage.Board, error)
	BoardViewByID(context.Context, int64) (storage.BoardView, error)
	MasterBoardView(context.Context) (storage.BoardView, error)
}

type BoardEditor interface {
	CreateBoardWithWorkdir(context.Context, string, string) (storage.Board, error)
	RenameBoard(context.Context, int64, string) error
	SetBoardWorkdir(context.Context, int64, string) error
	DeleteBoard(context.Context, int64) error
}

type ColumnResolver interface {
	ColumnIDByBoardAndName(context.Context, int64, string) (int64, error)
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
	store                   Store
	ctx                     context.Context
	view                    storage.BoardView
	boards                  []storage.Board
	boardID                 int64
	masterBoard             bool
	boardPicker             bool
	boardPickerMode         string // switch or create
	boardIndex              int
	boardRenaming           bool
	boardRenameReturnPicker bool
	boardRenameID           int64
	boardRenameName         string
	boardEditing            bool
	boardEditAction         string // create or cwd
	boardEditID             int64
	boardEditName           string
	boardEditCWD            string
	boardEditField          int
	boardDeleting           bool
	boardDeleteID           int64
	boardDeleteName         string
	masterCreateCol         string
	col                     int
	card                    int
	width                   int
	height                  int
	colScroll               []int // per-column vertical scroll offset (index of first visible card)
	colOffset               int   // horizontal scroll: index of first rendered column
	status                  string
	err                     error
	editing                 bool
	editField               int
	editTitle               string
	editBody                string
	editHard                string
	editCursor              int
	stateMenu               bool
	stateIndex              int
	columnEditing           bool
	columnAction            string
	columnName              string
	promptFallback          bool
	promptWindow            string
	promptText              string
	promptTicket            storage.Ticket
	repairing               bool
	repairEditingRef        bool
	repairRef               string
	repairTicket            storage.Ticket
	repairReason            string
	showHelp                bool
	editorTicketID          int64
}

// defaultTermSize is used before a WindowSizeMsg arrives.
const defaultTermWidth = 220
const defaultTermHeight = 40

func New(ctx context.Context, store Store) Model {
	m := Model{ctx: ctx, store: store, width: defaultTermWidth, height: defaultTermHeight}
	m.reloadBoards()
	m.reload()
	return m
}

func NewWithPicker(ctx context.Context, store Store) Model {
	m := New(ctx, store)
	m.boardPicker = true
	m.boardPickerMode = "switch"
	m.status = "select a board"
	return m
}

func (m Model) Init() tea.Cmd { return runtimeTickCmd() }

type runtimeTickMsg time.Time

type openTicketMsg struct {
	ticket     storage.Ticket
	sendPrompt bool
	err        error
}

type closeSessionMsg struct {
	displayID string
	err       error
}

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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncScrollDimensions()
		return m, nil
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
	case openTicketMsg:
		var promptErr tmux.PromptReadyError
		switch {
		case msg.err == nil:
			if msg.sendPrompt {
				m.status = "sent prompt " + msg.ticket.DisplayID
			} else {
				m.status = "opened " + msg.ticket.DisplayID
			}
			m.reload()
		case errors.As(msg.err, &promptErr):
			m.promptFallback = true
			m.promptWindow = promptErr.WindowName
			m.promptText = promptErr.Prompt
			m.promptTicket = msg.ticket
			m.status = msg.err.Error()
		case errors.Is(msg.err, tmux.ErrPromptAlreadySent):
			m.status = "Prompt already sent; open session instead?"
		case isRepairError(msg.err):
			m.startRepair(msg.ticket, msg.err)
		default:
			m.status = msg.err.Error()
		}
		return m, nil
	case closeSessionMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
		} else {
			m.status = "closed " + msg.displayID
			m.reload()
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Clear stale status on any keypress (unless a modal is consuming input).
	if !m.editing && !m.stateMenu && !m.columnEditing && !m.promptFallback && !m.repairing && !m.boardRenaming && !m.boardEditing && !m.boardDeleting {
		m.status = ""
	}
	if m.promptFallback {
		return m.updatePromptFallback(key)
	}
	if m.boardRenaming {
		return m.updateBoardRename(key), nil
	}
	if m.boardEditing {
		return m.updateBoardEdit(key), nil
	}
	if m.boardDeleting {
		return m.updateBoardDelete(key), nil
	}
	if m.boardPicker {
		return m.updateBoardPicker(key), nil
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
	if m.showHelp {
		switch key.String() {
		case "?", "esc", "q":
			m.showHelp = false
		}
		return m, nil
	}
	switch key.String() {
	case "?":
		m.showHelp = true
	case "b":
		m.reloadBoards()
		m.boardPicker = true
		m.boardPickerMode = "switch"
	case "q", "ctrl+c":
		if killer, ok := m.store.(AllSessionKiller); ok {
			_ = killer.KillAllSessions(m.ctx)
		}
		return m, tea.Quit
	case "!":
		m.moveAttention(1)
	case "h", "left":
		m.moveColumn(-1)
	case "l", "right":
		m.moveColumn(1)
	case "j", "down":
		m.moveCard(1)
	case "k", "up":
		m.moveCard(-1)
	case "H", "shift+left":
		m.moveTicketColumn(-1)
	case "L", "shift+right":
		m.moveTicketColumn(1)
	case "J", "shift+down":
		m.reorderTicket(1)
	case "K", "shift+up":
		m.reorderTicket(-1)
	case "ctrl+shift+left":
		m.reorderColumn(-1)
	case "ctrl+shift+right":
		m.reorderColumn(1)
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
		return m, m.openTicketCmd(true)
	case "o", "enter":
		return m, m.openTicketCmd(false)
	case "x":
		return m, m.closeSessionCmd()
	}
	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return "agent-kanban\n\n" + m.err.Error() + "\n\nq quit\n"
	}
	if m.showHelp {
		return m.helpView()
	}
	if m.boardRenaming {
		return m.boardRenameView()
	}
	if m.boardEditing {
		return m.boardEditView()
	}
	if m.boardDeleting {
		return m.boardDeleteView()
	}
	if m.boardPicker {
		return m.boardPickerView()
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
	// Header bar: full-width background strip.
	appName := headerBarStyle.Render("Agent Kanban")
	boardName := boardNameStyle.Render(m.view.Board.Name)
	barUsed := runeLen("Agent Kanban") + 2 + runeLen(m.view.Board.Name) + 1
	barPad := ""
	if m.width > barUsed {
		barPad = lipgloss.NewStyle().Background(palette.header).Render(strings.Repeat(" ", m.width-barUsed))
	}
	fmt.Fprintf(&b, "%s %s%s\n\n", appName, boardName, barPad)
	board := m.boardView()
	hint := ""
	if h := m.hScrollHint(); h != "" {
		hint = "\n" + h
	}
	// Footer lines: rule + hints + optional status.
	footerLines := 2
	if m.status != "" {
		footerLines++
	}
	// Count lines used so far: header (2) + board + hint (0 or 1 extra).
	contentLines := 2 + strings.Count(board, "\n")
	if hint != "" {
		contentLines++
	}
	pad := m.height - contentLines - footerLines
	if pad < 1 {
		pad = 1
	}
	fmt.Fprintf(&b, "%s%s%s", board, hint, strings.Repeat("\n", pad))
	// Footer separator rule.
	rule := footerRule.Render(strings.Repeat("─", m.width))
	fmt.Fprintf(&b, "%s\n", rule)
	b.WriteString("o:open  s:send  n:new  e:edit  a:archive  b:boards  !:attn  q:quit   ?:help\n")
	if m.status != "" {
		b.WriteString(statusStyle.Render(m.status) + "\n")
	}
	return b.String()
}

const (
	boardColumnWidth = 30
	boardColumnGap   = 1
)

func (m Model) boardView() string {
	if len(m.view.Columns) == 0 {
		return boxLines([]string{"No columns"}, boardColumnWidth)
	}

	colW := boardColumnWidth + boardColumnGap
	var columns []string
	usedWidth := 0
	for ci := m.colOffset; ci < len(m.view.Columns); ci++ {
		if usedWidth+colW > m.width {
			break
		}
		columns = append(columns, m.columnView(ci, m.view.Columns[ci]))
		usedWidth += colW
	}
	if len(columns) == 0 {
		// Terminal too narrow to fit even one column; show it anyway.
		columns = append(columns, m.columnView(m.colOffset, m.view.Columns[m.colOffset]))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, columns...)
}

// hScrollHint returns a one-line string like "◀ 2 hidden   3 hidden ▶" when
// columns are clipped off either side, or "" when everything is visible.
func (m Model) hScrollHint() string {
	if len(m.view.Columns) == 0 {
		return ""
	}
	hiddenLeft := m.colOffset

	colW := boardColumnWidth + boardColumnGap
	usedWidth := 0
	lastVisible := m.colOffset - 1
	for ci := m.colOffset; ci < len(m.view.Columns); ci++ {
		if usedWidth+colW > m.width {
			break
		}
		usedWidth += colW
		lastVisible = ci
	}
	hiddenRight := len(m.view.Columns) - 1 - lastVisible

	if hiddenLeft == 0 && hiddenRight <= 0 {
		return ""
	}

	dim := lipgloss.NewStyle().Faint(true)
	var left, right string
	if hiddenLeft > 0 {
		left = fmt.Sprintf("◄ %d hidden", hiddenLeft)
	}
	if hiddenRight > 0 {
		right = fmt.Sprintf("%d hidden ►", hiddenRight)
	}
	var hint string
	switch {
	case left != "" && right != "":
		hint = spaceBetween(left, right, m.width)
	case left != "":
		hint = left
	default:
		hint = right
	}
	return dim.Render(hint)
}

func (m Model) columnView(ci int, col storage.Column) string {
	focused := ci == m.col
	borderStyle := mutedBorder
	if focused {
		borderStyle = accentBorder
	}

	focus := " "
	if focused {
		focus = ">"
	}
	headerText := fmt.Sprintf("%s %s", focus, col.Name)
	if focused {
		headerText = accentBorder.Bold(true).Render(headerText)
	}
	count := fmt.Sprintf("%d", len(col.Tickets))
	lines := []string{
		spaceBetween(headerText, count, boardColumnWidth),
		borderStyle.Render(strings.Repeat("─", boardColumnWidth)),
	}

	if len(col.Tickets) == 0 {
		lines = append(lines, "", padLine("(empty)", boardColumnWidth))
		return lipgloss.NewStyle().MarginRight(boardColumnGap).Render(strings.Join(lines, "\n"))
	}

	// Determine which cards are visible given the scroll offset and available height.
	scrollTop := 0
	if ci < len(m.colScroll) {
		scrollTop = m.colScroll[ci]
	}
	if scrollTop < 0 {
		scrollTop = 0
	}
	if scrollTop >= len(col.Tickets) {
		scrollTop = len(col.Tickets) - 1
	}

	inner := boardColumnWidth - 2
	avail := m.boardContentHeight()

	// Walk forward from scrollTop accumulating cards until we run out of space.
	usedLines := 0
	visibleEnd := scrollTop - 1
	for ti := scrollTop; ti < len(col.Tickets); ti++ {
		h := cardHeight(col.Tickets[ti], inner)
		if usedLines+h > avail {
			break
		}
		usedLines += h
		visibleEnd = ti
	}
	if visibleEnd < scrollTop {
		visibleEnd = scrollTop // always show at least the top card
	}

	hiddenAbove := scrollTop
	hiddenBelow := len(col.Tickets) - 1 - visibleEnd

	if hiddenAbove > 0 {
		hint := fmt.Sprintf("(+%d more ▲)", hiddenAbove)
		lines = append(lines, mutedBorder.Render(padLine(hint, boardColumnWidth)))
	}
	for ti := scrollTop; ti <= visibleEnd; ti++ {
		lines = append(lines, cardView(ci == m.col && ti == m.card, col.Tickets[ti], boardColumnWidth, m.masterBoard)...)
	}
	if hiddenBelow > 0 {
		hint := fmt.Sprintf("(+%d more ▼)", hiddenBelow)
		lines = append(lines, mutedBorder.Render(padLine(hint, boardColumnWidth)))
	}

	return lipgloss.NewStyle().MarginRight(boardColumnGap).Render(strings.Join(lines, "\n"))
}

func cardView(focused bool, ticket storage.Ticket, width int, showBoard bool) []string {
	cardInnerWidth := width - 4
	cursor := " "
	if focused {
		cursor = ">"
	}

	title := ticket.DisplayID + " " + ticket.Title
	if showBoard && ticket.BoardName != "" {
		title = ticket.DisplayID + " [" + ticket.BoardName + "] " + ticket.Title
	}
	titleLines := wrapText(title, cardInnerWidth-2, 3)
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
	label := runtimeLabel(ticket)
	// Color the label according to runtime state.
	if label != "" {
		var labelStyle lipgloss.Style
		switch ticket.Runtime {
		case "needs_permission":
			labelStyle = lipgloss.NewStyle().Foreground(palette.error_).Bold(true)
		case "error":
			labelStyle = lipgloss.NewStyle().Foreground(palette.error_)
		default:
			labelStyle = lipgloss.NewStyle().Foreground(palette.muted)
		}
		label = labelStyle.Render(label)
	}
	var meta string
	switch {
	case label != "" && elapsed != "":
		meta = fmt.Sprintf("[%s] %s %s · %s", ticket.Harness, windowIndicator(ticket), label, elapsed)
	case label != "":
		meta = fmt.Sprintf("[%s] %s %s", ticket.Harness, windowIndicator(ticket), label)
	case elapsed != "":
		meta = fmt.Sprintf("[%s] %s · %s", ticket.Harness, windowIndicator(ticket), elapsed)
	default:
		meta = fmt.Sprintf("[%s] %s", ticket.Harness, windowIndicator(ticket))
	}
	content = append(content, padLine("  "+meta, cardInnerWidth))

	// Choose border style: attention colors take priority, then accent for focused, else muted.
	isResumable := ticket.SessionRef.Valid && ticket.SessionRef.String != ""
	var borderStyle lipgloss.Style
	switch {
	case ticket.Runtime == "needs_permission":
		borderStyle = lipgloss.NewStyle().Foreground(palette.error_)
	case ticket.Runtime == "error" && !isResumable:
		borderStyle = lipgloss.NewStyle().Foreground(palette.error_)
	case ticket.Runtime == "waiting_for_user":
		borderStyle = lipgloss.NewStyle().Foreground(palette.warning)
	case focused:
		borderStyle = accentBorder
	default:
		borderStyle = mutedBorder
	}
	lines := roundedBoxLines(content, width, borderStyle)
	if focused {
		return strings.Split(lipgloss.NewStyle().Bold(true).Render(strings.Join(lines, "\n")), "\n")
	}
	return lines
}

func runtimeLabel(ticket storage.Ticket) string {
	switch ticket.Runtime {
	case "error":
		if ticket.SessionRef.Valid && ticket.SessionRef.String != "" {
			return "" // resumable shown by ○
		}
		return "error"
	case "needs_permission":
		return "permission!"
	default:
		return ""
	}
}

func windowIndicator(ticket storage.Ticket) string {
	switch {
	case ticket.SessionActive && ticket.WindowName.Valid:
		return "●"
	case ticket.SessionRef.Valid && ticket.SessionRef.String != "" && !ticket.SessionActive:
		return "○"
	case ticket.Runtime == "error":
		return "!"
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

// palette defines the app-wide color tokens.
var palette = struct {
	muted   lipgloss.Color // borders, separators, dim chrome
	accent  lipgloss.Color // focused column header, focused card border
	warning lipgloss.Color // waiting_for_user
	error_  lipgloss.Color // error, needs_permission
	success lipgloss.Color // resumable
	header  lipgloss.Color // app header bar background
}{
	muted:   lipgloss.Color("240"),
	accent:  lipgloss.Color("75"),  // soft blue
	warning: lipgloss.Color("214"), // amber
	error_:  lipgloss.Color("203"), // coral red
	success: lipgloss.Color("71"),  // muted green
	header:  lipgloss.Color("237"), // dark gray bar
}

var (
	mutedBorder    = lipgloss.NewStyle().Foreground(palette.muted)
	accentBorder   = lipgloss.NewStyle().Foreground(palette.accent)
	footerRule     = lipgloss.NewStyle().Foreground(palette.muted)
	statusStyle    = lipgloss.NewStyle().Foreground(palette.muted).Italic(true)
	headerBarStyle = lipgloss.NewStyle().
			Bold(true).
			Background(palette.header).
			Foreground(lipgloss.Color("252")).
			PaddingLeft(1).
			PaddingRight(1)
	boardNameStyle = lipgloss.NewStyle().
			Background(palette.header).
			Foreground(lipgloss.Color("244")).
			PaddingRight(1)
)

func roundedBoxLines(lines []string, width int, border lipgloss.Style) []string {
	innerWidth := width - 2
	top := border.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	bot := border.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	out := make([]string, 0, len(lines)+2)
	out = append(out, top)
	for _, line := range lines {
		out = append(out, border.Render("│")+" "+padLine(line, innerWidth-2)+" "+border.Render("│"))
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
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if leftWidth+rightWidth+1 > width {
		return left
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
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
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

func (m *Model) reloadBoards() {
	selector, ok := m.store.(BoardSelector)
	if !ok {
		return
	}
	boards, err := selector.ListBoards(m.ctx)
	if err != nil {
		m.err = err
		return
	}
	m.boards = boards
	if m.boardID == 0 && len(boards) > 0 && !m.masterBoard {
		m.boardID = boards[0].ID
	}
}

func (m *Model) reload() {
	var view storage.BoardView
	var err error
	if selector, ok := m.store.(BoardSelector); ok {
		if m.masterBoard {
			view, err = selector.MasterBoardView(m.ctx)
		} else if m.boardID != 0 {
			view, err = selector.BoardViewByID(m.ctx, m.boardID)
		} else {
			view, err = m.store.BoardView(m.ctx)
		}
	} else {
		view, err = m.store.BoardView(m.ctx)
	}
	m.view = view
	m.err = err
	m.clamp()
	m.syncScrollDimensions()
	m.vScrollFollow()
	m.hScrollFollow()
}

// syncScrollDimensions ensures colScroll has one entry per column, preserving
// existing offsets and zeroing new ones.
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
// a column (box top + title lines + meta line + box bottom).
func cardHeight(ticket storage.Ticket, innerWidth int) int {
	cardInnerWidth := innerWidth - 4
	titleLines := wrapText(ticket.DisplayID+" "+ticket.Title, cardInnerWidth-2, 3)
	if len(titleLines) == 0 {
		titleLines = []string{ticket.DisplayID}
	}
	// top border + title lines + meta line + bottom border
	return 2 + len(titleLines) + 1 + 1
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
			h := cardHeight(col.Tickets[ti], inner)
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

func (m *Model) moveColumn(delta int) {
	m.col += delta
	m.clamp()
	m.hScrollFollow()
}

func (m *Model) moveCard(delta int) {
	m.card += delta
	m.clamp()
	m.vScrollFollow()
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
	if m.masterBoard {
		m.startMasterCreatePicker()
		return
	}
	m.createTicketInColumn(m.view.Columns[m.col].ID)
}

func (m *Model) startMasterCreatePicker() {
	if m.col < 0 || m.col >= len(m.view.Columns) {
		return
	}
	m.reloadBoards()
	if len(m.boards) == 0 {
		m.status = "no boards available"
		return
	}
	m.masterCreateCol = m.view.Columns[m.col].Name
	m.boardPicker = true
	m.boardPickerMode = "create"
	m.boardIndex = 0 // Master is not a create target; user must choose a real board.
	m.status = "choose board for new ticket"
}

func (m *Model) createTicketInColumn(columnID int64) {
	t, err := m.store.CreateTicket(m.ctx, columnID, "New ticket", "", "pi")
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
	toColumnID := m.view.Columns[to].ID
	if m.masterBoard || toColumnID < 0 {
		resolver, ok := m.store.(ColumnResolver)
		if !ok {
			m.status = "moving in master board unavailable"
			return
		}
		var err error
		toColumnID, err = resolver.ColumnIDByBoardAndName(m.ctx, t.BoardID, m.view.Columns[to].Name)
		if err != nil {
			m.status = "target column missing on ticket board"
			return
		}
	}
	if err := m.store.MoveTicket(m.ctx, t.ID, toColumnID); err != nil {
		m.status = err.Error()
		return
	}
	m.col = to
	m.reload()
	// Keep cursor on the ticket that was just moved.
	for i, ticket := range m.view.Columns[m.col].Tickets {
		if ticket.ID == t.ID {
			m.card = i
			break
		}
	}
	// Re-run scroll follow now that m.card reflects the moved ticket's position.
	m.vScrollFollow()
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

func (m *Model) openTicketCmd(sendPrompt bool) tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	opener, ok := m.store.(TicketOpener)
	if !ok {
		m.status = "open unavailable"
		return nil
	}
	if sendPrompt {
		m.status = "sending prompt " + t.DisplayID + "…"
	} else {
		m.status = "opening " + t.DisplayID + "…"
	}
	ctx := m.ctx
	return func() tea.Msg {
		return openTicketMsg{
			ticket:     t,
			sendPrompt: sendPrompt,
			err:        opener.OpenTicket(ctx, t, sendPrompt),
		}
	}
}

func (m *Model) closeSessionCmd() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	closer, ok := m.store.(SessionCloser)
	if !ok {
		m.status = "close unavailable"
		return nil
	}
	m.status = "closing " + t.DisplayID + "…"
	ctx := m.ctx
	displayID := t.DisplayID
	return func() tea.Msg {
		return closeSessionMsg{
			displayID: displayID,
			err:       closer.CloseTicketSession(ctx, t),
		}
	}
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
	body := m.editBody
	if m.editField != 1 {
		// Show a compact summary of the body when not editing it.
		newlines := strings.Count(body, "\n")
		if newlines > 0 {
			body = strings.SplitN(body, "\n", 2)[0] + lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(" (+%d lines)", newlines))
		}
	} else {
		body = render(1, body)
	}
	return fmt.Sprintf("Edit ticket\n\n%s title:   %s\n%s body:    %s\n%s harness: %s\n\nTab next field · Enter newline in body · ←/→ move · Home/End · Ctrl+E full editor · Esc cancel\n",
		rowCursor(0), render(0, m.editTitle),
		rowCursor(1), body,
		rowCursor(2), render(2, m.editHard))
}

func (m Model) stateMenuView() string {
	var b strings.Builder
	t, _ := m.selectedTicket()
	fmt.Fprintf(&b, "Mark state for %s\n\n", t.DisplayID)
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
	return fmt.Sprintf("%s\n\n> name: %s\n\nEnter save, Esc cancel\n", title, renderWithCursor(m.columnName, len([]rune(m.columnName))))
}

func (m Model) updateBoardPicker(key tea.KeyMsg) Model {
	count := len(m.boards) + 1 // master + boards
	if count > 0 && m.boardIndex >= count {
		m.boardIndex = count - 1
	}
	if m.boardIndex < 0 {
		m.boardIndex = 0
	}
	switch key.String() {
	case "esc", "b":
		m.boardPicker = false
	case "c":
		m.startBoardCreate()
	case "r":
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to rename"
			return m
		}
		b := m.boards[m.boardIndex-1]
		m.boardRenaming = true
		m.boardRenameReturnPicker = true
		m.boardRenameID = b.ID
		m.boardRenameName = b.Name
	case "w":
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to set cwd"
			return m
		}
		m.startBoardCWD(m.boards[m.boardIndex-1])
	case "d":
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to delete"
			return m
		}
		b := m.boards[m.boardIndex-1]
		m.boardDeleting = true
		m.boardDeleteID = b.ID
		m.boardDeleteName = b.Name
	case "j", "down":
		if count > 0 {
			m.boardIndex = (m.boardIndex + 1) % count
		}
	case "k", "up":
		if count > 0 {
			m.boardIndex--
			if m.boardIndex < 0 {
				m.boardIndex = count - 1
			}
		}
	case "enter":
		if m.boardPickerMode == "create" {
			if m.boardIndex == 0 {
				m.status = "choose a real board for new ticket"
				return m
			}
			if m.boardIndex-1 >= len(m.boards) {
				return m
			}
			resolver, ok := m.store.(ColumnResolver)
			if !ok {
				m.status = "board column lookup unavailable"
				m.boardPicker = false
				return m
			}
			b := m.boards[m.boardIndex-1]
			columnID, err := resolver.ColumnIDByBoardAndName(m.ctx, b.ID, m.masterCreateCol)
			if err != nil {
				m.status = "target board has no " + m.masterCreateCol + " column"
				m.boardPicker = false
				return m
			}
			m.boardPicker = false
			m.createTicketInColumn(columnID)
			return m
		}
		if m.boardIndex == 0 {
			m.masterBoard = true
			m.boardID = 0
			m.status = "switched to Master"
		} else if m.boardIndex-1 < len(m.boards) {
			b := m.boards[m.boardIndex-1]
			m.masterBoard = false
			m.boardID = b.ID
			m.status = "switched to " + b.Name
		}
		m.col, m.card, m.colOffset = 0, 0, 0
		m.colScroll = nil
		m.boardPicker = false
		m.reload()
	}
	return m
}

func (m Model) boardPickerView() string {
	var b strings.Builder
	title := "Select board"
	if m.boardPickerMode == "create" {
		title = "Create ticket in which board?"
	}
	fmt.Fprintf(&b, "%s\n\n", title)
	row := func(i int, name string) {
		cursor := " "
		if i == m.boardIndex {
			cursor = ">"
		}
		fmt.Fprintf(&b, "%s %s\n", cursor, name)
	}
	masterLabel := "Master (all boards)"
	if m.boardPickerMode == "create" {
		masterLabel = "Master (choose a real board below)"
	}
	row(0, masterLabel)
	for i, board := range m.boards {
		label := board.Name
		if board.Workdir != "" {
			label += "  " + lipgloss.NewStyle().Faint(true).Render(board.Workdir)
		}
		row(i+1, label)
	}
	if m.boardPickerMode == "create" {
		b.WriteString("\nEnter create · j/k move · Esc cancel\n")
	} else {
		b.WriteString("\nEnter select · c create · r rename · w cwd · d delete · j/k move · Esc cancel\n")
	}
	return b.String()
}

func (m *Model) startCurrentBoardRename(returnPicker bool) {
	if m.masterBoard || m.view.Board.ID == 0 {
		m.status = "cannot rename Master"
		return
	}
	m.boardRenaming = true
	m.boardRenameReturnPicker = returnPicker
	m.boardRenameID = m.view.Board.ID
	m.boardRenameName = m.view.Board.Name
}

func (m Model) updateBoardRename(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.boardRenaming = false
	case "enter":
		editor, ok := m.store.(BoardEditor)
		if !ok {
			m.status = "board rename unavailable"
			m.boardRenaming = false
			return m
		}
		name := strings.TrimSpace(m.boardRenameName)
		if err := editor.RenameBoard(m.ctx, m.boardRenameID, name); err != nil {
			m.status = err.Error()
			return m
		}
		m.status = "renamed board " + name
		m.boardRenaming = false
		m.reloadBoards()
		if !m.masterBoard && m.boardID == m.boardRenameID {
			m.reload()
		}
		if m.boardRenameReturnPicker {
			m.boardPicker = true
		}
	case "backspace":
		m.boardRenameName = popRune(m.boardRenameName)
	default:
		if len(key.Runes) > 0 {
			m.boardRenameName += string(key.Runes)
		}
	}
	return m
}

func (m Model) boardRenameView() string {
	return fmt.Sprintf("Rename board\n\n> name: %s\n\nEnter save · Esc cancel\n", renderWithCursor(m.boardRenameName, len([]rune(m.boardRenameName))))
}

func (m *Model) startBoardCreate() {
	cwd, _ := os.Getwd()
	m.boardEditing = true
	m.boardEditAction = "create"
	m.boardEditID = 0
	m.boardEditName = ""
	m.boardEditCWD = cwd
	m.boardEditField = 0
}

func (m *Model) startBoardCWD(board storage.Board) {
	m.boardEditing = true
	m.boardEditAction = "cwd"
	m.boardEditID = board.ID
	m.boardEditName = board.Name
	m.boardEditCWD = board.Workdir
	m.boardEditField = 1
}

func (m Model) updateBoardEdit(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.boardEditing = false
	case "tab":
		if m.boardEditAction == "create" {
			m.boardEditField = (m.boardEditField + 1) % 2
		}
	case "enter":
		editor, ok := m.store.(BoardEditor)
		if !ok {
			m.status = "board editing unavailable"
			m.boardEditing = false
			return m
		}
		switch m.boardEditAction {
		case "create":
			created, err := editor.CreateBoardWithWorkdir(m.ctx, strings.TrimSpace(m.boardEditName), strings.TrimSpace(m.boardEditCWD))
			if err != nil {
				m.status = err.Error()
				return m
			}
			m.status = "created board " + created.Name
		case "cwd":
			if err := editor.SetBoardWorkdir(m.ctx, m.boardEditID, strings.TrimSpace(m.boardEditCWD)); err != nil {
				m.status = err.Error()
				return m
			}
			m.status = "updated cwd " + m.boardEditName
		}
		m.boardEditing = false
		m.reloadBoards()
		m.boardPicker = true
	case "backspace":
		if m.boardEditField == 0 {
			m.boardEditName = popRune(m.boardEditName)
		} else {
			m.boardEditCWD = popRune(m.boardEditCWD)
		}
	default:
		if len(key.Runes) > 0 {
			if m.boardEditField == 0 {
				m.boardEditName += string(key.Runes)
			} else {
				m.boardEditCWD += string(key.Runes)
			}
		}
	}
	return m
}

func (m Model) boardEditView() string {
	status := ""
	if m.status != "" {
		status = "\n" + m.status + "\n"
	}
	if m.boardEditAction == "cwd" {
		return fmt.Sprintf("Set board cwd for %s%s\n> cwd: %s\n\nEnter save · Esc cancel\n", m.boardEditName, status, renderWithCursor(m.boardEditCWD, len([]rune(m.boardEditCWD))))
	}
	cursor := func(field int) string {
		if m.boardEditField == field {
			return ">"
		}
		return " "
	}
	render := func(field int, value string) string {
		if m.boardEditField == field {
			return renderWithCursor(value, len([]rune(value)))
		}
		return value
	}
	return fmt.Sprintf("Create board%s\n%s name: %s\n%s cwd:  %s\n\nTab switch field · Enter save · Esc cancel\n", status, cursor(0), render(0, m.boardEditName), cursor(1), render(1, m.boardEditCWD))
}

func (m Model) updateBoardDelete(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc", "n":
		m.boardDeleting = false
	case "y":
		editor, ok := m.store.(BoardEditor)
		if !ok {
			m.status = "board delete unavailable"
			m.boardDeleting = false
			return m
		}
		if err := editor.DeleteBoard(m.ctx, m.boardDeleteID); err != nil {
			m.status = err.Error()
			return m
		}
		m.status = "deleted board " + m.boardDeleteName
		m.boardDeleting = false
		m.reloadBoards()
		if m.boardID == m.boardDeleteID {
			m.masterBoard = true
			m.boardID = 0
			m.reload()
		}
		m.boardPicker = true
	}
	return m
}

func (m Model) boardDeleteView() string {
	return fmt.Sprintf("Delete board %q?\n\nThis deletes its tickets and sessions. Active sessions block deletion.\n\ny confirm · n/Esc cancel\n", m.boardDeleteName)
}

func (m Model) promptFallbackView() string {
	return fmt.Sprintf("Prompt readiness was not detected for %s.\n\np paste now\no open without sending\nc cancel\n", m.promptTicket.DisplayID)
}

func (m Model) repairView() string {
	header := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Faint(true)
	if m.repairEditingRef {
		return fmt.Sprintf("%s\n\n> ref: %s\n\nEnter save, Esc back\n",
			header.Render("Edit session ref for "+m.repairTicket.DisplayID),
			renderWithCursor(m.repairRef, len([]rune(m.repairRef))))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", header.Render("Session repair needed for "+m.repairTicket.DisplayID))
	fmt.Fprintf(&b, "%s\n\n", dim.Render(m.repairReason))
	b.WriteString("r  retry\n")
	b.WriteString("e  edit session ref\n")
	b.WriteString("f  start fresh\n")
	b.WriteString("c  cancel\n")
	if m.repairTicket.Harness == "copilot" && (!m.repairTicket.SessionRef.Valid || m.repairTicket.SessionRef.String == "") {
		fmt.Fprintf(&b, "\n%s\n", dim.Render("Note: Copilot does not expose a session ref; start fresh or edit ref manually."))
	}
	return b.String()
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

func (m Model) helpView() string {
	dim := lipgloss.NewStyle().Faint(true)
	var b strings.Builder

	section := func(title string) {
		fmt.Fprintf(&b, "\n%s\n", lipgloss.NewStyle().Foreground(palette.accent).Bold(true).Render(title))
	}
	row := func(key, desc string) {
		keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
		descStyle := lipgloss.NewStyle().Foreground(palette.muted)
		fmt.Fprintf(&b, "  %s%s\n", keyStyle.Render(padLine(key, 28)), descStyle.Render(desc))
	}

	section("Navigation")
	row("h/l  ←/→", "move focus between columns")
	row("j/k  ↑/⊓", "move focus between tickets")
	row("!", "jump to next attention ticket")

	section("Tickets")
	row("o  Enter", "open / switch to ticket session")
	row("s", "send prompt and open (never-started only)")
	row("x", "close ticket session")
	row("n", "new ticket in current column")
	row("e", "edit title / body / harness")
	row("E", "open body in $EDITOR")
	row("a", "archive ticket")
	row("m", "manually mark runtime state")
	row("H/L  Shift+←/→", "move ticket to adjacent column")
	row("J/K  Shift+↑/⊓", "reorder ticket within column")

	section("Columns")
	row("c", "add column")
	row("r", "rename column")
	row("D", "delete column (must be empty)")
	row("Ctrl+Shift+←/→", "reorder column")

	section("Boards")
	row("b", "switch board / open board picker")
	row("c in board picker", "create board")
	row("r in board picker", "rename selected board")
	row("w in board picker", "set selected board cwd")
	row("d in board picker", "delete selected board")

	section("App")
	row("q  Ctrl+C", "quit")
	row("?", "toggle this help")

	fmt.Fprintf(&b, "\n%s", dim.Render("Esc / ? to close"))

	// Wrap in a border box.
	body := b.String()
	bodyLines := strings.Split(body, "\n")
	// Find the widest visible line.
	maxW := 0
	for _, l := range bodyLines {
		if w := runeLen(lipgloss.NewStyle().Render(l)); w > maxW { // strip styles for measurement
			maxW = runeLen(l)
		}
	}
	if maxW < 40 {
		maxW = 40
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(0, 2).
		Render("\n" + lipgloss.NewStyle().Bold(true).Render("Keybindings") + body)
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
