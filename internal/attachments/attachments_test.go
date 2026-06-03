package attachments

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestDecodePastedImageDataURI(t *testing.T) {
	payload := "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyPNG)
	data, ext, ok := DecodePastedImage(payload)
	if !ok || ext != "png" || string(data) != string(tinyPNG) {
		t.Fatalf("DecodePastedImage() data=%q ext=%q ok=%v", data, ext, ok)
	}
}

func TestDecodePastedImageOSC52(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(tinyPNG)
	data, ext, ok := DecodePastedImage("\x1b]52;c;" + payload + "\a")
	if !ok || ext != "png" || string(data) != string(tinyPNG) {
		t.Fatalf("DecodePastedImage() data=%q ext=%q ok=%v", data, ext, ok)
	}
}

func TestSavePastedImageWritesPerTicketFileAndMarkdownRef(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	path, ref, ok, err := SavePastedImage(47, base64.StdEncoding.EncodeToString(tinyPNG), time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("paste was not detected as image")
	}
	wantPath := filepath.Join(dataHome, "agent-kanban", "attachments", "47", "20240101-120000-001.png")
	if path != wantPath {
		t.Fatalf("path=%q want %q", path, wantPath)
	}
	if ref != "![]("+filepath.ToSlash(wantPath)+")" {
		t.Fatalf("markdown ref=%q", ref)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(tinyPNG) {
		t.Fatalf("written data mismatch")
	}
}

func TestSavePastedImageAvoidsFilenameCollisions(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	payload := base64.StdEncoding.EncodeToString(tinyPNG)
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	first, _, _, err := SavePastedImage(47, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	second, _, _, err := SavePastedImage(47, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasSuffix(second, "20240101-120000-002.png") {
		t.Fatalf("collision not avoided: first=%q second=%q", first, second)
	}
}
