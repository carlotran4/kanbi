package archiveutil

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPathWithinResolvesExistingSymlinkAncestors(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "nested", "file")
	outside := filepath.Join(t.TempDir(), "file")
	for _, tc := range []struct {
		name      string
		candidate string
		want      bool
	}{
		{"equal", root, true},
		{"inside", inside, true},
		{"outside", outside, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PathWithin(root, tc.candidate)
			if err != nil || got != tc.want {
				t.Fatalf("PathWithin() = %v, %v; want %v, nil", got, err, tc.want)
			}
		})
	}

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	got, err := PathWithin(root, filepath.Join(alias, "missing", "file"))
	if err != nil || !got {
		t.Fatalf("PathWithin() through alias = %v, %v; want true, nil", got, err)
	}
}

func TestReadLimitedRejectsAdvertisedAndActualOverflow(t *testing.T) {
	zf := zipMember(t, "member", []byte("12345"), 0o600)
	if _, err := ReadLimited(zf, 4); err == nil {
		t.Fatal("advertised overflow accepted")
	}
	zf.UncompressedSize64 = 1 // Crafted metadata must not bypass streaming limit.
	if _, err := ReadLimited(zf, 4); err == nil {
		t.Fatal("actual overflow accepted")
	}
	zf = zipMember(t, "member", []byte("1234"), 0o600)
	data, err := ReadLimited(zf, 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("ReadLimited() = %q, %v", data, err)
	}
}

func TestExtractRegularSafetyAndLimits(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name   string
		target string
		mode   os.FileMode
		data   []byte
		limit  int64
	}{
		{"absolute target", "/escape", 0o600, []byte("x"), 1},
		{"traversal target", "../escape", 0o600, []byte("x"), 1},
		{"symlink member", "file", os.ModeSymlink | 0o777, []byte("x"), 1},
		{"directory member", "file", os.ModeDir | 0o755, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zf := zipMember(t, "member", tc.data, tc.mode)
			if err := ExtractRegular(root, zf, tc.target, tc.limit); err == nil {
				t.Fatal("unsafe extraction accepted")
			}
		})
	}

	zf := zipMember(t, "member", []byte("1234"), 0o600)
	if err := ExtractRegular(root, zf, "nested/file", 4); err != nil {
		t.Fatalf("exact limit extract: %v", err)
	}
	target := filepath.Join(root, "nested", "file")
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "1234" {
		t.Fatalf("extracted file = %q, %v", data, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions = %v, %v; want 0600", info.Mode(), err)
	}
	if info, err := os.Stat(filepath.Dir(target)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("parent permissions = %v, %v; want 0700", info.Mode(), err)
	}
	if err := ExtractRegular(root, zf, "nested/file", 4); err == nil {
		t.Fatal("existing target overwritten")
	}
}

func TestExtractRegularCleansPartialOutputOnActualOverflow(t *testing.T) {
	root := t.TempDir()
	zf := zipMember(t, "member", []byte("12345"), 0o600)
	zf.UncompressedSize64 = 1
	if err := ExtractRegular(root, zf, "partial", 4); err == nil {
		t.Fatal("actual overflow accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "partial")); !os.IsNotExist(err) {
		t.Fatalf("partial output remains: %v", err)
	}
}

func zipMember(t *testing.T, name string, data []byte, mode os.FileMode) *zip.File {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "member.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = zr.Close() })
	return zr.File[0]
}
