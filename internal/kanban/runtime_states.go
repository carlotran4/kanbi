package kanban

// RuntimeTone is a semantic presentation category. Presentation layers map it
// to their own colors or emphasis without duplicating state-specific rules.
type RuntimeTone string

const (
	RuntimeToneMuted   RuntimeTone = "muted"
	RuntimeToneSuccess RuntimeTone = "success"
	RuntimeToneWarning RuntimeTone = "warning"
	RuntimeToneError   RuntimeTone = "error"
)

// RuntimeStateInfo defines the stable semantics shared by runtime consumers.
// CompactLabel is for cards; ExpandedLabel is for inspectors and prose.
type RuntimeStateInfo struct {
	State            string
	CompactLabel     string
	ExpandedLabel    string
	Filterable       bool
	ManuallySettable bool
	Tone             RuntimeTone
	NeedsAttention   bool
}

var runtimeStates = [...]RuntimeStateInfo{
	{State: StateNotStarted, CompactLabel: "not started", ExpandedLabel: "not started", Filterable: true, Tone: RuntimeToneMuted},
	{State: StateStarting, CompactLabel: "starting", ExpandedLabel: "starting", Filterable: true, Tone: RuntimeToneMuted},
	{State: StateRunning, CompactLabel: "running", ExpandedLabel: "running", Filterable: true, ManuallySettable: true, Tone: RuntimeToneSuccess},
	{State: StateWaitingForUser, CompactLabel: "waiting", ExpandedLabel: "waiting for user", Filterable: true, ManuallySettable: true, Tone: RuntimeToneWarning, NeedsAttention: true},
	{State: StateNeedsPermission, CompactLabel: "permission", ExpandedLabel: "needs permission", Filterable: true, Tone: RuntimeToneError, NeedsAttention: true},
	{State: StateIdleUnknown, CompactLabel: "idle", ExpandedLabel: "idle unknown", Filterable: true, ManuallySettable: true, Tone: RuntimeToneMuted},
	{State: StateClosing, CompactLabel: "closing", ExpandedLabel: "closing", Filterable: true, Tone: RuntimeToneMuted},
	{State: StateClosed, CompactLabel: "closed", ExpandedLabel: "closed", Filterable: true, Tone: RuntimeToneMuted},
	{State: StateExited, CompactLabel: "exited", ExpandedLabel: "exited", Filterable: true, Tone: RuntimeToneMuted},
	{State: StateRepairNeeded, CompactLabel: "repair", ExpandedLabel: "repair needed", Filterable: true, Tone: RuntimeToneError},
	{State: StateError, CompactLabel: "error", ExpandedLabel: "error", Filterable: true, ManuallySettable: true, Tone: RuntimeToneError, NeedsAttention: true},
}

// RuntimeStates returns the ordered catalogue. Its order is the stable filter
// and manual-state-menu order.
func RuntimeStates() []RuntimeStateInfo {
	states := make([]RuntimeStateInfo, len(runtimeStates))
	copy(states, runtimeStates[:])
	return states
}

// RuntimeStateFor returns the semantic metadata for state. An empty persisted
// runtime is the legacy representation of not-started.
func RuntimeStateFor(state string) (RuntimeStateInfo, bool) {
	if state == "" {
		state = StateNotStarted
	}
	for _, info := range runtimeStates {
		if info.State == state {
			return info, true
		}
	}
	return RuntimeStateInfo{}, false
}

func FilterableRuntimeStates() []string {
	return runtimeStateIDs(func(info RuntimeStateInfo) bool { return info.Filterable })
}

func ManuallySettableRuntimeStates() []string {
	return runtimeStateIDs(func(info RuntimeStateInfo) bool { return info.ManuallySettable })
}

func NeedsAttention(state string) bool {
	info, ok := RuntimeStateFor(state)
	return ok && info.NeedsAttention
}

func runtimeStateIDs(include func(RuntimeStateInfo) bool) []string {
	states := make([]string, 0, len(runtimeStates))
	for _, info := range runtimeStates {
		if include(info) {
			states = append(states, info.State)
		}
	}
	return states
}
