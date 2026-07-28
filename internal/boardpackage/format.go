package boardpackage

import (
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

const (
	FormatName    = "kanbi-board-package"
	FormatVersion = 1
	// MinSupportedSchemaVersion is packages written against the earliest export.
	MinSupportedSchemaVersion = 5
)

// Manifest is the zip root metadata for a single-board package.
type Manifest struct {
	Format          string    `json:"format"`
	Version         int       `json:"version"`
	SchemaVersion   int       `json:"schema_version"`
	CreatedAt       time.Time `json:"created_at"`
	SourceBoardUUID string    `json:"source_board_uuid"`
	SourceBoardName string    `json:"source_board_name"`
	TicketCount     int       `json:"ticket_count"`
	NoteCount       int       `json:"note_count"`
	SessionCount    int       `json:"session_count"`
	CheckpointCount int       `json:"checkpoint_count,omitempty"`
	AttachmentCount int       `json:"attachment_count"`
}

// Document is the logical board aggregate payload in board.json.
type Document struct {
	Board       BoardPayload        `json:"board"`
	Columns     []ColumnPayload     `json:"columns"`
	Tickets     []TicketPayload     `json:"tickets"`
	Notes       []NotePayload       `json:"notes"`
	Sessions    []SessionPayload    `json:"sessions"`
	Checkpoints []CheckpointPayload `json:"pause_checkpoints,omitempty"`
	Attachments []AttachmentEntry   `json:"attachments"`
}

type BoardPayload struct {
	SourceID         int64      `json:"source_id"`
	Name             string     `json:"name"`
	UUID             string     `json:"uuid"`
	Workdir          string     `json:"workdir,omitempty"`
	WorktreeMode     string     `json:"worktree_mode,omitempty"`
	TicketBackend    string     `json:"ticket_backend"`
	BackendQuery     string     `json:"backend_query,omitempty"`
	BackendConfig    string     `json:"backend_config,omitempty"`
	NextTicketNumber int        `json:"next_ticket_number"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
	SyncEnabled      bool       `json:"sync_enabled"`
	SourceExportUUID string     `json:"source_export_uuid,omitempty"`
}

type ColumnPayload struct {
	SourceID    int64  `json:"source_id"`
	Name        string `json:"name"`
	WorkflowKey string `json:"workflow_key"`
	Position    int    `json:"position"`
}

type TicketPayload struct {
	SourceID              int64      `json:"source_id"`
	ColumnSourceID        int64      `json:"column_source_id"`
	ExternalID            *string    `json:"external_id,omitempty"`
	ExternalURL           *string    `json:"external_url,omitempty"`
	ExternalUpdatedAt     *time.Time `json:"external_updated_at,omitempty"`
	SyncVersion           *string    `json:"sync_version,omitempty"`
	DisplayID             string     `json:"display_id"`
	DisplayNumber         int        `json:"display_number"`
	Title                 string     `json:"title"`
	Body                  string     `json:"body"`
	Harness               string     `json:"harness"`
	Position              int        `json:"position"`
	ArchivedAt            *time.Time `json:"archived_at,omitempty"`
	FocusPaused           bool       `json:"focus_paused,omitempty"`
	RemotePushState       *string    `json:"remote_push_state,omitempty"`
	RemotePushToken       *string    `json:"remote_push_token,omitempty"`
	RemotePushAttemptedAt *time.Time `json:"remote_push_attempted_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type NotePayload struct {
	SourceID          int64      `json:"source_id"`
	TicketSourceID    int64      `json:"ticket_source_id"`
	ExternalID        *string    `json:"external_id,omitempty"`
	ExternalUpdatedAt *time.Time `json:"external_updated_at,omitempty"`
	SyncVersion       *string    `json:"sync_version,omitempty"`
	Body              string     `json:"body"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type CheckpointPayload struct {
	SourceID       int64      `json:"source_id"`
	TicketSourceID int64      `json:"ticket_source_id"`
	Why            string     `json:"why"`
	Completed      string     `json:"completed"`
	NextAction     string     `json:"next_action"`
	PausedAt       time.Time  `json:"paused_at"`
	ResumedAt      *time.Time `json:"resumed_at,omitempty"`
}

type SessionPayload struct {
	SourceID            int64      `json:"source_id"`
	TicketSourceID      int64      `json:"ticket_source_id"`
	Harness             string     `json:"harness"`
	HarnessSessionRef   *string    `json:"harness_session_ref,omitempty"`
	HarnessSessionName  *string    `json:"harness_session_name,omitempty"`
	TmuxSessionName     string     `json:"tmux_session_name"`
	TmuxWindowID        *string    `json:"tmux_window_id,omitempty"`
	TmuxWindowName      string     `json:"tmux_window_name"`
	Multiplexer         string     `json:"multiplexer"`
	MuxNamespace        *string    `json:"mux_namespace,omitempty"`
	MuxContainerID      *string    `json:"mux_container_id,omitempty"`
	MuxContainerName    *string    `json:"mux_container_name,omitempty"`
	MuxMetadata         *string    `json:"mux_metadata,omitempty"`
	Status              string     `json:"status"`
	IsActive            bool       `json:"is_active"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	ClosedAt            *time.Time `json:"closed_at,omitempty"`
	LastSeenTmuxAt      *time.Time `json:"last_seen_tmux_at,omitempty"`
	LastOutputAt        *time.Time `json:"last_output_at,omitempty"`
	LastStateChangeAt   *time.Time `json:"last_state_change_at,omitempty"`
	LastDetectedState   *string    `json:"last_detected_state,omitempty"`
	LastAttentionReason *string    `json:"last_attention_reason,omitempty"`
	LastDetectionSource *string    `json:"last_detection_source,omitempty"`
	LastObservedExcerpt *string    `json:"last_observed_excerpt,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// AttachmentEntry inventories one package file under attachments/.
type AttachmentEntry struct {
	SourceTicketID int64  `json:"source_ticket_id"`
	RelativePath   string `json:"relative_path"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
}

// Report is the non-mutating preview of a package.
type Report struct {
	Manifest            Manifest `json:"manifest"`
	BoardName           string   `json:"board_name"`
	TicketBackend       string   `json:"ticket_backend"`
	TicketCount         int      `json:"ticket_count"`
	NoteCount           int      `json:"note_count"`
	SessionCount        int      `json:"session_count"`
	CheckpointCount     int      `json:"checkpoint_count"`
	AttachmentCount     int      `json:"attachment_count"`
	ActiveSessionCount  int      `json:"active_session_count"`
	NameCollision       bool     `json:"name_collision"`
	MissingAttachments  []string `json:"missing_attachments,omitempty"`
	UnlistedAttachments []string `json:"unlisted_attachments,omitempty"`
	InvalidAttachments  []string `json:"invalid_attachments,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
	SchemaCompatible    bool     `json:"schema_compatible"`
}

// ImportOptions controls create-new-only import.
type ImportOptions struct {
	// NameOverride renames the imported board when the package name collides.
	NameOverride string
}

// Result is the import apply outcome.
// Board is intentionally omitted from JSON so callers emit a stable encoder.
type Result struct {
	Board               storage.Board   `json:"-"`
	TicketIDRemap       map[int64]int64 `json:"ticket_id_remap"`
	SourceBoardUUID     string          `json:"source_board_uuid"`
	DuplicateProvenance bool            `json:"duplicate_provenance"`
	Warnings            []string        `json:"warnings,omitempty"`
}
