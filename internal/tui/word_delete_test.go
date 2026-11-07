package tui

import (
	"testing"

	"github.com/carlotran4/kanbi/internal/tui/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestInputBufferWordDeletion(t *testing.T) {
	for _, shortcut := range []string{"ctrl+backspace", "ctrl+h", "alt+backspace", "ctrl+w"} {
		for _, tc := range []struct {
			value    string
			cursor   int
			want     string
			position int
		}{
			{"hello world", 11, "hello ", 6},
			{"hello world  ", 13, "hello ", 6},
			{"café 世界 suffix", 7, "café  suffix", 5},
			{"hello", 0, "hello", 0},
			{"", 0, "", 0},
			{"one\u00a0two", 7, "one\u00a0", 4},
		} {
			b := NewInputBuffer(tc.value)
			b.SetCursor(tc.cursor)
			if !b.HandleKey(shortcut, nil) || b.Value() != tc.want || b.Cursor() != tc.position {
				t.Fatalf("%s: got %q/%d want %q/%d", shortcut, b.Value(), b.Cursor(), tc.want, tc.position)
			}
		}
	}
}

func TestTextEditorsWordDeletion(t *testing.T) {
	for name, input := range map[string]func() textarea.Model{
		"body":    func() textarea.Model { return newBodyTextarea("hello café  suffix", 160) },
		"note":    func() textarea.Model { return newNoteTextarea("hello café  suffix", 160) },
		"handoff": func() textarea.Model { return focusTextarea("hello café  suffix", 76, 3) },
	} {
		for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlH}, {Type: tea.KeyBackspace, Alt: true}, {Type: tea.KeyCtrlW}} {
			ta := input()
			ta.Focus()
			ta.SetCursor(12)
			ta, _ = ta.Update(key)
			if ta.Value() != "hello suffix" {
				t.Fatalf("%s %s: %q", name, key.String(), ta.Value())
			}
		}
	}
}

func TestTextEditorBackspaceStillDeletesOneCharacter(t *testing.T) {
	ta := newBodyTextarea("hello world", 80)
	ta.Focus()
	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if ta.Value() != "hello worl" {
		t.Fatalf("backspace: %q", ta.Value())
	}
}
