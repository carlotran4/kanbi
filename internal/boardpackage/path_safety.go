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

// pathWithin reports whether candidate is inside root after resolving absolute
// paths and existing symlink ancestors. A candidate that does not exist yet is
// checked via its nearest existing ancestor so export can reject destinations
// that would land under a symlink alias of the attachments tree.
func pathWithin(root, candidate string) (bool, error) {
	rootResolved, err := resolveExistingPath(root)
	if err != nil {
		return false, err
	}
	candidateResolved, err := resolveExistingPath(candidate)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(rootResolved, candidateResolved)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func resolveExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	// Walk parents until an existing prefix can be evaluated, then rejoin the
	// missing suffix so destinations under symlink-aliased roots still match.
	cur := abs
	suffix := ""
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		base := filepath.Base(cur)
		if suffix == "" {
			suffix = base
		} else {
			suffix = filepath.Join(base, suffix)
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, suffix), nil
		}
		cur = parent
	}
}

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

func readLimited(zf *zip.File, limit int64) ([]byte, error) {
	r, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, limit))
}

func extractRegular(zf *zip.File, target string) error {
	if zf.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink package entry rejected: %s", zf.Name)
	}
	if zf.UncompressedSize64 > maxAttachmentBytes {
		return fmt.Errorf("package entry too large: %s", zf.Name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	r, err := zf.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r, maxAttachmentBytes+1))
	if err != nil {
		return err
	}
	if n > maxAttachmentBytes {
		return fmt.Errorf("package entry too large: %s", zf.Name)
	}
	return nil
}
