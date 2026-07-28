// Package app contains presentation-independent application orchestration.
package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/carlotran4/kanbi/internal/attachments"
	"github.com/carlotran4/kanbi/internal/boardpackage"
	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/ticketbackend"
)

// SessionManager is the provider-neutral runtime seam used by the application.
type SessionManager interface {
	RenameTicketWindow(context.Context, storage.Ticket, string) error
	OpenTicket(context.Context, storage.Ticket, bool) error
	SwitchToTicket(context.Context, storage.Ticket) error
	RefreshRuntime(context.Context) error
	PastePromptNow(context.Context, string, string) error
	CloseSession(context.Context, storage.Ticket) error
	KillSession(context.Context) error
	StartFreshTicket(context.Context, storage.Ticket, bool) error
	MoveTicketToDefaultMultiplexer(context.Context, storage.Ticket) error
}

type IntegrationRuntimeManager interface {
	FocusIntegration(context.Context, storage.IntegrationRun) error
}

// TicketMessageSender is optional because only configured multiplexers that can
// address a durable session container support handoff injection.
type TicketMessageSender interface {
	SendTicketMessage(context.Context, storage.Ticket, string) error
}

// ResumeRuntimeError means the focus slot was durably claimed, but the normal
// runtime resume/repair step failed. Callers must not retry the checkpoint
// transaction or silently re-pause the ticket.
type ResumeRuntimeError struct{ Err error }

func (e ResumeRuntimeError) Error() string { return e.Err.Error() }
func (e ResumeRuntimeError) Unwrap() error { return e.Err }

type WorkspaceManager interface {
	PreflightTicketWorkspace(context.Context, storage.Ticket, string) (storage.WorkspacePreflight, error)
	PrepareTicketWorkspace(context.Context, storage.Ticket, string, bool) error
	ResolveTicketWorkspace(context.Context, storage.Ticket) error
	IntegrateTicketWorkspace(context.Context, storage.Ticket) error
}

// TicketSyncer is the board-scoped synchronization seam used after mutations.
// ScheduleBoardSync is preferred for mutation paths so ownership stays inside
// the sync manager (cancel + drain) instead of fire-and-forget app goroutines.
type TicketSyncer interface {
	SyncBoard(context.Context, storage.Board) (ticketbackend.Result, error)
	ScheduleBoardSync(boardID int64)
}

type Service struct {
	Store   *storage.Store
	Manager SessionManager
	Syncer  TicketSyncer
	// DataDir is the Kanbi data root (parent of attachments/). Used by board package ops.
	DataDir     string
	Integration *integrationpkg.Service
}

func NewService(store *storage.Store, manager SessionManager) *Service {
	return &Service{Store: store, Manager: manager}
}

func NewServiceWithSyncer(store *storage.Store, manager SessionManager, syncer TicketSyncer) *Service {
	return &Service{Store: store, Manager: manager, Syncer: syncer}
}

func (s *Service) SetFocusPolicy(policy storage.FocusPolicy) { s.Store.SetFocusPolicy(policy) }
func (s *Service) FocusStatus(ctx context.Context) (storage.FocusStatus, error) {
	return s.Store.FocusStatus(ctx)
}
func (s *Service) FocusedTickets(ctx context.Context) ([]storage.Ticket, error) {
	return s.Store.FocusedTickets(ctx)
}

