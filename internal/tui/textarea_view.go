package tui

import (
	"regexp"

	"github.com/charmbracelet/bubbles/textarea"
)

// textareaOverlayView preserves ordinary ANSI styling (including the Bubbles
// cursor) while removing only erase-in-line/display CSI controls, which would
// otherwise affect the dimmed board behind an overlay.
var eraseCSI = regexp.MustCompile(`\x1b\[[0-9;?]*[JK]`)

func textareaOverlayView(ta textarea.Model) string {
	return sanitizeTextareaOverlayView(ta.View())
}

func sanitizeTextareaOverlayView(view string) string {
	return eraseCSI.ReplaceAllString(view, "")
}
