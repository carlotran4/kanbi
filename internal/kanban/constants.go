package kanban

const (
	StateNotStarted      = "not_started"
	StateStarting        = "starting"
	StateRunning         = "running"
	StateWaitingForUser  = "waiting_for_user"
	StateNeedsPermission = "needs_permission"
	StateIdleUnknown     = "idle_unknown"
	StateClosing         = "closing"
	StateClosed          = "closed"
	StateExited          = "exited"
	StateError           = "error"
	StateRepairNeeded    = "repair_needed"
)

type DefaultColumn struct {
	Name     string
	Position int
}

var defaultColumnNames = [...]string{"Open", "In Progress", "Review", "Done"}

func DefaultColumns() []DefaultColumn {
	columns := make([]DefaultColumn, 0, len(defaultColumnNames))
	for i, name := range defaultColumnNames {
		columns = append(columns, DefaultColumn{Name: name, Position: i})
	}
	return columns
}

func DefaultColumnNames() []string {
	names := make([]string, len(defaultColumnNames))
	copy(names, defaultColumnNames[:])
	return names
}
