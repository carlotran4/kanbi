package kanban

import "testing"

func TestRuntimeStateCatalogueIsCompleteAndOrdered(t *testing.T) {
	want := []string{
		StateNotStarted,
		StateStarting,
		StateRunning,
		StateWaitingForUser,
		StateNeedsPermission,
		StateIdleUnknown,
		StateClosing,
		StateClosed,
		StateExited,
		StateRepairNeeded,
		StateError,
	}
	states := RuntimeStates()
	if len(states) != len(want) {
		t.Fatalf("catalogue has %d states, want %d", len(states), len(want))
	}
	seen := make(map[string]bool, len(states))
	for i, info := range states {
		if info.State != want[i] {
			t.Fatalf("catalogue[%d]=%q, want %q", i, info.State, want[i])
		}
		if seen[info.State] || info.CompactLabel == "" || info.ExpandedLabel == "" || info.Tone == "" {
			t.Fatalf("invalid runtime metadata: %+v", info)
		}
		seen[info.State] = true
		got, ok := RuntimeStateFor(info.State)
		if !ok || got != info {
			t.Fatalf("lookup %q = %+v, %v; want %+v", info.State, got, ok, info)
		}
	}
	if got, ok := RuntimeStateFor(""); !ok || got.State != StateNotStarted {
		t.Fatalf("empty runtime lookup = %+v, %v; want not_started", got, ok)
	}
}

func TestRuntimeStateDerivedListsFollowCatalogue(t *testing.T) {
	states := RuntimeStates()
	var filterable, manual []string
	for _, info := range states {
		if info.Filterable {
			filterable = append(filterable, info.State)
		}
		if info.ManuallySettable {
			manual = append(manual, info.State)
		}
		if got := NeedsAttention(info.State); got != info.NeedsAttention {
			t.Fatalf("NeedsAttention(%q)=%v, want %v", info.State, got, info.NeedsAttention)
		}
	}
	assertStringsEqual(t, FilterableRuntimeStates(), filterable)
	assertStringsEqual(t, ManuallySettableRuntimeStates(), manual)
}

func assertStringsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}
