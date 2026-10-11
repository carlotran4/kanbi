package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type inspectorState struct {
	ticketID                    int64
	savedScroll                 []int
	savedOffset                 int
	anchorBoardID               int64
	anchorMaster                bool
	open, focused               bool
	section, scroll, horizontal int
	followOutput                bool
	target                      string
	generation                  uint64
	busy                        bool
	cancel                      context.CancelFunc
	preview                     multiplexer.TerminalPreview
	err                         string
	next                        time.Time
}
type inspectorTickMsg time.Time
type inspectorPreviewMsg struct {
	generation uint64
	preview    multiplexer.TerminalPreview
	err        error
}

func inspectorTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return inspectorTickMsg(t) })
}

// Update schedules one bounded capture at a time, after all selection/modal changes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.update(msg)
	switch next := updated.(type) {
	case Model:
		capture := next.syncInspector()
		return next, tea.Batch(cmd, capture)
	case *Model:
		capture := next.syncInspector()
		return next, tea.Batch(cmd, capture)
	default:
		return updated, cmd
	}
}

func (m *Model) inspectorNarrow() bool { return m.width < 120 }
func (m Model) boardWidth() int {
	if m.inspector.open && !m.inspectorNarrow() {
		return maxInt(30, m.width-m.inspectorWidth()-1)
	}
	return m.width
}
func (m Model) inspectorWidth() int {
	if m.inspectorNarrow() {
		return maxInt(1, m.width)
	}
	return minInt(64, maxInt(40, m.width/3))
}
func (m *Model) syncInspector() tea.Cmd {
	t, ok := m.selectedTicket()
	if t.ID != m.inspector.ticketID {
		m.inspector.ticketID = t.ID
		m.inspector.scroll, m.inspector.horizontal = 0, 0
		m.inspector.followOutput = true
	}
	target := ""
	if ok {
		target = fmt.Sprintf("%d/%d/%s/%s/%s/%t", t.ID, t.SessionID.Int64, t.MuxContainerID.String, t.MuxNamespace.String, t.MuxMetadata.String, t.SessionActive)
	}
	visible := m.inspector.open && m.inspector.section == 1 && m.activeModalKind() == modalNone
	if !visible {
		target = ""
	}
	if target != m.inspector.target {
		if m.inspector.cancel != nil {
			m.inspector.cancel()
		}
		m.inspector.generation++
		m.inspector.target = target
		m.inspector.preview = multiplexer.TerminalPreview{}
		m.inspector.err = ""
		m.inspector.next = time.Time{}
		m.inspector.scroll, m.inspector.horizontal = 0, 0
		m.inspector.followOutput = true
	}
	if !visible || !ok || m.inspector.busy || time.Now().Before(m.inspector.next) {
		return nil
	}
	if !t.SessionActive {
		m.inspector.err = "no active terminal container"
		return nil
	}
	reader, ok := m.actions.(app.TerminalPreviewer)
	if !ok {
		m.inspector.err = "terminal preview unsupported"
		return nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	m.inspector.cancel = cancel
	m.inspector.busy = true
	generation := m.inspector.generation
	return func() tea.Msg {
		defer cancel()
		preview, err := reader.PreviewTicket(ctx, t)
		return inspectorPreviewMsg{generation: generation, preview: preview, err: err}
	}
}

func (m *Model) inspectorKey(key tea.KeyMsg) bool {
	switch key.String() {
	case "i":
		if !m.inspector.open {
			m.inspector.savedScroll = append([]int(nil), m.colScroll...)
			m.inspector.savedOffset = m.colOffset
			m.inspector.anchorBoardID, m.inspector.anchorMaster = m.boardID, m.masterBoard
		} else {
			m.restoreInspectorAnchor()
		}
		m.inspector.open = !m.inspector.open
		m.inspector.focused = m.inspector.open && m.inspectorNarrow()
		if m.inspector.open && m.inspector.section == 1 {
			m.inspector.followOutput = true
		}
		m.renderVersion++
		m.hScrollFollow()
		m.vScrollFollow()
		return true
	case "esc":
		if m.inspector.open {
			if m.inspectorNarrow() || !m.inspector.focused {
				m.restoreInspectorAnchor()
				m.inspector.open = false
			} else {
				m.inspector.focused = false
			}
			m.renderVersion++
			m.hScrollFollow()
			m.vScrollFollow()
			return true
		}
	case "tab", "shift+tab":
		if m.inspector.open && !m.inspectorNarrow() {
			m.inspector.focused = !m.inspector.focused
			return true
		}
	case "[", "]":
		if m.inspector.open {
			delta := 1
			if key.String() == "[" {
				delta = 2
			}
			m.inspector.section = (m.inspector.section + delta) % 3
			m.inspector.scroll, m.inspector.horizontal = 0, 0
			m.inspector.followOutput = m.inspector.section == 1
			return true
		}
	}
	if !m.inspector.open || !(m.inspector.focused || m.inspectorNarrow()) {
		return false
	}
	if m.inspector.section == 1 && m.inspector.followOutput {
		switch key.String() {
		case "j", "down", "k", "up", "pgdown", "pgup":
			// View renders a copy of the model. Persist the visible bottom before
			// applying a manual scroll so Up moves one line from that position.
			_ = m.inspectorView(m.boardContentHeight() + 2)
		}
	}
	switch key.String() {
	case "h", "l":
		delta := 1
		if key.String() == "h" {
			delta = 2
		}
		m.inspector.section = (m.inspector.section + delta) % 3
		m.inspector.scroll, m.inspector.horizontal = 0, 0
		m.inspector.followOutput = m.inspector.section == 1
		return true
	case "j", "down":
		m.inspector.scroll++
	case "k", "up":
		m.inspector.scroll = maxInt(0, m.inspector.scroll-1)
		m.inspector.followOutput = false
	case "pgdown":
		m.inspector.scroll += maxInt(1, m.boardContentHeight()-5)
	case "pgup":
		m.inspector.scroll = maxInt(0, m.inspector.scroll-maxInt(1, m.boardContentHeight()-5))
		m.inspector.followOutput = false
	case "left":
		m.inspector.horizontal = maxInt(0, m.inspector.horizontal-4)
	case "right":
		m.inspector.horizontal += 4
	case "home":
		m.inspector.scroll, m.inspector.horizontal = 0, 0
		m.inspector.followOutput = false
	default:
		return false
	}
	_ = m.inspectorView(m.boardContentHeight() + 2) // clamp after scrolling
	return true
}

func (m *Model) inspectorView(height int) string {
	width := m.inspectorWidth()
	inner := maxInt(1, width-2)
	t, ok := m.selectedTicket()
	if !ok {
		return "Inspector · no selected ticket"
	}
	muted := lipgloss.NewStyle().Foreground(palette.muted)
	heading := lipgloss.NewStyle().Foreground(palette.accent).Bold(true)
	warning := lipgloss.NewStyle().Foreground(palette.warning).Bold(true)
	strong := lipgloss.NewStyle().Bold(true)
	column := t.ColumnName
	if column == "" && m.col >= 0 && m.col < len(m.view.Columns) {
		column = m.view.Columns[m.col].Name
	}
	// Treat detail as another board column: one heading underline, then canvas.
	rule := mutedBorder
	if m.inspector.focused || m.inspectorNarrow() {
		rule = accentBorder
	}
	headers := []string{
		spaceBetween(heading.Render(" INSPECTOR"), muted.Render(safePreviewANSI(t.DisplayID)), width),
		rule.Render(strings.Repeat("─", width)),
		"",
	}
	title := strings.Split(ansi.Wrap(safePreviewANSI(t.Title), inner, ""), "\n")
	for _, line := range title[:minInt(3, len(title))] {
		headers = append(headers, strong.Render(line))
	}
	location := t.BoardName + " / " + column
	runtime := strings.TrimPrefix(cardStatusLine(t, inner), "· ")
	identity := muted.Render(location+"  ·  ") + inspectorRuntimeStyle(t).Render(runtime)
	if ansi.StringWidth(identity) > inner {
		headers = append(headers, muted.Render(location), inspectorRuntimeStyle(t).Render(runtime))
	} else {
		headers = append(headers, identity)
	}
	headers = append(headers, "")
	sections := []string{"Context", "Output", "Worktree"}
	for i, name := range sections {
		if i == m.inspector.section {
			sections[i] = heading.Underline(true).Padding(0, 1).Render(name)
		} else {
			sections[i] = muted.Padding(0, 1).Render(name)
		}
	}
	headers = append(headers, strings.Join(sections, " "), "")
	var body []string
	// Sanitize user and captured text before applying our own styling.
	appendText := func(text string, style lipgloss.Style) {
		for _, line := range strings.Split(ansi.Wrap(safePreviewANSI(text), inner, ""), "\n") {
			body = append(body, style.Render(line))
		}
	}
	gap := func() { body = append(body, "") }
	field := func(label, value string) {
		appendText(label, muted)
		if strings.TrimSpace(value) == "" {
			value = "Unavailable"
		}
		appendText(value, lipgloss.NewStyle())
	}
	switch m.inspector.section {
	case 0:
		if cp := t.LatestCheckpoint; cp != nil {
			appendText("NEXT ACTION · latest pause", warning)
			appendText(cp.NextAction, strong)
			gap()
			for _, row := range [][2]string{{"Completed", cp.Completed}, {"Why", cp.Why}} {
				value := row[1]
				if strings.TrimSpace(value) == "" {
					value = "Unavailable"
				}
				appendText(muted.Render(row[0]+": ")+safePreviewANSI(value), lipgloss.NewStyle())
			}
			gap()
		}
		appendText("TASK", heading)
		if strings.TrimSpace(t.Body) == "" {
			appendText("(No task body)", muted)
		} else {
			body = append(body, inspectorTaskLines(t.Body, inner)...)
		}
		// Clipped identity remains available in scrollable detail.
		if len(title) > 3 || ansi.StringWidth(location) > inner {
			gap()
			if len(title) > 3 {
				field("Full title", t.Title)
			}
			if ansi.StringWidth(location) > inner {
				field("Board", t.BoardName)
				field("Column", column)
			}
		}
		gap()
		if cp := t.LatestCheckpoint; cp != nil {
			appendText("Latest pause saved "+cp.PausedAt.Format(time.RFC3339), muted)
		}
		source := t.LastDetectionSource.String
		if source == "" {
			source = "unknown"
		}
		appendText("Runtime observation: "+source, muted)
		if t.LastAttentionReason.String != "" {
			field("Attention reason", t.LastAttentionReason.String)
		}
	case 1:
		p := m.inspector.preview
		freshness := ""
		style := muted
		if !p.CapturedAt.IsZero() {
			freshness = fmt.Sprintf("Observed %ds ago", maxInt(0, int(time.Since(p.CapturedAt).Seconds())))
			if time.Since(p.CapturedAt) > 6*time.Second || m.inspector.err != "" {
				freshness = "STALE · " + freshness
				style = warning
			}
		} else if m.inspector.busy && p.Text != "" {
			freshness = "Capturing read-only screen…"
		}
		// Keep freshness pinned, while the footer identifies the capture source.
		if strings.HasPrefix(p.Source, "visible screen") {
			freshness += " · screen"
		}
		if freshness != "" {
			headers = append(headers, style.Render(freshness))
		}
		if p.Source != "" && !strings.HasPrefix(p.Source, "visible screen") {
			// A transcript fallback must remain explicit even at the top of a long screen.
			headers = append(headers, muted.Render(safePreviewANSI(p.Source)))
		}
		if m.inspector.err != "" && p.Text != "" {
			appendText("Unavailable: "+m.inspector.err, warning)
		}
		if p.Warning != "" {
			appendText(p.Warning, warning)
		}
		content := p.Text
		if len(content) > 256*1024 {
			start := len(content) - 256*1024
			for start < len(content) && !utf8.RuneStart(content[start]) {
				start++
			}
			content = content[start:]
		}
		if content == "" {
			switch {
			case m.inspector.busy && m.inspector.err == "":
				appendText("Capturing terminal output…", muted)
			case !p.CapturedAt.IsZero() && m.inspector.err == "":
				appendText("Terminal screen is empty.", muted)
			default:
				appendText("No terminal output", strong)
				reason := m.inspector.err
				if !t.SessionActive {
					reason = "No active agent session."
				} else if reason == "" {
					reason = "No screen snapshot has been captured yet."
				}
				appendText(reason, muted)
				gap()
				appendText("Enter to open the agent.", muted)
			}
		} else {
			gap()
			screen := styledPreviewLines(safePreviewANSI(content))
			body = append(body, screen[maxInt(0, len(screen)-160):]...)
		}
	case 2:
		appendText("WORKTREE", heading)
		if !t.WorkspaceID.Valid {
			appendText("No ticket worktree.", muted)
			gap()
			field("Board directory", t.BoardWorkdir)
		} else {
			field("Branch", t.WorkspaceBranch.String)
			field("Source branch", t.WorkspaceSourceBranch.String)
			gap()
			field("Directory", t.WorkspaceLaunchCWD.String)
			field("State", t.WorkspaceState.String)
			gap()
			appendText("GIT HEALTH", heading)
			if detail := ticketWorkspaceDetailLine(t); detail != "" {
				appendText(detail, lipgloss.NewStyle())
			}
			appendText("Cached observation · age unknown", muted)
			if !t.WorkspaceStatusJSON.Valid {
				appendText("Git observation unavailable", muted)
			}
			if t.WorkspaceLastError.String != "" {
				appendText("Observation unavailable: "+t.WorkspaceLastError.String, warning)
			}
		}
	}
	if len(headers) > maxInt(1, height-2) {
		headers = headers[:maxInt(1, height-2)]
	}
	var lines []string
	for i, line := range headers {
		if i < 2 {
			lines = append(lines, line)
			continue
		}
		line = ansi.Truncate(strings.ReplaceAll(safePreviewANSI(line), "\n", " "), inner, "…") + "\x1b[0m"
		lines = append(lines, padLine(" "+line, width))
	}
	avail := maxInt(1, height-len(lines)-1)
	maxScroll := maxInt(0, len(body)-avail)
	offset := minInt(m.inspector.scroll, maxScroll)
	if m.inspector.section == 1 && m.inspector.followOutput {
		offset = maxScroll
	}
	horizontal := m.inspector.horizontal
	maxWidth := 0
	for _, line := range body {
		maxWidth = maxInt(maxWidth, ansi.StringWidth(line))
	}
	horizontal = minInt(horizontal, maxInt(0, maxWidth-inner))
	m.inspector.scroll, m.inspector.horizontal = offset, horizontal
	if m.inspector.section == 1 && offset == maxScroll && m.inspector.preview.Text != "" {
		m.inspector.followOutput = true
	}
	for _, line := range body[offset:minInt(len(body), offset+avail)] {
		lines = append(lines, padLine(" "+ansi.Cut(line, horizontal, horizontal+inner)+"\x1b[0m", width))
	}
	for len(lines) < height-1 {
		lines = append(lines, strings.Repeat(" ", width))
	}
	position := fmt.Sprintf("%d–%d/%d", offset+1, minInt(len(body), offset+avail), len(body))
	if offset+avail < len(body) {
		position += " · ↓ more"
	}
	if m.inspector.section == 1 && horizontal > 0 {
		position += fmt.Sprintf(" · col %d", horizontal+1)
	}
	if m.inspector.section == 1 {
		source := safePreviewANSI(m.inspector.preview.Source)
		if strings.HasPrefix(source, "visible screen") {
			source = "screen"
		}
		if source == "" {
			source = "no observation"
		}
		position = "Read-only · " + position + " · " + source
	}
	lines = append(lines, padLine(" "+muted.Render(ansi.Truncate(position, inner, "…")), width))
	return strings.Join(lines, "\n")
}

func inspectorRuntimeStyle(t storage.Ticket) lipgloss.Style {
	// Reuse card tone rules; keyboard focus never changes runtime attention.
	style := lipgloss.NewStyle().Foreground(palette.muted)
	if t.Runtime == kanban.StateError || t.Runtime == kanban.StateRepairNeeded {
		return style.Foreground(palette.error_).Bold(true)
	}
	if t.Runtime == kanban.StateNeedsPermission || t.Runtime == kanban.StateWaitingForUser {
		return style.Foreground(palette.warning).Bold(true)
	}
	return style
}

func inspectorTaskLines(body string, width int) []string {
	var lines []string
	codeFence := ""
	blank := func() {
		if len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) != "" {
			lines = append(lines, "")
		}
	}
	for raw := range strings.SplitSeq(safePreviewANSI(body), "\n") {
		trimmed := strings.TrimSpace(raw)
		if codeFence != "" {
			if strings.HasPrefix(trimmed, codeFence) && strings.TrimSpace(strings.TrimPrefix(trimmed, codeFence)) == "" {
				codeFence = ""
				blank()
				continue
			}
			// Preserve code literally, including backticks and Markdown-looking comments.
			for _, line := range strings.Split(ansi.Wrap(raw, maxInt(1, width-2), ""), "\n") {
				lines = append(lines, lipgloss.NewStyle().Foreground(palette.accent).Render("  "+line))
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[0]
			length := 0
			for length < len(trimmed) && trimmed[length] == marker {
				length++
			}
			codeFence = trimmed[:length]
			blank()
			continue
		}
		if trimmed == "" {
			blank()
			continue
		}
		text, style := markdownLineStyle(raw)
		isHeading := strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ")
		if isHeading {
			blank()
			text, style = markdownLineStyle(trimmed)
		}
		indent := ""
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			text, style = markdownLineStyle(trimmed)
			indent = "  "
		}
		text = renderInlineMarkdown(text)
		wrapWidth := maxInt(1, width-len(indent))
		if indent != "" {
			wrapWidth = maxInt(1, width-4)
		}
		for i, line := range strings.Split(ansi.Wrap(text, wrapWidth, ""), "\n") {
			prefix := indent
			if indent != "" && i > 0 {
				prefix = "    "
			}
			lines = append(lines, style.Render(prefix+line))
		}
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// safePreviewANSI allows printable text, line breaks, tabs and numeric SGR only.
// OSC (including clipboard/hyperlinks), DCS/APC, cursor controls and C1 are removed.
func safePreviewANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i++
			if i == len(s) {
				break
			}
			kind := s[i]
			i++
			if kind == '[' {
				start := i
				numeric := true
				for i < len(s) && !(s[i] >= 0x40 && s[i] <= 0x7e) {
					if !(s[i] >= '0' && s[i] <= '9' || s[i] == ';' || s[i] == ':') {
						numeric = false
					}
					i++
				}
				if i < len(s) {
					if s[i] == 'm' && numeric {
						out.WriteString("\x1b[" + s[start:i+1])
					}
					i++
				}
			} else if kind == ']' || kind == 'P' || kind == '_' || kind == '^' || kind == 'X' {
				for i < len(s) {
					if s[i] == 7 {
						i++
						break
					}
					if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			} else if kind >= 0x20 && kind <= 0x2f {
				for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
					i++
				}
				if i < len(s) {
					i++
				}
			}
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if r == '\n' {
			out.WriteRune(r)
		} else if r == '\t' {
			out.WriteString("    ")
		} else if !unicode.IsControl(r) && r != utf8.RuneError {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (m *Model) restoreInspectorAnchor() {
	if m.inspector.anchorBoardID != m.boardID || m.inspector.anchorMaster != m.masterBoard {
		return
	}
	if len(m.inspector.savedScroll) == len(m.colScroll) {
		copy(m.colScroll, m.inspector.savedScroll)
	}
	m.colOffset = m.inspector.savedOffset
}

// Carry SGR across rows before isolating each rendered line with a reset.
func styledPreviewLines(text string) []string {
	lines := strings.Split(text, "\n")
	carry := ""
	for i, line := range lines {
		lines[i] = carry + line
		for j := 0; j < len(line); j++ {
			if line[j] != 0x1b || j+1 >= len(line) || line[j+1] != '[' {
				continue
			}
			end := strings.IndexByte(line[j:], 'm')
			if end < 0 {
				break
			}
			seq := line[j : j+end+1]
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				carry = ""
			} else {
				carry += seq
			}
			j += end
		}
		if len(carry) > 4096 {
			carry = ""
		}
	}
	return lines
}
