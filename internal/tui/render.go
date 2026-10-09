package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/statusbar"
	"github.com/carlotran4/kanbi/internal/storage"
)

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Kanbi could not load the board.\n\nCause: %v\n\nNext: check the database path and permissions, then run `kanbi doctor`.\nPress Ctrl+C to exit safely; active agent sessions are not terminated.\n", m.err)
	}

	// Always render the base board first so the selected modal can overlay it.
	base := m.cachedBaseView()
	switch m.activeModalKind() {
	case modalIntegration:
		return overlayModal(base, fitModal(m.integrationView(), m.height, 0, true), m.width, m.height)
	case modalFocusSettings:
		return overlayModal(base, fitModal(m.focusSettingsView(), m.height, 0, true), m.width, m.height)
	case modalFocusReplace:
		return overlayModal(base, fitModal(m.focusReplaceView(), m.height, 0, true), m.width, m.height)
	case modalPause:
		return overlayModal(base, fitModal(m.pauseView(), m.height, 0, true), m.width, m.height)
	case modalResume:
		return overlayModal(base, fitModal(m.resumeView(), m.height, 0, true), m.width, m.height)
	case modalOnboarding:
		return overlayModal(base, fitModal(m.onboardingView(), m.height, 0, false), m.width, m.height)
	case modalHelp:
		return overlayModal(base, fitModal(m.helpView(), m.height, m.modalScroll, false), m.width, m.height)
	case modalBoardRename:
		return overlayModal(base, fitModal(m.boardRenameView(), m.height, 0, true), m.width, m.height)
	case modalBoardWorktreeEnable:
		return overlayModal(base, fitModal(m.boardWorktreeEnableView(), m.height, 0, true), m.width, m.height)
	case modalBoardEdit:
		return overlayModal(base, fitModal(m.boardEditView(), m.height, 0, true), m.width, m.height)
	case modalBoardDelete:
		return overlayModal(base, fitModal(m.boardDeleteView(), m.height, 0, true), m.width, m.height)
	case modalBoardExport:
		return overlayModal(base, fitModal(m.boardExportView(), m.height, 0, true), m.width, m.height)
	case modalBoardImport:
		return overlayModal(base, fitModal(m.boardImportView(), m.height, 0, true), m.width, m.height)
	case modalBoardPicker:
		return overlayModal(base, fitModal(m.boardPickerView(), m.height, 0, true), m.width, m.height)
	case modalMasterFilter:
		return overlayModal(base, fitModal(m.masterFilterView(), m.height, 0, true), m.width, m.height)
	case modalEdit:
		return overlayModal(base, fitModal(m.editView(), m.inspectorViewportHeight()+2, 0, true), m.width, m.height)
	case modalStateMenu:
		return overlayModal(base, fitModal(m.stateMenuView(), m.height, 0, true), m.width, m.height)
	case modalColumnEdit:
		return overlayModal(base, fitModal(m.columnEditView(), m.height, 0, true), m.width, m.height)
	case modalPromptFallback:
		return overlayModal(base, fitModal(m.promptFallbackView(), m.height, 0, true), m.width, m.height)
	case modalRepair:
		return overlayModal(base, fitModal(m.repairView(), m.height, 0, true), m.width, m.height)
	case modalBranchName:
		return overlayModal(base, fitModal(m.branchNameView(), m.height, 0, true), m.width, m.height)
	case modalWorkspaceIntegration:
		return overlayModal(base, fitModal(m.workspaceIntegrationView(), m.height, 0, true), m.width, m.height)
	default:
		return base
	}
}

