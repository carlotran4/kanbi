package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/boardpackage"
	"github.com/carlotran4/kanbi/internal/storage"
)

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
		m.boardDeleteInput = NewInputBuffer("")
	case "a":
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to archive/unarchive"
			return m
		}
		b := m.boards[m.boardIndex-1]
		var err error
		if b.ArchivedAt.Valid {
			err = m.actions.UnarchiveBoard(m.ctx, b.ID)
			if err == nil {
				m.status = "unarchived " + b.Name + " (sync still disabled)"
			}
		} else {
			err = m.actions.ArchiveBoard(m.ctx, b.ID)
			if err == nil {
				m.status = "archived " + b.Name
			}
		}
		if err != nil {
			m.status = err.Error()
			return m
		}
		m.reloadBoards()
		if !b.ArchivedAt.Valid && !m.masterBoard && m.boardID == b.ID {
			m.masterBoard = true
			m.boardID = 0
			m.col, m.card, m.colOffset = 0, 0, 0
			m.colScroll = nil
			m.reload()
		}
		return m
	case "t":
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to enable worktrees"
			return m
		}
		b := m.boards[m.boardIndex-1]
		if b.WorktreeMode == storage.WorktreeModeGit {
			m.status = "Git worktrees are a durable board policy; create a separate shared-directory board if needed"
			return m
		}
		m.boardWorktreeEnabling = true
		m.boardWorktreeBoard = b
		return m
	case "s":
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to toggle sync"
			return m
		}
		b := m.boards[m.boardIndex-1]
		if b.ArchivedAt.Valid {
			m.status = "unarchive before enabling sync"
			return m
		}
		if err := m.actions.SetBoardSyncEnabled(m.ctx, b.ID, !b.SyncEnabled); err != nil {
			m.status = err.Error()
			return m
		}
		if b.SyncEnabled {
			m.status = "disabled sync for " + b.Name
		} else {
			m.status = "enabled sync for " + b.Name
		}
		m.reloadBoards()
		return m
	case "e":
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to export"
			return m
		}
		m.boardExporting = true
		m.boardExportPath = ""
		return m
	case "i":
		m.boardImporting = true
		m.boardImportPath = ""
		m.boardImportName = ""
		m.boardImportField = 0
		m.boardImportPreviewed = false
		return m
	case "A":
		m.boardShowArchived = !m.boardShowArchived
		m.reloadBoards()
		if m.boardShowArchived {
			m.status = "showing archived boards"
		} else {
			m.status = "hiding archived boards"
		}
		return m
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
			b := m.boards[m.boardIndex-1]
			key := m.masterCreateKey
			if key == "" {
				key = m.masterCreateCol
			}
			columnID, err := m.actions.ColumnIDByBoardAndWorkflowKey(m.ctx, b.ID, key)
			if err != nil {
				m.status = "target board has no workflow key " + key
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
	var lines []string
	title := "Select board"
	if m.boardPickerMode == "create" {
		title = "Create ticket in which board?"
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render(title)
	lines = append(lines, header, "")
	if strings.TrimSpace(m.reconcileWarning) != "" {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(palette.error_).Render("runtime reconciliation degraded (local data available)"),
			lipgloss.NewStyle().Faint(true).Render("Cause: "+trimToWidth(m.reconcileWarning, max(24, popupWidth(m.width)-16))),
			lipgloss.NewStyle().Faint(true).Render("Next: run `kanbi doctor`, then reopen or press Enter to continue offline"),
			"",
		)
	}
	row := func(i int, name string) string {
		if i == m.boardIndex {
			return lipgloss.NewStyle().Foreground(palette.accent).Render(">") + " " + name
		}
		return "  " + name
	}
	masterLabel := "Master (all boards)"
	if m.boardPickerMode == "create" {
		masterLabel = "Master (choose a real board below)"
	}
	lines = append(lines, row(0, masterLabel))
	for i, board := range m.boards {
		label := board.Name
		if board.WorktreeMode == storage.WorktreeModeGit {
			label += "  " + lipgloss.NewStyle().Faint(true).Render("[Git worktrees]")
		}
		if board.ArchivedAt.Valid {
			label += "  " + lipgloss.NewStyle().Faint(true).Render("[archived]")
		} else if !board.SyncEnabled {
			label += "  " + lipgloss.NewStyle().Faint(true).Render("[sync off]")
		}
		if board.LastSyncError.Valid && strings.TrimSpace(board.LastSyncError.String) != "" {
			label += "  " + lipgloss.NewStyle().Foreground(palette.error_).Render("provider sync degraded (local data available)")
		}
		if board.Workdir != "" {
			label += "  " + lipgloss.NewStyle().Faint(true).Render(board.Workdir)
		}
		if board.LastSyncError.Valid && strings.TrimSpace(board.LastSyncError.String) != "" {
			label += "  " + lipgloss.NewStyle().Faint(true).Render("Cause: "+trimToWidth(board.LastSyncError.String, 32)+" Next: kanbi sync --board "+board.Name)
		}
		lines = append(lines, row(i+1, label))
	}
	lines = append(lines, "")
	var hint string
	if m.boardPickerMode == "create" {
		hint = "Enter create · j/k move · Esc cancel"
	} else {
		hint = "Enter select · c create · r rename · w cwd · t enable worktrees · a archive · s sync · e export · i import · A archived · d delete · j/k · Esc"
	}
	lines = append(lines, lipgloss.NewStyle().Faint(true).Render(hint))
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}

