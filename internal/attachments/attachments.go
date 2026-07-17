package attachments

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	appDir               = "kanbi"
	MaxDecodedImageBytes = 10 << 20 // 10 MiB
	maxEncodedImageBytes = (MaxDecodedImageBytes+2)/3*4 + 1024
)

var dataImageRE = regexp.MustCompile(`^data:(image/[a-zA-Z0-9.+-]+);base64,(.*)$`)

// BaseDir returns the XDG data directory used for ticket attachments.
func BaseDir() string {
	if dataDir := strings.TrimSpace(os.Getenv("KANBI_DATA_DIR")); dataDir != "" {
		return filepath.Join(dataDir, "attachments")
	}
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

// SavePastedImage detects encoded image content or a pasted image file path,
// copies it into the ticket attachment directory, and returns a Markdown ref.
func SavePastedImage(ticketID int64, pasted string, now time.Time) (string, string, bool, error) {
	if len(pasted) > maxEncodedImageBytes && looksLikeEncodedImage(pasted) {
		return "", "", true, fmt.Errorf("pasted image exceeds %d MiB limit", MaxDecodedImageBytes>>20)
	}
	data, ext, ok, err := readPastedImagePath(pasted)
	if err != nil {
		return "", "", ok, err
	}
	if !ok {
		data, ext, ok = DecodePastedImage(pasted)
	}
	if !ok {
		return "", "", false, nil
	}
	path, ref, err := SaveImage(ticketID, data, ext, now)
	if err != nil {
		return "", "", true, err
	}
	return path, ref, true, nil
}

// SaveImage writes validated image bytes and returns its durable path and
// Markdown image reference.
func SaveImage(ticketID int64, data []byte, ext string, now time.Time) (string, string, error) {
	if len(data) > MaxDecodedImageBytes {
		return "", "", fmt.Errorf("image exceeds %d MiB limit", MaxDecodedImageBytes>>20)
	}
	if detected := extensionForImage(data, ""); detected == "" || detected != strings.ToLower(ext) {
		return "", "", fmt.Errorf("image data does not match extension %q", ext)
	}
	path, err := WriteImage(ticketID, data, ext, now)
	if err != nil {
		return "", "", err
	}
	return path, fmt.Sprintf("![](%s)", markdownPath(path)), nil
}

func DecodePastedImage(pasted string) ([]byte, string, bool) {
	if len(pasted) > maxEncodedImageBytes {
		return nil, "", false
	}
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

func readPastedImagePath(pasted string) ([]byte, string, bool, error) {
	candidate := strings.TrimSpace(pasted)
	if candidate == "" || strings.ContainsAny(candidate, "\r\n") {
		return nil, "", false, nil
	}
	if len(candidate) >= 2 && ((candidate[0] == '\'' && candidate[len(candidate)-1] == '\'') || (candidate[0] == '"' && candidate[len(candidate)-1] == '"')) {
		candidate = candidate[1 : len(candidate)-1]
	}
	if strings.HasPrefix(candidate, "file://") {
		u, err := url.Parse(candidate)
		if err != nil || (u.Host != "" && u.Host != "localhost") {
			return nil, "", false, nil
		}
		candidate = u.Path
	}
	if candidate == "~" || strings.HasPrefix(candidate, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
		}
	}
	likelyImage := isImageExtension(filepath.Ext(candidate))
	if !likelyImage {
		return nil, "", false, nil
	}
	info, err := os.Stat(candidate)
	if os.IsNotExist(err) || (err == nil && !info.Mode().IsRegular()) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", likelyImage, err
	}
	if info.Size() > MaxDecodedImageBytes {
		if likelyImage {
			return nil, "", true, fmt.Errorf("pasted image exceeds %d MiB limit", MaxDecodedImageBytes>>20)
		}
		return nil, "", false, nil
	}
	f, err := os.Open(candidate)
	if err != nil {
		return nil, "", likelyImage, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDecodedImageBytes+1))
	if err != nil {
		return nil, "", likelyImage, err
	}
	if len(data) > MaxDecodedImageBytes {
		return nil, "", likelyImage, fmt.Errorf("pasted image exceeds %d MiB limit", MaxDecodedImageBytes>>20)
	}
	ext := extensionForImage(data, "")
	if ext == "" {
		return nil, "", false, nil
	}
	return data, ext, true, nil
}

func isImageExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
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
	if len(payload) > maxEncodedImageBytes {
		return nil, fmt.Errorf("encoded image exceeds size limit")
	}
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
	if base64.StdEncoding.DecodedLen(len(payload)) > MaxDecodedImageBytes {
		return nil, fmt.Errorf("decoded image exceeds size limit")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
	}
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDecodedImageBytes {
		return nil, fmt.Errorf("decoded image exceeds size limit")
	}
	return data, nil
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

// DeleteTicket removes attachment files owned by a permanently deleted ticket.
// Archiving intentionally preserves attachments with the durable ticket.
func DeleteTicket(ticketID int64) error {
	if ticketID <= 0 {
		return fmt.Errorf("invalid ticket id")
	}
	return os.RemoveAll(TicketDir(ticketID))
}

func looksLikeEncodedImage(pasted string) bool {
	trimmed := strings.TrimSpace(pasted)
	if strings.HasPrefix(strings.ToLower(trimmed), "data:image/") || strings.Contains(trimmed, "\x1b]52;") {
		return true
	}
	for _, prefix := range []string{"iVBOR", "/9j/", "R0lG", "UklG"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
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