func (m Model) baseView() string {
	// Status bar: independently anchored left, center, and right module zones.
	left, center, right := m.statusBarZones(time.Now())
	if left != "" {
		left = " " + left
	}
	if right != "" {
		right += " "
	}
	headerLine := statusbar.Layout(left, center, right, maxInt(1, m.width))
	boardName := m.view.Board.Name
	if boardName == "" {
		boardName = "Board"
	}
	if m.focus.Enabled {
		focusText := fmt.Sprintf("Focus %d/%d", m.focus.Used, m.focus.Limit)
		if m.focus.OverCapacity {
			focusText = fmt.Sprintf("FOCUS %d/%d · pause %d to continue", m.focus.Used, m.focus.Limit, m.focus.Used-m.focus.Limit)
		} else if keys := strings.Join(m.focus.WorkflowKeys, ", "); keys != "" {
			focusText += " · " + keys
		}
		headerLine = focusHeaderLine(" Kanbi · "+boardName, focusText+" ", maxInt(1, m.width))
	}
	header := statusBarStyle.Render(headerLine) + "\n\n"
	board := m.boardView()
	hint := m.hScrollHint()

	footer := []string{
		footerRule.Render(strings.Repeat("─", m.width)),
		m.contextBar(),
	}
	if m.integrationNotice != "" {
		footer = append(footer, lipgloss.NewStyle().Foreground(palette.warning).Bold(true).Render(trimToWidth(m.integrationNotice, maxInt(1, m.width))))
	}
	if m.status != "" {
		if m.errOperation != "" {
			footer = append(footer,
				statusStyle.Render(trimToWidth("Failed operation: "+m.errOperation, maxInt(1, m.width))),
				statusStyle.Render(trimToWidth("Cause: "+m.status, maxInt(1, m.width))),
				statusStyle.Render(trimToWidth("Next: "+m.errNext, maxInt(1, m.width))),
			)
		} else {
			footer = append(footer, statusStyle.Render(trimToWidth(m.status, maxInt(1, m.width))))
		}
	}

	if m.refreshError != "" {
		footer = append(footer, statusStyle.Render(trimToWidth(m.refreshError, maxInt(1, m.width))))
	}

	headerRows := 2
	usedRows := headerRows + lipgloss.Height(board) + len(footer)
	if hint != "" {
		usedRows++
	}
	blankRows := m.height - usedRows
	if blankRows < 0 {
		blankRows = 0
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString(board)
	if hint != "" {
		b.WriteString("\n" + hint)
	}
	b.WriteString(strings.Repeat("\n", blankRows+1))
	b.WriteString(strings.Join(footer, "\n"))
	return b.String()
}

func focusHeaderLine(left, focusText string, width int) string {
	if width <= 0 {
		return ""
	}
	focusText = trimToWidth(focusText, width)
	focusWidth := lipgloss.Width(focusText)
	if focusWidth >= width {
		return focusText
	}
	leftWidth := width - focusWidth - 1
	left = trimToWidth(left, maxInt(0, leftWidth))
	return spaceBetween(left, focusText, width)
}

func (m Model) contextBar() string {
	items := []string{"Enter:send/open", "n:new", "e:ticket", "I:integrate", "b:boards", "F:focus"}
	if m.masterBoard {
		items = append(items, "f:filters")
	}
	items = append(items, "!:attention", "q:quit", "?:help+legend")
	if m.width <= 80 {
		items = []string{"Enter:open", "n:new", "b:boards", "F:focus"}
		if m.masterBoard {
			items = append(items, "f:filters")
		}
		items = append(items, "q:quit", "?:help")
	}
	return trimToWidth(strings.Join(items, "  "), maxInt(1, m.width))
}

const (
	boardColumnMinWidth = 30
	boardColumnMaxWidth = 44
	boardColumnGap      = 1
)

// boardColumnLayout returns the shared width and number of columns rendered
// from the current horizontal offset. Cards expand into spare room but retain
// the original 30-cell minimum on narrow terminals.
func (m Model) boardColumnLayout() (width, count int) {
	remaining := len(m.view.Columns) - m.colOffset
	if remaining <= 0 {
		return boardColumnMinWidth, 0
	}
	count = m.width / (boardColumnMinWidth + boardColumnGap)
	if count < 1 {
		count = 1
	}
	if count > remaining {
		count = remaining
	}
	width = m.width/count - boardColumnGap
	if width < boardColumnMinWidth {
		width = boardColumnMinWidth
	}
	if width > boardColumnMaxWidth {
		width = boardColumnMaxWidth
	}
	return width, count
}

func (m Model) boardView() string {
	if len(m.view.Columns) == 0 {
		return boxLines([]string{"No columns yet.", "Press c to create the first column.", "Press ? for help."}, minInt(boardColumnMinWidth, maxInt(12, m.width)))
	}

	columnWidth, visibleCount := m.boardColumnLayout()
	columns := make([]string, 0, visibleCount)
	for ci := m.colOffset; ci < m.colOffset+visibleCount; ci++ {
		columns = append(columns, m.columnView(ci, m.view.Columns[ci], columnWidth))
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

	_, visibleCount := m.boardColumnLayout()
	lastVisible := m.colOffset + visibleCount - 1
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

const (
	focusSectionFocused = iota
	focusSectionPaused
	focusSectionArchived
)

func focusTicketSection(ticket storage.Ticket) int {
	if ticket.ArchivedAt.Valid {
		return focusSectionArchived
	}
	if ticket.FocusPaused {
		return focusSectionPaused
	}
	return focusSectionFocused
}

func (m Model) columnView(ci int, col storage.Column, columnWidth int) string {
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
		spaceBetween(headerText, count, columnWidth),
		borderStyle.Render(strings.Repeat("─", columnWidth)),
	}

	summary := m.summarizeFocusColumn(col)
	isFocusColumn := summary.enabled
	focusedCount, pausedCount, archivedCount := summary.focusedCount, summary.pausedCount, summary.archivedCount
	if isFocusColumn {
		lines = append(lines, padLine(fmt.Sprintf("FOCUSED · %d", focusedCount), columnWidth))
		if focusedCount == 0 {
			lines = append(lines, mutedBorder.Render(padLine("No focused tickets", columnWidth)))
		}
	}
	if isFocusColumn && len(col.Tickets) == 0 {
		lines = append(lines, padLine("PAUSED · 0", columnWidth), mutedBorder.Render(padLine("No paused tickets", columnWidth)))
	}

	if len(col.Tickets) == 0 {
		empty := "No tickets. Press n to create one."
		if m.masterBoard {
			empty = "No tickets match. Press f to change filters."
		}
		for _, line := range wrapText(empty, columnWidth, 3) {
			lines = append(lines, padLine(line, columnWidth))
		}
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

	visibleEnd, showAbove, showBelow := m.visibleCardRangeWithSummary(ci, col, scrollTop, columnWidth, summary)
	hiddenAbove := scrollTop
	hiddenBelow := len(col.Tickets) - 1 - visibleEnd

	if showAbove {
		hint := fmt.Sprintf("(+%d more ▲)", hiddenAbove)
		lines = append(lines, mutedBorder.Render(padLine(hint, columnWidth)))
	}
	pausedHeadingShown := false
	archivedHeadingShown := false
	firstSection := focusTicketSection(col.Tickets[scrollTop])
	if isFocusColumn && pausedCount > 0 && firstSection == focusSectionPaused {
		lines = append(lines, padLine(fmt.Sprintf("PAUSED · %d", pausedCount), columnWidth))
		pausedHeadingShown = true
	}
	if isFocusColumn && firstSection == focusSectionArchived {
		if pausedCount == 0 {
			lines = append(lines, padLine("PAUSED · 0", columnWidth), mutedBorder.Render(padLine("No paused tickets", columnWidth)))
			pausedHeadingShown = true
		}
		lines = append(lines, padLine(fmt.Sprintf("ARCHIVED · %d", archivedCount), columnWidth))
		archivedHeadingShown = true
	}
	for ti := scrollTop; ti <= visibleEnd; ti++ {
		section := focusTicketSection(col.Tickets[ti])
		if isFocusColumn && section == focusSectionPaused && !pausedHeadingShown {
			lines = append(lines, padLine(fmt.Sprintf("PAUSED · %d", pausedCount), columnWidth))
			pausedHeadingShown = true
		}
		if isFocusColumn && section == focusSectionArchived && !archivedHeadingShown {
			if pausedCount == 0 && !pausedHeadingShown {
				lines = append(lines, padLine("PAUSED · 0", columnWidth), mutedBorder.Render(padLine("No paused tickets", columnWidth)))
				pausedHeadingShown = true
			}
			lines = append(lines, padLine(fmt.Sprintf("ARCHIVED · %d", archivedCount), columnWidth))
			archivedHeadingShown = true
		}
		lines = append(lines, m.cachedCardView(ci == m.col && ti == m.card, col.Tickets[ti], columnWidth, m.masterBoard)...)
	}
	if isFocusColumn && pausedCount == 0 && !pausedHeadingShown && !showBelow {
		lines = append(lines, padLine("PAUSED · 0", columnWidth), mutedBorder.Render(padLine("No paused tickets", columnWidth)))
	}
	if showBelow {
		hint := fmt.Sprintf("(+%d more ▼)", hiddenBelow)
		lines = append(lines, mutedBorder.Render(padLine(hint, columnWidth)))
	}

	return lipgloss.NewStyle().MarginRight(boardColumnGap).Render(strings.Join(lines, "\n"))
}

func hasFocusKey(status storage.FocusStatus, key string) bool {
	for _, configured := range status.WorkflowKeys {
		if configured == key {
			return true
		}
	}
	return false
}

func cardView(focused bool, ticket storage.Ticket, width int, showBoard bool) []string {
	preview := ""
	if focused {
		body := ticket.Body
		if ticket.FocusPaused && ticket.LatestCheckpoint != nil {
			body = "Next: " + ticket.LatestCheckpoint.NextAction
		}
		preview = renderBodyPreview(body, maxInt(10, width-8))
	}
	return cardViewWithPreview(focused, ticket, width, showBoard, preview)
}

func cardViewWithPreview(focused bool, ticket storage.Ticket, width int, showBoard bool, preview string) []string {
	cardInnerWidth := width - 4
	cursor := " "
	if focused {
		cursor = ">"
	}

	titleLines := wrapText(cardTitle(ticket, showBoard), cardInnerWidth-2, 3)
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

	stateText := cardStatusLine(ticket, cardInnerWidth-2)
	stateStyle := lipgloss.NewStyle().Foreground(palette.muted)
	if ticket.Runtime == kanban.StateNeedsPermission || ticket.Runtime == kanban.StateError || ticket.Runtime == kanban.StateRepairNeeded {
		stateStyle = lipgloss.NewStyle().Foreground(palette.error_).Bold(true)
	} else if ticket.Runtime == kanban.StateWaitingForUser {
		stateStyle = lipgloss.NewStyle().Foreground(palette.warning).Bold(true)
	}
	content = append(content, padLine("  "+stateStyle.Render(stateText), cardInnerWidth))
	if workspaceLine := ticketWorkspaceCardLine(ticket, cardInnerWidth-2); workspaceLine != "" {
		content = append(content, padLine("  "+workspaceLine, cardInnerWidth))
	}

	// Body preview — only shown on the focused card.
	if focused {
		for _, pl := range strings.Split(preview, "\n") {
			if strings.TrimSpace(pl) == "" {
				continue
			}
			// Use lipgloss.Width to check fit; do not trim graphics escapes,
			// since slicing them corrupts the terminal image command.
			if !containsImageEscape(pl) && lipgloss.Width(pl) > cardInnerWidth-2 {
				pl = pl[:len([]rune(pl))-1] // best-effort trim; glamour wraps to width so this rarely fires
			}
			content = append(content, padLine("  "+pl, cardInnerWidth))
		}
	}

	// Choose border style: attention colors take priority, then accent for focused, else muted.
	isResumable := ticket.SessionRef.Valid && ticket.SessionRef.String != ""
	var borderStyle lipgloss.Style
	switch {
	case ticket.Runtime == kanban.StateNeedsPermission:
		borderStyle = lipgloss.NewStyle().Foreground(palette.error_)
	case ticket.Runtime == kanban.StateError && !isResumable:
		borderStyle = lipgloss.NewStyle().Foreground(palette.error_)
	case ticket.Runtime == kanban.StateWaitingForUser:
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

func cardTitle(ticket storage.Ticket, showBoard bool) string {
	if showBoard && ticket.BoardName != "" {
		return ticket.DisplayID + " [" + ticket.BoardName + "] " + ticket.Title
	}
	return ticket.DisplayID + " " + ticket.Title
}

func cardStatusLine(ticket storage.Ticket, width int) string {
	harness := strings.TrimSpace(ticket.Harness)
	if harness == "" {
		harness = "pi"
	}
	indicator, label := runtimeIndicator(ticket), runtimeLabel(ticket)
	fixedWidth := lipgloss.Width(indicator) + 1 + 2 + lipgloss.Width(label)
	if maxHarnessWidth := width - fixedWidth; lipgloss.Width(harness) > maxHarnessWidth {
		harness = trimToWidth(harness, maxInt(1, maxHarnessWidth))
	}
	prefix := ""
	if ticket.FocusMember {
		if ticket.FocusPaused {
			prefix = "⏸ paused · "
		} else {
			prefix = "◆ focus · "
		}
	}
	line := fmt.Sprintf("%s%s %s  %s", prefix, indicator, harness, label)
	if elapsed := elapsedLabel(ticket); elapsed != "" {
		withElapsed := line + " · " + elapsed
		if lipgloss.Width(withElapsed) <= width {
			line = withElapsed
		}
	}
	return trimToWidth(line, width)
}

func runtimeLabel(ticket storage.Ticket) string {
	if info, ok := kanban.RuntimeStateFor(ticket.Runtime); ok {
		return info.CompactLabel
	}
	return "unknown"
}

func runtimeIndicator(ticket storage.Ticket) string {
	resumable := ticket.SessionRef.Valid && strings.TrimSpace(ticket.SessionRef.String) != "" && !ticket.SessionActive
	switch {
	case ticket.Runtime == kanban.StateWaitingForUser:
		return "?"
	case ticket.Runtime == kanban.StateNeedsPermission || ticket.Runtime == kanban.StateRepairNeeded:
		return "!"
	case resumable:
		return "○"
	case ticket.Runtime == kanban.StateError:
		return "×"
	case ticket.Runtime == kanban.StateStarting || ticket.Runtime == kanban.StateClosing:
		return "◐"
	case ticket.Runtime == kanban.StateIdleUnknown:
		return "◌"
	case ticket.Runtime == kanban.StateRunning && ticket.SessionActive && (ticket.WindowID.Valid || ticket.MuxContainerID.Valid):
		return "●"
	default:
		return "·"
	}
}

func workspaceNeedsResolution(ticket storage.Ticket) bool {
	if ticket.WorkspaceState.String == storage.WorkspaceStateResolving {
		return true
	}
	var obs struct {
		Conflicts []string `json:"conflicts"`
		Mergeable bool     `json:"mergeable"`
		Known     bool     `json:"mergeability_known"`
	}
	if !ticket.WorkspaceStatusJSON.Valid || json.Unmarshal([]byte(ticket.WorkspaceStatusJSON.String), &obs) != nil {
		return false
	}
	return len(obs.Conflicts) > 0 || (obs.Known && !obs.Mergeable)
}

func ticketWorkspaceCardLine(ticket storage.Ticket, width int) string {
	if !ticket.WorkspaceID.Valid || strings.TrimSpace(ticket.WorkspaceBranch.String) == "" {
		return ""
	}
	terminalState := ""
	switch ticket.WorkspaceState.String {
	case storage.WorkspaceStateIntegrated:
		terminalState = "git ✓ integrated"
	case storage.WorkspaceStateResolving:
		terminalState = "git ◐ resolving"
	case storage.WorkspaceStateRepairNeeded:
		terminalState = "git ! workspace repair"
	case storage.WorkspaceStateCleanupReq:
		terminalState = "git ! cleanup required"
	}
	if terminalState != "" {
		return trimToWidth(terminalState, width)
	}
	if ticket.WorkspaceLastError.Valid && strings.TrimSpace(ticket.WorkspaceLastError.String) != "" {
		return trimToWidth("git ! status unavailable", width)
	}

	var obs struct {
		Ahead             int      `json:"ahead"`
		Behind            int      `json:"behind"`
		Dirty             bool     `json:"dirty"`
		ChangedFiles      int      `json:"changed_files"`
		Conflicts         []string `json:"conflicts"`
		Mergeable         bool     `json:"mergeable"`
		MergeabilityKnown bool     `json:"mergeability_known"`
	}
	if !ticket.WorkspaceStatusJSON.Valid || json.Unmarshal([]byte(ticket.WorkspaceStatusJSON.String), &obs) != nil {
		return trimToWidth("git … status pending", width)
	}
	if len(obs.Conflicts) > 0 {
		return trimToWidth(fmt.Sprintf("git ✕ %d conflicts", len(obs.Conflicts)), width)
	}
	if obs.MergeabilityKnown && !obs.Mergeable {
		return trimToWidth("git ✕ conflicts", width)
	}

	line := "git"
	if obs.Dirty || obs.ChangedFiles > 0 {
		if obs.ChangedFiles > 0 {
			line = appendCardMeta(line, fmt.Sprintf("%d files", obs.ChangedFiles), width)
		} else {
			line = appendCardMeta(line, "dirty", width)
		}
	}
	divergence := ""
	if obs.Ahead > 0 {
		divergence = fmt.Sprintf("+%d", obs.Ahead)
	}
	if obs.Behind > 0 {
		if divergence == "" {
			divergence = fmt.Sprintf("-%d", obs.Behind)
		} else {
			divergence += fmt.Sprintf("/-%d", obs.Behind)
		}
	}
	if divergence != "" {
		line = appendCardMeta(line, divergence, width)
	}
	if !obs.Dirty && obs.ChangedFiles == 0 && divergence == "" {
		line = appendCardMeta(line, "clean", width)
	}
	if obs.MergeabilityKnown && obs.Mergeable {
		line = appendCardMeta(line, "✓ mergeable", width)
	}
	return line
}

func appendCardMeta(line, value string, width int) string {
	candidate := line + " · " + value
	if lipgloss.Width(candidate) <= width {
		return candidate
	}
	return line
}

func ticketWorkspaceDetailLine(ticket storage.Ticket) string {
	if !ticket.WorkspaceID.Valid || strings.TrimSpace(ticket.WorkspaceBranch.String) == "" {
		return ""
	}
	line := " " + ticket.WorkspaceBranch.String
	if status := ticketWorkspaceCardLine(ticket, 1<<20); status != "" {
		line += " · " + strings.TrimPrefix(status, "git ")
	}
	if ticket.WorkspaceState.String == storage.WorkspaceStateIntegrated {
		line += " · retired (reopens same session)"
	}
	return line
}

func (m Model) workspaceIntegrationView() string {
	t := m.workspaceActionTicket
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.warning).Render("Integrate ticket workspace locally?"),
		"",
		"Source: " + t.WorkspaceSourceBranch.String,
		"Ticket: " + t.WorkspaceBranch.String,
		ticketWorkspaceDetailLine(t),
		"",
		"Kanbi will revalidate both checkouts, close the agent, merge only into the recorded source, and retire the filesystem checkout. The branch/session remain for exact-path reopen.",
		"",
		lipgloss.NewStyle().Faint(true).Render("Enter integrate · Esc cancel"),
	}
	return modalFrame(lines, popupWidth(m.width), palette.warning)
}

func (m Model) branchNameView() string {
	p := m.branchPreflight
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.accent).Render("Branch name"),
		"",
		"Source: " + p.SourceBranch,
		"> " + modalInput(m.branchName, true, maxInt(1, modalContentWidth(popupWidth(m.width))-2)),
	}
	if p.SourceDirty {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(palette.warning).Render("Source has uncommitted changes; they are not included."))
	}
	if p.BranchExists {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(palette.warning).Render("Branch already exists. Press Enter again to use it explicitly."))
	}
	if m.branchConfirmed && p.SourceDirty && !p.BranchExists {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(palette.warning).Render("Press Enter again to continue without source changes."))
	}
	lines = append(lines, "", lipgloss.NewStyle().Faint(true).Render("Enter validate/create · Esc cancel (creates no session or workspace)"))
	return modalFrame(lines, popupWidth(m.width), palette.accent)
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

