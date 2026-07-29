package ticketbackend

import (
	"context"
	"database/sql"
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
	UpsertRemoteTicketIfUnchanged(context.Context, storage.RemoteTicket, time.Time) (storage.Ticket, bool, error)
	ListNotes(context.Context, int64) ([]storage.Note, error)
	ListNotesForSync(context.Context, int64) ([]storage.Note, error)
	UpsertRemoteNote(context.Context, int64, string, string, time.Time) error
	LinkLocalNoteToRemote(context.Context, int64, string, time.Time) error
	MarkTicketRemotePushPending(context.Context, int64, string) error
	ClearTicketRemotePush(context.Context, int64) error
	MarkTicketRemotePushFailed(context.Context, int64, string) error
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
//
// Manager owns mutation-triggered, startup, and periodic board sync work. Call
// ScheduleBoardSync from use-case code; call the stop func returned by Start to
// cancel and drain in-flight syncs on shutdown.
type Manager struct {
	Store    *storage.Store
	Registry *Registry
	Interval time.Duration

	locksMu    sync.Mutex
	boardLocks map[int64]*sync.Mutex
	Owner      string
	LeaseTTL   time.Duration

	lifeMu    sync.Mutex
	runCtx    context.Context
	stop      context.CancelFunc
	closed    bool
	scheduled map[int64]bool // board id -> one follow-up sync requested while worker is active
	wg        sync.WaitGroup // all owned background work including loop + scheduled ops
}

func NewManager(store *storage.Store) *Manager {
	return newManager(store, nil)
}

// NewManagerWithContext binds mutation-triggered work to ctx without starting
// startup or periodic sync. It is for short-lived command paths whose caller
// must be able to cancel scheduled provider work during shutdown.
func NewManagerWithContext(ctx context.Context, store *storage.Store) *Manager {
	return newManager(store, ctx)
}

func newManager(store *storage.Store, runCtx context.Context) *Manager {
	return &Manager{Store: store, Registry: DefaultRegistry(), Interval: time.Minute, boardLocks: map[int64]*sync.Mutex{}, Owner: fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid()), LeaseTTL: 5 * time.Minute, runCtx: runCtx}
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
	// Prefer freshest board metadata when caller only passed an ID/name snapshot.
	if m.Store != nil && board.ID != 0 {
		if fresh, err := m.Store.BoardByID(ctx, board.ID); err == nil {
			board = fresh
		}
	}
	if board.ArchivedAt.Valid || !board.SyncEnabled {
		return Result{}, storage.ErrBoardSyncSkipped
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
	// Re-check after lease acquisition. This closes the race where a board is
	// archived between the initial snapshot and acquiring the durable lease.
	fresh, err := m.Store.BoardByID(ctx, board.ID)
	if err != nil {
		_ = m.Store.ReleaseBoardSyncLease(context.WithoutCancel(ctx), board.ID, owner)
		return Result{}, err
	}
	if fresh.ArchivedAt.Valid || !fresh.SyncEnabled {
		_ = m.Store.ReleaseBoardSyncLease(context.WithoutCancel(ctx), board.ID, owner)
		return Result{}, storage.ErrBoardSyncSkipped
	}
	board = fresh
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
	if syncErr != nil && !errors.Is(syncErr, storage.ErrBoardSyncInProgress) {
		m.recordSyncDiagnostic(board.ID, 0, "sync_board", 1, syncErr)
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
		if board.ArchivedAt.Valid || !board.SyncEnabled {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.SyncBoard(ctx, board); err != nil {
				if errors.Is(err, storage.ErrBoardSyncSkipped) {
					return
				}
				errsMu.Lock()
				errs = append(errs, fmt.Errorf("sync board %q: %w", board.Name, err))
				errsMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// Start launches startup + periodic sync owned by Manager. The returned stop
// function cancels the run context and waits for the loop and all scheduled
// SyncBoard operations to finish. Start is safe for one active run per Manager.
func (m *Manager) Start(ctx context.Context) func() {
	if m == nil {
		return func() {}
	}
	interval := m.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.lifeMu.Lock()
	if m.closed {
		m.lifeMu.Unlock()
		cancel()
		return func() {}
	}
	m.runCtx = runCtx
	m.stop = cancel
	m.closed = false
	m.wg.Add(1)
	m.lifeMu.Unlock()

	go func() {
		defer m.wg.Done()
		_ = m.SyncAll(runCtx)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				_ = m.SyncAll(runCtx)
			}
		}
	}()
	return func() {
		m.Stop()
	}
}

// Stop cancels owned sync work and waits for drain. Safe to call more than once.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.lifeMu.Lock()
	if m.stop != nil {
		m.stop()
	}
	m.closed = true
	m.lifeMu.Unlock()
	m.wg.Wait()
}

// ScheduleBoardSync queues a board sync owned by this Manager. The work uses
// the Manager run context when Start is active, otherwise a detached background
// context tied only to process lifetime for short CLI paths. After Stop, calls
// are no-ops.
func (m *Manager) ScheduleBoardSync(boardID int64) {
	if m == nil || m.Store == nil || boardID == 0 {
		return
	}
	m.lifeMu.Lock()
	if m.closed {
		m.lifeMu.Unlock()
		return
	}
	if m.scheduled == nil {
		m.scheduled = map[int64]bool{}
	}
	if _, running := m.scheduled[boardID]; running {
		m.scheduled[boardID] = true
		m.lifeMu.Unlock()
		return
	}
	m.scheduled[boardID] = false
	parent := m.runCtx
	if parent == nil {
		parent = context.Background()
	}
	m.wg.Add(1)
	m.lifeMu.Unlock()

	go func() {
		defer m.wg.Done()
		for {
			ctx := parent
			board, err := m.Store.BoardByID(ctx, boardID)
			if err != nil {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, sql.ErrNoRows) {
					m.recordSyncDiagnostic(boardID, 0, "schedule_board_by_id", 1, err)
				}
			} else if _, err := m.SyncBoard(ctx, board); err != nil && !errors.Is(err, storage.ErrBoardSyncInProgress) && !errors.Is(err, storage.ErrBoardSyncSkipped) {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					m.recordSyncDiagnostic(board.ID, 0, "schedule_sync_board", 1, err)
				}
			}

			m.lifeMu.Lock()
			rerun := m.scheduled[boardID] && !m.closed && parent.Err() == nil
			if rerun {
				m.scheduled[boardID] = false
			} else {
				delete(m.scheduled, boardID)
			}
			m.lifeMu.Unlock()
			if !rerun {
				return
			}
		}
	}()
}

func (m *Manager) recordSyncDiagnostic(boardID, ticketID int64, operation string, attempt int, err error) {
	if m == nil || m.Store == nil || err == nil {
		return
	}
	// Diagnostics must outlive a canceled request context.
	ctx := context.Background()
	_ = m.Store.InsertRuntimeDiagnostic(ctx, storage.RuntimeDiagnosticInput{
		Kind:      storage.DiagnosticKindSync,
		Operation: operation,
		BoardID:   boardID,
		TicketID:  ticketID,
		Attempt:   attempt,
		Message:   "provider sync degraded (local data available)",
		Cause:     err.Error(),
	})
}
