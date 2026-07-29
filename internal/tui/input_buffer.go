package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
)

// InputBuffer owns editable text field state for TUI modals.
type InputBuffer struct {
	value  string
	cursor int
}

func NewInputBuffer(value string) InputBuffer {
	b := InputBuffer{value: value}
	b.End()
	return b
}

func (b InputBuffer) Value() string { return b.value }

func (b InputBuffer) Cursor() int { return b.cursor }

func (b *InputBuffer) Set(value string) {
	b.value = value
	b.clampCursor()
}

func (b *InputBuffer) SetCursor(cursor int) {
	b.cursor = cursor
	b.clampCursor()
}

func (b *InputBuffer) Insert(text string) {
	r := []rune(b.value)
	ins := []rune(text)
	b.clampCursor()
	newValue := make([]rune, 0, len(r)+len(ins))
	newValue = append(newValue, r[:b.cursor]...)
	newValue = append(newValue, ins...)
	newValue = append(newValue, r[b.cursor:]...)
	b.value = string(newValue)
	b.cursor += len(ins)
}

func (b *InputBuffer) Backspace() {
	r := []rune(b.value)
	b.clampCursor()
	if b.cursor == 0 || len(r) == 0 {
		return
	}
	newValue := make([]rune, 0, len(r)-1)
	newValue = append(newValue, r[:b.cursor-1]...)
	newValue = append(newValue, r[b.cursor:]...)
	b.value = string(newValue)
	b.cursor--
}

func (b *InputBuffer) Delete() {
	r := []rune(b.value)
	b.clampCursor()
	if b.cursor >= len(r) {
		return
	}
	newValue := make([]rune, 0, len(r)-1)
	newValue = append(newValue, r[:b.cursor]...)
	newValue = append(newValue, r[b.cursor+1:]...)
	b.value = string(newValue)
}

func (b *InputBuffer) HandleKey(name string, runes []rune) bool {
	switch name {
	case "backspace":
		b.Backspace()
	case "delete":
		b.Delete()
	case "left":
		b.Left()
	case "right":
		b.Right()
	case "home", "ctrl+a":
		b.Home()
	case "end", "ctrl+e":
		b.End()
	case " ":
		b.Insert(" ")
	default:
		if len(runes) == 0 {
			return false
		}
		b.Insert(string(runes))
	}
	return true
}

func (b *InputBuffer) Left() {
	if b.cursor > 0 {
		b.cursor--
	}
}

func (b *InputBuffer) Right() {
	if b.cursor < len([]rune(b.value)) {
		b.cursor++
	}
}

func (b *InputBuffer) Home() { b.cursor = 0 }

func (b *InputBuffer) End() { b.cursor = len([]rune(b.value)) }

type inputGrapheme struct {
	text       string
	start, end int // rune offsets, retained for InputBuffer's existing API
	width      int
}

func inputGraphemes(value string) []inputGrapheme {
	g := uniseg.NewGraphemes(value)
	var result []inputGrapheme
	runeOffset := 0
	for g.Next() {
		text := g.Str()
		count := len([]rune(text))
		result = append(result, inputGrapheme{text: text, start: runeOffset, end: runeOffset + count, width: lipgloss.Width(text)})
		runeOffset += count
	}
	return result
}

// Viewport returns a display-width-bounded single-line presentation that keeps
// the editing cursor visible. It uses grapheme boundaries so emoji and
// combining sequences are never split. Ellipses indicate clipped neighbours.
func (b InputBuffer) Viewport(width int) string {
	if width <= 0 {
		return ""
	}
	graphemes := inputGraphemes(b.value)
	cursor := b.cursor
	if cursor < 0 {
		cursor = 0
	}
	if max := len([]rune(b.value)); cursor > max {
		cursor = max
	}
	cursorIndex := len(graphemes) // blank cursor at end
	for i, g := range graphemes {
		if cursor < g.end {
			cursorIndex = i
			break
		}
	}
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	// A cell remains visible even at end-of-input. Width one intentionally
	// prefers that cell over clipping indicators.
	if width == 1 {
		if cursorIndex < len(graphemes) && graphemes[cursorIndex].width == 1 {
			return cursorStyle.Render(graphemes[cursorIndex].text)
		}
		return cursorStyle.Render(" ")
	}
	cursorWidth := 1
	cursorText := " "
	if cursorIndex < len(graphemes) {
		cursorText = graphemes[cursorIndex].text
		cursorWidth = graphemes[cursorIndex].width
		if cursorWidth < 1 {
			cursorWidth = 1
		}
		// A double-width grapheme cannot fit in a one-cell viewport, but it can
		// still be represented by the cursor cell without overflowing it.
		if cursorWidth > width {
			cursorText, cursorWidth = " ", 1
		}
	}
	start, end := cursorIndex, cursorIndex
	if cursorIndex < len(graphemes) {
		end++ // the focused grapheme is rendered separately below
	}
	used := cursorWidth
	// Prefer preceding context, then add following context while reserving room
	// for indicators when content remains hidden.
	for start > 0 {
		candidate := graphemes[start-1].width
		if candidate < 1 {
			candidate = 1
		}
		if used+candidate > width {
			break
		}
		start--
		used += candidate
	}
	for end < len(graphemes) {
		candidate := graphemes[end].width
		if candidate < 1 {
			candidate = 1
		}
		if used+candidate > width {
			break
		}
		end++
		used += candidate
	}
	left, right := start > 0, end < len(graphemes)
	// Make room for clipping markers by removing farthest non-cursor context.
	for (left || right) && used+boolWidth(left)+boolWidth(right) > width {
		if end > cursorIndex+1 {
			end--
			used -= maxInt(1, graphemes[end].width)
			right = end < len(graphemes)
			continue
		}
		if start < cursorIndex {
			used -= maxInt(1, graphemes[start].width)
			start++
			left = start > 0
			continue
		}
		break
	}
	var out strings.Builder
	if left {
		out.WriteString("…")
	}
	for i := start; i < cursorIndex; i++ {
		out.WriteString(graphemes[i].text)
	}
	out.WriteString(cursorStyle.Render(cursorText))
	for i := cursorIndex + 1; i < end; i++ {
		out.WriteString(graphemes[i].text)
	}
	if right {
		out.WriteString("…")
	}
	return trimToWidth(out.String(), width)
}

func boolWidth(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (b *InputBuffer) clampCursor() {
	if b.cursor < 0 {
		b.cursor = 0
	}
	if max := len([]rune(b.value)); b.cursor > max {
		b.cursor = max
	}
}
