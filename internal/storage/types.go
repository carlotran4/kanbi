package storage

import (
	"database/sql"
	"errors"
	"time"
)

var ErrActiveSessionExists = errors.New("ticket already has an active session")

var ErrTicketHasActiveSession = errors.New("cannot archive ticket with an active session; close it first")

var ErrBoardHasCurrentWorkspaces = errors.New("board has current ticket workspaces; integrate or repair them first")

const (
	// WorktreeModeOff keeps launches in the shared board working directory.
	WorktreeModeOff = "off"
	// WorktreeModeGit creates isolated Git worktrees per ticket workspace.
	WorktreeModeGit = "git"

	WorkspaceKindGitWorktree = "git_worktree"

	WorkspaceStateProvisioning = "provisioning"
	WorkspaceStateReady        = "ready"
	WorkspaceStateResolving    = "resolving"
	WorkspaceStateRepairNeeded = "repair_needed"
	WorkspaceStateIntegrated   = "integrated"
	WorkspaceStateRetired      = "retired"
	WorkspaceStateCleanupReq   = "cleanup_required"
)

type Board struct {
	ID               int64
	Name             string
	UUID             string
	Workdir          string
	TicketBackend    string
	BackendQuery     string
	BackendConfig    string
	LastSyncAt       sql.NullTime
	LastSyncError    sql.NullString
	ArchivedAt       sql.NullTime
	SyncEnabled      bool
	SourceExportUUID sql.NullString
	// WorktreeMode is off or git. Existing boards default to off.
	WorktreeMode string
}

// ColumnView is a query model for a board column and its projected tickets.
// It is not a direct representation of a columns table row.
type ColumnView struct {
	ID          int64
	BoardID     int64
	Name        string
	WorkflowKey string
	Position    int
	Tickets     []Ticket
}

// Column is retained as a compatibility name for the column query model.
type Column = ColumnView

