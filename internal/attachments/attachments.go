package attachments

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const appDir = "agent-kanban"

var dataImageRE = regexp.MustCompile(`^data:(image/[a-zA-Z0-9.+-]+);base64,(.*)$`)

// BaseDir returns the XDG data directory used for ticket attachments.
func BaseDir() string {
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, appDir, "attachments")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".local", "share", appDir, "attachments")
	}
	return filepath.Join(home, ".local", "share", appDir, "attachments")
}

func TicketDir(ticketID int64) string {
	return filepath.Join(BaseDir(), fmt.Sprintf("%d", ticketID))
}

// SavePastedImage detects base64-encoded image clipboard content, writes it to
// the ticket attachment directory, and returns a markdown image reference.
func SavePastedImage(ticketID int64, pasted string, now time.Time) (string, string, bool, error) {
	data, ext, ok := DecodePastedImage(pasted)
	if !ok {
		return "", "", false, nil
	}
	path, err := WriteImage(ticketID, data, ext, now)
	if err != nil {
		return "", "", true, err
	}
	return path, fmt.Sprintf("![](%s)", markdownPath(path)), true, nil
}

func DecodePastedImage(pasted string) ([]byte, string, bool) {
	payload := strings.TrimSpace(extractOSC52(pasted))
	if matches := dataImageRE.FindStringSubmatch(payload); len(matches) == 3 {
		data, err := decodeBase64ImagePayload(matches[2])
		if err != nil {
			return nil, "", false
		}
		if ext := extensionForImage(data, matches[1]); ext != "" {
			return data, ext, true
		}
		return nil, "", false
	}
	data, err := decodeBase64ImagePayload(payload)
	if err != nil {
		return nil, "", false
	}
	ext := extensionForImage(data, "")
	if ext == "" {
		return nil, "", false
	}
	return data, ext, true
}

func WriteImage(ticketID int64, data []byte, ext string, now time.Time) (string, error) {
	if ext == "" || strings.ContainsAny(ext, `/\`) {
		return "", fmt.Errorf("invalid image extension")
	}
	dir := TicketDir(ticketID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	stamp := now.Format("20060102-150405")
	for i := 1; i <= 999; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%s-%03d.%s", stamp, i, ext))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			_ = os.Remove(path)
			return "", writeErr
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return "", closeErr
		}
		return path, nil
	}
	return "", fmt.Errorf("could not allocate attachment filename")
}

func decodeBase64ImagePayload(payload string) ([]byte, error) {
	payload = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, payload)
	if payload == "" {
		return nil, fmt.Errorf("empty base64 payload")
	}
	if data, err := base64.StdEncoding.DecodeString(payload); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(payload)
}

func extensionForImage(data []byte, mediaType string) string {
	switch strings.ToLower(mediaType) {
	case "image/png":
		if hasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
			return "png"
		}
	case "image/jpeg", "image/jpg":
		if hasPrefix(data, []byte("\xff\xd8\xff")) {
			return "jpg"
		}
	case "image/gif":
		if hasPrefix(data, []byte("GIF87a")) || hasPrefix(data, []byte("GIF89a")) {
			return "gif"
		}
	case "image/webp":
		if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			return "webp"
		}
	}
	if hasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return "png"
	}
	if hasPrefix(data, []byte("\xff\xd8\xff")) {
		return "jpg"
	}
	if hasPrefix(data, []byte("GIF87a")) || hasPrefix(data, []byte("GIF89a")) {
		return "gif"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "webp"
	}
	ct := http.DetectContentType(data)
	switch ct {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	}
	return ""
}

func hasPrefix(data, prefix []byte) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == string(prefix)
}

func extractOSC52(s string) string {
	const prefix = "\x1b]52;"
	start := strings.Index(s, prefix)
	if start < 0 {
		return s
	}
	rest := s[start+len(prefix):]
	semi := strings.IndexByte(rest, ';')
	if semi < 0 {
		return s
	}
	payload := rest[semi+1:]
	if end := strings.IndexByte(payload, '\a'); end >= 0 {
		return payload[:end]
	}
	if end := strings.Index(payload, "\x1b\\"); end >= 0 {
		return payload[:end]
	}
	return payload
}

func markdownPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if rel, relErr := filepath.Rel(home, path); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}
