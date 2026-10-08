package tui

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/tmux"
)

// Opt-in measurements use identical source on the baseline and candidate.
// These are CPU/service measurements, not physical terminal presentation times.
func TestPerformanceEvidence(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("set KANBI_PERFORMANCE=1 for reproducible measurements")
	}
	t.Logf("reference Go=%s OS=%s arch=%s CPUs=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	for _, hidden := range []int{0, 2000} {
		t.Run(fmt.Sprintf("hidden=%d", hidden), func(t *testing.T) {
			store, ctx := newTestStore(t)
			view := defaultBoardView(t, ctx, store)
			for i := 0; i < 30; i++ {
				createTicket(t, ctx, store, view.Columns[i%len(view.Columns)].ID, fmt.Sprintf("Visible ticket %d", i), strings.Repeat("normal body ", 90), "pi")
			}
			if hidden > 0 {
				board, err := store.CreateBoardWithWorkdir(ctx, "Hidden", t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				hiddenView, err := store.BoardViewByID(ctx, board.ID)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < hidden; i++ {
					createTicket(t, ctx, store, hiddenView.Columns[0].ID, fmt.Sprintf("Hidden %d", i), strings.Repeat("body ", 1640), "pi")
				}
			}
			manager := tmux.NewManager(config.Defaults(config.Paths{}), store)
			defer manager.Close()
			service := NewService(store, manager)
			m := New(ctx, service)
			for _, size := range [][2]int{{80, 24}, {160, 40}} {
				m.width, m.height = size[0], size[1]
				m.syncScrollDimensions()
				m.vScrollFollow()
				m.card, m.colScroll[m.col] = 0, 0
				measurePerformance(t, fmt.Sprintf("stable-view-%dx%d", size[0], size[1]), 200, func() { _ = m.View() })
				direction := 1
				measurePerformance(t, fmt.Sprintf("navigation-%dx%d", size[0], size[1]), 200, func() {
					if m.card >= len(m.view.Columns[m.col].Tickets)-1 {
						direction = -1
					}
					if m.card == 0 {
						direction = 1
					}
					key := 'j'
					if direction < 0 {
						key = 'k'
					}
					updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
					m = updated.(Model)
					_ = m.View()
				})
			}
			measurePerformance(t, "refresh-and-projection", 100, func() {
				if err := service.RefreshRuntime(ctx); err != nil {
					t.Fatal(err)
				}
				m.reload()
			})
			m.masterBoard = true
			measurePerformance(t, "master-projection", 40, func() { m.reload() })
		})
	}
	for _, bodyBytes := range []int{1024, 65536} {
		t.Run(fmt.Sprintf("body=%d", bodyBytes), func(t *testing.T) {
			m, store, ctx := newTestModel(t)
			view := defaultBoardView(t, ctx, store)
			for i := 0; i < 10; i++ {
				createTicket(t, ctx, store, view.Columns[0].ID, fmt.Sprintf("Long body %d", i), strings.Repeat("markdown **body** ", bodyBytes/18), "pi")
			}
			m.reload()
			m.width, m.height = 160, 40
			measurePerformance(t, "long-body-navigation", 200, func() {
				m.moveCard(1)
				if m.card == 9 {
					m.card = 0
				}
				_ = m.View()
			})
		})
	}
	t.Run("notes=50", func(t *testing.T) {
		m, _, _ := newTestModel(t)
		for i := 0; i < 50; i++ {
			m.notes = append(m.notes, storage.Note{ID: int64(i + 1), Body: fmt.Sprintf("Note %d: ", i) + strings.Repeat("note **markdown** ", 60), CreatedAt: time.Now(), UpdatedAt: time.Now()})
		}
		measurePerformance(t, "notes-thread", 200, func() { m.noteIndex = (m.noteIndex + 1) % len(m.notes); _ = m.notesThreadView(80) })
	})
}

func measurePerformance(t *testing.T, name string, count int, operation func()) {
	t.Helper()
	for i := 0; i < 3; i++ {
		operation()
	}
	times := make([]int64, count)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range times {
		start := time.Now()
		operation()
		times[i] = time.Since(start).Nanoseconds()
	}
	runtime.ReadMemStats(&after)
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	q := func(p int) float64 { return float64(times[(count*p+99)/100-1]) / 1e6 }
	t.Logf("PERF %s n=%d p50=%.3fms p95=%.3fms p99=%.3fms bytes/op=%d allocs/op=%d", name, count, q(50), q(95), q(99), (after.TotalAlloc-before.TotalAlloc)/uint64(count), (after.Mallocs-before.Mallocs)/uint64(count))
}
