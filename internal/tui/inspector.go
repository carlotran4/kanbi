package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/multiplexer"
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
			return true
		}
	}
	if !m.inspector.open || !(m.inspector.focused || m.inspectorNarrow()) {
		return false
	}
	switch key.String() {
	case "j", "down":
		m.inspector.scroll++
	case "k", "up":
		m.inspector.scroll = maxInt(0, m.inspector.scroll-1)
	case "pgdown":
		m.inspector.scroll += maxInt(1, m.boardContentHeight()-5)
	case "pgup":
		m.inspector.scroll = maxInt(0, m.inspector.scroll-maxInt(1, m.boardContentHeight()-5))
	case "h", "left":
		m.inspector.horizontal = maxInt(0, m.inspector.horizontal-4)
	case "l", "right":
		m.inspector.horizontal += 4
	case "home":
		m.inspector.scroll, m.inspector.horizontal = 0, 0
	default:
		return false
	}
	_ = m.inspectorView(m.boardContentHeight() + 2) // clamp after scrolling
	return true
}

func (m *Model) inspectorView(height int) string {
	width := m.inspectorWidth()
	inner := maxInt(1, width-4)
	t, ok := m.selectedTicket()
	if !ok {
		return "Inspector · no selected ticket"
	}
	title := "INSPECTOR · Tab focus · i close"
	if m.inspector.focused || m.inspectorNarrow() {
		title = "INSPECTOR · scrolling · Esc return"
	}
	headers := []string{title, t.DisplayID + " " + t.Title}
	column := t.ColumnName
	if column == "" && m.col >= 0 && m.col < len(m.view.Columns) {
		column = m.view.Columns[m.col].Name
	}
	headers = append(headers, t.BoardName+" / "+column+" / "+t.Harness+" · "+runtimeLabel(t))
	sections := []string{"Context", "Output", "Worktree"}
	sections[m.inspector.section] = "[" + sections[m.inspector.section] + "]"
	headers = append(headers, strings.Join(sections, " · ")+"  [/]")
	var content string
	switch m.inspector.section {
	case 0:
		content = "TASK\n" + t.Body
		if ansi.StringWidth(t.DisplayID+" "+t.Title) > inner {
			content = "TITLE\n" + t.Title + "\n\n" + content
		}
		if ansi.StringWidth(t.BoardName+" / "+column+" / "+t.Harness+" · "+runtimeLabel(t)) > inner {
			content = "Board: " + t.BoardName + "\nColumn: " + column + "\n\n" + content
		}
		if strings.TrimSpace(t.Body) == "" {
			content += "(No task body)"
		}
		if cp := t.LatestCheckpoint; cp != nil {
			content += "\n\nLATEST PAUSE CHECKPOINT · " + cp.PausedAt.Format(time.RFC3339) + "\nWhy: " + cp.Why + "\nCompleted: " + cp.Completed + "\nNext: " + cp.NextAction
		}
		content += "\n\nRuntime: " + runtimeLabel(t) + "\nObservation source: " + t.LastDetectionSource.String
		if t.LastDetectionSource.String == "" {
			content += "unknown"
		}
		if t.LastAttentionReason.String != "" {
			content += "\nReason: " + t.LastAttentionReason.String
		}
	case 1:
		p := m.inspector.preview
		freshness := "Unavailable · no observation"
		if !p.CapturedAt.IsZero() {
			freshness = fmt.Sprintf("Observed %ds ago · %s", maxInt(0, int(time.Since(p.CapturedAt).Seconds())), p.Source)
			if time.Since(p.CapturedAt) > 6*time.Second || m.inspector.err != "" {
				freshness = "STALE · " + freshness
			}
		} else if m.inspector.busy {
			freshness = "Capturing read-only screen…"
		}
		headers = append(headers, freshness)
		if m.inspector.err != "" {
			headers = append(headers, "Unavailable: "+m.inspector.err)
		}
		if p.Warning != "" {
			headers = append(headers, p.Warning)
		}
		content = p.Text
		if len(content) > 256*1024 {
			content = content[:256*1024]
		}
		if content == "" {
			content = "(No terminal output available)"
		}
	case 2:
		content = ticketWorkspaceDetailLine(t)
		if !t.WorkspaceID.Valid {
			content = "No ticket worktree.\nBoard directory: " + t.BoardWorkdir
		} else {
			content = "Branch: " + t.WorkspaceBranch.String + "\nSource: " + t.WorkspaceSourceBranch.String + "\nDirectory: " + t.WorkspaceLaunchCWD.String + "\nState: " + t.WorkspaceState.String + "\n" + content + "\nGit health: cached observation (age unknown)"
			if !t.WorkspaceStatusJSON.Valid {
				content += "\nGit observation unavailable"
			}
			if t.WorkspaceLastError.String != "" {
				content += "\nObservation unavailable: " + t.WorkspaceLastError.String
			}
		}
	}
	if len(headers) > maxInt(1, height-5) {
		headers = headers[:maxInt(1, height-5)]
	}
	// Only SGR survives. Every line ends with reset so styling stays inside the panel.
	var lines []string
	for _, line := range headers {
		lines = append(lines, ansi.Truncate(strings.ReplaceAll(safePreviewANSI(line), "\n", " "), inner, "~")+"\x1b[0m")
	}
	lines = append(lines, "")
	content = safePreviewANSI(content)
	if m.inspector.section != 1 {
		content = ansi.Wrap(content, inner, "")
	}
	body := strings.Split(content, "\n")
	if m.inspector.section == 1 {
		if len(body) > 160 {
			body = body[:160]
		}
		body = styledPreviewLines(strings.Join(body, "\n"))
	}
	avail := maxInt(1, height-2-len(lines)-1)
	offset := minInt(m.inspector.scroll, maxInt(0, len(body)-avail))
	horizontal := m.inspector.horizontal
	maxWidth := 0
	for _, line := range body {
		maxWidth = maxInt(maxWidth, ansi.StringWidth(line))
	}
	horizontal = minInt(horizontal, maxInt(0, maxWidth-inner))
	m.inspector.scroll, m.inspector.horizontal = offset, horizontal
	for _, line := range body[offset:minInt(len(body), offset+avail)] {
		lines = append(lines, ansi.Cut(line, horizontal, horizontal+inner)+"\x1b[0m")
	}
	for len(lines) < height-3 {
		lines = append(lines, "")
	}
	lines = append(lines, ansi.Truncate(fmt.Sprintf("%d-%d/%d · hjkl scroll · Enter open", offset+1, minInt(len(body), offset+avail), len(body)), inner, "~"))
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(palette.accent).Padding(0, 1).Width(maxInt(1, width-2))
	return style.Render(strings.Join(lines, "\n"))
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
