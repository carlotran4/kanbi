package tui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/carlotran4/kanbi/internal/tui/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlotran4/kanbi/internal/storage"
)

const (
	maxProjectFiles          = 5000
	maxFileCompletionResults = 8
)

// fileCompletionQuery identifies the @query portion of an editor value using
// rune offsets. The at sign is at start and the query text ends at end.
type fileCompletionQuery struct {
	start int
	end   int
	text  string
}

type fileCompletionTarget string

const (
	fileCompletionBody fileCompletionTarget = "body"
	fileCompletionNote fileCompletionTarget = "note"
)

type fileCompletionState struct {
	target     fileCompletionTarget
	ticketID   int64
	request    uint64
	root       string
	query      fileCompletionQuery
	paths      []string
	candidates []string
	selected   int
	loading    bool
}

type fileCompletionResultMsg struct {
	target   fileCompletionTarget
	ticketID int64
	request  uint64
	root     string
	paths    []string
	err      error
}

// activeFileCompletionQuery returns the active @ reference ending at cursor.
// A reference must start at the beginning of input or after a non-word
// boundary, so email addresses are not treated as file references.
func activeFileCompletionQuery(value string, cursor int) (fileCompletionQuery, bool) {
	runes := []rune(value)
	if cursor < 0 || cursor > len(runes) {
		return fileCompletionQuery{}, false
	}
	lineStart := cursor
	for lineStart > 0 && runes[lineStart-1] != '\n' {
		lineStart--
	}
	at := -1
	for i := cursor - 1; i >= lineStart; i-- {
		if runes[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 || (at > 0 && isFileReferenceWord(runes[at-1])) {
		return fileCompletionQuery{}, false
	}
	for _, r := range runes[at+1 : cursor] {
		if unicode.IsSpace(r) {
			return fileCompletionQuery{}, false
		}
	}
	return fileCompletionQuery{start: at, end: cursor, text: string(runes[at+1 : cursor])}, true
}

func isFileReferenceWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}

var errProjectFileLimit = errors.New("project file completion limit reached")

var ignoredProjectDirectories = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"coverage":     true,
	"target":       true,
	".next":        true,
	".cache":       true,
}

