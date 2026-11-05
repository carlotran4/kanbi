//go:build linux || darwin

package tui

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/statusbar"
	tea "github.com/charmbracelet/bubbletea"
)

type statusBarPerformanceModel struct {
	Model
	results *atomic.Int64
	returns *atomic.Int64
}

func (m statusBarPerformanceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(statusBarResultMsg); ok {
		m.results.Add(1)
	}
	next, cmd := m.Model.Update(msg)
	m.Model = next.(Model)
	if result, ok := msg.(fleetResult); ok && result.operation == "return" {
		m.returns.Add(1)
	}
	return m, cmd
}

// Controlled scheduler reproduction: real Bubble Tea loop, SQLite projection,
// custom subprocess and renderer; Fleet return completions are injected, so no
// real agent or Herdr process is involved. CPU excludes command children.
func TestPerformanceStatusBarLifetime(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("opt-in status refresh lifetime measurement")
	}
	seconds := 3
	if value := os.Getenv("KANBI_STATUS_SOAK_SECONDS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			t.Fatal("KANBI_STATUS_SOAK_SECONDS must be positive")
		}
		seconds = n
	}
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("navigation=%t", active), func(t *testing.T) {
			m, store, ctx := newTestModel(t)
			defer m.Close()
			view := defaultBoardView(t, ctx, store)
			for i := 0; i < 50; i++ {
				createTicket(t, ctx, store, view.Columns[0].ID, fmt.Sprintf("Work %d", i), "representative body", "pi")
			}
			m.reload()
			m.width, m.height = 160, 40
			m.statusBar = statusbar.Config{Left: "$kanbi $board ${custom.sample}", Custom: map[string]statusbar.CustomModule{"sample": {Command: []string{"printf", "sample"}, RefreshInterval: "100ms"}}}
			if err := statusbar.Normalize(&m.statusBar); err != nil {
				t.Fatal(err)
			}
			m.saveFleetView()
			var results, returns atomic.Int64
			out := &idleOutput{}
			p := tea.NewProgram(statusBarPerformanceModel{Model: m, results: &results, returns: &returns}, tea.WithInput(nil), tea.WithOutput(out), tea.WithFPS(120), tea.WithoutSignalHandler())
			done := make(chan error, 1)
			go func() { _, err := p.Run(); done <- err }()
			defer func() {
				p.Quit()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			p.Send(tea.WindowSizeMsg{Width: 160, Height: 40})
			time.Sleep(200 * time.Millisecond)
			for _, visits := range []int{0, 40} {
				for i := 0; i < visits; i++ {
					p.Send(fleetResult{operation: "return"})
				}
				deadline := time.Now().Add(5 * time.Second)
				for returns.Load() < int64(visits) && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if returns.Load() != int64(visits) {
					t.Fatal("return messages not drained")
				}
				time.Sleep(200 * time.Millisecond)
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				cpu, count := processCPU(t), results.Load()
				goroutines := runtime.NumGoroutine()
				at := time.Now()
				until := at.Add(time.Duration(seconds) * time.Second)
				for time.Now().Before(until) {
					if active {
						p.Send(fleetKey("j"))
					}
					time.Sleep(100 * time.Millisecond)
				}
				elapsed := time.Since(at)
				used := processCPU(t) - cpu
				runtime.ReadMemStats(&after)
				t.Logf("PERF visits=%d elapsed=%.3fs processCPU=%.3fms corePercent=%.2f results=%d allocated=%d GC=%d goroutines=%d->%d heap=%d->%d", visits, elapsed.Seconds(), float64(used)/1000, float64(used)/elapsed.Seconds()/1e4, results.Load()-count, after.TotalAlloc-before.TotalAlloc, after.NumGC-before.NumGC, goroutines, runtime.NumGoroutine(), before.HeapAlloc, after.HeapAlloc)
			}
		})
	}
}
