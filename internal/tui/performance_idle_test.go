//go:build linux || darwin

package tui

import (
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type idleFrameModel struct{ frame string }

func (m idleFrameModel) Init() tea.Cmd                       { return nil }
func (m idleFrameModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m idleFrameModel) View() string                        { return m.frame }

type idleOutput struct {
	mu            sync.Mutex
	bytes, writes int64
}

func (w *idleOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bytes += int64(len(p))
	w.writes++
	return len(p), nil
}
func (w *idleOutput) counts() (int64, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytes, w.writes
}
func processCPU(t *testing.T) int64 {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	return usage.Utime.Sec*1e6 + int64(usage.Utime.Usec) + usage.Stime.Sec*1e6 + int64(usage.Stime.Usec)
}

// Measure idle renderer wakeups separately from periodic observation and actual
// terminal painting. Run without other tests in the same process for timings.
func TestPerformanceRendererIdle(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("opt-in idle CPU measurement")
	}
	m, _, _ := newTestModel(t)
	m.width, m.height = 160, 40
	defer func() {
		if closer, ok := any(m).(interface{ Close() }); ok {
			closer.Close()
		}
	}()
	frame := m.View()
	if !strings.Contains(frame, "Kanbi") {
		t.Fatal("missing board frame")
	}
	for _, fps := range []int{60, 120} {
		out := &idleOutput{}
		p := tea.NewProgram(idleFrameModel{frame: frame}, tea.WithInput(nil), tea.WithOutput(out), tea.WithFPS(fps), tea.WithoutSignalHandler())
		done := make(chan error, 1)
		go func() { _, err := p.Run(); done <- err }()
		p.Send(tea.WindowSizeMsg{Width: 160, Height: 40})
		time.Sleep(100 * time.Millisecond)
		bytes, writes := out.counts()
		cpu := processCPU(t)
		at := time.Now()
		time.Sleep(1500 * time.Millisecond)
		elapsed := time.Since(at)
		used := processCPU(t) - cpu
		afterBytes, afterWrites := out.counts()
		p.Quit()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if afterBytes != bytes || afterWrites != writes {
			t.Fatal("idle renderer emitted extra output")
		}
		t.Logf("PERF idle-renderer maximum=%dFPS elapsed=%.3fs processCPU=%.3fms corePercent=%.3f idleBytes=0 idleWrites=0", fps, elapsed.Seconds(), float64(used)/1000, float64(used)/elapsed.Seconds()/1e4)
	}
}