func (s *Service) IntegrationCandidates(ctx context.Context, boardID int64) ([]integrationpkg.Candidate, error) {
	if s.Integration == nil {
		return nil, fmt.Errorf("integration unavailable")
	}
	return s.Integration.Eligible(ctx, boardID)
}
func (s *Service) CreateIntegration(ctx context.Context, opts integrationpkg.CreateOptions) (integrationpkg.CreateResult, error) {
	if s.Integration == nil {
		return integrationpkg.CreateResult{}, fmt.Errorf("integration unavailable")
	}
	return s.Integration.Create(ctx, opts)
}
func (s *Service) CancelIntegration(ctx context.Context, publicID string) error {
	if s.Integration == nil {
		return fmt.Errorf("integration unavailable")
	}
	return s.Integration.Cancel(ctx, publicID)
}
func (s *Service) PromoteIntegration(ctx context.Context, publicID string) error {
	if s.Integration == nil {
		return fmt.Errorf("integration unavailable")
	}
	return s.Integration.Promote(ctx, publicID, integrationpkg.PromoteOptions{CloseTicket: func(ctx context.Context, t storage.Ticket) error {
		if s.Manager == nil {
			return fmt.Errorf("session close unavailable")
		}
		return s.Manager.CloseSession(ctx, t)
	}})
}
func (s *Service) ListIntegrationRuns(ctx context.Context, boardID int64) ([]storage.IntegrationRun, error) {
	return s.Store.ListIntegrationRuns(ctx, boardID)
}
func (s *Service) FocusIntegration(ctx context.Context, run storage.IntegrationRun) error {
	manager, ok := s.Manager.(IntegrationRuntimeManager)
	if !ok {
		return fmt.Errorf("integration focus unavailable")
	}
	return manager.FocusIntegration(ctx, run)
}

func (s *Service) BoardView(ctx context.Context) (storage.BoardView, error) {
	return s.Store.BoardView(ctx)
}
func (s *Service) ListBoards(ctx context.Context) ([]storage.Board, error) {
	return s.Store.ListBoards(ctx)
}
func (s *Service) ListBoardsFiltered(ctx context.Context, includeArchived bool) ([]storage.Board, error) {
	return s.Store.ListBoardsFiltered(ctx, includeArchived)
}
func (s *Service) ArchiveBoard(ctx context.Context, boardID int64) error {
	return s.Store.ArchiveBoard(ctx, boardID)
}
func (s *Service) UnarchiveBoard(ctx context.Context, boardID int64) error {
	return s.Store.UnarchiveBoard(ctx, boardID)
}
func (s *Service) SetBoardSyncEnabled(ctx context.Context, boardID int64, enabled bool) error {
	return s.Store.SetBoardSyncEnabled(ctx, boardID, enabled)
}
func (s *Service) ExportBoard(ctx context.Context, boardID int64, dest string) error {
	dataDir := s.dataDir()
	return boardpackage.Export(ctx, s.Store, dataDir, boardID, dest)
}
func (s *Service) PreviewBoardPackage(ctx context.Context, path string) (boardpackage.Report, error) {
	return boardpackage.Preview(ctx, s.Store, path)
}
func (s *Service) ImportBoardPackage(ctx context.Context, path string, opts boardpackage.ImportOptions) (boardpackage.Result, error) {
	return boardpackage.Import(ctx, s.Store, s.dataDir(), path, opts)
}
func (s *Service) dataDir() string {
	if s != nil && strings.TrimSpace(s.DataDir) != "" {
		return s.DataDir
	}
	// Fall back to attachment base parent.
	return filepath.Dir(attachments.BaseDir())
}
func (s *Service) BoardViewByID(ctx context.Context, id int64) (storage.BoardView, error) {
	return s.Store.BoardViewByID(ctx, id)
}
func (s *Service) MasterBoardView(ctx context.Context) (storage.BoardView, error) {
	return s.Store.MasterBoardView(ctx)
}
func (s *Service) MasterBoardViewWithFilter(ctx context.Context, f storage.MasterFilter) (storage.BoardView, error) {
	return s.Store.MasterBoardViewWithFilter(ctx, f)
}
func (s *Service) ListTickets(ctx context.Context, archived bool) ([]storage.Ticket, error) {
	return s.Store.ListTickets(ctx, archived)
}
func (s *Service) CreateBoardWithWorkdir(ctx context.Context, name, cwd string) (storage.Board, error) {
	return s.Store.CreateBoardWithWorkdir(ctx, name, cwd)
}
func (s *Service) CreateBoardWithWorkdirMode(ctx context.Context, name, cwd, mode string) (storage.Board, error) {
	return s.Store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: name, Workdir: cwd, WorktreeMode: mode, TicketBackend: "local"})
}
func (s *Service) RenameBoard(ctx context.Context, id int64, name string) error {
	return s.Store.RenameBoard(ctx, id, name)
}
func (s *Service) SetBoardWorkdir(ctx context.Context, id int64, cwd string) error {
	return s.Store.SetBoardWorkdir(ctx, id, cwd)
}
func (s *Service) SetBoardWorktreeMode(ctx context.Context, id int64, mode string) error {
	return s.Store.SetBoardWorktreeMode(ctx, id, mode)
}

