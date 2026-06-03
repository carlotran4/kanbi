package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"agent-kanban/internal/kanban"
	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
)

type Model struct {
	actions                 Actions
	ctx                     context.Context
	view                    storage.BoardView
	boards                  []storage.Board
	boardID                 int64
	masterBoard             bool
	masterFilter            storage.MasterFilter
	masterFilterOpen        bool
	masterFilterField       int
	masterFilterHarnesses   []string
	masterFilterRuntimes    []string
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
	editInputs              [3]InputBuffer
	bodyTA                  textarea.Model
	stateMenu               bool
	stateIndex              int
	columnEditing           bool
	columnAction            string
	columnInput             InputBuffer
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

	// Notes state (used within the edit modal, editField==3)
	notes       []storage.Note
	noteIndex   int
	noteEditing bool
	noteIsNew   bool
	noteEditID  int64
	noteTA      textarea.Model
}

// defaultTermSize is used before a WindowSizeMsg arrives.
const defaultTermWidth = 220
const defaultTermHeight = 40

func New(ctx context.Context, store Actions) Model {
	m := Model{ctx: ctx, actions: store, width: defaultTermWidth, height: defaultTermHeight}
	m.reloadBoards()
	m.reload()
	return m
}

func NewWithPicker(ctx context.Context, store Actions) Model {
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
		if err := m.actions.RefreshRuntime(m.ctx); err != nil {
			m.status = err.Error()
		} else {
			m.reload()
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
	if !m.editing && !m.stateMenu && !m.columnEditing && !m.promptFallback && !m.repairing && !m.boardRenaming && !m.boardEditing && !m.boardDeleting && !m.masterFilterOpen {
		m.status = ""
	}
	if m.masterFilterOpen {
		return m.updateMasterFilter(key), nil
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
	case "f":
		m.startMasterFilter()
	case "q", "ctrl+c":
		_ = m.actions.KillAllSessions(m.ctx)
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
	case "o":
		return m, m.openTicketCmd(false)
	case "enter":
		return m, m.enterTicketCmd()
	case "x":
		return m, m.closeSessionCmd()
	}
	return m, nil
}

func (m *Model) reloadBoards() {
	boards, err := m.actions.ListBoards(m.ctx)
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
	if m.masterBoard {
		view, err = m.actions.MasterBoardViewWithFilter(m.ctx, m.masterFilter)
	} else if m.boardID != 0 {
		view, err = m.actions.BoardViewByID(m.ctx, m.boardID)
	} else {
		view, err = m.actions.BoardView(m.ctx)
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
	t, err := m.actions.CreateTicket(m.ctx, columnID, "New ticket", "", "pi")
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

func (m *Model) reorderColumn(delta int) {
	if m.col < 0 || m.col >= len(m.view.Columns) {
		return
	}
	if err := m.actions.ReorderColumn(m.ctx, m.view.Columns[m.col].ID, delta); err != nil {
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
	name := m.view.Columns[m.col].Name
	if err := m.actions.DeleteColumn(m.ctx, m.view.Columns[m.col].ID); err != nil {
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
	if t.SessionID.Valid {
		if err := m.actions.CloseTicketSession(m.ctx, t); err != nil {
			m.status = err.Error()
			return
		}
	}
	if err := m.actions.ArchiveTicket(m.ctx, t.ID); err != nil {
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
		var err error
		toColumnID, err = m.actions.ColumnIDByBoardAndName(m.ctx, t.BoardID, m.view.Columns[to].Name)
		if err != nil {
			m.status = "target column missing on ticket board"
			return
		}
	}
	if err := m.actions.MoveTicket(m.ctx, t.ID, toColumnID); err != nil {
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
	if err := m.actions.ReorderTicket(m.ctx, t.ID, delta); err != nil {
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
	m.editInputs = [3]InputBuffer{
		NewInputBuffer(t.Title),
		NewInputBuffer(t.Body),
		NewInputBuffer(t.Harness),
	}
	m.bodyTA = newBodyTextarea(t.Body, m.width)
	m.loadNotes(t.ID)
}

func (m *Model) loadNotes(ticketID int64) {
	notes, err := m.actions.ListNotes(m.ctx, ticketID)
	if err != nil {
		m.notes = nil
	} else {
		m.notes = notes
	}
	m.noteIndex = len(m.notes) - 1
	if m.noteIndex < 0 {
		m.noteIndex = 0
	}
	m.noteEditing = false
	m.noteIsNew = false
}

func (m *Model) enterTicketCmd() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	return m.openSelectedTicketCmd(t, !t.SessionID.Valid)
}

func (m *Model) openTicketCmd(sendPrompt bool) tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	return m.openSelectedTicketCmd(t, sendPrompt)
}

func (m *Model) openSelectedTicketCmd(t storage.Ticket, sendPrompt bool) tea.Cmd {
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
			err:        m.actions.OpenTicket(ctx, t, sendPrompt),
		}
	}
}

func (m *Model) closeSessionCmd() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	m.status = "closing " + t.DisplayID + "…"
	ctx := m.ctx
	displayID := t.DisplayID
	return func() tea.Msg {
		return closeSessionMsg{
			displayID: displayID,
			err:       m.actions.CloseTicketSession(ctx, t),
		}
	}
}

func (m Model) openBodyEditor() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	body := t.Body
	ticketID := t.ID
	if m.editing {
		body = m.bodyTA.Value()
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
		m.editInputs[1] = NewInputBuffer(msg.body)
		m.bodyTA.SetValue(msg.body)
		m.status = "body loaded from editor"
		return
	}
	t, err := findTicketByID(m.view, msg.ticketID)
	if err != nil {
		m.status = err.Error()
		return
	}
	if err := m.actions.UpdateTicket(m.ctx, t.ID, t.Title, msg.body, t.Harness); err != nil {
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

func isAttention(state string) bool {
	return state == kanban.StateWaitingForUser || state == kanban.StateNeedsPermission || state == kanban.StateError
}
