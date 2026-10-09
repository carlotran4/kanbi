package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/statusbar"
	"github.com/carlotran4/kanbi/internal/storage"
)

const (
	focusPerformanceColumns = 4
	focusPerformanceTickets = 2000
	focusPerformanceCard    = 1500
)

// TestPerformanceFocusNavigation measures the Focus-specific horizontal path.
// It is opt-in because timings are evidence, not a portable correctness oracle.
func TestPerformanceFocusNavigation(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("set KANBI_PERFORMANCE=1 for Focus navigation measurements")
	}
	t.Logf("reference Go=%s OS=%s arch=%s CPUs=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	for _, enabled := range []bool{false, true} {
		for _, size := range [][2]int{{80, 24}, {160, 40}} {
			for _, key := range []tea.KeyMsg{
				{Type: tea.KeyRunes, Runes: []rune{'l'}},
				{Type: tea.KeyRight},
				{Type: tea.KeyRunes, Runes: []rune{'h'}},
				{Type: tea.KeyLeft},
			} {
				name := fmt.Sprintf("enabled=%t/%dx%d/%s", enabled, size[0], size[1], key.String())
				t.Run(name, func(t *testing.T) {
					m := focusPerformanceModel(enabled, size[0], size[1])
					measureFocusNavigation(t, "update", 30, func() {
						resetFocusNavigation(&m, key)
						updated, _ := m.Update(key)
						m = updated.(Model)
					})
					measureFocusNavigation(t, "view", 30, func() { _ = m.View() })
				})
			}
		}
	}
}

// TestPerformanceFocusProgram drives the real Bubble Tea input/update/renderer
// loop while a refresh observation is blocked. It measures receipt-to-writer,
// not physical terminal paint time.
func TestPerformanceFocusProgram(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("set KANBI_PERFORMANCE=1 for Focus program measurements")
	}
	for _, enabled := range []bool{false, true} {
		for _, size := range [][2]int{{80, 24}, {160, 40}} {
			for _, input := range []struct {
				name  string
				bytes string
			}{
				{name: "l", bytes: "l"},
				{name: "right", bytes: "\x1b[C"},
				{name: "h", bytes: "h"},
				{name: "left", bytes: "\x1b[D"},
			} {
				name := fmt.Sprintf("enabled=%t/%dx%d/%s", enabled, size[0], size[1], input.name)
				t.Run(name, func(t *testing.T) {
					latencies := make([]int64, 10)
					for i := range latencies {
						latencies[i] = runFocusProgramTrial(t, enabled, size[0], size[1], input.name, input.bytes)
					}
					sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
					q := func(p int) float64 { return float64(latencies[(len(latencies)*p+99)/100-1]) / 1e6 }
					t.Logf("PERF focus-program-receipt-to-write n=%d p50=%.3fms p95=%.3fms p99=%.3fms", len(latencies), q(50), q(95), q(99))
				})
			}
		}
	}
}

func focusPerformanceModel(enabled bool, width, height int) Model {
	columns := make([]storage.Column, focusPerformanceColumns)
	keys := make([]string, focusPerformanceColumns)
	for ci := range columns {
		key := fmt.Sprintf("focus-%d", ci)
		keys[ci] = key
		columns[ci] = storage.Column{ID: int64(ci + 1), Name: fmt.Sprintf("Column %d", ci), WorkflowKey: key}
		columns[ci].Tickets = make([]storage.Ticket, focusPerformanceTickets)
		for ti := range columns[ci].Tickets {
			paused := ti >= focusPerformanceTickets/2
			ticket := storage.Ticket{
				ID:          int64(ci*focusPerformanceTickets + ti + 1),
				DisplayID:   fmt.Sprintf("C%d-%04d", ci, ti),
				Title:       fmt.Sprintf("ticket %d", ti),
				Body:        "focused navigation body",
				Harness:     "pi",
				Runtime:     "not_started",
				FocusMember: enabled,
				FocusPaused: paused,
			}
			if paused {
				ticket.LatestCheckpoint = &storage.PauseCheckpoint{NextAction: fmt.Sprintf("continue ticket %d", ti)}
			}
			columns[ci].Tickets[ti] = ticket
		}
	}
	m := Model{
		renderCache: newRenderCache(),
		view:        storage.BoardView{Board: storage.Board{ID: 1, Name: "Focus performance"}, Columns: columns},
		focus:       storage.FocusStatus{Enabled: enabled, Limit: focusPerformanceTickets, Used: focusPerformanceTickets / 2, WorkflowKeys: keys},
		width:       width,
		height:      height,
		col:         0,
		card:        focusPerformanceCard,
		colScroll:   make([]int, focusPerformanceColumns),
	}
	m.colScroll[0] = focusPerformanceCard
	return m
}

