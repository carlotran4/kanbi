package tui

import (
	"context"
	"fmt"

	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
)

// Actions is the TUI's application-service seam. It groups board, ticket,
// column, lifecycle, repair, and filter actions behind one contract so the
// model does not probe for many tiny optional capabilities during normal use.
type Actions interface {
	BoardView(context.Context) (storage.BoardView, error)
	ListBoards(context.Context) ([]storage.Board, error)
	BoardViewByID(context.Context, int64) (storage.BoardView, error)
	MasterBoardView(context.Context) (storage.BoardView, error)
	MasterBoardViewWithFilter(context.Context, storage.MasterFilter) (storage.BoardView, error)
	ListTickets(context.Context, bool) ([]storage.Ticket, error)

	CreateBoardWithWorkdir(context.Context, string, string) (storage.Board, error)
	RenameBoard(context.Context, int64, string) error
	SetBoardWorkdir(context.Context, int64, string) error
	DeleteBoard(context.Context, int64) error
	ColumnIDByBoardAndName(context.Context, int64, string) (int64, error)

	CreateTicket(context.Context, int64, string, string, string) (storage.Ticket, error)
	UpdateTicket(context.Context, int64, string, string, string) error
	ArchiveTicket(context.Context, int64) error
	MoveTicket(context.Context, int64, int64) error
	ReorderTicket(context.Context, int64, int) error
	MarkTicketState(context.Context, int64, string) error

	AddColumn(context.Context, int64, string) (storage.Column, error)
	RenameColumn(context.Context, int64, string) error
	DeleteColumn(context.Context, int64) error
	ReorderColumn(context.Context, int64, int) error

	OpenTicket(context.Context, storage.Ticket, bool) error
	RefreshRuntime(context.Context) error
	PastePromptNow(context.Context, string, string) error
	CloseTicketSession(context.Context, storage.Ticket) error
	KillAllSessions(context.Context) error
	StartFreshTicket(context.Context, storage.Ticket, bool) error
	UpdateSessionRef(context.Context, storage.Ticket, string) error
}

// Service is the production implementation of Actions. Storage remains the
// durable owner and tmux.Manager remains the lifecycle/runtime owner; Service
// only performs UI-oriented orchestration between them.
type Service struct {
	Store   *storage.Store
	Manager *tmux.Manager
}

func NewService(store *storage.Store, manager *tmux.Manager) *Service {
	return &Service{Store: store, Manager: manager}
}

func (s *Service) BoardView(ctx context.Context) (storage.BoardView, error) {
	return s.Store.BoardView(ctx)
}
func (s *Service) ListBoards(ctx context.Context) ([]storage.Board, error) {
	return s.Store.ListBoards(ctx)
}
func (s *Service) BoardViewByID(ctx context.Context, boardID int64) (storage.BoardView, error) {
	return s.Store.BoardViewByID(ctx, boardID)
}
func (s *Service) MasterBoardView(ctx context.Context) (storage.BoardView, error) {
	return s.Store.MasterBoardView(ctx)
}
func (s *Service) MasterBoardViewWithFilter(ctx context.Context, filter storage.MasterFilter) (storage.BoardView, error) {
	return s.Store.MasterBoardViewWithFilter(ctx, filter)
}
func (s *Service) ListTickets(ctx context.Context, includeArchived bool) ([]storage.Ticket, error) {
	return s.Store.ListTickets(ctx, includeArchived)
}
func (s *Service) CreateBoardWithWorkdir(ctx context.Context, name, workdir string) (storage.Board, error) {
	return s.Store.CreateBoardWithWorkdir(ctx, name, workdir)
}
func (s *Service) RenameBoard(ctx context.Context, boardID int64, name string) error {
	return s.Store.RenameBoard(ctx, boardID, name)
}
func (s *Service) SetBoardWorkdir(ctx context.Context, boardID int64, workdir string) error {
	return s.Store.SetBoardWorkdir(ctx, boardID, workdir)
}
func (s *Service) DeleteBoard(ctx context.Context, boardID int64) error {
	return s.Store.DeleteBoard(ctx, boardID)
}
func (s *Service) ColumnIDByBoardAndName(ctx context.Context, boardID int64, name string) (int64, error) {
	return s.Store.ColumnIDByBoardAndName(ctx, boardID, name)
}
func (s *Service) CreateTicket(ctx context.Context, columnID int64, title, body, harnessName string) (storage.Ticket, error) {
	return s.Store.CreateTicket(ctx, columnID, title, body, harnessName)
}
func (s *Service) UpdateTicket(ctx context.Context, id int64, title, body, harnessName string) error {
	before, err := s.Store.TicketByID(ctx, id)
	if err != nil {
		return err
	}
	if before.Title != title && before.WindowName.Valid && s.Manager != nil {
		if err := s.Manager.RenameTicketWindow(ctx, before, title); err != nil {
			return err
		}
	}
	return s.Store.UpdateTicket(ctx, id, title, body, harnessName)
}
func (s *Service) ArchiveTicket(ctx context.Context, id int64) error {
	return s.Store.ArchiveTicket(ctx, id)
}
func (s *Service) MoveTicket(ctx context.Context, id, columnID int64) error {
	return s.Store.MoveTicket(ctx, id, columnID)
}
func (s *Service) ReorderTicket(ctx context.Context, id int64, delta int) error {
	return s.Store.ReorderTicket(ctx, id, delta)
}
func (s *Service) MarkTicketState(ctx context.Context, ticketID int64, state string) error {
	return s.Store.MarkTicketRuntime(ctx, ticketID, state, "manual", "manual override")
}
func (s *Service) AddColumn(ctx context.Context, boardID int64, name string) (storage.Column, error) {
	return s.Store.AddColumn(ctx, boardID, name)
}
func (s *Service) RenameColumn(ctx context.Context, columnID int64, name string) error {
	return s.Store.RenameColumn(ctx, columnID, name)
}
func (s *Service) DeleteColumn(ctx context.Context, columnID int64) error {
	return s.Store.DeleteColumn(ctx, columnID)
}
func (s *Service) ReorderColumn(ctx context.Context, columnID int64, delta int) error {
	return s.Store.ReorderColumn(ctx, columnID, delta)
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
func (s *Service) PastePromptNow(ctx context.Context, windowName, text string) error {
	if s.Manager == nil {
		return fmt.Errorf("paste unavailable")
	}
	return s.Manager.PastePromptNow(ctx, windowName, text)
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
func (s *Service) StartFreshTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	if s.Manager == nil {
		return fmt.Errorf("start fresh unavailable")
	}
	return s.Manager.StartFreshTicket(ctx, ticket, sendPrompt)
}
func (s *Service) UpdateSessionRef(ctx context.Context, ticket storage.Ticket, ref string) error {
	if ticket.SessionID.Valid {
		return s.Store.UpdateSessionRef(ctx, ticket.SessionID.Int64, ref)
	}
	return fmt.Errorf("ticket has no session to repair")
}
