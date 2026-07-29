package tui

import (
	"testing"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestRuntimeStateConsumersUseCanonicalCatalogue(t *testing.T) {
	infos := kanban.RuntimeStates()
	if len(infos) == 0 {
		t.Fatal("runtime catalogue is empty")
	}

	filterable := kanban.FilterableRuntimeStates()
	manual := kanban.ManuallySettableRuntimeStates()
	assertRuntimeStatesEqual(t, filterable, masterRuntimeOptions())
	assertRuntimeStatesEqual(t, manual, manualStates)

	for _, info := range infos {
		ticket := storage.Ticket{Runtime: info.State}
		if got := runtimeLabel(ticket); got != info.CompactLabel {
			t.Errorf("card label for %q = %q, want %q", info.State, got, info.CompactLabel)
		}
		if got := inspectorStatusLabel(ticket); got != info.ExpandedLabel {
			t.Errorf("inspector label for %q = %q, want %q", info.State, got, info.ExpandedLabel)
		}
		if got := isAttention(info.State); got != info.NeedsAttention {
			t.Errorf("attention for %q = %v, want %v", info.State, got, info.NeedsAttention)
		}
	}
}

func TestRuntimeStateUnknownFallbacksRemainMeaningful(t *testing.T) {
	ticket := storage.Ticket{Runtime: "provider_future_state"}
	if got := runtimeLabel(ticket); got != "unknown" {
		t.Fatalf("card unknown label = %q", got)
	}
	if got := inspectorStatusLabel(ticket); got != "provider future state" {
		t.Fatalf("inspector unknown label = %q", got)
	}
}

func assertRuntimeStatesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("runtime states = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runtime states = %#v, want %#v", got, want)
		}
	}
}
