package attachments

import (
	"errors"
	"reflect"
	"testing"
)

func TestReadClipboardImageUsesWaylandAdvertisedImageType(t *testing.T) {
	var calls [][]string
	run := func(name string, args ...string) ([]byte, error) {
		call := append([]string{name}, args...)
		calls = append(calls, call)
		switch len(calls) {
		case 1:
			return []byte("text/plain\nimage/jpeg\nimage/png\n"), nil
		case 2:
			return tinyPNG, nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	getenv := func(key string) string {
		if key == "WAYLAND_DISPLAY" {
			return "wayland-1"
		}
		return ""
	}

	data, ext, ok, err := readClipboardImage("linux", getenv, run)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || ext != "png" || string(data) != string(tinyPNG) {
		t.Fatalf("readClipboardImage() data=%q ext=%q ok=%v", data, ext, ok)
	}
	want := [][]string{
		{"wl-paste", "--list-types"},
		{"wl-paste", "--type", "image/png", "--no-newline"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
}

func TestReadClipboardImageFallsBackToNoImageWhenHelpersUnavailable(t *testing.T) {
	run := func(string, ...string) ([]byte, error) { return nil, errors.New("not found") }
	data, ext, ok, err := readClipboardImage("linux", func(string) string { return "" }, run)
	if err != nil || ok || data != nil || ext != "" {
		t.Fatalf("readClipboardImage() data=%q ext=%q ok=%v err=%v", data, ext, ok, err)
	}
}

func TestReadClipboardImageUsesXclipOnX11(t *testing.T) {
	var calls [][]string
	run := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if len(calls) == 1 {
			return []byte("image/png\ntext/plain\n"), nil
		}
		return tinyPNG, nil
	}
	data, ext, ok, err := readClipboardImage("linux", func(string) string { return "" }, run)
	if err != nil || !ok || ext != "png" || string(data) != string(tinyPNG) {
		t.Fatalf("readClipboardImage() data=%q ext=%q ok=%v err=%v", data, ext, ok, err)
	}
	if calls[0][0] != "xclip" || calls[1][0] != "xclip" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestReadClipboardImageUsesPngpasteOnMacOS(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		if name != "pngpaste" || !reflect.DeepEqual(args, []string{"-"}) {
			t.Fatalf("command=%s args=%v", name, args)
		}
		return tinyPNG, nil
	}
	_, ext, ok, err := readClipboardImage("darwin", func(string) string { return "" }, run)
	if err != nil || !ok || ext != "png" {
		t.Fatalf("readClipboardImage() ext=%q ok=%v err=%v", ext, ok, err)
	}
}

func TestReadClipboardImageRejectsInvalidAdvertisedImage(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		if name == "wl-paste" && len(args) == 1 {
			return []byte("image/png\n"), nil
		}
		return []byte("not an image"), nil
	}
	_, _, ok, err := readClipboardImage("linux", func(string) string { return "wayland-1" }, run)
	if err == nil || !ok {
		t.Fatalf("readClipboardImage() ok=%v err=%v", ok, err)
	}
}

func TestReadClipboardTextUsesBoundedWaylandHelper(t *testing.T) {
	var calls [][]string
	run := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if len(calls) == 1 {
			return []byte("text/plain\ntext/plain;charset=utf-8\n"), nil
		}
		return []byte("clipboard text"), nil
	}
	text, err := readClipboardText("linux", func(key string) string {
		if key == "WAYLAND_DISPLAY" {
			return "wayland-1"
		}
		return ""
	}, run)
	if err != nil || text != "clipboard text" {
		t.Fatalf("readClipboardText() text=%q err=%v", text, err)
	}
	want := [][]string{
		{"wl-paste", "--list-types"},
		{"wl-paste", "--type", "text/plain;charset=utf-8", "--no-newline"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
}

func TestReadClipboardTextRejectsOversizedContent(t *testing.T) {
	run := func(string, ...string) ([]byte, error) {
		return make([]byte, maxClipboardTextBytes+1), nil
	}
	_, err := readClipboardText("darwin", func(string) string { return "" }, run)
	if err == nil {
		t.Fatal("oversized clipboard text accepted")
	}
}
