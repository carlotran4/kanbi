package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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

func (b InputBuffer) Render() string {
	r := []rune(b.value)
	cursor := b.cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(r) {
		cursor = len(r)
	}
	var out strings.Builder
	out.WriteString(string(r[:cursor]))
	if cursor < len(r) {
		out.WriteString(lipgloss.NewStyle().Reverse(true).Render(string(r[cursor : cursor+1])))
		out.WriteString(string(r[cursor+1:]))
	} else {
		out.WriteString(lipgloss.NewStyle().Reverse(true).Render(" "))
	}
	return out.String()
}

func (b *InputBuffer) clampCursor() {
	if b.cursor < 0 {
		b.cursor = 0
	}
	if max := len([]rune(b.value)); b.cursor > max {
		b.cursor = max
	}
}

func popRune(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(r[:len(r)-1])
}
