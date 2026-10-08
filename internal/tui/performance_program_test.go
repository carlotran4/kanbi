package tui

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
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
	fps := 120
	if setting := os.Getenv("KANBI_PERFORMANCE_FPS"); setting != "" {
		value, err := strconv.Atoi(setting)
		if err != nil || value < 1 || value > 120 {
			t.Fatal("KANBI_PERFORMANCE_FPS must be 1..120")
		}
		fps = value
	}
	for _, scenario := range []string{"simulated-300ms-observation", "actual-300ms-SQLite-writer"} {
		t.Run(scenario, func(t *testing.T) {
			latencies := make([]int64, 50)
			var totalBytes, totalWrites int64
			for i := range latencies {
				var store *storage.Store
				var ctx context.Context
				var lock *sql.Conn
				if scenario == "actual-300ms-SQLite-writer" {
					ctx = context.Background()
					path := filepath.Join(t.TempDir(), "latency.db")
					var err error
					store, err = storage.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = store.Close() })
					if err := store.Init(ctx); err != nil {
						t.Fatal(err)
					}
					dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_busy_timeout=5000"
					locker, err := sql.Open("sqlite3", dsn)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = locker.Close() })
					lock, err = locker.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					store, ctx = newTestStore(t)
				}
				view := defaultBoardView(t, ctx, store)
				createTicket(t, ctx, store, view.Columns[0].ID, "first", "body", "pi")
				createTicket(t, ctx, store, view.Columns[0].ID, "second", "body", "pi")
				parent, cancel := context.WithCancel(ctx)
				actions := &programDelayActions{Actions: NewService(store, nil), started: make(chan struct{}), writeTicket: scenario == "actual-300ms-SQLite-writer"}
				m := New(parent, actions)
				m.width, m.height = 160, 40
				if lock != nil {
					if _, err := lock.ExecContext(ctx, "begin immediate"); err != nil {
						t.Fatal(err)
					}
				}
				input, feed := io.Pipe()
				receipt := &programReceiptReader{Reader: input}
				output := &programOutputProbe{receipt: receipt, ready: make(chan int64, 1), initial: make(chan struct{})}
				p := tea.NewProgram(m, tea.WithContext(parent), tea.WithInput(receipt), tea.WithOutput(output), tea.WithoutSignalHandler(), tea.WithFPS(fps))
				done := make(chan struct{})
				go func() { defer close(done); _, _ = p.Run() }()
				select {
				case <-output.initial:
				case <-time.After(3 * time.Second):
					cancel()
					t.Fatal("initial board did not reach writer")
				}
				p.Send(runtimeTickMsg(time.Now()))
				select {
				case <-actions.started:
				case <-time.After(3 * time.Second):
					cancel()
					t.Fatal("observer did not start")
				}
				var unlock chan struct{}
				if lock != nil {
					unlock = make(chan struct{})
					time.AfterFunc(300*time.Millisecond, func() {
						_, _ = lock.ExecContext(ctx, "commit")
						_ = lock.Close()
						close(unlock)
					})
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
				output.mu.Lock()
				idleWrites := output.writes
				output.mu.Unlock()
				minute := time.Now().Format("15:04")
				for range 5 {
					p.Send(struct{}{})
				}
				time.Sleep(50 * time.Millisecond)
				output.mu.Lock()
				idleUnchanged := output.writes == idleWrites || minute != time.Now().Format("15:04")
				output.mu.Unlock()
				cancel()
				_ = feed.Close()
				<-done
				if closer, ok := any(m).(interface{ Close() }); ok {
					closer.Close()
				}
				if unlock != nil {
					<-unlock
				}
				output.mu.Lock()
				totalBytes += output.bytes
				totalWrites += output.writes
				output.mu.Unlock()
				if !idleUnchanged {
					t.Fatal("unchanged frames emitted extra redraw traffic")
				}
			}
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			q := func(p int) float64 { return float64(latencies[(len(latencies)*p+99)/100-1]) / 1e6 }
			t.Logf("PERF program-receipt-to-write-%s inline/%dFPS n=%d p50=%.3fms p95=%.3fms p99=%.3fms bytes/trial=%d writes/trial=%.1f", scenario, fps, len(latencies), q(50), q(95), q(99), totalBytes/int64(len(latencies)), float64(totalWrites)/float64(len(latencies)))
			// The baseline intentionally exceeds this goal. Opt-in enforcement is for
			// the candidate, so the same measurement source works on the old commit.
			if os.Getenv("KANBI_PERFORMANCE_ENFORCE") == "1" && q(95) >= 50 {
				t.Fatal(fmt.Sprintf("input-to-write p95 %.3fms exceeds 50ms", q(95)))
			}
		})
	}
}

type programDelayActions struct {
	Actions
	started     chan struct{}
	writeTicket bool
}

func (a *programDelayActions) RefreshRuntime(ctx context.Context) error {
	close(a.started)
	if a.writeTicket {
		view, err := a.Actions.BoardView(ctx)
		if err != nil {
			return err
		}
		ticket := view.Columns[0].Tickets[0]
		return a.Actions.UpdateTicket(ctx, ticket.ID, ticket.Title, ticket.Body, ticket.Harness)
	}
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
	initial       chan struct{}
	initialOnce   sync.Once
	data          strings.Builder
	bytes, writes int64
}

func (w *programOutputProbe) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bytes += int64(len(p))
	w.writes++
	w.data.Write(p)
	plain := ansiStrip(w.data.String())
	if strings.Contains(plain, "> T-001") {
		w.initialOnce.Do(func() { close(w.initial) })
	}
	if at := w.receipt.at.Load(); at != 0 && strings.Contains(plain, "> T-002") {
		select {
		case w.ready <- time.Now().UnixNano() - at:
		default:
		}
	}
	return len(p), nil
}