func (m Model) updateBoardWorktreeEnable(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.boardWorktreeEnabling = false
		m.status = "worktree enable cancelled"
	case "enter":
		b := m.boardWorktreeBoard
		if err := m.actions.SetBoardWorktreeMode(m.ctx, b.ID, storage.WorktreeModeGit); err != nil {
			m.status = err.Error()
			return m
		}
		m.boardWorktreeEnabling = false
		m.status = "enabled Git worktrees for " + b.Name
		m.reloadBoards()
	}
	return m
}

func (m Model) boardWorktreeEnableView() string {
	b := m.boardWorktreeBoard
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render("Enable Git worktrees for " + b.Name + "?"),
		"",
		"This changes the board's durable execution policy.",
		"Existing inactive session history is preserved, but legacy sessions will start fresh when first opened in a worktree.",
		"After the board creates workspace history, worktrees cannot be disabled; create a separate shared-directory board instead.",
		"",
		lipgloss.NewStyle().Faint(true).Render("Enter enable · Esc cancel"),
	}
	return lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(palette.warning).Padding(1, 2).Width(popupWidth(m.width) - 4).Render(strings.Join(lines, "\n"))
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
		name := strings.TrimSpace(m.boardRenameName)
		if err := m.actions.RenameBoard(m.ctx, m.boardRenameID, name); err != nil {
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
	content := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Rename board") +
		"\n\n" +
		fmt.Sprintf("%s name: %s", lipgloss.NewStyle().Foreground(palette.accent).Render(">"), renderWithCursor(m.boardRenameName, len([]rune(m.boardRenameName)))) +
		"\n\n" + lipgloss.NewStyle().Faint(true).Render("Enter save · Esc cancel")
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(content)
}

func (m *Model) startBoardCreate() {
	cwd, _ := os.Getwd()
	m.boardEditing = true
	m.boardEditAction = "create"
	m.boardEditID = 0
	m.boardEditName = ""
	m.boardEditCWD = cwd
	m.boardEditMode = storage.WorktreeModeOff
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
			m.boardEditField = (m.boardEditField + 1) % 3
		}
	case "left", "right", " ":
		if m.boardEditAction == "create" && m.boardEditField == 2 {
			if m.boardEditMode == storage.WorktreeModeGit {
				m.boardEditMode = storage.WorktreeModeOff
			} else {
				m.boardEditMode = storage.WorktreeModeGit
			}
		}
	case "enter":
		switch m.boardEditAction {
		case "create":
			created, err := m.actions.CreateBoardWithWorkdirMode(m.ctx, strings.TrimSpace(m.boardEditName), strings.TrimSpace(m.boardEditCWD), m.boardEditMode)
			if err != nil {
				m.status = err.Error()
				return m
			}
			m.status = "created board " + created.Name
		case "cwd":
			if err := m.actions.SetBoardWorkdir(m.ctx, m.boardEditID, strings.TrimSpace(m.boardEditCWD)); err != nil {
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
		} else if m.boardEditField == 1 {
			m.boardEditCWD = popRune(m.boardEditCWD)
		}
	default:
		if len(key.Runes) > 0 {
			if m.boardEditField == 0 {
				m.boardEditName += string(key.Runes)
			} else if m.boardEditField == 1 {
				m.boardEditCWD += string(key.Runes)
			}
		}
	}
	return m
}

func (m Model) boardEditView() string {
	popupW := popupWidth(m.width)
	var lines []string
	cursor := func(field int) string {
		if m.boardEditField == field {
			return lipgloss.NewStyle().Foreground(palette.accent).Render(">")
		}
		return " "
	}
	render := func(field int, value string) string {
		if m.boardEditField == field {
			return renderWithCursor(value, len([]rune(value)))
		}
		return value
	}
	if m.boardEditAction == "cwd" {
		header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Set board cwd for " + m.boardEditName)
		lines = append(lines, header, "")
		if m.status != "" {
			lines = append(lines, statusStyle.Render(m.status), "")
		}
		lines = append(lines, fmt.Sprintf("%s cwd: %s", cursor(1), render(1, m.boardEditCWD)))
	} else {
		header := lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Create board")
		lines = append(lines, header, "")
		if m.status != "" {
			lines = append(lines, statusStyle.Render(m.status), "")
		}
		lines = append(lines, fmt.Sprintf("%s name: %s", cursor(0), render(0, m.boardEditName)))
		lines = append(lines, fmt.Sprintf("%s cwd:  %s", cursor(1), render(1, m.boardEditCWD)))
		modeLabel := "shared board directory"
		if m.boardEditMode == storage.WorktreeModeGit {
			modeLabel = "isolated Git worktrees"
		}
		lines = append(lines, fmt.Sprintf("%s execution: %s", cursor(2), modeLabel))
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Tab switch field · ←/→ change execution · Enter save · Esc cancel"))
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}

func (m Model) updateBoardDelete(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.boardDeleting = false
	case "enter":
		if m.boardDeleteInput.Value() != m.boardDeleteName {
			m.status = "type the exact board name to confirm deletion"
			return m
		}
		if err := m.actions.DeleteBoard(m.ctx, m.boardDeleteID); err != nil {
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
	default:
		m.boardDeleteInput.HandleKey(key.String(), key.Runes)
	}
	return m
}

func (m Model) updateBoardExport(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.boardExporting = false
	case "enter":
		path := strings.TrimSpace(m.boardExportPath)
		if path == "" {
			m.status = "export path is required"
			return m
		}
		if m.boardIndex == 0 || m.boardIndex-1 >= len(m.boards) {
			m.status = "choose a real board to export"
			m.boardExporting = false
			return m
		}
		b := m.boards[m.boardIndex-1]
		if err := m.actions.ExportBoard(m.ctx, b.ID, path); err != nil {
			m.status = err.Error()
			return m
		}
		m.status = "exported " + b.Name + " to " + path
		m.boardExporting = false
	case "backspace":
		m.boardExportPath = popRune(m.boardExportPath)
	default:
		if len(key.Runes) > 0 {
			m.boardExportPath += string(key.Runes)
		}
	}
	return m
}

