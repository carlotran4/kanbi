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

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/session"
	"github.com/carlotran4/kanbi/internal/statusbar"
	"github.com/carlotran4/kanbi/internal/storage"
)

type Model struct {
	actions                 Actions
	ctx                     context.Context
	view                    storage.BoardView
	boards                  []storage.Board
	boardID                 int64
	masterBoard             bool
	masterFilter            storage.MasterFilter
	masterFilterDraft       storage.MasterFilter
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
	boardEditMode           string
	boardEditField          int
	boardWorktreeEnabling   bool
	boardWorktreeBoard      storage.Board
	boardDeleting           bool
	boardDeleteID           int64
	boardDeleteName         string
	boardDeleteInput        InputBuffer
	masterCreateCol         string
	masterCreateKey         string
	activePresetName        string
	filterPresets           []storage.MasterFilterPreset
	filterPresetIndex       int
	filterPresetMode        string // "", "list", "save"
	filterPresetName        string
	boardShowArchived       bool
	boardExporting          bool
	boardExportPath         string
	boardImporting          bool
	boardImportPath         string
	boardImportName         string
	boardImportField        int
	boardImportPreviewed    bool
	col                     int
	card                    int
	width                   int
	height                  int
	colScroll               []int // per-column vertical scroll offset (index of first visible card)
	colOffset               int   // horizontal scroll: index of first rendered column
	status                  string
	err                     error
	editing                 bool
	editTicket              storage.Ticket
	editField               int
	editInputs              [3]InputBuffer
	bodyTA                  textarea.Model
	bodyPasteRequest        uint64
	bodyPastePending        bool
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
	firstRun                bool
	onboardingPage          int
	modalScroll             int
	errOperation            string
	errNext                 string
	editorTicketID          int64
	reconcileWarning        string
	branchNaming            bool
	branchTicket            storage.Ticket
	branchName              string
	branchPreflight         storage.WorkspacePreflight
	branchConfirmed         bool
	branchSendPrompt        bool
	workspaceIntegrating    bool
	workspaceActionTicket   storage.Ticket
	integrationOpen         bool
	integrationCandidates   []integrationpkg.Candidate
	integrationSelected     map[int64]bool
	integrationIndex        int
	integrationRun          storage.IntegrationRun
	integrationPromoting    bool
	integrationCancelling   bool
	integrationNotice       string
	statusBar               statusbar.Config
	statusBarResults        map[string]statusbar.ModuleResult
	statusBarGeneration     uint64
	statusBarCtx            context.Context
	statusBarCancel         context.CancelFunc

	fileCompletion            fileCompletionState
	fileCompletionRequest     uint64
	fileCompletionIndexRoot   string
	fileCompletionIndexPaths  []string
	fileCompletionIndexLoaded bool
	fileCompletionIndexBusy   bool
	fileCompletionIndexReq    uint64

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
	return NewWithStatusBar(ctx, store, statusbar.DefaultConfig())
}

func NewWithStatusBar(ctx context.Context, store Actions, statusBar statusbar.Config) Model {
	statusBarCtx, statusBarCancel := context.WithCancel(ctx)
	m := Model{
		ctx:              ctx,
		actions:          store,
		width:            defaultTermWidth,
		height:           defaultTermHeight,
		statusBar:        statusBar,
		statusBarResults: make(map[string]statusbar.ModuleResult),
		statusBarCtx:     statusBarCtx,
		statusBarCancel:  statusBarCancel,
	}
	m.reloadBoards()
	m.reload()
	return m
}

func NewWithPicker(ctx context.Context, store Actions) Model {
	return NewWithPickerOptions(ctx, store, "")
}

// NewWithPickerOptions constructs the startup board picker model. reconcileWarning
// is an optional bootstrap message for startup reconciliation degraded state.
func NewWithPickerOptions(ctx context.Context, store Actions, reconcileWarning string) Model {
	return NewWithPickerStatusBarOptions(ctx, store, reconcileWarning, statusbar.DefaultConfig())
}