// theme defines app-wide semantic color tokens. Keep this as the single place
// where TUI colors are chosen so future user-configurable themes can swap one
// value object instead of chasing color literals across render code.
type theme struct {
	muted            lipgloss.TerminalColor // borders, separators, dim chrome
	accent           lipgloss.TerminalColor // focused column header, focused card border
	warning          lipgloss.TerminalColor // waiting_for_user
	error_           lipgloss.TerminalColor // error, needs_permission
	success          lipgloss.TerminalColor // resumable
	header           lipgloss.TerminalColor // app header bar background
	headerText       lipgloss.TerminalColor
	headerSubtleText lipgloss.TerminalColor
	chipText         lipgloss.TerminalColor
	chipTextInverted lipgloss.TerminalColor
	warningChipText  lipgloss.TerminalColor
	successChipText  lipgloss.TerminalColor
}

// palette defines app-wide semantic color tokens. Use AdaptiveColor so the
// interface keeps reasonable contrast across light and dark terminal themes.
var palette = theme{
	muted:            lipgloss.AdaptiveColor{Light: "245", Dark: "240"},
	accent:           lipgloss.AdaptiveColor{Light: "25", Dark: "75"},
	warning:          lipgloss.AdaptiveColor{Light: "130", Dark: "214"},
	error_:           lipgloss.AdaptiveColor{Light: "124", Dark: "203"},
	success:          lipgloss.AdaptiveColor{Light: "28", Dark: "71"},
	header:           lipgloss.AdaptiveColor{Light: "254", Dark: "237"},
	headerText:       lipgloss.AdaptiveColor{Light: "235", Dark: "252"},
	headerSubtleText: lipgloss.AdaptiveColor{Light: "240", Dark: "244"},
	chipText:         lipgloss.AdaptiveColor{Light: "235", Dark: "252"},
	chipTextInverted: lipgloss.AdaptiveColor{Light: "255", Dark: "255"},
	warningChipText:  lipgloss.AdaptiveColor{Light: "255", Dark: "230"},
	successChipText:  lipgloss.AdaptiveColor{Light: "255", Dark: "235"},
}

