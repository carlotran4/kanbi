package attachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	clipboardCommandTimeout = 3 * time.Second
	maxClipboardTextBytes   = 1 << 20 // 1 MiB
)

type clipboardCommand func(name string, args ...string) ([]byte, error)

// ReadClipboardImage reads a supported image from the desktop clipboard.
// It uses standard platform clipboard helpers and returns ok=false when no
// supported image (or helper) is available.
func ReadClipboardImage() ([]byte, string, bool, error) {
	return readClipboardImage(runtime.GOOS, os.Getenv, runClipboardCommand)
}

// ReadClipboardText reads bounded UTF-8 text from the desktop clipboard.
func ReadClipboardText() (string, error) {
	return readClipboardText(runtime.GOOS, os.Getenv, runClipboardTextCommand)
}

func readClipboardImage(goos string, getenv func(string) string, run clipboardCommand) ([]byte, string, bool, error) {
	var data []byte
	var mediaType string
	var advertised bool

	switch goos {
	case "linux":
		if getenv("WAYLAND_DISPLAY") != "" || strings.EqualFold(getenv("XDG_SESSION_TYPE"), "wayland") {
			types, err := run("wl-paste", "--list-types")
			if err == nil {
				mediaType = preferredClipboardImageType(string(types))
				advertised = mediaType != ""
				if advertised {
					data, err = run("wl-paste", "--type", mediaType, "--no-newline")
					if err != nil {
						return nil, "", true, fmt.Errorf("read image clipboard with wl-paste: %w", err)
					}
				}
			}
		}
		if len(data) == 0 {
			types, err := run("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
			if err == nil {
				mediaType = preferredClipboardImageType(string(types))
				advertised = advertised || mediaType != ""
				if mediaType != "" {
					data, err = run("xclip", "-selection", "clipboard", "-t", mediaType, "-o")
					if err != nil {
						return nil, "", true, fmt.Errorf("read image clipboard with xclip: %w", err)
					}
				}
			}
		}
	case "darwin":
		var err error
		data, err = run("pngpaste", "-")
		if err == nil && len(data) > 0 {
			mediaType = "image/png"
			advertised = true
		}
	default:
		return nil, "", false, nil
	}

	if len(data) == 0 {
		return nil, "", advertised, nil
	}
	if len(data) > MaxDecodedImageBytes {
		return nil, "", true, fmt.Errorf("clipboard image exceeds %d MiB limit", MaxDecodedImageBytes>>20)
	}
	ext := extensionForImage(data, mediaType)
	if ext == "" {
		return nil, "", true, errors.New("clipboard image format is unsupported or invalid")
	}
	return data, ext, true, nil
}

func readClipboardText(goos string, getenv func(string) string, run clipboardCommand) (string, error) {
	var attempts [][]string
	switch goos {
	case "linux":
		if getenv("WAYLAND_DISPLAY") != "" || strings.EqualFold(getenv("XDG_SESSION_TYPE"), "wayland") {
			if types, err := run("wl-paste", "--list-types"); err == nil {
				if mediaType := preferredClipboardTextType(string(types)); mediaType != "" {
					if data, readErr := run("wl-paste", "--type", mediaType, "--no-newline"); readErr == nil {
						if len(data) > maxClipboardTextBytes {
							return "", fmt.Errorf("clipboard text exceeds %d MiB limit", maxClipboardTextBytes>>20)
						}
						return string(data), nil
					}
				}
			}
		}
		attempts = append(attempts, []string{"xclip", "-selection", "clipboard", "-o"})
	case "darwin":
		attempts = append(attempts, []string{"pbpaste"})
	default:
		return "", errors.New("clipboard text paste is unsupported on this platform")
	}
	var lastErr error
	for _, attempt := range attempts {
		data, err := run(attempt[0], attempt[1:]...)
		if err != nil {
			lastErr = err
			continue
		}
		if len(data) > maxClipboardTextBytes {
			return "", fmt.Errorf("clipboard text exceeds %d MiB limit", maxClipboardTextBytes>>20)
		}
		return string(data), nil
	}
	if lastErr == nil {
		lastErr = errors.New("no clipboard helper available")
	}
	return "", fmt.Errorf("read clipboard text: %w", lastErr)
}

func preferredClipboardTextType(types string) string {
	available := make(map[string]string)
	for _, line := range strings.Split(types, "\n") {
		raw := strings.TrimSpace(line)
		available[strings.ToLower(raw)] = raw
	}
	for _, preferred := range []string{"text/plain;charset=utf-8", "text/plain", "utf8_string", "string"} {
		if raw := available[preferred]; raw != "" {
			return raw
		}
	}
	return ""
}

func preferredClipboardImageType(types string) string {
	available := make(map[string]string)
	for _, line := range strings.Fields(types) {
		base := strings.ToLower(strings.TrimSpace(strings.SplitN(line, ";", 2)[0]))
		if strings.HasPrefix(base, "image/") {
			available[base] = strings.TrimSpace(line)
		}
	}
	for _, preferred := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if raw := available[preferred]; raw != "" {
			return raw
		}
	}
	return ""
}

func runClipboardCommand(name string, args ...string) ([]byte, error) {
	return runBoundedClipboardCommand(MaxDecodedImageBytes+1, name, args...)
}

func runClipboardTextCommand(name string, args ...string) ([]byte, error) {
	return runBoundedClipboardCommand(maxClipboardTextBytes+1, name, args...)
}

func runBoundedClipboardCommand(limit int, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, remaining: limit}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, remaining: 4096}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

type limitedWriter struct {
	w         *bytes.Buffer
	remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	if original > w.remaining {
		_, _ = w.w.Write(p[:w.remaining])
		w.remaining = 0
		return original, nil
	}
	_, _ = w.w.Write(p)
	w.remaining -= original
	return original, nil
}
