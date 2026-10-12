package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// LockRuntime serializes external terminal operations and backup snapshots for
// one canonical file database. The lock does not hold a SQLite connection.
func (s *Store) LockRuntime(ctx context.Context) (func(), error) {
	_, unlock, err := s.LockRuntimeFile(ctx)
	return unlock, err
}
func (s *Store) LockRuntimeFile(ctx context.Context) (*os.File, func(), error) {
	if s.path == "" || s.path == ":memory:" {
		return nil, func() {}, nil
	}
	path, err := filepath.Abs(s.path)
	if err != nil {
		return nil, nil, err
	}
	if real, e := filepath.EvalSymlinks(path); e == nil {
		path = real
	}
	f, err := os.OpenFile(path+".runtime.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			f.Close()
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return f, func() { _ = f.Close() }, nil
}
