package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// This drives the real Bubble Tea input/update/renderer loop in production
// inline mode. The writer is a measurement sink, not a physical terminal.
// Observation delay is deliberately simulated and labeled separately from the
// real-tmux fixture exercised by scripts/performance-ui.py.
func TestPerformanceProgram(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("opt-in input-to-write measurements")
	}
	latencies := make([]int64, 50)
	var totalBytes, totalWrites int64
	for i := range latencies {
		store, ctx := newTestStore(t)
		view := defaultBoardView(t, ctx, store)
		createTicket(t, ctx, store, view.Columns[0].ID, "first", "body", "pi")
		createTicket(t, ctx, store, view.Columns[0].ID, "second", "body", "pi")
		parent, cancel := context.WithCancel(ctx)
		actions := &programDelayActions{Actions: NewService(store, nil), started: make(chan struct{})}
		m := New(parent, actions)
		m.width, m.height = 160, 40
		input, feed := io.Pipe()
		receipt := &programReceiptReader{Reader: input}
		output := &programOutputProbe{receipt: receipt, ready: make(chan int64, 1)}
		p := tea.NewProgram(m, tea.WithContext(parent), tea.WithInput(receipt), tea.WithOutput(output), tea.WithoutSignalHandler())
		done := make(chan struct{})
		go func() { defer close(done); _, _ = p.Run() }()
		p.Send(runtimeTickMsg(time.Now()))
		select {
		case <-actions.started:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("observer did not start")
		}
		if _, err := feed.Write([]byte("j")); err != nil {
			t.Fatal(err)
		}
		select {
		case latencies[i] = <-output.ready:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("navigation frame did not reach writer")
		}
		cancel()
		_ = feed.Close()
		<-done
		if closer, ok := any(m).(interface{ Close() }); ok {
			closer.Close()
		}
		output.mu.Lock()
		totalBytes += output.bytes
		totalWrites += output.writes
		output.mu.Unlock()
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	q := func(p int) float64 { return float64(latencies[(len(latencies)*p+99)/100-1]) / 1e6 }
	t.Logf("PERF program-receipt-to-write-simulated-300ms-observation inline/60FPS n=%d p50=%.3fms p95=%.3fms p99=%.3fms bytes/trial=%d writes/trial=%.1f", len(latencies), q(50), q(95), q(99), totalBytes/int64(len(latencies)), float64(totalWrites)/float64(len(latencies)))
	// The baseline intentionally exceeds this goal. Opt-in enforcement is for
	// the candidate, so the same measurement source works on the old commit.
	if os.Getenv("KANBI_PERFORMANCE_ENFORCE") == "1" && q(95) >= 50 {
		t.Fatal(fmt.Sprintf("input-to-write p95 %.3fms exceeds 50ms", q(95)))
	}
}

type programDelayActions struct {
	Actions
	started chan struct{}
}

func (a *programDelayActions) RefreshRuntime(ctx context.Context) error {
	close(a.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

type programReceiptReader struct {
	io.Reader
	at atomic.Int64
}

func (r *programReceiptReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.at.Store(time.Now().UnixNano())
	}
	return n, err
}

type programOutputProbe struct {
	mu            sync.Mutex
	receipt       *programReceiptReader
	ready         chan int64
	data          strings.Builder
	bytes, writes int64
}

func (w *programOutputProbe) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bytes += int64(len(p))
	w.writes++
	w.data.Write(p)
	if at := w.receipt.at.Load(); at != 0 && strings.Contains(ansiStrip(w.data.String()), "> T-002") {
		select {
		case w.ready <- time.Now().UnixNano() - at:
		default:
		}
	}
	return len(p), nil
}
