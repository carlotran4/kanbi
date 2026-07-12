package ticketbackend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
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

// SyncRepository is the persistence surface available to ticket metadata
// providers. Board discovery and sync bookkeeping remain Manager concerns.
type SyncRepository interface {
	SyncBoardColumns(context.Context, int64, []string) (map[string]int64, error)
	SyncTicketsForBoard(context.Context, int64) ([]storage.Ticket, error)
	UpsertRemoteTicket(context.Context, storage.RemoteTicket) (storage.Ticket, error)
	ListNotes(context.Context, int64) ([]storage.Note, error)
	ListNotesForSync(context.Context, int64) ([]storage.Note, error)
	UpsertRemoteNote(context.Context, int64, string, string, time.Time) error
	LinkLocalNoteToRemote(context.Context, int64, string, time.Time) error
}

var _ SyncRepository = (*storage.Store)(nil)

// Backend is implemented once per ticket metadata provider. A board has exactly
// one Backend, selected at board creation. Implementations should sync columns,
// tickets, and comments/notes according to the remote source's model. When the
// remote system offers a query language, Board.BackendQuery scopes the synced
// subset. GitHub uses URL query parameters for the Issues list API (state,
// labels, assignee, mentioned, milestone, since); Atlassian/Jira must use JQL.
type Backend interface {
	Kind() string
	Sync(ctx context.Context, store SyncRepository, board storage.Board) (Result, error)
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
	return NewRegistry(LocalBackend{}, GitHubBackend{}, JiraBackend{})
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

func (LocalBackend) Sync(context.Context, SyncRepository, storage.Board) (Result, error) {
	return Result{}, nil
}

// Manager runs startup and in-process periodic sync. It intentionally does not
// create a daemon; syncing stops when the executable exits.
type Manager struct {
	Store    *storage.Store
	Registry *Registry
	Interval time.Duration

	locksMu    sync.Mutex
	boardLocks map[int64]*sync.Mutex
	Owner      string
	LeaseTTL   time.Duration
}

func NewManager(store *storage.Store) *Manager {
	return &Manager{Store: store, Registry: DefaultRegistry(), Interval: time.Minute, boardLocks: map[int64]*sync.Mutex{}, Owner: fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid()), LeaseTTL: 5 * time.Minute}
}

func (m *Manager) boardLock(boardID int64) *sync.Mutex {
	m.locksMu.Lock()
	defer m.locksMu.Unlock()
	if m.boardLocks == nil {
		m.boardLocks = map[int64]*sync.Mutex{}
	}
	if m.boardLocks[boardID] == nil {
		m.boardLocks[boardID] = &sync.Mutex{}
	}
	return m.boardLocks[boardID]
}

func (m *Manager) SyncBoard(ctx context.Context, board storage.Board) (Result, error) {
	if m == nil || m.Store == nil {
		return Result{}, nil
	}
	lock := m.boardLock(board.ID)
	lock.Lock()
	defer lock.Unlock()
	owner := m.Owner
	if owner == "" {
		owner = fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	ttl := m.LeaseTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	acquired, err := m.Store.AcquireBoardSyncLease(ctx, board.ID, owner, ttl)
	if err != nil {
		return Result{}, err
	}
	if !acquired {
		return Result{}, storage.ErrBoardSyncInProgress
	}
	syncCtx, cancelSync := context.WithCancel(ctx)
	renewCtx, stopRenew := context.WithCancel(context.Background())
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		interval := ttl / 3
		if interval <= 0 {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				ok, err := m.Store.RenewBoardSyncLease(renewCtx, board.ID, owner, ttl)
				if err != nil || !ok {
					cancelSync()
					return
				}
			}
		}
	}()
	defer func() {
		cancelSync()
		stopRenew()
		<-renewDone
		_ = m.Store.ReleaseBoardSyncLease(context.WithoutCancel(ctx), board.ID, owner)
	}()

	registry := m.Registry
	if registry == nil {
		registry = DefaultRegistry()
	}
	kind := board.TicketBackend
	if kind == "" {
		kind = KindLocal
	}
	backend, err := registry.MustGet(kind)
	if err != nil {
		_ = m.Store.MarkBoardSync(ctx, board.ID, err)
		return Result{}, err
	}
	res, syncErr := backend.Sync(syncCtx, m.Store, board)
	if markErr := m.Store.MarkBoardSync(ctx, board.ID, syncErr); markErr != nil {
		return res, errors.Join(syncErr, markErr)
	}
	return res, syncErr
}

func (m *Manager) SyncAll(ctx context.Context) error {
	if m == nil || m.Store == nil {
		return nil
	}
	boards, err := m.Store.ListBoards(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	var errsMu sync.Mutex
	var errs []error
	for _, board := range boards {
		board := board
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.SyncBoard(ctx, board); err != nil {
				errsMu.Lock()
				errs = append(errs, fmt.Errorf("sync board %q: %w", board.Name, err))
				errsMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (m *Manager) Start(ctx context.Context) func() {
	interval := m.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.SyncAll(ctx)

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
	return func() {
		cancel()
		<-done
	}
}