// scanProjectFiles returns a bounded, sorted list of paths relative to root.
// Dot directories are included unless they are explicitly known to be heavy
// or irrelevant to an agent prompt.
func scanProjectFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // unreadable descendants should not prevent completion
		}
		if entry.IsDir() {
			if path != root && ignoredProjectDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if len(files) >= maxProjectFiles {
			return errProjectFileLimit
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.IndexFunc(rel, unicode.IsControl) >= 0 {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil && !errors.Is(err, errProjectFileLimit) {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

type rankedFileCompletionCandidate struct {
	path  string
	score int
}

// filterFileCompletionCandidates uses a case-insensitive contiguous match when
// possible, then a deterministic ordered fuzzy match for partial paths.
func filterFileCompletionCandidates(paths []string, query string) []string {
	query = strings.ToLower(query)
	ranked := make([]rankedFileCompletionCandidate, 0, len(paths))
	for _, path := range paths {
		lower := strings.ToLower(path)
		score, ok := fileCompletionScore(lower, query)
		if ok {
			ranked = append(ranked, rankedFileCompletionCandidate{path: path, score: score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score < ranked[j].score
		}
		return ranked[i].path < ranked[j].path
	})
	if len(ranked) > maxFileCompletionResults {
		ranked = ranked[:maxFileCompletionResults]
	}
	result := make([]string, len(ranked))
	for i, candidate := range ranked {
		result[i] = candidate.path
	}
	return result
}

func fileCompletionScore(path, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	if index := strings.Index(path, query); index >= 0 {
		return index, true
	}
	queryRunes := []rune(query)
	pathRunes := []rune(path)
	matched := 0
	first := -1
	last := -1
	for i, r := range pathRunes {
		if matched < len(queryRunes) && r == queryRunes[matched] {
			if first < 0 {
				first = i
			}
			last = i
			matched++
		}
	}
	if matched != len(queryRunes) {
		return 0, false
	}
	// Keep contiguous matches ahead of fuzzy ones while preferring early,
	// compact fuzzy matches.
	return 10_000 + first*100 + (last - first), true
}

func textareaCursorOffset(input textarea.Model) int {
	lines := strings.Split(input.Value(), "\n")
	line := input.Line()
	if line < 0 {
		line = 0
	}
	if line >= len(lines) {
		line = len(lines) - 1
	}
	offset := 0
	for i := 0; i < line; i++ {
		offset += len([]rune(lines[i])) + 1
	}
	info := input.LineInfo()
	return offset + info.StartColumn + info.ColumnOffset
}

func setTextareaCursorOffset(input *textarea.Model, offset int) {
	value := input.Value()
	runes := []rune(value)
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes) {
		offset = len(runes)
	}
	before := runes[:offset]
	line, column := 0, 0
	for _, r := range before {
		if r == '\n' {
			line++
			column = 0
		} else {
			column++
		}
	}
	for input.Line() > line {
		input.CursorUp()
	}
	for input.Line() < line {
		input.CursorDown()
	}
	input.SetCursor(column)
}

func replaceFileCompletionQuery(input *textarea.Model, query fileCompletionQuery, path string) {
	runes := []rune(input.Value())
	if query.start < 0 || query.end < query.start || query.end > len(runes) {
		return
	}
	insert := []rune("@" + path)
	updated := make([]rune, 0, len(runes)-query.end+query.start+len(insert))
	updated = append(updated, runes[:query.start]...)
	updated = append(updated, insert...)
	updated = append(updated, runes[query.end:]...)
	input.SetValue(string(updated))
	setTextareaCursorOffset(input, query.start+len(insert))
}

func fileCompletionRoot(ticket storage.Ticket) string {
	for _, candidate := range []string{ticket.WorkspaceLaunchCWD.String, ticket.BoardWorkdir} {
		if candidate == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			return resolved
		}
	}
	return ""
}

func scanFileCompletionCmd(target fileCompletionTarget, ticketID int64, request uint64, root string) tea.Cmd {
	return func() tea.Msg {
		paths, err := scanProjectFiles(root)
		return fileCompletionResultMsg{target: target, ticketID: ticketID, request: request, root: root, paths: paths, err: err}
	}
}

func (m *Model) clearFileCompletion() {
	if m.fileCompletion.target == fileCompletionNote {
		m.noteTA.SetHeight(6)
	}
	m.fileCompletion = fileCompletionState{}
}

func (m *Model) resetFileCompletionIndex() {
	m.fileCompletionIndexRoot = ""
	m.fileCompletionIndexPaths = nil
	m.fileCompletionIndexLoaded = false
	m.fileCompletionIndexBusy = false
	m.fileCompletionIndexReq = 0
}

func (m *Model) completionInput(target fileCompletionTarget) *textarea.Model {
	switch target {
	case fileCompletionBody:
		return &m.bodyTA
	case fileCompletionNote:
		return &m.noteTA
	default:
		return nil
	}
}

func (m *Model) refreshFileCompletion(target fileCompletionTarget) tea.Cmd {
	input := m.completionInput(target)
	if input == nil || m.editTicket.ID == 0 {
		m.clearFileCompletion()
		return nil
	}
	query, ok := activeFileCompletionQuery(input.Value(), textareaCursorOffset(*input))
	if !ok {
		m.clearFileCompletion()
		return nil
	}
	root := fileCompletionRoot(m.editTicket)
	if root == "" {
		m.clearFileCompletion()
		return nil
	}
	state := m.fileCompletion
	if state.target == target && state.ticketID == m.editTicket.ID && state.root == root {
		state.query = query
		state.selected = 0
		if !state.loading {
			state.candidates = filterFileCompletionCandidates(state.paths, query.text)
		}
		m.fileCompletion = state
		return nil
	}
	if target == fileCompletionNote {
		m.noteTA.SetHeight(3)
	}
	if m.fileCompletionIndexRoot == root {
		m.fileCompletion = fileCompletionState{
			target: target, ticketID: m.editTicket.ID, request: m.fileCompletionIndexReq, root: root, query: query,
			paths: m.fileCompletionIndexPaths, candidates: filterFileCompletionCandidates(m.fileCompletionIndexPaths, query.text), loading: m.fileCompletionIndexBusy,
		}
		if m.fileCompletionIndexLoaded || m.fileCompletionIndexBusy {
			return nil
		}
	}
	m.fileCompletionRequest++
	m.fileCompletionIndexRoot = root
	m.fileCompletionIndexPaths = nil
	m.fileCompletionIndexLoaded = false
	m.fileCompletionIndexBusy = true
	m.fileCompletionIndexReq = m.fileCompletionRequest
	m.fileCompletion = fileCompletionState{
		target: target, ticketID: m.editTicket.ID, request: m.fileCompletionRequest, root: root, query: query, loading: true,
	}
	return scanFileCompletionCmd(target, m.editTicket.ID, m.fileCompletionRequest, root)
}

func (m *Model) applyFileCompletionResult(msg fileCompletionResultMsg) {
	if m.fileCompletionIndexRoot != msg.root || m.fileCompletionIndexReq != msg.request {
		return
	}
	m.fileCompletionIndexBusy = false
	m.fileCompletionIndexLoaded = msg.err == nil
	if msg.err == nil {
		m.fileCompletionIndexPaths = msg.paths
	}
	state := m.fileCompletion
	if !m.editing || state.ticketID != msg.ticketID || state.request != msg.request || state.root != msg.root || m.editTicket.ID != msg.ticketID {
		return
	}
	input := m.completionInput(state.target)
	if input == nil {
		return
	}
	query, ok := activeFileCompletionQuery(input.Value(), textareaCursorOffset(*input))
	if !ok {
		m.clearFileCompletion()
		return
	}
	state.loading = false
	state.query = query
	if msg.err == nil {
		state.paths = msg.paths
		state.candidates = filterFileCompletionCandidates(msg.paths, query.text)
	}
	if state.selected >= len(state.candidates) {
		state.selected = 0
	}
	m.fileCompletion = state
}

func (m *Model) fileCompletionSelectable(target fileCompletionTarget) bool {
	return m.fileCompletion.target == target && len(m.fileCompletion.candidates) > 0 && m.fileCompletion.selected >= 0 && m.fileCompletion.selected < len(m.fileCompletion.candidates)
}

func (m *Model) selectFileCompletion(target fileCompletionTarget) bool {
	if !m.fileCompletionSelectable(target) {
		return false
	}
	input := m.completionInput(target)
	if input == nil {
		return false
	}
	replaceFileCompletionQuery(input, m.fileCompletion.query, m.fileCompletion.candidates[m.fileCompletion.selected])
	m.clearFileCompletion()
	return true
}

func (m *Model) moveFileCompletion(target fileCompletionTarget, delta int) bool {
	if !m.fileCompletionSelectable(target) {
		return false
	}
	m.fileCompletion.selected = (m.fileCompletion.selected + delta) % len(m.fileCompletion.candidates)
	if m.fileCompletion.selected < 0 {
		m.fileCompletion.selected += len(m.fileCompletion.candidates)
	}
	return true
}

func (m *Model) dismissFileCompletion(target fileCompletionTarget) bool {
	if m.fileCompletion.target != target {
		return false
	}
	m.clearFileCompletion()
	return true
}

func (m Model) fileCompletionRenderRows(target fileCompletionTarget) int {
	if m.fileCompletion.target != target {
		return 0
	}
	if m.fileCompletion.loading || len(m.fileCompletion.candidates) == 0 {
		return 2
	}
	count := len(m.fileCompletion.candidates)
	if count > 3 {
		count = 3
	}
	return count + 1
}

func (m Model) fileCompletionView(target fileCompletionTarget, width int) string {
	if m.fileCompletion.target != target {
		return ""
	}
	if width < 12 {
		width = 12
	}
	muted := lipgloss.NewStyle().Faint(true)
	if m.fileCompletion.loading {
		return muted.Render("@ files · scanning…\nEsc dismiss")
	}
	if len(m.fileCompletion.candidates) == 0 {
		query := trimFileCompletionPath("@"+m.fileCompletion.query.text, max(1, width-21))
		return muted.Render("No files match " + query + "\nEsc dismiss")
	}
	const visible = 3
	start := 0
	if m.fileCompletion.selected >= visible {
		start = m.fileCompletion.selected - visible + 1
	}
	end := start + visible
	if end > len(m.fileCompletion.candidates) {
		end = len(m.fileCompletion.candidates)
	}
	selected := lipgloss.NewStyle().Foreground(palette.accent)
	lines := []string{muted.Render("@ files · ↑/↓ select · Enter/Tab insert")}
	for i := start; i < end; i++ {
		prefix := "  "
		style := muted
		if i == m.fileCompletion.selected {
			prefix = "> "
			style = selected
		}
		lines = append(lines, style.Render(prefix+trimFileCompletionPath(m.fileCompletion.candidates[i], width-2)))
	}
	return strings.Join(lines, "\n")
}

func trimFileCompletionPath(path string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(path) <= width {
		return path
	}
	if width == 1 {
		return "~"
	}
	var out strings.Builder
	for _, r := range path {
		candidate := out.String() + string(r) + "~"
		if lipgloss.Width(candidate) > width {
			break
		}
		out.WriteRune(r)
	}
	return out.String() + "~"
}
