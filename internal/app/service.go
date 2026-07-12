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
	DataDir string
}

func NewService(store *storage.Store, manager SessionManager) *Service {
	return &Service{Store: store, Manager: manager}
}

func NewServiceWithSyncer(store *storage.Store, manager SessionManager, syncer TicketSyncer) *Service {
	return &Service{Store: store, Manager: manager, Syncer: syncer}
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
func (s *Service) RenameBoard(ctx context.Context, id int64, name string) error {
	return s.Store.RenameBoard(ctx, id, name)
}
func (s *Service) SetBoardWorkdir(ctx context.Context, id int64, cwd string) error {
	return s.Store.SetBoardWorkdir(ctx, id, cwd)
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
