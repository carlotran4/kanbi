package ticketbackend

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
)

// GitHub uses a Markdown HTML comment so the marker is invisible in issue
// rendering. Jira uses a trailing plain-text line because ADF normalization
// commonly preserves trailing text and Kanbi avoids config-coupled labels.
var (
	githubPushMarkerRE = regexp.MustCompile(`(?s)<!--\s*kanbi:local-ticket=(\d+)\s+token=([A-Fa-f0-9]+)\s*-->`)
	jiraPushMarkerRE   = regexp.MustCompile(`(?m)^kanbi:local-ticket=(\d+)\s+token=([A-Fa-f0-9]+)\s*$`)
)

type pushMarker struct {
	TicketID int64
	Token    string
}

// mintPushToken returns a non-secret random token for find-or-link recovery.
func mintPushToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func embedGitHubPushMarker(body string, ticketID int64, token string) string {
	marker := fmt.Sprintf("<!-- kanbi:local-ticket=%d token=%s -->", ticketID, token)
	body = stripGitHubPushMarker(body)
	body = strings.TrimRight(body, " \t\r\n")
	if body == "" {
		return marker
	}
	return body + "\n\n" + marker
}

func embedJiraPushMarker(body string, ticketID int64, token string) string {
	marker := fmt.Sprintf("kanbi:local-ticket=%d token=%s", ticketID, token)
	body = stripJiraPushMarker(body)
	body = strings.TrimRight(body, " \t\r\n")
	if body == "" {
		return marker
	}
	return body + "\n" + marker
}

func stripGitHubPushMarker(body string) string {
	return strings.TrimSpace(githubPushMarkerRE.ReplaceAllString(body, ""))
}

func stripJiraPushMarker(body string) string {
	return strings.TrimSpace(jiraPushMarkerRE.ReplaceAllString(body, ""))
}

func parseGitHubPushMarker(body string) (pushMarker, bool) {
	m := githubPushMarkerRE.FindStringSubmatch(body)
	if len(m) != 3 {
		return pushMarker{}, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return pushMarker{}, false
	}
	return pushMarker{TicketID: id, Token: strings.ToLower(m[2])}, true
}

func parseJiraPushMarker(body string) (pushMarker, bool) {
	m := jiraPushMarkerRE.FindStringSubmatch(body)
	if len(m) != 3 {
		return pushMarker{}, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return pushMarker{}, false
	}
	return pushMarker{TicketID: id, Token: strings.ToLower(m[2])}, true
}

func ticketRemotePushActive(t storage.Ticket) bool {
	if !t.RemotePushState.Valid {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(t.RemotePushState.String))
	return state == storage.RemotePushStatePending || state == storage.RemotePushStateFailed
}

func ticketRemotePushToken(t storage.Ticket) string {
	if !t.RemotePushToken.Valid {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(t.RemotePushToken.String))
}
