// Package archiveutil provides format-neutral filesystem and ZIP extraction
// safety helpers. Archive formats own their paths, limits, and error context.
package archiveutil

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

// PathWithin reports whether candidate is root or below root after resolving
// absolute paths and existing symlink ancestors. Missing descendants are
// resolved through their nearest existing ancestor so symlink aliases are not
// mistaken for paths outside root.
func PathWithin(root, candidate string) (bool, error) {
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

func resolveExistingPath(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}

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

// ReadLimited reads one ZIP member, rejecting both an advertised and an actual
// uncompressed size over limit. It never returns a silently truncated member.
func ReadLimited(zf *zip.File, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, errors.New("archive member limit must not be negative")
	}
	if zf.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("archive member exceeds limit: %s", zf.Name)
	}
	r, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("archive member exceeds limit: %s", zf.Name)
	}
	return data, nil
}

// ExtractRegular extracts zf below root at relativeTarget. It rejects absolute
// and traversal targets and non-regular ZIP entries, enforces limit against
// ZIP metadata and the actual stream, creates private parents/files, and
// removes incomplete output on failure.
func ExtractRegular(root string, zf *zip.File, relativeTarget string, limit int64) (err error) {
	if limit < 0 {
		return errors.New("archive member limit must not be negative")
	}
	target, err := rootRelativeTarget(root, relativeTarget)
	if err != nil {
		return err
	}
	mode := zf.FileInfo().Mode()
	if mode&os.ModeSymlink != 0 || !mode.IsRegular() {
		return fmt.Errorf("non-regular archive entry rejected: %s", zf.Name)
	}
	if zf.UncompressedSize64 > uint64(limit) {
		return fmt.Errorf("archive member exceeds limit: %s", zf.Name)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if inside, err := PathWithin(root, target); err != nil {
		return err
	} else if !inside {
		return fmt.Errorf("archive extraction target escapes root: %q", relativeTarget)
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
	complete := false
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if !complete || err != nil {
			_ = os.Remove(target)
		}
	}()

	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("archive member exceeds limit: %s", zf.Name)
	}
	complete = true
	return nil
}

func rootRelativeTarget(root, relativeTarget string) (string, error) {
	canonical := strings.ReplaceAll(strings.TrimSpace(relativeTarget), "\\", "/")
	if canonical == "" || pathpkg.IsAbs(canonical) || filepath.IsAbs(relativeTarget) {
		return "", fmt.Errorf("unsafe archive extraction target %q", relativeTarget)
	}
	for _, part := range strings.Split(canonical, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe archive extraction target %q", relativeTarget)
		}
	}
	if pathpkg.Clean(canonical) != canonical {
		return "", fmt.Errorf("unsafe archive extraction target %q", relativeTarget)
	}
	return filepath.Join(root, filepath.FromSlash(canonical)), nil
}
