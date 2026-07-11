package tmux

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	defaultRefCapturePollInterval  = 500 * time.Millisecond
	defaultPiRefCaptureTimeout     = 30 * time.Second
	defaultClaudeRefCaptureTimeout = 5 * time.Minute
)

type backgroundState struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	wg     sync.WaitGroup
}

func newBackgroundState(parent context.Context) *backgroundState {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &backgroundState{ctx: ctx, cancel: cancel}
}

// Close cancels and waits for asynchronous session-ref capture owned by this
// manager. It is safe to call more than once.
func (m *Manager) Close() {
	if m == nil || m.background == nil {
		return
	}
	m.background.mu.Lock()
	if !m.background.closed {
		m.background.closed = true
		m.background.cancel()
	}
	m.background.mu.Unlock()
	m.background.wg.Wait()
}

func (m *Manager) startSessionRefCapture(sessionID int64, harnessName string, timeout time.Duration, capture func() (string, bool)) {
	if m == nil || m.Store == nil || m.background == nil || sessionID == 0 {
		return
	}
	pollInterval := m.RefCapturePollInterval
	if pollInterval <= 0 {
		pollInterval = defaultRefCapturePollInterval
	}
	m.background.mu.Lock()
	if m.background.closed {
		m.background.mu.Unlock()
		return
	}
	ctx := m.background.ctx
	m.background.wg.Add(1)
	m.background.mu.Unlock()

	go func() {
		defer m.background.wg.Done()
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ref, ok := capture()
				if !ok {
					continue
				}
				// Bind the captured ref to the row created by this launch. Never
				// look up the ticket's current active session: it may be a newer
				// attempt by the time a slow harness exposes its ref.
				if err := m.Store.UpdateSessionRef(ctx, sessionID, ref); err != nil {
					if ctx.Err() == nil {
						m.reportBackgroundError(fmt.Errorf("capture %s session ref for session %d: %w", harnessName, sessionID, err))
					}
					return
				}
				return
			}
		}
	}()
}

func (m *Manager) reportBackgroundError(err error) {
	if err == nil {
		return
	}
	if m.BackgroundError != nil {
		m.BackgroundError(err)
		return
	}
	log.Printf("kanbi: %v", err)
}