func NewWithPickerStatusBarOptions(ctx context.Context, store Actions, reconcileWarning string, statusBar statusbar.Config) Model {
	m := NewWithStatusBar(ctx, store, statusBar)
	m.boardPicker = true
	m.boardPickerMode = "switch"
	m.status = "select a board"
	m.reconcileWarning = strings.TrimSpace(reconcileWarning)
	if m.reconcileWarning != "" {
		m.status = m.reconcileWarning
	}
	// The startup path is the only place onboarding is enabled, keeping model
	// tests and embedded uses unobstructed. An empty installation can dismiss it
	// immediately; it is intentionally transient and does not alter durable data.
	if tickets, err := store.ListTickets(ctx, true); err == nil && len(tickets) == 0 {
		m.firstRun = true
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(runtimeTickCmd(), m.initialStatusBarCmd())
}

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

type moveMultiplexerMsg struct {
	displayID string
	err       error
}

type integrationActionMsg struct {
	action string
	run    storage.IntegrationRun
	err    error
}

type workspaceActionMsg struct {
	displayID string
	action    string
	err       error
}

type editorFinishedMsg struct {
	ticketID int64
	body     string
	err      error
}

type openExternalTicketMsg struct {
	displayID string
	url       string
	err       error
}

var externalURLCommand = defaultExternalURLCommand

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
		m.hScrollFollow()
		m.vScrollFollow()
		return m, nil
	case statusBarResultMsg:
		if !m.applyStatusBarResult(msg) {
			return m, nil
		}
		return m, m.scheduleStatusBarRefresh(msg.name, msg.generation)
	case statusBarRefreshMsg:
		if msg.generation != m.statusBarGeneration {
			return m, nil
		}
		return m, m.runStatusBarModule(msg.name)
	case runtimeTickMsg:
		if err := m.actions.RefreshRuntime(m.ctx); err != nil {
			m.setActionError("refresh runtime state", err, "Run `kanbi doctor`, then retry. Existing sessions are left running.")
		} else {
			m.reload()
		}
		return m, runtimeTickCmd()
	case editorFinishedMsg:
		m.applyEditorResult(msg)
		return m, tea.ClearScreen
	case bodyClipboardPasteMsg:
		return m.applyBodyClipboardPaste(msg), nil
	case fileCompletionResultMsg:
		m.applyFileCompletionResult(msg)
		return m, nil
	case openExternalTicketMsg:
		if msg.err != nil {
			m.setActionError("open GitHub issue", msg.err, "Check the ticket URL and your browser configuration, then press g to retry.")
		} else {
			m.status = "opened " + msg.displayID + " in GitHub"
		}
		return m, nil
	case openTicketMsg:
		var promptErr session.PromptReadyError
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
		case errors.Is(msg.err, session.ErrPromptAlreadySent):
			m.status = "Prompt already sent; open session instead?"
		case isRepairError(msg.err):
			m.startRepair(msg.ticket, msg.err)
		default:
			m.setActionError("open ticket session", msg.err, "Run `kanbi doctor`; fix the reported prerequisite, then press Enter to retry.")
		}
		return m, nil
	case closeSessionMsg:
		if msg.err != nil {
			m.setActionError("close ticket session", msg.err, "Open the session to inspect it, then press x to retry. Kanbi did not discard session history.")
		} else {
			m.status = "closed " + msg.displayID
			m.reload()
		}
		return m, nil
	case moveMultiplexerMsg:
		if msg.err != nil {
			m.setActionError("move ticket session", msg.err, "Run `kanbi doctor` and confirm a resumable session ref exists, then press M to retry.")
		} else {
			m.status = "moved " + msg.displayID + " to default multiplexer"
			m.reload()
		}
		return m, nil
	case integrationActionMsg:
		if msg.err != nil {
			m.setActionError(msg.action+" integration", msg.err, "Review the integration run, ticket branches, and source checkout, then retry.")
		} else {
			m.status = msg.action + " integration " + msg.run.PublicID
			m.integrationRun = msg.run
			m.reload()
		}
		return m, nil
	case workspaceActionMsg:
		if msg.err != nil {
			m.setActionError(msg.action+" ticket workspace", msg.err, "Commit or repair the workspace and source checkout, refresh status, then retry.")
		} else {
			m.status = msg.action + "d " + msg.displayID
			m.reload()
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Ctrl+C is a global, non-destructive application quit. Ticket runtime
	// containers remain alive regardless of which modal currently has focus.
	if key.String() == "ctrl+c" {
		m.statusBarCancel()
		return m, tea.Quit
	}
	if isKittyGraphicsResponse(string(key.Runes)) || isKittyGraphicsResponse(key.String()) {
		if m.editing {
			for i := range m.editInputs {
				clean := stripKittyGraphicsResponseFragments(m.editInputs[i].Value())
				if clean != m.editInputs[i].Value() {
					m.editInputs[i].Set(clean)
				}
			}
		}
		return m, nil
	}
	if m.firstRun {
		return m.updateOnboarding(key), nil
	}
	// Clear stale status on any keypress (unless a modal is consuming input).
	if !m.editing && !m.stateMenu && !m.columnEditing && !m.promptFallback && !m.repairing && !m.branchNaming && !m.integrationOpen && !m.boardRenaming && !m.boardEditing && !m.boardWorktreeEnabling && !m.boardDeleting && !m.boardExporting && !m.boardImporting && !m.masterFilterOpen {
		m.status = ""
		m.errOperation = ""
		m.errNext = ""
	}
	if m.integrationOpen {
		return m.updateIntegration(key)
	}
	if m.masterFilterOpen {
		return m.updateMasterFilter(key), nil
	}
	if m.workspaceIntegrating {
		switch key.String() {
		case "esc":
			m.workspaceIntegrating = false
			return m, nil
		case "enter":
			m.workspaceIntegrating = false
			t := m.workspaceActionTicket
			return m, func() tea.Msg {
				return workspaceActionMsg{displayID: t.DisplayID, action: "integrate", err: m.actions.IntegrateTicketWorkspace(m.ctx, t)}
			}
		}
		return m, nil
	}
	if m.branchNaming {
		return m.updateBranchName(key)
	}
	if m.promptFallback {
		return m.updatePromptFallback(key)
	}
	if m.boardRenaming {
		return m.updateBoardRename(key), nil
	}
	if m.boardWorktreeEnabling {
		return m.updateBoardWorktreeEnable(key), nil
	}
	if m.boardEditing {
		return m.updateBoardEdit(key), nil
	}
	if m.boardDeleting {
		return m.updateBoardDelete(key), nil
	}
	if m.boardExporting {
		return m.updateBoardExport(key), nil
	}
	if m.boardImporting {
		return m.updateBoardImport(key), nil
	}
	if m.boardPicker {
		previousBoardID, previousMaster := m.boardID, m.masterBoard
		m = m.updateBoardPicker(key)
		if previousBoardID != m.boardID || previousMaster != m.masterBoard {
			m.statusBarGeneration++
			m.statusBarResults = make(map[string]statusbar.ModuleResult)
			return m, m.initialStatusBarCmd()
		}
		return m, nil
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
			m.modalScroll = 0
		case "j", "down", "pgdown":
			m.modalScroll++
		case "k", "up", "pgup":
			if m.modalScroll > 0 {
				m.modalScroll--
			}
		}
		return m, nil
	}
	switch key.String() {
	case "?":
		m.showHelp = true
		m.modalScroll = 0
	case "b":
		m.reloadBoards()
		m.boardPicker = true
		m.boardPickerMode = "switch"
	case "f":
		m.startMasterFilter()
	case "q":
		m.statusBarCancel()
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
		if t, ok := m.selectedTicket(); ok && t.WorkspaceID.Valid && workspaceNeedsResolution(t) {
			return m, func() tea.Msg {
				return workspaceActionMsg{displayID: t.DisplayID, action: "resolve", err: m.actions.ResolveTicketWorkspace(m.ctx, t)}
			}
		}
		m.startColumnEdit("rename")
	case "D":
		m.deleteColumn()
	case "a":
		m.archiveTicket()
	case "e":
		m.startEdit()
	case "E":
		return m, m.openBodyEditor()
	case "g":
		return m, m.openExternalTicket()
	case "m":
		if t, ok := m.selectedTicket(); ok && t.WorkspaceID.Valid {
			m.workspaceIntegrating = true
			m.workspaceActionTicket = t
			m.status = "confirm local integration"
		} else {
			m.startStateMenu()
		}
	case "M":
		return m, m.moveToDefaultMultiplexerCmd()
	case "I":
		return m, m.openIntegration()
	case "enter":
		return m, withClearKittyImages(m.defaultTicketCmd())
	case "x":
		return m, m.closeSessionCmd()
	}
	return m, nil
}