func (s *Service) DeleteBoard(ctx context.Context, boardID int64) error {
	tickets, err := s.Store.ListTickets(ctx, true)
	if err != nil {
		return err
	}
	var ids []int64
	for _, ticket := range tickets {
		if ticket.BoardID == boardID {
			ids = append(ids, ticket.ID)
		}
	}
	if err := s.Store.DeleteBoard(ctx, boardID); err != nil {
		return err
	}
	var cleanupErrs []error
	for _, id := range ids {
		if err := attachments.DeleteTicket(id); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	if err := errors.Join(cleanupErrs...); err != nil {
		return fmt.Errorf("board deleted, but attachment cleanup failed: %w", err)
	}
	return nil
}

func (s *Service) ColumnIDByBoardAndName(ctx context.Context, boardID int64, name string) (int64, error) {
	return s.Store.ColumnIDByBoardAndName(ctx, boardID, name)
}
func (s *Service) ColumnIDByBoardAndWorkflowKey(ctx context.Context, boardID int64, key string) (int64, error) {
	return s.Store.ColumnIDByBoardAndWorkflowKey(ctx, boardID, key)
}
func (s *Service) SetColumnWorkflowKey(ctx context.Context, columnID int64, key string) error {
	return s.Store.SetColumnWorkflowKey(ctx, columnID, key)
}
func (s *Service) ListFilterPresets(ctx context.Context) ([]storage.MasterFilterPreset, error) {
	return s.Store.ListFilterPresets(ctx)
}
func (s *Service) SaveFilterPreset(ctx context.Context, name string, filter storage.DurableMasterFilter) (storage.MasterFilterPreset, error) {
	return s.Store.SaveFilterPreset(ctx, name, filter)
}
func (s *Service) DeleteFilterPreset(ctx context.Context, id int64) error {
	return s.Store.DeleteFilterPreset(ctx, id)
}
func (s *Service) ResolveMasterFilter(ctx context.Context, d storage.DurableMasterFilter) (storage.MasterFilter, []string, error) {
	return s.Store.ResolveMasterFilter(ctx, d)
}
func (s *Service) DurableFromMasterFilter(ctx context.Context, f storage.MasterFilter) (storage.DurableMasterFilter, error) {
	return s.Store.DurableFromMasterFilter(ctx, f)
}
func (s *Service) CreateTicket(ctx context.Context, columnID int64, title, body, harness string) (storage.Ticket, error) {
	ticket, err := s.Store.CreateTicket(ctx, columnID, title, body, harness)
	if err != nil {
		return storage.Ticket{}, err
	}
	s.syncBoardAfterTicketChange(ctx, ticket.BoardID)
	return ticket, nil
}

func (s *Service) UpdateTicket(ctx context.Context, id int64, title, body, harness string) error {
	before, err := s.Store.TicketByID(ctx, id)
	if err != nil {
		return err
	}
	// Runtime changes must follow validation so a rejected edit cannot rename a
	// live terminal or its session metadata.
	if err := s.Store.ValidateTicketUpdate(title, harness); err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if before.Title != title && before.WindowName.Valid && s.Manager != nil {
		if err := s.Manager.RenameTicketWindow(ctx, before, title); err != nil {
			return err
		}
	}
	if err := s.Store.UpdateTicket(ctx, id, title, body, harness); err != nil {
		return err
	}
	s.syncBoardAfterTicketChange(ctx, before.BoardID)
	return nil
}

func (s *Service) ArchiveTicket(ctx context.Context, id int64) error {
	ticket, err := s.Store.TicketByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.ArchiveTicket(ctx, id); err != nil {
		return err
	}
	s.syncBoardAfterTicketChange(ctx, ticket.BoardID)
	return nil
}

// PauseTicket closes a live session before writing the handoff checkpoint. A
// failed close leaves both the ticket and the append-only checkpoint history
// unchanged.
func (s *Service) PauseTicket(ctx context.Context, id int64, why, completed, next string) error {
	if err := storage.ValidatePauseCheckpoint(why, completed, next); err != nil {
		return err
	}
	ticket, err := s.Store.TicketByID(ctx, id)
	if err != nil {
		return err
	}
	if ticket.SessionActive {
		if s.Manager == nil {
			return fmt.Errorf("session close unavailable")
		}
		if err := s.Manager.CloseSession(ctx, ticket); err != nil {
			return err
		}
		refreshed, err := s.Store.TicketByID(ctx, id)
		if err != nil {
			return err
		}
		if refreshed.SessionActive {
			return fmt.Errorf("session is still active after close")
		}
	}
	return s.Store.PauseTicket(ctx, id, why, completed, next)
}

func (s *Service) closeFocusReplacement(ctx context.Context, id int64) (storage.Ticket, error) {
	ticket, err := s.Store.TicketByID(ctx, id)
	if err != nil {
		return storage.Ticket{}, err
	}
	if ticket.SessionActive {
		if s.Manager == nil {
			return storage.Ticket{}, fmt.Errorf("session close unavailable")
		}
		if err := s.Manager.CloseSession(ctx, ticket); err != nil {
			return storage.Ticket{}, err
		}
		refreshed, err := s.Store.TicketByID(ctx, id)
		if err != nil {
			return storage.Ticket{}, err
		}
		if refreshed.SessionActive {
			return storage.Ticket{}, fmt.Errorf("session is still active after close")
		}
		ticket = refreshed
	}
	return ticket, nil
}

// PauseAndMove closes the selected replacement first, then atomically records
// its checkpoint and admits the incoming ticket in SQLite.
func (s *Service) PauseAndMove(ctx context.Context, replacementID, targetID, destinationID int64, why, completed, next string) error {
	if err := storage.ValidatePauseCheckpoint(why, completed, next); err != nil {
		return err
	}
	if _, err := s.closeFocusReplacement(ctx, replacementID); err != nil {
		return err
	}
	if err := s.Store.PauseAndMove(ctx, replacementID, targetID, destinationID, why, completed, next); err != nil {
		return err
	}
	target, err := s.Store.TicketByID(ctx, targetID)
	if err == nil {
		s.syncBoardAfterTicketChange(ctx, target.BoardID)
	}
	return err
}

// PauseAndResume closes the replacement and atomically transfers its focus
// slot to target before using the ordinary resume/repair lifecycle.
func (s *Service) PauseAndResume(ctx context.Context, replacementID, targetID int64, why, completed, next string, sendHandoff bool) error {
	if err := storage.ValidatePauseCheckpoint(why, completed, next); err != nil {
		return err
	}
	target, err := s.Store.TicketByID(ctx, targetID)
	if err != nil {
		return err
	}
	checkpoint := target.LatestCheckpoint
	if !target.FocusPaused || checkpoint == nil {
		return fmt.Errorf("ticket is not paused")
	}
	if _, err := s.closeFocusReplacement(ctx, replacementID); err != nil {
		return err
	}
	if err := s.Store.PauseAndResume(ctx, replacementID, targetID, why, completed, next); err != nil {
		return err
	}
	if err := s.openResumedTicket(ctx, target, checkpoint, sendHandoff); err != nil {
		return ResumeRuntimeError{Err: err}
	}
	return nil
}

// ResumePausedTicket uses normal lifecycle handling. Plain resume never sends
// a message; sendHandoff injects the saved checkpoint only after resume works.
func (s *Service) ResumePausedTicket(ctx context.Context, id int64, sendHandoff bool) error {
	ticket, err := s.Store.TicketByID(ctx, id)
	if err != nil {
		return err
	}
	checkpoint := ticket.LatestCheckpoint
	if !ticket.FocusPaused || checkpoint == nil {
		return fmt.Errorf("ticket is not paused")
	}
	if err := s.Store.ResumeTicket(ctx, id); err != nil {
		return err
	}
	if err := s.openResumedTicket(ctx, ticket, checkpoint, sendHandoff); err != nil {
		return ResumeRuntimeError{Err: err}
	}
	return nil
}

func (s *Service) openResumedTicket(ctx context.Context, ticket storage.Ticket, checkpoint *storage.PauseCheckpoint, sendHandoff bool) error {
	if s.Manager == nil {
		return fmt.Errorf("open unavailable")
	}
	if err := s.Manager.OpenTicket(ctx, ticket, false); err != nil {
		return err
	}
	if !sendHandoff {
		return nil
	}
	sender, ok := s.Manager.(TicketMessageSender)
	if !ok {
		return fmt.Errorf("handoff message unavailable for this runtime")
	}
	refreshed, err := s.Store.TicketByID(ctx, ticket.ID)
	if err != nil {
		return err
	}
	message := fmt.Sprintf("## Resuming paused work\n\n**Why this was paused**\n%s\n\n**Already completed**\n%s\n\n**Next action**\n%s", checkpoint.Why, checkpoint.Completed, checkpoint.NextAction)
	return sender.SendTicketMessage(ctx, refreshed, message)
}

func (s *Service) MoveTicket(ctx context.Context, id, columnID int64) error {
	ticket, err := s.Store.TicketByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.MoveTicket(ctx, id, columnID); err != nil {
		return err
	}
	s.syncBoardAfterTicketChange(ctx, ticket.BoardID)
	return nil
}

func (s *Service) ReorderTicket(ctx context.Context, id int64, delta int) error {
	return s.Store.ReorderTicket(ctx, id, delta)
}
func (s *Service) MarkTicketState(ctx context.Context, id int64, state string) error {
	return s.Store.MarkTicketRuntime(ctx, id, state, "manual", "manual override")
}
func (s *Service) AddColumn(ctx context.Context, boardID int64, name string) (storage.Column, error) {
	return s.Store.AddColumn(ctx, boardID, name)
}
func (s *Service) RenameColumn(ctx context.Context, id int64, name string) error {
	return s.Store.RenameColumn(ctx, id, name)
}
func (s *Service) DeleteColumn(ctx context.Context, id int64) error {
	return s.Store.DeleteColumn(ctx, id)
}
func (s *Service) ReorderColumn(ctx context.Context, id int64, delta int) error {
	return s.Store.ReorderColumn(ctx, id, delta)
}

func (s *Service) OpenTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	if s.Manager == nil {
		return fmt.Errorf("open unavailable")
	}
	if sendPrompt {
		return s.Manager.OpenTicket(ctx, ticket, true)
	}
	return s.Manager.SwitchToTicket(ctx, ticket)
}
func (s *Service) RefreshRuntime(ctx context.Context) error {
	if s.Manager == nil {
		return nil
	}
	return s.Manager.RefreshRuntime(ctx)
}
func (s *Service) PastePromptNow(ctx context.Context, name, text string) error {
	if s.Manager == nil {
		return fmt.Errorf("paste unavailable")
	}
	return s.Manager.PastePromptNow(ctx, name, text)
}
func (s *Service) CloseTicketSession(ctx context.Context, ticket storage.Ticket) error {
	if s.Manager == nil {
		return fmt.Errorf("close unavailable")
	}
	return s.Manager.CloseSession(ctx, ticket)
}
func (s *Service) KillAllSessions(ctx context.Context) error {
	if s.Manager == nil {
		return nil
	}
	return s.Manager.KillSession(ctx)
}
func (s *Service) StartFreshTicket(ctx context.Context, ticket storage.Ticket, send bool) error {
	if s.Manager == nil {
		return fmt.Errorf("start fresh unavailable")
	}
	return s.Manager.StartFreshTicket(ctx, ticket, send)
}
func (s *Service) MoveTicketToDefaultMultiplexer(ctx context.Context, ticket storage.Ticket) error {
	if s.Manager == nil {
		return fmt.Errorf("move to default multiplexer unavailable")
	}
	return s.Manager.MoveTicketToDefaultMultiplexer(ctx, ticket)
}
func (s *Service) PreflightTicketWorkspace(ctx context.Context, ticket storage.Ticket, branch string) (storage.WorkspacePreflight, error) {
	manager, ok := s.Manager.(WorkspaceManager)
	if !ok {
		return storage.WorkspacePreflight{}, fmt.Errorf("workspace preparation unavailable")
	}
	return manager.PreflightTicketWorkspace(ctx, ticket, branch)
}
func (s *Service) PrepareTicketWorkspace(ctx context.Context, ticket storage.Ticket, branch string, existing bool) error {
	manager, ok := s.Manager.(WorkspaceManager)
	if !ok {
		return fmt.Errorf("workspace preparation unavailable")
	}
	return manager.PrepareTicketWorkspace(ctx, ticket, branch, existing)
}
func (s *Service) ResolveTicketWorkspace(ctx context.Context, ticket storage.Ticket) error {
	manager, ok := s.Manager.(WorkspaceManager)
	if !ok {
		return fmt.Errorf("workspace resolution unavailable")
	}
	return manager.ResolveTicketWorkspace(ctx, ticket)
}
func (s *Service) IntegrateTicketWorkspace(ctx context.Context, ticket storage.Ticket) error {
	manager, ok := s.Manager.(WorkspaceManager)
	if !ok {
		return fmt.Errorf("workspace integration unavailable")
	}
	return manager.IntegrateTicketWorkspace(ctx, ticket)
}
func (s *Service) UpdateSessionRef(ctx context.Context, ticket storage.Ticket, ref string) error {
	if ticket.SessionID.Valid {
		return s.Store.UpdateSessionRef(ctx, ticket.SessionID.Int64, ref)
	}
	return fmt.Errorf("ticket has no session to repair")
}

