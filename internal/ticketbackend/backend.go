package ticketbackend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"agent-kanban/internal/storage"
)

const (
	KindLocal     = "local"
	KindGitHub    = "github"
	KindAtlassian = "atlassian"
	KindAsana     = "asana"
)

// Direction captures the intended durable sync model for external ticketing
// systems. SQLite remains the local cache/runtime store; external backends own
// ticket metadata for their boards.
type Direction string

const (
	DirectionPull Direction = "pull"
	DirectionPush Direction = "push"
)

// Change describes one planned sync mutation. Backends use newest-updated-at
// conflict resolution before applying local or remote updates.
type Change struct {
	Direction  Direction
	TicketID   int64
	ExternalID string
	UpdatedAt  time.Time
	Summary    string
}

type Result struct {
	Pulled    int
	Pushed    int
	Conflicts int
}

// Backend is implemented once per ticket metadata provider. A board has exactly
// one Backend, selected at board creation. Implementations should sync columns,
// tickets, and comments/notes according to the remote source's model. When the
// remote system offers a query language, Board.BackendQuery scopes the synced
// subset (JQL for Atlassian/Jira; a future GitHub adapter must define its query
// surface before implementation).
type Backend interface {
	Kind() string
	Sync(ctx context.Context, store *storage.Store, board storage.Board) (Result, error)
}

type Registry struct {
	mu       sync.RWMutex
	backends map[string]Backend
}

func NewRegistry(backends ...Backend) *Registry {
	r := &Registry{backends: map[string]Backend{}}
	for _, backend := range backends {
		r.Register(backend)
	}
	return r
}

func DefaultRegistry() *Registry {
	return NewRegistry(LocalBackend{})
}

func (r *Registry) Register(backend Backend) {
	if backend == nil || backend.Kind() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[backend.Kind()] = backend
}

func (r *Registry) Get(kind string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	backend, ok := r.backends[kind]
	return backend, ok
}

func (r *Registry) MustGet(kind string) (Backend, error) {
	backend, ok := r.Get(kind)
	if !ok {
		return nil, fmt.Errorf("ticket backend %q is not registered", kind)
	}
	return backend, nil
}

// LocalBackend preserves current SQLite-local behavior. It is a no-op sync
// participant so startup/periodic sync can run uniformly for every board.
type LocalBackend struct{}

func (LocalBackend) Kind() string { return KindLocal }

func (LocalBackend) Sync(context.Context, *storage.Store, storage.Board) (Result, error) {
	return Result{}, nil
}

// Manager runs startup and in-process periodic sync. It intentionally does not
// create a daemon; syncing stops when the executable exits.
type Manager struct {
	Store    *storage.Store
	Registry *Registry
	Interval time.Duration
}

func NewManager(store *storage.Store) *Manager {
	return &Manager{Store: store, Registry: DefaultRegistry(), Interval: time.Minute}
}

func (m *Manager) SyncAll(ctx context.Context) error {
	if m == nil || m.Store == nil {
		return nil
	}
	registry := m.Registry
	if registry == nil {
		registry = DefaultRegistry()
	}
	boards, err := m.Store.ListBoards(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, board := range boards {
		kind := board.TicketBackend
		if kind == "" {
			kind = KindLocal
		}
		backend, err := registry.MustGet(kind)
		if err != nil {
			_ = m.Store.MarkBoardSync(ctx, board.ID, err)
			errs = append(errs, err)
			continue
		}
		_, syncErr := backend.Sync(ctx, m.Store, board)
		if markErr := m.Store.MarkBoardSync(ctx, board.ID, syncErr); markErr != nil {
			errs = append(errs, markErr)
		}
		if syncErr != nil {
			errs = append(errs, syncErr)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) Start(ctx context.Context) func() {
	interval := m.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.SyncAll(ctx)
			}
		}
	}()
	return cancel
}
