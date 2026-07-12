package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
)

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Kanbi could not load the board.\n\nCause: %v\n\nNext: check the database path and permissions, then run `kanbi doctor`.\nPress Ctrl+C to exit safely; active agent sessions are not terminated.\n", m.err)
	}

	// Always render the base board first so modals can overlay it.
	base := m.baseView()

	if m.firstRun {
		return overlayModal(base, fitModal(m.onboardingView(), m.height, 0, false), m.width, m.height)
	}
	if m.showHelp {
		return overlayModal(base, fitModal(m.helpView(), m.height, m.modalScroll, false), m.width, m.height)
	}
	if m.boardRenaming {
		return overlayModal(base, fitModal(m.boardRenameView(), m.height, 0, true), m.width, m.height)
	}
	if m.boardEditing {
		return overlayModal(base, fitModal(m.boardEditView(), m.height, 0, true), m.width, m.height)
	}
	if m.boardDeleting {
		return overlayModal(base, fitModal(m.boardDeleteView(), m.height, 0, true), m.width, m.height)
	}
	if m.boardPicker {
		return overlayModal(base, fitModal(m.boardPickerView(), m.height, 0, true), m.width, m.height)
	}
	if m.masterFilterOpen {
		return overlayModal(base, fitModal(m.masterFilterView(), m.height, 0, true), m.width, m.height)
	}
	if m.editing {
		return overlayModal(base, fitModal(m.editView(), m.height, 0, true), m.width, m.height)
	}
	if m.stateMenu {
		return overlayModal(base, fitModal(m.stateMenuView(), m.height, 0, true), m.width, m.height)
	}
	if m.columnEditing {
		return overlayModal(base, fitModal(m.columnEditView(), m.height, 0, true), m.width, m.height)
	}
	if m.promptFallback {
		return overlayModal(base, fitModal(m.promptFallbackView(), m.height, 0, true), m.width, m.height)
	}
	if m.repairing {
		return overlayModal(base, fitModal(m.repairView(), m.height, 0, true), m.width, m.height)
	}

	return base
}