var (
	mutedBorder    = lipgloss.NewStyle().Foreground(palette.muted)
	accentBorder   = lipgloss.NewStyle().Foreground(palette.accent)
	footerRule     = lipgloss.NewStyle().Foreground(palette.muted)
	statusStyle    = lipgloss.NewStyle().Foreground(palette.muted).Italic(true)
	statusBarStyle = lipgloss.NewStyle().
			Bold(true).
			Background(palette.header).
			Foreground(palette.headerText)
)

var previewMarkdownReplacer = strings.NewReplacer("**", "", "__", "", "*", "", "_", "", "##", "", "#", "", "`", "")

// renderBodyPreview renders 1-2 lines of body preview for a card.
// It skips glamour to avoid ANSI width measurement issues in card layout;
// instead it word-wraps plain text and styles it faintly.
func renderBodyPreview(body string, width int) string {
	if strings.TrimSpace(body) == "" {
		return lipgloss.NewStyle().Faint(true).Italic(true).Render("(no description)")
	}
	plain := strings.TrimSpace(body)
	firstParagraph := firstPreviewParagraph(plain)
	if imageLines := renderMarkdownImagesPlaceholder(firstParagraph, width, 4); len(imageLines) > 0 {
		if len(imageLines) > 4 {
			imageLines = imageLines[:4]
		}
		return strings.Join(imageLines, "\n")
	}
	if imageLine := firstMarkdownImageLine(plain); imageLine != "" {
		if imageLines := renderMarkdownImagesPlaceholder(imageLine, width, 4); len(imageLines) > 0 {
			if len(imageLines) > 4 {
				imageLines = imageLines[:4]
			}
			return strings.Join(imageLines, "\n")
		}
	}
	// Plain-text wrap: take up to 2 lines from the first paragraph.
	plain = firstParagraph
	// Strip markdown syntax chars for a cleaner preview.
	plain = previewMarkdownReplacer.Replace(plain)
	lines := wrapText(strings.ReplaceAll(plain, "\n", " "), width, 2)
	if len(lines) == 0 {
		return lipgloss.NewStyle().Faint(true).Italic(true).Render("(no description)")
	}
	result := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Faint(true).Render(result)
}

