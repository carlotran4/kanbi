package session

import (
	"context"

	"kanbi/internal/storage"
)

// Repository is the durable session-state contract consumed by lifecycle and
// runtime-observation use cases. Its operations intentionally describe domain
// transitions rather than exposing generic persistence primitives.
type Repository interface {
	ClaimSession(context.Context, int64, storage.Session, bool) (int64, error)
	CompleteSessionLaunch(context.Context, int64, storage.Session) error
	FailSessionLaunch(context.Context, int64, string) error

	ActiveSession(context.Context, int64) (storage.Session, bool, error)
	LatestSession(context.Context, int64) (storage.Session, bool, error)
	UpdateSessionRef(context.Context, int64, string) error
	UpdateSessionRuntime(context.Context, int64, string, string, string, string, bool) error
	MarkSessionMissing(context.Context, int64) error
	MarkSessionClosed(context.Context, int64, string, string, string) error

	TicketByID(context.Context, int64) (storage.Ticket, error)
	ListTickets(context.Context, bool) ([]storage.Ticket, error)
}

var _ Repository = (*storage.Store)(nil)