func (s *Service) ListNotes(ctx context.Context, ticketID int64) ([]storage.Note, error) {
	return s.Store.ListNotes(ctx, ticketID)
}
func (s *Service) AddNote(ctx context.Context, ticketID int64, body string) (storage.Note, error) {
	note, err := s.Store.AddNote(ctx, ticketID, body)
	if err != nil {
		return storage.Note{}, err
	}
	s.syncTicketBoardAfterChange(ctx, ticketID)
	return note, nil
}
func (s *Service) UpdateNote(ctx context.Context, id int64, body string) error {
	note, err := s.Store.NoteByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.UpdateNote(ctx, id, body); err != nil {
		return err
	}
	s.syncTicketBoardAfterChange(ctx, note.TicketID)
	return nil
}
func (s *Service) DeleteNote(ctx context.Context, id int64) error {
	note, err := s.Store.NoteByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteNote(ctx, id); err != nil {
		return err
	}
	s.syncTicketBoardAfterChange(ctx, note.TicketID)
	return nil
}

func (s *Service) syncTicketBoardAfterChange(ctx context.Context, ticketID int64) {
	if ticket, err := s.Store.TicketByID(ctx, ticketID); err == nil {
		s.syncBoardAfterTicketChange(ctx, ticket.BoardID)
	}
}

func (s *Service) syncBoardAfterTicketChange(ctx context.Context, boardID int64) {
	if s == nil || s.Syncer == nil || boardID == 0 {
		return
	}
	// Manager owns lifecycle-bound scheduling/drain; app never spawns free goroutines.
	_ = ctx
	s.Syncer.ScheduleBoardSync(boardID)
}