func firstPreviewParagraph(body string) string {
	if idx := strings.Index(body, "\n\n"); idx >= 0 {
		return body[:idx]
	}
	return body
}

func firstMarkdownImageLine(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if markdownImageRE.MatchString(line) || bareImagePathRE.MatchString(line) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

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

// popupWidth returns the width for a centered popup given terminal width.
func popupWidth(termWidth int) int {
	w := termWidth * 9 / 10
	if w < 60 {
		w = 60
	}
	if w > termWidth {
		w = termWidth
	}
	return w
}

// overlayModal renders boardContent dimmed and composites popup centered on top.
func overlayModal(boardContent string, popup string, termWidth, termHeight int) string {
	// Strip existing ANSI from each board line before applying dim, so the faint
	// style actually takes hold rather than being ignored by already-styled text.
	dimStyle := lipgloss.NewStyle().Faint(true)
	bgLines := strings.Split(boardContent, "\n")
	for i, l := range bgLines {
		bgLines[i] = dimStyle.Render(ansiStrip(l))
	}
	// Fit the background to the actual terminal. This prevents a tall board from
	// pushing modal controls below the visible area in constrained layouts.
	if termHeight > 0 && len(bgLines) > termHeight {
		bgLines = bgLines[:termHeight]
	}
	for len(bgLines) < termHeight {
		bgLines = append(bgLines, "")
	}

	// Split popup into lines.
	popupLines := strings.Split(popup, "\n")
	popupH := len(popupLines)
	popupW := 0
	for _, l := range popupLines {
		if w := lipgloss.Width(l); w > popupW {
			popupW = w
		}
	}

	// Center position.
	startRow := (termHeight - popupH) / 2
	if startRow < 0 {
		startRow = 0
	}
	startCol := (termWidth - popupW) / 2
	if startCol < 0 {
		startCol = 0
	}

	// Composite: replace the background region with the popup lines.
	for i, pl := range popupLines {
		row := startRow + i
		if row >= len(bgLines) {
			break
		}
		bg := bgLines[row]
		// Strip ANSI from bg line to get plain runes for splicing.
		visible := []rune(lipgloss.NewStyle().Render(strings.Repeat(" ", startCol)))
		_ = visible
		// Build: left-pad of startCol spaces + popup line + (background discarded).
		left := ""
		if startCol > 0 {
			// Preserve the dimmed background on the left margin.
			bgRunes := []rune(ansiStrip(bg))
			if startCol <= len(bgRunes) {
				left = dimStyle.Render(string(bgRunes[:startCol]))
			} else {
				left = dimStyle.Render(padLine("", startCol))
			}
		}
		bgLines[row] = left + pl
	}
	return strings.Join(bgLines, "\n")
}

// ansiStrip removes ANSI escape sequences from a string.
func ansiStrip(s string) string {
	out := strings.Builder{}
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
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
	var lines []string
	current := ""
	for word := range strings.FieldsSeq(text) {
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
	visible := displayWidth(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

func displayWidth(s string) int {
	if containsImageEscape(s) {
		return lipgloss.Width(stripKittyGraphics(s))
	}
	return lipgloss.Width(s)
}

func stripKittyGraphics(s string) string {
	for {
		start := strings.Index(s, "\x1b_G")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "\x1b\\")
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + s[start+end+2:]
	}
	for {
		start := strings.Index(s, "\x1bPtmux;")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "\x1b\\")
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + s[start+end+2:]
	}
	return s
}

func trimToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "~")
}

