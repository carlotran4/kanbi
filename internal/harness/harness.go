package harness

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/kanban"
)

const (
	PromptModePaste = "paste"
	PromptModeArg   = "arg"

	StateNotStarted      = kanban.StateNotStarted
	StateStarting        = kanban.StateStarting
	StateRunning         = kanban.StateRunning
	StateWaitingForUser  = kanban.StateWaitingForUser
	StateNeedsPermission = kanban.StateNeedsPermission
	StateIdleUnknown     = kanban.StateIdleUnknown
	StateClosing         = kanban.StateClosing
	StateClosed          = kanban.StateClosed
	StateExited          = kanban.StateExited
	StateError           = kanban.StateError
)

func StartCommand(harnesses map[string]Config, name string) ([]string, error) {
	h, ok := harnesses[name]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q", name)
	}
	if len(h.Start) == 0 {
		return nil, fmt.Errorf("harness %q has no start command", name)
	}
	return append([]string(nil), h.Start...), nil
}

func StartCommandWithPrompt(harnesses map[string]Config, name, prompt string, sendPrompt bool) ([]string, bool, error) {
	if sendPrompt && PromptMode(harnesses, name) == PromptModeArg {
		h := harnesses[name]
		base := h.Start
		if len(h.StartWithPrompt) > 0 {
			base = h.StartWithPrompt
		}
		cmd := append(append([]string(nil), base...), prompt)
		return cmd, true, nil
	}
	cmd, err := StartCommand(harnesses, name)
	if err != nil {
		return nil, false, err
	}
	return cmd, false, nil
}

func ResumeCommand(harnesses map[string]Config, name, sessionRef string) ([]string, error) {
	h, ok := harnesses[name]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q", name)
	}
	if len(h.Resume) == 0 {
		return nil, fmt.Errorf("harness %q has no resume command", name)
	}
	out := append([]string(nil), h.Resume...)
	for i := range out {
		out[i] = strings.ReplaceAll(out[i], "{session_ref}", sessionRef)
	}
	return out, nil
}

func ExitKeys(harnesses map[string]Config, name string) []string {
	if h, ok := harnesses[name]; ok && len(h.Exit) > 0 {
		return append([]string(nil), h.Exit...)
	}
	return []string{"C-c", "exit", "Enter"}
}

func PromptMode(harnesses map[string]Config, name string) string {
	if h, ok := harnesses[name]; ok && h.PromptMode != "" {
		return h.PromptMode
	}
	return PromptModePaste
}

func DetectState(output string, previousExcerpt string, idleFor time.Duration) (state, source, reason, excerpt string, outputChanged bool) {
	excerpt = LastExcerpt(output, 1800)
	outputChanged = excerpt != "" && excerpt != previousExcerpt
	lower := strings.ToLower(output)
	switch {
	case containsAny(lower, "do you want to proceed", "approve", "allow this", "allow command", "escalat", "proceed?", "requires permission", "grant permission", "request permission"):
		return StateNeedsPermission, "pattern", "permission requested", excerpt, outputChanged
	case containsAny(lower, "waiting for user", "needs input", "your response", "prompt_ready", "\n> "):
		return StateWaitingForUser, "pattern", "waiting for user input", excerpt, outputChanged
	case outputChanged:
		return StateRunning, "pane", "", excerpt, true
	case idleFor > 0:
		return StateIdleUnknown, "idle", "no recent output", excerpt, false
	default:
		return StateRunning, "pane", "", excerpt, false
	}
}

func LastExcerpt(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" || max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[len(r)-max:])
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func PromptReadyPattern(harnesses map[string]Config, name string) string {
	if h, ok := harnesses[name]; ok && h.PromptReady != "" {
		return h.PromptReady
	}
	return "PROMPT_READY"
}

func SessionRefPattern(harnesses map[string]Config, name string) string {
	if h, ok := harnesses[name]; ok && h.SessionRef != "" {
		return h.SessionRef
	}
	return "SESSION_REF="
}

func ParseSessionRef(output, marker string) (string, bool) {
	if marker == "" {
		marker = "SESSION_REF="
	}
	for _, line := range strings.Split(output, "\n") {
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		ref := strings.TrimSpace(line[idx+len(marker):])
		if ref == "" {
			continue
		}
		fields := strings.Fields(ref)
		if len(fields) > 0 {
			ref = fields[0]
		}
		return ref, true
	}
	return "", false
}

func CaptureSessionRef(name, promptText string, since time.Time) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return CaptureSessionRefInCWD(name, promptText, cwd, since)
}

