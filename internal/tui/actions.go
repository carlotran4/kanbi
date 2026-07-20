package tui

import (
	"context"

	"github.com/carlotran4/kanbi/internal/app"
	"github.com/carlotran4/kanbi/internal/boardpackage"
	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
	"github.com/carlotran4/kanbi/internal/storage"
)

type BoardQueries interface {
	BoardView(context.Context) (storage.BoardView, error)
	ListBoards(context.Context) ([]storage.Board, error)
	ListBoardsFiltered(context.Context, bool) ([]storage.Board, error)
	BoardViewByID(context.Context, int64) (storage.BoardView, error)
	MasterBoardView(context.Context) (storage.BoardView, error)
	MasterBoardViewWithFilter(context.Context, storage.MasterFilter) (storage.BoardView, error)
	ListTickets(context.Context, bool) ([]storage.Ticket, error)
	ListFilterPresets(context.Context) ([]storage.MasterFilterPreset, error)
}

type BoardCommands interface {
	CreateBoardWithWorkdir(context.Context, string, string) (storage.Board, error)
	CreateBoardWithWorkdirMode(context.Context, string, string, string) (storage.Board, error)
	RenameBoard(context.Context, int64, string) error
	SetBoardWorkdir(context.Context, int64, string) error
	SetBoardWorktreeMode(context.Context, int64, string) error
	DeleteBoard(context.Context, int64) error
	ArchiveBoard(context.Context, int64) error
	UnarchiveBoard(context.Context, int64) error
	SetBoardSyncEnabled(context.Context, int64, bool) error
	ExportBoard(context.Context, int64, string) error
	PreviewBoardPackage(context.Context, string) (boardpackage.Report, error)
	ImportBoardPackage(context.Context, string, boardpackage.ImportOptions) (boardpackage.Result, error)
	ColumnIDByBoardAndName(context.Context, int64, string) (int64, error)
	ColumnIDByBoardAndWorkflowKey(context.Context, int64, string) (int64, error)
	SetColumnWorkflowKey(context.Context, int64, string) error
	SaveFilterPreset(context.Context, string, storage.DurableMasterFilter) (storage.MasterFilterPreset, error)
	DeleteFilterPreset(context.Context, int64) error
	ResolveMasterFilter(context.Context, storage.DurableMasterFilter) (storage.MasterFilter, []string, error)
	DurableFromMasterFilter(context.Context, storage.MasterFilter) (storage.DurableMasterFilter, error)
}

type TicketCommands interface {
	CreateTicket(context.Context, int64, string, string, string) (storage.Ticket, error)
	UpdateTicket(context.Context, int64, string, string, string) error
	ArchiveTicket(context.Context, int64) error
	MoveTicket(context.Context, int64, int64) error
	ReorderTicket(context.Context, int64, int) error
	MarkTicketState(context.Context, int64, string) error
}

type ColumnCommands interface {
	AddColumn(context.Context, int64, string) (storage.Column, error)
	RenameColumn(context.Context, int64, string) error
	DeleteColumn(context.Context, int64) error
	ReorderColumn(context.Context, int64, int) error
}

type IntegrationCommands interface {
	IntegrationCandidates(context.Context, int64) ([]integrationpkg.Candidate, error)
	CreateIntegration(context.Context, integrationpkg.CreateOptions) (integrationpkg.CreateResult, error)
	PromoteIntegration(context.Context, string) error
	CancelIntegration(context.Context, string) error
	ListIntegrationRuns(context.Context, int64) ([]storage.IntegrationRun, error)
	FocusIntegration(context.Context, storage.IntegrationRun) error
}

type SessionCommands interface {
	OpenTicket(context.Context, storage.Ticket, bool) error
	RefreshRuntime(context.Context) error
	PastePromptNow(context.Context, string, string) error
	CloseTicketSession(context.Context, storage.Ticket) error
	KillAllSessions(context.Context) error
	StartFreshTicket(context.Context, storage.Ticket, bool) error
	MoveTicketToDefaultMultiplexer(context.Context, storage.Ticket) error
	UpdateSessionRef(context.Context, storage.Ticket, string) error
	PreflightTicketWorkspace(context.Context, storage.Ticket, string) (storage.WorkspacePreflight, error)
	PrepareTicketWorkspace(context.Context, storage.Ticket, string, bool) error
	ResolveTicketWorkspace(context.Context, storage.Ticket) error
	IntegrateTicketWorkspace(context.Context, storage.Ticket) error
}

type NoteCommands interface {
	ListNotes(context.Context, int64) ([]storage.Note, error)
	AddNote(context.Context, int64, string) (storage.Note, error)
	UpdateNote(context.Context, int64, string) error
	DeleteNote(context.Context, int64) error
}

// Actions is the aggregate TUI application seam.
type Actions interface {
	BoardQueries
	BoardCommands
	TicketCommands
	ColumnCommands
	SessionCommands
	IntegrationCommands
	NoteCommands
}

// Compatibility aliases keep existing TUI callers source-compatible while
// production orchestration lives in the presentation-independent app package.
type SessionManager = app.SessionManager
type TicketSyncer = app.TicketSyncer
type Service = app.Service

var _ Actions = (*Service)(nil)

func NewService(store *storage.Store, manager SessionManager) *Service {
	return app.NewService(store, manager)
}

func NewServiceWithSyncer(store *storage.Store, manager SessionManager, syncer TicketSyncer) *Service {
	return app.NewServiceWithSyncer(store, manager, syncer)
}
