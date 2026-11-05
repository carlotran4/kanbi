package tui

import (
	"errors"
	"testing"

	"github.com/carlotran4/kanbi/internal/statusbar"
)

func TestFleetReturnRetiresStatusRefreshChains(t *testing.T) {
	m, _, _ := newTestModel(t)
	defer m.Close()
	m.statusBar = statusbar.Config{Left: "$kanbi ${custom.usage}", Custom: map[string]statusbar.CustomModule{"usage": {Command: []string{"printf", "current"}}}}
	if err := statusbar.Normalize(&m.statusBar); err != nil {
		t.Fatal(err)
	}
	m.saveFleetView()
	// Repeated visits must retire both queued timers and in-flight results.
	// No wall-clock sleeps or subprocesses: deliver the delayed messages directly.
	for visit := 0; visit < 1000; visit++ {
		old := m.statusBarGeneration
		m.statusBarResults["usage"] = statusbar.ModuleResult{Value: "cached"}
		m.fleet.active = true
		m, _ = fleetApply(t, m, fleetResult{operation: "return"})
		if m.statusBarGeneration <= old {
			t.Fatalf("visit %d retained old refresh generation", visit)
		}
		if got := m.statusBarResults["usage"].Value; got != "cached" {
			t.Fatalf("return lost cached value: %q", got)
		}
		// Use update to inspect the status command itself, without Update's outer Batch.
		next, cmd := m.update(statusBarRefreshMsg{name: "usage", generation: old})
		m = next.(Model)
		if cmd != nil {
			t.Fatalf("visit %d restarted an obsolete timer", visit)
		}
		next, cmd = m.update(statusBarResultMsg{name: "usage", generation: old, value: "stale"})
		m = next.(Model)
		if cmd != nil || m.statusBarResults["usage"].Value != "cached" {
			t.Fatalf("visit %d accepted an obsolete result", visit)
		}
		next, cmd = m.update(statusBarResultMsg{name: "usage", generation: m.statusBarGeneration, value: "current"})
		m = next.(Model)
		if cmd == nil || m.statusBarResults["usage"].Value != "current" {
			t.Fatal("current refresh chain did not continue")
		}
	}
}

func TestFailedFleetReturnKeepsStatusRefreshGeneration(t *testing.T) {
	m, _, _ := newTestModel(t)
	defer m.Close()
	m.fleet.active = true
	old := m.statusBarGeneration
	m, _ = fleetApply(t, m, fleetResult{operation: "return", err: errors.New("return failed")})
	if m.statusBarGeneration != old {
		t.Fatal("failed return retired the ongoing refresh chain")
	}
}