// TicketProjection is the read model returned by ticket and board queries. It
// combines durable ticket fields with board metadata, the latest session, its
// observed runtime state, and the ticket's note count.
//
// It is intentionally not a direct representation of a tickets table row.
type TicketProjection struct {
	ID                    int64
	BoardID               int64
	BoardName             string
	BoardUUID             string
	BoardWorkdir          string
	BoardWorktreeMode     string
	ColumnID              int64
	ExternalID            sql.NullString
	ExternalURL           sql.NullString
	ExternalUpdatedAt     sql.NullTime
	SyncVersion           sql.NullString
	DisplayID             string
	DisplayNum            int
	Title                 string
	Body                  string
	Harness               string
	Position              int
	ArchivedAt            sql.NullTime
	RemotePushState       sql.NullString
	RemotePushToken       sql.NullString
	RemotePushAttemptedAt sql.NullTime
	Runtime               string
	SessionActive         bool
	TmuxSessionName       sql.NullString
	WindowID              sql.NullString
	WindowName            sql.NullString
	Multiplexer           sql.NullString
	MuxNamespace          sql.NullString
	MuxContainerID        sql.NullString
	MuxContainerName      sql.NullString
	MuxMetadata           sql.NullString
	SessionID             sql.NullInt64
	SessionRef            sql.NullString
	SessionWorkspaceID    sql.NullInt64
	SessionLaunchCWD      sql.NullString
	LastOutputAt          sql.NullTime
	LastStateChangeAt     sql.NullTime
	LastDetectedState     sql.NullString
	LastAttentionReason   sql.NullString
	LastDetectionSource   sql.NullString
	LastObservedExcerpt   sql.NullString
	// Current workspace projection (nullable when no unfinished workspace).
	WorkspaceID           sql.NullInt64
	WorkspaceState        sql.NullString
	WorkspaceBranch       sql.NullString
	WorkspaceSourceBranch sql.NullString
	WorkspaceLaunchCWD    sql.NullString
	WorkspaceStatusJSON   sql.NullString
	WorkspaceLastError    sql.NullString
	NoteCount             int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Ticket is retained as a compatibility name for the ticket query model.
type Ticket = TicketProjection

type Note struct {
	ID                int64
	TicketID          int64
	ExternalID        sql.NullString
	ExternalUpdatedAt sql.NullTime
	SyncVersion       sql.NullString
	Body              string
	DeletedAt         sql.NullTime
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Session struct {
	ID                  int64
	TicketID            int64
	Harness             string
	HarnessSessionRef   sql.NullString
	HarnessSessionName  sql.NullString
	TmuxSessionName     string
	TmuxWindowID        sql.NullString
	TmuxWindowName      string
	Multiplexer         string
	MuxNamespace        sql.NullString
	MuxContainerID      sql.NullString
	MuxContainerName    sql.NullString
	MuxMetadata         sql.NullString
	WorkspaceID         sql.NullInt64
	LaunchCWD           sql.NullString
	Status              string
	IsActive            bool
	StartedAt           sql.NullTime
	ClosedAt            sql.NullTime
	LastSeenTmuxAt      sql.NullTime
	LastOutputAt        sql.NullTime
	LastStateChangeAt   sql.NullTime
	LastDetectedState   sql.NullString
	LastAttentionReason sql.NullString
	LastDetectionSource sql.NullString
	LastObservedExcerpt sql.NullString
}

// Workspace is the durable execution workspace for an isolated ticket checkout.
// WorkspacePreflight is the read-only result used before a user confirms
// creation or explicit reuse of a Git branch.
type WorkspacePreflight struct {
	SourceBranch string
	SourceCommit string
	SourceDirty  bool
	Branch       string
	BranchExists bool
}

type Workspace struct {
	ID              int64
	TicketID        int64
	BoardID         int64
	Kind            string
	State           string
	IsCurrent       bool
	OwnsWorktree    bool
	RepositoryRoot  string
	CommonDir       string
	WorktreePath    string
	LaunchSubdir    string
	LaunchCWD       string
	BranchName      string
	SourceBranch    string
	SourceCommitSHA string
	BaseCommitSHA   string
	LastStatusJSON  sql.NullString
	LastError       sql.NullString
	IntegratedAt    sql.NullTime
	RetiredAt       sql.NullTime
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// BoardView is a query model containing a board and its populated columns.
type BoardView struct {
	Board   Board
	Columns []Column
}

// MasterFilter describes query controls for the cross-board Master view.
// Empty slices mean "all" for that dimension. Runtime filters use board IDs
// after durable UUID presets are resolved at load time.
type MasterFilter struct {
	BoardIDs        []int64
	Runtimes        []string
	Harnesses       []string
	Search          string
	IncludeArchived bool
}

// DurableMasterFilter is the persisted form of MasterFilter. Board refs use
// stable board UUIDs so renames do not invalidate presets.
type DurableMasterFilter struct {
	BoardUUIDs      []string `json:"board_uuids,omitempty"`
	Runtimes        []string `json:"runtimes,omitempty"`
	Harnesses       []string `json:"harnesses,omitempty"`
	Search          string   `json:"search,omitempty"`
	IncludeArchived bool     `json:"include_archived,omitempty"`
}

// MasterFilterPreset is a named, durable Master filter preference.
type MasterFilterPreset struct {
	ID        int64
	Name      string
	Filter    DurableMasterFilter
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrBoardSyncSkipped reports that a board was intentionally not synced.
var ErrBoardSyncSkipped = errors.New("board sync skipped")

// ErrBoardHasActiveSessions reports that archive/export would race live work.
var ErrBoardHasActiveSessions = errors.New("board has active sessions")

// RemoteTicket is the ticket metadata projection written by external ticket
// backends. It intentionally excludes local runtime/session fields.
type RemoteTicket struct {
	BoardID           int64
	ColumnID          int64
	ExternalID        string
	ExternalURL       string
	ExternalUpdatedAt time.Time
	DisplayID         string
	DisplayNumber     int
	Title             string
	Body              string
	ArchivedAt        *time.Time
	// SourceTicketID optionally links a just-created remote issue back to the
	// local placeholder ticket that produced it, instead of inserting a second
	// ticket with the remote display ID.
	SourceTicketID int64
}

type CreateBoardOptions struct {
	Name          string
	Workdir       string
	TicketBackend string
	BackendQuery  string
	BackendConfig string
}

const (
	RemotePushStatePending = "pending"
	RemotePushStateFailed  = "failed"

	DiagnosticKindSync      = "sync"
	DiagnosticKindReconcile = "reconcile"
	DiagnosticKindRuntime   = "runtime"
	DiagnosticKindShutdown  = "shutdown"
)

// RuntimeDiagnostic is a durable, redacted failure record for sync/runtime ops.
type RuntimeDiagnostic struct {
	ID        int64
	CreatedAt time.Time
	Kind      string
	Operation string
	BoardID   sql.NullInt64
	TicketID  sql.NullInt64
	SessionID sql.NullInt64
	Attempt   int
	Message   string
	Cause     string
}

// RuntimeDiagnosticInput is the write model for InsertRuntimeDiagnostic.
type RuntimeDiagnosticInput struct {
	Kind      string
	Operation string
	BoardID   int64
	TicketID  int64
	SessionID int64
	Attempt   int
	Message   string
	Cause     string
}