func (m Model) boardExportView() string {
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Export board package"),
		"",
		"path: " + renderWithCursor(m.boardExportPath, len([]rune(m.boardExportPath))),
		"",
		lipgloss.NewStyle().Faint(true).Render("Writes a kanbi-board-package zip. Active sessions block export. Enter export · Esc cancel"),
	}
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}

func (m Model) updateBoardImport(key tea.KeyMsg) Model {
	switch key.String() {
	case "esc":
		m.boardImporting = false
	case "tab":
		m.boardImportField = (m.boardImportField + 1) % 2
	case "enter":
		path := strings.TrimSpace(m.boardImportPath)
		if path == "" {
			m.status = "import path is required"
			return m
		}
		if !m.boardImportPreviewed {
			report, err := m.actions.PreviewBoardPackage(m.ctx, path)
			if err != nil {
				m.status = err.Error()
				return m
			}
			m.boardImportPreviewed = true
			m.status = fmt.Sprintf("preview %s tickets=%d notes=%d attachments=%d collision=%v · Enter to import", report.BoardName, report.TicketCount, report.NoteCount, report.AttachmentCount, report.NameCollision)
			if report.NameCollision && strings.TrimSpace(m.boardImportName) == "" {
				m.status += " (set rename first)"
			}
			return m
		}
		result, err := m.actions.ImportBoardPackage(m.ctx, path, boardpackage.ImportOptions{NameOverride: strings.TrimSpace(m.boardImportName)})
		if err != nil {
			m.status = err.Error()
			return m
		}
		m.status = "imported " + result.Board.Name + " (archived, sync disabled)"
		m.boardImporting = false
		m.reloadBoards()
		m.boardPicker = true
	case "backspace":
		if m.boardImportField == 0 {
			m.boardImportPath = popRune(m.boardImportPath)
		} else {
			m.boardImportName = popRune(m.boardImportName)
		}
		m.boardImportPreviewed = false
	default:
		if len(key.Runes) > 0 {
			if m.boardImportField == 0 {
				m.boardImportPath += string(key.Runes)
			} else {
				m.boardImportName += string(key.Runes)
			}
			m.boardImportPreviewed = false
		}
	}
	return m
}

func (m Model) boardImportView() string {
	pathLine := "path: " + m.boardImportPath
	nameLine := "rename: " + m.boardImportName
	if m.boardImportField == 0 {
		pathLine = "> path: " + renderWithCursor(m.boardImportPath, len([]rune(m.boardImportPath)))
		nameLine = "  rename: " + m.boardImportName
	} else {
		pathLine = "  path: " + m.boardImportPath
		nameLine = "> rename: " + renderWithCursor(m.boardImportName, len([]rune(m.boardImportName)))
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Import board package"),
		"",
		pathLine,
		nameLine,
		"",
		lipgloss.NewStyle().Faint(true).Render("Enter previews then imports create-new board (always archived+sync off). Tab field · Esc cancel"),
	}
	if strings.TrimSpace(m.status) != "" {
		lines = append(lines, "", statusStyle.Render(m.status))
	}
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}

func (m Model) boardDeleteView() string {
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.error_).Render("Delete board \"" + m.boardDeleteName + "\"?"),
		"",
		"Permanently deletes tickets, notes, complete session history, and attachments.",
		lipgloss.NewStyle().Faint(true).Render("Hard delete is local-only. Prefer archive (a) to hide without destroying history. Active sessions block deletion."),
		"",
		"Type the exact board name to confirm:",
		m.boardDeleteInput.Render(),
		"",
		lipgloss.NewStyle().Faint(true).Render("Enter delete · Esc cancel"),
	}
	popupW := popupWidth(m.width)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.error_).
		Padding(1, 2).
		Width(popupW - 4).
		Render(strings.Join(lines, "\n"))
}