func CaptureSessionRefInCWD(name, promptText, cwd string, since time.Time) (string, bool) {
	if promptText == "" {
		return "", false
	}
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", false
		}
	}
	switch name {
	case "pi":
		return latestPiSessionInCWD(promptText, cwd, since)
	case "codex":
		return latestCodexHistorySessionInCWD(promptText, cwd, since)
	case "copilot":
		return latestCopilotSessionInCWD(promptText, cwd, since)
	case "claude":
		return latestClaudeSessionInCWD(promptText, cwd, since)
	default:
		contract, ok := BuiltinContract(name)
		if !ok || contract.CaptureRef == nil {
			return "", false
		}
		return contract.CaptureRef(promptText, since)
	}
}

func ValidateSessionRef(name, sessionRef, promptText string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	return ValidateSessionRefInCWD(name, sessionRef, promptText, cwd)
}

func ValidateSessionRefInCWD(name, sessionRef, promptText, cwd string) bool {
	if strings.TrimSpace(sessionRef) == "" {
		return false
	}
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return false
		}
	}
	switch name {
	case "copilot":
		return validateCopilotSessionRefInCWD(sessionRef, promptText, cwd)
	default:
		return true
	}
}

type codexHistoryEntry struct {
	SessionID string  `json:"session_id"`
	Timestamp float64 `json:"ts"`
	Text      string  `json:"text"`
}

type codexSessionMeta struct {
	Type    string `json:"type"`
	Payload struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		Timestamp string `json:"timestamp"`
		CWD       string `json:"cwd"`
	} `json:"payload"`
}

func latestCodexHistorySession(promptText string, since time.Time) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return latestCodexHistorySessionInCWD(promptText, cwd, since)
}

func latestCodexHistorySessionInCWD(promptText, cwd string, since time.Time) (string, bool) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		codexHome = filepath.Join(home, ".codex")
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "history.jsonl"))
	if err != nil {
		return "", false
	}
	candidates := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry codexHistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.SessionID == "" || entry.Text != promptText {
			continue
		}
		if entryTime(entry).Before(since.Add(-2 * time.Second)) {
			continue
		}
		candidates[entry.SessionID] = struct{}{}
	}

	var matched string
	for sessionID := range candidates {
		if !codexSessionMatchesLaunch(codexHome, sessionID, cwd, since) {
			continue
		}
		if matched != "" && matched != sessionID {
			return "", false
		}
		matched = sessionID
	}
	return matched, matched != ""
}

func codexSessionMatchesLaunch(codexHome, sessionID, cwd string, since time.Time) bool {
	if strings.ContainsAny(sessionID, `/\\*?[]`) {
		return false
	}
	pattern := filepath.Join(codexHome, "sessions", "*", "*", "*", "*"+sessionID+".jsonl")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return false
	}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		ok := scanner.Scan()
		line := scanner.Bytes()
		_ = file.Close()
		if !ok {
			continue
		}
		var meta codexSessionMeta
		if err := json.Unmarshal(line, &meta); err != nil || meta.Type != "session_meta" {
			continue
		}
		metaID := meta.Payload.ID
		if metaID == "" {
			metaID = meta.Payload.SessionID
		}
		createdAt, err := time.Parse(time.RFC3339Nano, meta.Payload.Timestamp)
		if err != nil || createdAt.Before(since) {
			continue
		}
		if metaID == sessionID && filepath.Clean(meta.Payload.CWD) == filepath.Clean(cwd) {
			return true
		}
	}
	return false
}

func entryTime(entry codexHistoryEntry) time.Time {
	sec := int64(entry.Timestamp)
	nsec := int64((entry.Timestamp - float64(sec)) * 1_000_000_000)
	return time.Unix(sec, nsec)
}