func runeLen(s string) int {
	return len([]rune(s))
}

func (m Model) helpView() string {
	var lines []string
	section := func(title string) { lines = append(lines, "", strings.ToUpper(title)) }
	row := func(key, desc string) { lines = append(lines, fmt.Sprintf("%-19s %s", key, desc)) }

	lines = append(lines, "KEYBINDINGS AND LEGEND")
	section("Navigation")
	row("h/l or ←/→", "move between columns")
	row("j/k or ↓/↑", "move between tickets; in help, scroll")
	row("!", "jump to next attention ticket")

	section("Tickets")
	row("Enter", "send a new prompt, or open/resume the session")
	row("x", "safely close session")
	row("n / e / E", "new / inspect / edit body in $EDITOR")
	row("g / a / m", "open GitHub / archive / mark state")
	row("H/L", "move ticket left/right")
	row("J/K", "reorder ticket up/down")
	row("M", "move resumable session to configured multiplexer")
	row("I", "select worktrees for an agent-assisted integration run")

	section("Boards and columns")
	row("b / f", "board picker / Master filters")
	row("F", "global Focus Mode settings")
	row("c / r / D", "add / rename / delete column")
	row("Ctrl+Shift+←/→", "reorder column")
	row("picker c/r/w/d", "create / rename / set cwd / delete board")
	row("filter C", "clear all Master filters")

	section("Textual state legend")
	row("not started", "no session attempt yet")
	row("starting / running", "launching / agent is active")
	row("waiting", "agent needs user input")
	row("permission", "agent requests approval; not the same as waiting")
	row("idle", "no confident activity signal")
	row("closed / exited", "latest session ended")
	row("repair", "retry, edit ref, or start fresh")
	row("error", "operation or session failed; read Cause and Next")

	section("Card indicators (work without color)")
	row("● running", "validated live terminal container")
	row("○ resumable", "verified ref can resume the shown state")
	row("? / !", "waiting / permission or repair")
	row("◐ / ◌ / ×", "transitioning / idle / error")
	row("· not started", "no special runtime capability")
	row("> focused", "current keyboard target")

	section("Safety")
	row("q / Ctrl+C", "quit Kanbi; running agent sessions stay alive")
	row("? / Esc", "open / close help")
	lines = append(lines, "", "Scroll: j/k or ↑/↓ · Close: Esc, q, or ?")

	width := popupWidth(m.width)
	bodyWidth := maxInt(20, width-8)
	var wrapped []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			wrapped = append(wrapped, "")
			continue
		}
		parts := wrapText(line, bodyWidth, 10)
		wrapped = append(wrapped, parts...)
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(palette.accent).
		Padding(0, 2).
		Width(maxInt(1, width-4)).
		Render(strings.Join(wrapped, "\n"))
}