func (m *Model) reloadBoards() {
	boards, err := m.actions.ListBoardsFiltered(m.ctx, m.boardShowArchived)
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
	m.hScrollFollow()
	m.integrationNotice = ""
	if m.view.Board.ID != 0 && m.view.Board.WorktreeMode == storage.WorktreeModeGit {
		if runs, listErr := m.actions.ListIntegrationRuns(m.ctx, m.view.Board.ID); listErr == nil {
			for _, run := range runs {
				if m.integrationOpen && run.PublicID == m.integrationRun.PublicID {
					m.integrationRun = run
				}
				switch run.State {
				case storage.IntegrationStateWaitingForUser:
					m.integrationNotice = "integration agent waiting for user · press I"
				case storage.IntegrationStateNeedsPermission:
					m.integrationNotice = "integration agent needs permission · press I"
				case storage.IntegrationStateReady:
					if m.integrationNotice == "" {
						m.integrationNotice = "integration candidate ready to promote · press I"
					}
				}
			}
		}
	}
	m.vScrollFollow()
}

// syncScrollDimensions ensures colScroll has one entry per column, preserving
// existing offsets and zeroing new ones.
func (m *Model) moveColumn(delta int) {
	m.col += delta
	m.clamp()
	m.hScrollFollow()
	m.vScrollFollow()
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
	m.masterCreateKey = m.view.Columns[m.col].WorkflowKey
	if m.masterCreateKey == "" {
		m.masterCreateKey = m.masterCreateCol
	}
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
		key := m.view.Columns[to].WorkflowKey
		if key == "" {
			key = m.view.Columns[to].Name
		}
		toColumnID, err = m.actions.ColumnIDByBoardAndWorkflowKey(m.ctx, t.BoardID, key)
		if err != nil {
			m.status = "target workflow key missing on ticket board"
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
	m.editTicket = t
	m.editField = 0
	m.bodyPasteRequest++
	m.bodyPastePending = false
	m.clearFileCompletion()
	m.resetFileCompletionIndex()
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

func (m *Model) defaultTicketCmd() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	sendPrompt := !t.SessionID.Valid
	if t.BoardWorktreeMode == storage.WorktreeModeGit && !t.WorkspaceID.Valid && !t.SessionActive {
		branch := normalizeBranchName(t.Title)
		preflight, err := m.actions.PreflightTicketWorkspace(m.ctx, t, branch)
		if err != nil {
			m.setActionError("prepare Git workspace", err, "Fix the board Git checkout, branch name, or worktree collision, then press Enter to retry.")
			return nil
		}
		m.branchNaming = true
		m.branchTicket = t
		m.branchName = branch
		m.branchPreflight = preflight
		m.branchConfirmed = false
		m.branchSendPrompt = sendPrompt
		m.status = "choose a branch for " + t.DisplayID
		return nil
	}
	return m.openTicketCmd(t, sendPrompt)
}

func (m *Model) openTicketCmd(t storage.Ticket, sendPrompt bool) tea.Cmd {
	if sendPrompt {
		m.status = "sending prompt " + t.DisplayID + "…"
	} else {
		m.status = "opening " + t.DisplayID + "…"
	}
	ctx := m.ctx
	return func() tea.Msg {
		return openTicketMsg{ticket: t, sendPrompt: sendPrompt, err: m.actions.OpenTicket(ctx, t, sendPrompt)}
	}
}

func normalizeBranchName(title string) string {
	var out strings.Builder
	lastSeparator := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			out.WriteRune(r)
			lastSeparator = false
		case r == '/':
			value := strings.Trim(out.String(), "-./")
			out.Reset()
			out.WriteString(value)
			if value != "" && !strings.HasSuffix(value, "/") {
				out.WriteRune('/')
			}
			lastSeparator = false
		default:
			if out.Len() > 0 && !lastSeparator && !strings.HasSuffix(out.String(), "/") {
				out.WriteRune('-')
				lastSeparator = true
			}
		}
	}
	value := strings.Trim(out.String(), "-./")
	if value == "" {
		return "ticket"
	}
	return value
}