type piHeader struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type piEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"message"`
}

func latestPiSession(promptText string, since time.Time) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return latestPiSessionInCWD(promptText, cwd, since)
}

func latestPiSessionInCWD(promptText, cwd string, since time.Time) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	root := filepath.Join(home, ".pi", "agent", "sessions")
	var bestID string
	var bestTime time.Time
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		id, ts, ok := inspectPiSession(path, cwd, promptText, since)
		if ok && (bestID == "" || ts.After(bestTime)) {
			bestID = id
			bestTime = ts
		}
		return nil
	})
	return bestID, bestID != ""
}

func inspectPiSession(path, cwd, promptText string, since time.Time) (string, time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, false
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", time.Time{}, false
	}
	var header piHeader
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		return "", time.Time{}, false
	}
	if header.Type != "session" || header.ID == "" || header.CWD != cwd {
		return "", time.Time{}, false
	}
	headerTime, err := time.Parse(time.RFC3339Nano, header.Timestamp)
	if err != nil {
		return "", time.Time{}, false
	}
	if headerTime.Before(since.Add(-2 * time.Second)) {
		return "", time.Time{}, false
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry piEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type == "message" && entry.Message.Role == "user" && contentText(entry.Message.Content) == promptText {
			return header.ID, headerTime, true
		}
	}
	return "", time.Time{}, false
}

func contentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok || m["type"] != "text" {
				continue
			}
			if text, ok := m["text"].(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	default:
		rv := reflect.ValueOf(content)
		if rv.IsValid() && rv.Kind() == reflect.String {
			return rv.String()
		}
		return ""
	}
}

// latestCopilotSession scans ~/.copilot/session-store.db for the most recent
// Copilot CLI session whose first user turn matches promptText and whose cwd
// matches the current working directory. The session ID is stable and can be
// passed to `copilot --resume=<id>`.
func latestCopilotSession(promptText string, since time.Time) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return latestCopilotSessionInCWD(promptText, cwd, since)
}

func latestCopilotSessionInCWD(promptText, cwd string, since time.Time) (string, bool) {
	db, err := openCopilotSessionStore()
	if err != nil {
		return "", false
	}
	defer db.Close()

	// Match the first user turn exactly. Accepting sessions whose first turn has not
	// been written yet is too loose and can capture unrelated Copilot sessions from
	// the same cwd, which later makes resume fail with a stale session id.
	sinceStr := since.UTC().Add(-10 * time.Second).Format(time.RFC3339)
	query := `
		SELECT s.id
		FROM sessions s
		JOIN turns t ON t.session_id = s.id AND t.turn_index = 0
		WHERE s.cwd = ?
		  AND s.created_at >= ?
		  AND t.user_message = ?
		ORDER BY s.created_at DESC
		LIMIT 1`
	var id string
	err = db.QueryRow(query, cwd, sinceStr, promptText).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, id != ""
}

func validateCopilotSessionRefInCWD(sessionRef, promptText, cwd string) bool {
	db, err := openCopilotSessionStore()
	if err != nil {
		return false
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`
		SELECT count(*)
		FROM sessions s
		JOIN turns t ON t.session_id = s.id AND t.turn_index = 0
		WHERE s.id = ?
		  AND s.cwd = ?
		  AND t.user_message = ?`,
		sessionRef, cwd, promptText,
	).Scan(&count)
	return err == nil && count > 0
}

func openCopilotSessionStore() (*sql.DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(home, ".copilot", "session-store.db")
	return sql.Open("sqlite3", dbPath+"?mode=ro")
}

type claudeSessionEntry struct {
	Type      string `json:"type"`
	IsMeta    bool   `json:"isMeta"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"message"`
}

// latestClaudeSession scans ~/.claude/projects/**/*.jsonl for the most recent
// Claude Code session whose first non-meta user turn matches promptText and
// whose recorded cwd matches the current working directory. The sessionId is
// stable and can be passed to `claude --resume <id>`.
func latestClaudeSession(promptText string, since time.Time) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return latestClaudeSessionInCWD(promptText, cwd, since)
}

func latestClaudeSessionInCWD(promptText, cwd string, since time.Time) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	root := filepath.Join(home, ".claude", "projects")
	var bestID string
	var bestTime time.Time
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		id, ts, ok := inspectClaudeSession(path, cwd, promptText, since)
		if ok && (bestID == "" || ts.After(bestTime)) {
			bestID = id
			bestTime = ts
		}
		return nil
	})
	return bestID, bestID != ""
}

func inspectClaudeSession(path, cwd, promptText string, since time.Time) (string, time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, false
	}
	var bestID string
	var bestTime time.Time
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry claudeSessionEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "user" || entry.IsMeta || entry.SessionID == "" || entry.CWD != cwd {
			continue
		}
		if entry.Message.Role != "user" || contentText(entry.Message.Content) != promptText {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil || ts.Before(since.Add(-2*time.Second)) {
			continue
		}
		if bestID == "" || ts.After(bestTime) {
			bestID = entry.SessionID
			bestTime = ts
		}
	}
	return bestID, bestTime, bestID != ""
}
