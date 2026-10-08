package tui

import (
	"context"
	"sync"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
)

const runtimeRefreshTimeout = 1500 * time.Millisecond

// Shared by value copies of Model. Close prevents queued commands from starting
// and joins already-started work before the application's store is closed.
type runtimeRefreshWorker struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	wg     sync.WaitGroup
}

func newRuntimeRefreshWorker(parent context.Context) *runtimeRefreshWorker {
	ctx, cancel := context.WithCancel(parent)
	return &runtimeRefreshWorker{ctx: ctx, cancel: cancel}
}

func (w *runtimeRefreshWorker) stop() {
	w.mu.Lock()
	w.closed = true
	w.cancel()
	w.mu.Unlock()
}

// Close cancels and drains TUI observation and status commands. Call after Run,
// before closing Actions' storage. It does not close ticket agent sessions.
func (m Model) Close() {
	if m.statusBarCancel != nil {
		m.statusBarCancel()
	}
	if m.refreshWorker != nil {
		m.refreshWorker.stop()
		m.refreshWorker.wg.Wait()
	}
}

type boardSnapshot struct {
	view  storage.BoardView
	focus storage.FocusStatus
	runs  []storage.IntegrationRun
}

type runtimeRefreshedMsg struct {
	generation uint64
	snapshot   boardSnapshot
	err        error
}

// readSnapshot performs only data acquisition. The command must never mutate
// model state or textarea buffers; application happens in Update.
func (m Model) readSnapshot(ctx context.Context) (boardSnapshot, error) {
	var snapshot boardSnapshot
	var err error
	if m.masterBoard {
		snapshot.view, err = m.actions.MasterBoardViewWithFilter(ctx, m.masterFilter)
	} else if m.boardID != 0 {
		snapshot.view, err = m.actions.BoardViewByID(ctx, m.boardID)
	} else {
		snapshot.view, err = m.actions.BoardView(ctx)
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.focus, err = m.actions.FocusStatus(ctx)
	if err != nil {
		return snapshot, err
	}
	if snapshot.view.Board.ID != 0 && snapshot.view.Board.WorktreeMode == storage.WorktreeModeGit {
		snapshot.runs, err = m.actions.ListIntegrationRuns(ctx, snapshot.view.Board.ID)
	}
	return snapshot, err
}

func (m Model) runtimeRefreshCmd() tea.Cmd {
	// Filter drafts can reuse backing arrays; isolate the request's selectors.
	m.masterFilter.BoardIDs = append([]int64(nil), m.masterFilter.BoardIDs...)
	m.masterFilter.Runtimes = append([]string(nil), m.masterFilter.Runtimes...)
	m.masterFilter.Harnesses = append([]string(nil), m.masterFilter.Harnesses...)
	return func() tea.Msg {
		w := m.refreshWorker
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return nil
		}
		w.wg.Add(1)
		w.mu.Unlock()
		defer w.wg.Done()
		ctx, cancel := context.WithTimeout(w.ctx, runtimeRefreshTimeout)
		defer cancel()
		result := runtimeRefreshedMsg{generation: m.projectionGeneration}
		if result.err = m.actions.RefreshRuntime(ctx); result.err == nil {
			result.snapshot, result.err = m.readSnapshot(ctx)
		}
		return result
	}
}

// Keep the last valid board visible if polling fails. This is separate from a
// foreground load error, which can legitimately have no board to display.
func (m *Model) applyRuntimeRefresh(result runtimeRefreshedMsg) {
	if result.generation != m.projectionGeneration {
		return
	}
	if result.err != nil {
		m.refreshError = "runtime refresh stale: " + storage.RedactSecretText(result.err.Error())
		return
	}
	m.refreshError = ""
	m.applySnapshot(result.snapshot)
}