func (m Model) updateBranchName(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.branchNaming = false
		m.status = "workspace creation cancelled"
		return m, nil
	case "backspace":
		m.branchName = popRune(m.branchName)
		m.branchConfirmed = false
		return m, nil
	case "enter":
		preflight, err := m.actions.PreflightTicketWorkspace(m.ctx, m.branchTicket, strings.TrimSpace(m.branchName))
		if err != nil {
			m.status = err.Error()
			m.branchConfirmed = false
			return m, nil
		}
		warningsChanged := preflight.BranchExists != m.branchPreflight.BranchExists || preflight.SourceDirty != m.branchPreflight.SourceDirty || preflight.SourceCommit != m.branchPreflight.SourceCommit || preflight.SourceBranch != m.branchPreflight.SourceBranch
		m.branchPreflight = preflight
		if warningsChanged {
			m.branchConfirmed = false
		}
		if (preflight.BranchExists || preflight.SourceDirty) && !m.branchConfirmed {
			m.branchConfirmed = true
			m.status = "review the warning, then press Enter again to confirm"
			return m, nil
		}
		if err := m.actions.PrepareTicketWorkspace(m.ctx, m.branchTicket, preflight.Branch, preflight.BranchExists); err != nil {
			m.status = err.Error()
			m.branchConfirmed = false
			return m, nil
		}
		m.branchNaming = false
		if m.branchTicket.SessionID.Valid {
			t := m.branchTicket
			return m, func() tea.Msg {
				return openTicketMsg{ticket: t, sendPrompt: true, err: m.actions.StartFreshTicket(m.ctx, t, true)}
			}
		}
		return m, m.openTicketCmd(m.branchTicket, m.branchSendPrompt)
	default:
		if len(key.Runes) > 0 {
			m.branchName += string(key.Runes)
			m.branchConfirmed = false
		}
		return m, nil
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

func (m *Model) moveToDefaultMultiplexerCmd() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	m.status = "moving " + t.DisplayID + " to default multiplexer…"
	ctx := m.ctx
	displayID := t.DisplayID
	return func() tea.Msg {
		return moveMultiplexerMsg{
			displayID: displayID,
			err:       m.actions.MoveTicketToDefaultMultiplexer(ctx, t),
		}
	}
}