func (m Model) baseView() string {
	var b strings.Builder
	// Header bar: full-width background strip.
	appName := headerBarStyle.Render("Kanbi")
	headerName := m.view.Board.Name
	if m.masterBoard {
		if summary := m.masterFilterSummary(); summary != "" {
			headerName += "  filter: " + summary
		}
	}
	boardName := boardNameStyle.Render(headerName)
	barUsed := runeLen("Kanbi") + 2 + runeLen(headerName) + 1
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
	// Footer lines: rule + hints + optional status/remediation.
	footerLines := 2
	if m.status != "" {
		footerLines++
		if m.errOperation != "" {
			footerLines += 2
		}
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
	b.WriteString(m.contextBar() + "\n")
	if m.status != "" {
		if m.errOperation != "" {
			b.WriteString(statusStyle.Render(trimToWidth("Failed operation: "+m.errOperation, maxInt(1, m.width))) + "\n")
			b.WriteString(statusStyle.Render(trimToWidth("Cause: "+m.status, maxInt(1, m.width))) + "\n")
			b.WriteString(statusStyle.Render(trimToWidth("Next: "+m.errNext, maxInt(1, m.width))) + "\n")
		} else {
			b.WriteString(statusStyle.Render(trimToWidth(m.status, maxInt(1, m.width))) + "\n")
		}
	}
	return b.String()
}

func (m Model) contextBar() string {
	items := []string{"Enter:send/open", "n:new", "e:ticket", "b:boards"}
	if m.masterBoard {
		items = append(items, "f:filters")
	}
	items = append(items, "!:attention", "q:quit", "?:help+legend")
	if m.width < 60 {
		items = []string{"Enter:open", "n:new", "q:quit", "?:help"}
	}
	return trimToWidth(strings.Join(items, "  "), maxInt(1, m.width))
}

const (
	boardColumnWidth = 30
	boardColumnGap   = 1
)

func (m Model) boardView() string {
	if len(m.view.Columns) == 0 {
		return boxLines([]string{"No columns yet.", "Press c to create the first column.", "Press ? for help."}, minInt(boardColumnWidth, maxInt(12, m.width)))
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
		empty := "No tickets. Press n to create one."
		if m.masterBoard {
			empty = "No tickets match. Press f to change filters."
		}
		for _, line := range wrapText(empty, boardColumnWidth, 3) {
			lines = append(lines, padLine(line, boardColumnWidth))
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

	inner := boardColumnWidth - 2
	avail := m.boardContentHeight()

	// Walk forward from scrollTop accumulating cards until we run out of space.
	usedLines := 0
	visibleEnd := scrollTop - 1
	for ti := scrollTop; ti < len(col.Tickets); ti++ {
		h := cardHeightEx(col.Tickets[ti], inner, ci == m.col && ti == m.card)
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
	stateText := fmt.Sprintf("[%s] %s", ticket.Harness, label)
	if elapsed != "" {
		stateText += " · " + elapsed
	}
	stateStyle := lipgloss.NewStyle().Foreground(palette.muted)
	if ticket.Runtime == kanban.StateNeedsPermission || ticket.Runtime == kanban.StateError || ticket.Runtime == kanban.StateRepairNeeded {
		stateStyle = lipgloss.NewStyle().Foreground(palette.error_).Bold(true)
	} else if ticket.Runtime == kanban.StateWaitingForUser {
		stateStyle = lipgloss.NewStyle().Foreground(palette.warning).Bold(true)
	}
	for _, line := range wrapText(stateText, cardInnerWidth-2, 2) {
		content = append(content, padLine("  "+stateStyle.Render(line), cardInnerWidth))
	}
	for _, line := range wrapText("session: "+windowIndicator(ticket), cardInnerWidth-2, 2) {
		content = append(content, padLine("  "+line, cardInnerWidth))
	}

	// Body preview — only shown on the focused card.
	if focused {
		previewWidth := cardInnerWidth - 4
		if previewWidth < 10 {
			previewWidth = 10
		}
		preview := renderBodyPreview(ticket.Body, previewWidth)
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

func runtimeLabel(ticket storage.Ticket) string {
	resumable := ticket.SessionRef.Valid && strings.TrimSpace(ticket.SessionRef.String) != "" && !ticket.SessionActive
	switch ticket.Runtime {
	case kanban.StateNotStarted, "":
		return "not started"
	case kanban.StateStarting:
		return "starting"
	case kanban.StateRunning:
		return "running"
	case kanban.StateWaitingForUser:
		return "waiting for user"
	case kanban.StateNeedsPermission:
		return "permission required"
	case kanban.StateIdleUnknown:
		return "idle / unknown"
	case kanban.StateClosing:
		return "closing"
	case kanban.StateClosed:
		if resumable {
			return "closed / resumable"
		}
		return "closed"
	case kanban.StateExited:
		if resumable {
			return "exited / resumable"
		}
		return "exited"
	case kanban.StateRepairNeeded:
		return "repair required"
	case kanban.StateError:
		if resumable {
			return "error / resumable"
		}
		return "error"
	default:
		return "idle / unknown (" + strings.ReplaceAll(ticket.Runtime, "_", " ") + ")"
	}
}

func windowIndicator(ticket storage.Ticket) string {
	switch {
	case ticket.SessionActive && (ticket.WindowID.Valid || ticket.MuxContainerID.Valid):
		return "● active container"
	case ticket.SessionRef.Valid && strings.TrimSpace(ticket.SessionRef.String) != "" && !ticket.SessionActive:
		return "○ resumable"
	case ticket.Runtime == kanban.StateRepairNeeded:
		return "! repair"
	case ticket.Runtime == kanban.StateError:
		return "! error"
	default:
		return "- no active container"
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
	headerBarStyle = lipgloss.NewStyle().
			Bold(true).
			Background(palette.header).
			Foreground(palette.headerText).
			PaddingLeft(1).
			PaddingRight(1)
	boardNameStyle = lipgloss.NewStyle().
			Background(palette.header).
			Foreground(palette.headerSubtleText).
			PaddingRight(1)
)

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
	plain = strings.NewReplacer(
		"**", "", "__", "", "*", "", "_", "",
		"##", "", "#", "", "`", "",
	).Replace(plain)
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
	for _, line := range strings.Split(body, "\n") {
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

	section("Boards and columns")
	row("b / f", "board picker / Master filters")
	row("c / r / D", "add / rename / delete column")
	row("Ctrl+Shift+←/→", "reorder column")
	row("picker c/r/w/d", "create / rename / set cwd / delete board")
	row("filter C", "clear all Master filters")

	section("Textual state legend")
	row("not started", "no session attempt yet")
	row("starting / running", "launching / agent is active")
	row("waiting for user", "agent needs user input")
	row("permission required", "agent requests approval; not the same as waiting")
	row("idle / unknown", "no confident activity signal")
	row("closed / resumable", "container closed; verified ref can resume")
	row("repair required", "retry, edit ref, or start fresh")
	row("error", "operation or session failed; read Cause and Next")

	section("Indicator legend (works without color)")
	row("● active container", "validated live terminal container")
	row("○ resumable", "no live container; verified session ref exists")
	row("! error / repair", "action is required")
	row("- no active", "no validated live container")
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
