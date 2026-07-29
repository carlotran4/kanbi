package tui

import (
	"strings"
	"testing"
)

func TestInputBufferEditsTextAtCursor(t *testing.T) {
	buf := NewInputBuffer("ab")
	buf.Left()
	buf.Insert("🙂c")
	if got, want := buf.Value(), "a🙂cb"; got != want {
		t.Fatalf("value = %q, want %q", got, want)
	}
	if got, want := buf.Cursor(), 3; got != want {
		t.Fatalf("cursor = %d, want %d", got, want)
	}

	buf.Backspace()
	if got, want := buf.Value(), "a🙂b"; got != want {
		t.Fatalf("after backspace value = %q, want %q", got, want)
	}
	if got, want := buf.Cursor(), 2; got != want {
		t.Fatalf("after backspace cursor = %d, want %d", got, want)
	}

	buf.Delete()
	if got, want := buf.Value(), "a🙂"; got != want {
		t.Fatalf("after delete value = %q, want %q", got, want)
	}
	if got, want := buf.Cursor(), 2; got != want {
		t.Fatalf("after delete cursor = %d, want %d", got, want)
	}
}

func TestInputBufferViewportKeepsCursorVisibleAndBounded(t *testing.T) {
	cases := []struct {
		value         string
		cursor, width int
	}{
		{"", 0, 1}, {"abcdef", 0, 4}, {"abcdef", 3, 4}, {"abcdef", 6, 4},
		{"a🙂界b", 1, 4}, {"a🙂界b", 2, 4}, {"a🙂界b", 3, 4},
	}
	for _, tc := range cases {
		buf := NewInputBuffer(tc.value)
		buf.SetCursor(tc.cursor)
		view := buf.Viewport(tc.width)
		if got := displayWidth(view); got > tc.width {
			t.Fatalf("%q cursor=%d width=%d rendered %d: %q", tc.value, tc.cursor, tc.width, got, view)
		}
		if ansiStrip(view) == "" {
			t.Fatalf("cursor cell missing from %q", view)
		}
	}
	buf := NewInputBuffer("abcdef")
	buf.End()
	if view := ansiStrip(buf.Viewport(4)); !strings.HasPrefix(view, "…") || !strings.Contains(view, " ") {
		t.Fatalf("end viewport=%q", view)
	}
}

func TestInputBufferMovementAndBounds(t *testing.T) {
	buf := NewInputBuffer("abc")
	if got, want := buf.Cursor(), 3; got != want {
		t.Fatalf("new cursor = %d, want %d", got, want)
	}

	buf.Home()
	buf.Left()
	buf.Backspace()
	if got, want := buf.Cursor(), 0; got != want {
		t.Fatalf("cursor before start = %d, want %d", got, want)
	}
	if got, want := buf.Value(), "abc"; got != want {
		t.Fatalf("value changed at start = %q, want %q", got, want)
	}

	buf.Right()
	buf.Right()
	buf.Right()
	buf.Right()
	if got, want := buf.Cursor(), 3; got != want {
		t.Fatalf("cursor past end = %d, want %d", got, want)
	}
	buf.Delete()
	if got, want := buf.Value(), "abc"; got != want {
		t.Fatalf("value changed at end = %q, want %q", got, want)
	}

	buf.SetCursor(99)
	if got, want := buf.Cursor(), 3; got != want {
		t.Fatalf("clamped cursor = %d, want %d", got, want)
	}
	buf.Set("x")
	if got, want := buf.Cursor(), 1; got != want {
		t.Fatalf("cursor after shorter set = %d, want %d", got, want)
	}
	buf.HandleKey("home", nil)
	buf.HandleKey("x", []rune("y"))
	buf.HandleKey("end", nil)
	if got, want := buf.Value(), "yx"; got != want {
		t.Fatalf("home/end value = %q, want %q", got, want)
	}
	if got, want := buf.Cursor(), 2; got != want {
		t.Fatalf("end cursor = %d, want %d", got, want)
	}
}
