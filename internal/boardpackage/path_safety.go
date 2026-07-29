package boardpackage

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

const maxAttachmentBytes = 50 << 20 // 50 MiB per attachment, tighter than full-backup budgets

// safeRelativePath cleans and rejects traversal paths. Attachment inventory
// relative paths must be single basenames; nested segments are rejected by
// callers that require flat packaging.
func safeRelativePath(name string) (string, error) {
	name = pathpkg.Clean(filepath.ToSlash(strings.TrimSpace(name)))
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe package path %q", name)
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("unsafe package path %q", name)
	}
	return name, nil
}

func flatAttachmentName(name string) (string, error) {
	if strings.Contains(name, `\`) {
		return "", fmt.Errorf("backslash attachment path rejected: %q", name)
	}
	safe, err := safeRelativePath(name)
	if err != nil {
		return "", err
	}
	if strings.Contains(safe, "/") {
		return "", fmt.Errorf("nested attachment path rejected: %q", name)
	}
	return safe, nil
}

func writeBytes(zw *zip.Writer, name string, data []byte, mode os.FileMode) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func writeFile(zw *zip.Writer, name, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported attachment file: %s", src)
	}
	if info.Size() > maxAttachmentBytes {
		return fmt.Errorf("attachment too large: %s", src)
	}
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(0o600)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, io.LimitReader(f, maxAttachmentBytes+1))
	return err
}
