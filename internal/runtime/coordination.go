package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/carlotran4/kanbi/internal/multiplexer"

	"golang.org/x/sys/unix"
)

type runtimeLockKey struct{}

// All Kanbi processes coordinate live container operations through one lock for
// the canonical database path. The context marker permits internal composition.
func (m *Manager) lockRuntime(ctx context.Context) (context.Context, func(), error) {
	if ctx.Value(runtimeLockKey{}) == m {
		return ctx, func() {}, nil
	}
	m.operationOnce.Do(func() { m.operationGate = make(chan struct{}, 1) })
	select {
	case m.operationGate <- struct{}{}:
	case <-ctx.Done():
		return ctx, nil, ctx.Err()
	}
	release := func() { <-m.operationGate }
	if m.Store == nil {
		return context.WithValue(ctx, runtimeLockKey{}, m), release, nil
	}
	file, unlock, err := m.Store.LockRuntimeFile(ctx)
	if err != nil {
		release()
		return ctx, nil, err
	}
	return context.WithValue(multiplexer.WithOperationLock(ctx, file), runtimeLockKey{}, m), func() { unlock(); release() }, nil

}
func (m *Manager) runtimeReady(ctx context.Context) error {
	if m.Store == nil {
		return nil
	}
	j, err := m.loadFleet(ctx)
	if err != nil {
		return err
	}
	if j != nil && j.Phase != "active" {
		return errors.New("Fleet restoration pending: open Kanbi and press v to restore panes")
	}
	return nil
}
func (m *Manager) acquireFleetOwner() error {
	m.fleetOwnerMu.Lock()
	defer m.fleetOwnerMu.Unlock()
	if m.fleetOwner != nil {
		return nil
	}
	path := m.Config.DBPath
	if m.Store != nil {
		path = m.Store.DatabasePath()
	}
	if path == "" || path == ":memory:" {
		return errors.New("Fleet requires a file database")
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if real, e := filepath.EvalSymlinks(p); e == nil {
		p = real
	}
	f, err := os.OpenFile(p+".fleet.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return errors.New("another Kanbi process owns Fleet; return from its header first")
	}
	m.fleetOwner = f
	return nil
}
func (m *Manager) releaseFleetOwner() {
	m.fleetOwnerMu.Lock()
	defer m.fleetOwnerMu.Unlock()
	if m.fleetOwner != nil {
		_ = unix.Flock(int(m.fleetOwner.Fd()), unix.LOCK_UN)
		_ = m.fleetOwner.Close()
		m.fleetOwner = nil
	}
}

// Probe ownership without retaining or displacing the live visit's owner.
func (m *Manager) fleetOwnerAvailable() (bool, error) {
	m.fleetOwnerMu.Lock()
	defer m.fleetOwnerMu.Unlock()
	if m.fleetOwner != nil {
		return true, nil
	}
	path := m.Config.DBPath
	if m.Store != nil {
		path = m.Store.DatabasePath()
	}
	if path == "" || path == ":memory:" {
		return false, errors.New("Fleet requires a file database")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	if real, e := filepath.EvalSymlinks(path); e == nil {
		path = real
	}
	f, err := os.OpenFile(path+".fleet.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func (m *Manager) hasFleetOwner() bool {
	m.fleetOwnerMu.Lock()
	defer m.fleetOwnerMu.Unlock()
	return m.fleetOwner != nil
}