func (m *Model) openExternalTicket() tea.Cmd {
	t, ok := m.selectedTicket()
	if !ok {
		return nil
	}
	if !t.ExternalURL.Valid || strings.TrimSpace(t.ExternalURL.String) == "" || !isGitHubTicketURL(t) {
		m.status = "selected ticket has no GitHub URL"
		return nil
	}
	url := strings.TrimSpace(t.ExternalURL.String)
	cmd, err := externalURLCommand(url)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.status = "opening " + t.DisplayID + " in GitHub…"
	displayID := t.DisplayID
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return openExternalTicketMsg{displayID: displayID, url: url, err: err}
	})
}

func isGitHubTicketURL(t storage.Ticket) bool {
	url := strings.ToLower(strings.TrimSpace(t.ExternalURL.String))
	return strings.HasPrefix(strings.ToUpper(t.DisplayID), "GH-") || strings.Contains(url, "github")
}

func defaultExternalURLCommand(url string) (*exec.Cmd, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("empty GitHub URL")
	}
	if browser := strings.TrimSpace(os.Getenv("BROWSER")); browser != "" {
		parts := strings.Fields(browser)
		if len(parts) > 0 {
			return exec.Command(parts[0], append(parts[1:], url)...), nil
		}
	}
	candidates := []struct {
		name string
		args []string
	}{
		{name: "xdg-open"},
		{name: "open"},
		{name: "wslview"},
		{name: "gio", args: []string{"open"}},
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		args := append(append([]string{}, candidate.args...), url)
		return exec.Command(path, args...), nil
	}
	return nil, fmt.Errorf("no browser opener found for GitHub URL")
}

func (m *Model) openBodyEditor() tea.Cmd {
	t, ok := m.selectedTicket()
	if m.editing {
		t = m.editTicket
		ok = t.ID != 0
	}
	if !ok {
		return nil
	}
	body := t.Body
	ticketID := t.ID
	if m.editing {
		body = m.bodyTA.Value()
	}
	m.editorTicketID = ticketID
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if _, err := exec.LookPath("nano"); err == nil {
			editor = "nano"
		} else {
			editor = "vi"
		}
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("kanbi-%s.md", t.DisplayID))
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
	body := numberPromptImages(msg.body)
	if m.editing {
		if msg.ticketID != m.editTicket.ID || msg.ticketID != m.editorTicketID {
			return
		}
		m.editInputs[1] = NewInputBuffer(body)
		m.bodyTA.SetValue(body)
		m.status = "body loaded from editor"
		return
	}
	t, err := findTicketByID(m.view, msg.ticketID)
	if err != nil {
		m.status = err.Error()
		return
	}
	if err := m.actions.UpdateTicket(m.ctx, t.ID, t.Title, body, t.Harness); err != nil {
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
