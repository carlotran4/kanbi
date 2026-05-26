package tui

import "testing"

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
