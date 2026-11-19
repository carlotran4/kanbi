// Package devreload implements opt-in development builds. It never owns agents.
package devreload

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Result reports a completed build; compiler output is kept outside the terminal.
type Result struct {
	Path, Log string
	Err       error
}

// Fingerprint includes source additions, edits, deletions and embedded assets.
func Fingerprint(root string) ([32]byte, error) {
	h := sha256.New()
	for _, name := range []string{"cmd", "internal", "go.mod", "go.sum"} {
		err := filepath.WalkDir(filepath.Join(root, name), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%s\x00", rel)
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			h.Write([]byte{0})
			return nil
		})
		if err != nil {
			return [32]byte{}, err
		}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// Watch debounces changes across two polls, builds atomically and retries only
// after another edit. Cancellation stops and reaps an in-flight compiler.
func Watch(ctx context.Context, root, target string, report func(Result)) {
	previous, err := Fingerprint(root)
	if err != nil {
		report(Result{Err: err})
		return
	}
	attempted := previous
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		current, err := Fingerprint(root)
		if err != nil {
			report(Result{Err: err})
			continue
		}
		if current != previous {
			previous = current
			continue
		}
		if current == attempted {
			continue
		}
		attempted = current
		result := build(ctx, root, target)
		if ctx.Err() != nil {
			return
		}
		// Do not restart into a build superseded by saves during compilation.
		after, err := Fingerprint(root)
		if err != nil || after != current {
			continue
		}
		report(result)
	}
}

func build(ctx context.Context, root, target string) Result {
	result := Result{Log: target + ".reload.log"}
	dir, err := os.MkdirTemp(filepath.Dir(target), "reload-build-")
	if err != nil {
		result.Err = err
		return result
	}
	defer os.RemoveAll(dir)
	log, err := os.OpenFile(result.Log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		result.Err = err
		return result
	}
	candidate := filepath.Join(dir, "kanbi")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", candidate, "./cmd/kanbi")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TMPDIR="+dir, "GOTMPDIR="+dir)
	cmd.Stdout, cmd.Stderr = log, log
	result.Err = cmd.Run()
	closeErr := log.Close()
	if result.Err == nil {
		result.Err = closeErr
	}
	if result.Err == nil {
		result.Err = os.Rename(candidate, target)
	}
	if result.Err == nil {
		result.Path = target
	}
	return result
}