func resetFocusNavigation(m *Model, key tea.KeyMsg) {
	for i := range m.colScroll {
		m.colScroll[i] = 0
	}
	m.card = focusPerformanceCard
	if key.String() == "h" || key.String() == "left" {
		m.col = 1
		m.colScroll[1] = focusPerformanceCard
	} else {
		m.col = 0
		m.colScroll[0] = focusPerformanceCard
	}
	m.colOffset = 0
}

func measureFocusNavigation(t *testing.T, label string, count int, operation func()) {
	t.Helper()
	times := make([]int64, count)
	for i := range times {
		start := time.Now()
		operation()
		times[i] = time.Since(start).Nanoseconds()
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	q := func(p int) float64 { return float64(times[(count*p+99)/100-1]) / 1e6 }
	t.Logf("PERF focus-%s n=%d p50=%.3fms p95=%.3fms p99=%.3fms", label, count, q(50), q(95), q(99))
}

type focusProgramActions struct {
	Actions
	started chan struct{}
	release chan struct{}
}

func (a *focusProgramActions) RefreshRuntime(ctx context.Context) error {
	close(a.started)
	select {
	case <-a.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type focusReceiptReader struct {
	io.Reader
	at atomic.Int64
}

func (r *focusReceiptReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.at.Store(time.Now().UnixNano())
	}
	return n, err
}

type focusOutputProbe struct {
	mu          sync.Mutex
	receipt     *focusReceiptReader
	initialText string
	changedText string
	initial     chan struct{}
	ready       chan int64
	initialOnce sync.Once
	data        strings.Builder
}

func (w *focusOutputProbe) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data.Write(p)
	plain := ansiStrip(w.data.String())
	if strings.Contains(plain, w.initialText) {
		w.initialOnce.Do(func() { close(w.initial) })
	}
	if at := w.receipt.at.Load(); at != 0 && strings.Contains(plain, w.changedText) {
		select {
		case w.ready <- time.Now().UnixNano() - at:
		default:
		}
	}
	return len(p), nil
}

func runFocusProgramTrial(t *testing.T, enabled bool, width, height int, keyName, inputBytes string) int64 {
	t.Helper()
	store, ctx := newTestStore(t)
	actions := &focusProgramActions{Actions: NewService(store, nil), started: make(chan struct{}), release: make(chan struct{})}
	m := focusPerformanceModel(enabled, width, height)
	m.ctx = ctx
	m.actions = actions
	m.refreshWorker = newRuntimeRefreshWorker(ctx)
	m.statusBarResults = make(map[string]statusbar.ModuleResult)
	m.statusBarCtx, m.statusBarCancel = context.WithCancel(ctx)
	m.statusBar = statusbar.DefaultConfig()
	resetFocusNavigation(&m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyName[:1])})
	if keyName == "left" {
		resetFocusNavigation(&m, tea.KeyMsg{Type: tea.KeyLeft})
	} else if keyName == "right" {
		resetFocusNavigation(&m, tea.KeyMsg{Type: tea.KeyRight})
	}
	sourceCol, targetCol := 0, 1
	if keyName == "h" || keyName == "left" {
		sourceCol, targetCol = 1, 0
	}
	input, feed := io.Pipe()
	receipt := &focusReceiptReader{Reader: input}
	output := &focusOutputProbe{
		receipt: receipt, initialText: fmt.Sprintf("> C%d-%04d", sourceCol, focusPerformanceCard),
		changedText: fmt.Sprintf("> C%d-%04d", targetCol, focusPerformanceCard), initial: make(chan struct{}), ready: make(chan int64, 1),
	}
	parent, cancel := context.WithCancel(ctx)
	p := tea.NewProgram(m, tea.WithContext(parent), tea.WithInput(receipt), tea.WithOutput(output), tea.WithoutSignalHandler(), tea.WithFPS(120))
	done := make(chan struct{})
	go func() { defer close(done); _, _ = p.Run() }()
	select {
	case <-output.initial:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("initial Focus frame did not reach writer")
	}
	p.Send(runtimeTickMsg(time.Now()))
	select {
	case <-actions.started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("background observation did not start")
	}
	if _, err := feed.Write([]byte(inputBytes)); err != nil {
		t.Fatal(err)
	}
	var latency int64
	select {
	case latency = <-output.ready:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Focus navigation frame did not reach writer")
	}
	close(actions.release)
	cancel()
	_ = feed.Close()
	<-done
	m.Close()
	return latency
}
