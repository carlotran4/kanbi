package ticketbackend

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"kanbi/internal/storage"
)

type recordingBackend struct {
	kind   string
	boards []int64
}

func (b *recordingBackend) Kind() string { return b.kind }
func (b *recordingBackend) Sync(ctx context.Context, store SyncRepository, board storage.Board) (Result, error) {
	b.boards = append(b.boards, board.ID)
	return Result{Pulled: 1}, nil
}

type blockingBackend struct {
	kind    string
	started chan int64
	release chan struct{}
	done    chan struct{}
}

func (b *blockingBackend) Kind() string { return b.kind }
func (b *blockingBackend) Sync(ctx context.Context, store SyncRepository, board storage.Board) (Result, error) {
	defer close(b.done)
	select {
	case b.started <- board.ID:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	select {
	case <-b.release:
		return Result{Pulled: 1}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func TestManagerSyncAllDispatchesByBoardBackend(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	backend := &recordingBackend{kind: KindGitHub}
	if _, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote", Workdir: t.TempDir(), TicketBackend: KindGitHub}); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{Store: store, Registry: NewRegistry(LocalBackend{}, backend), Interval: time.Hour}
	if err := manager.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(backend.boards) != 1 {
		t.Fatalf("github backend called for %d boards, want 1", len(backend.boards))
	}
}

func TestManagerLeaseSerializesSameBoardAcrossStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanbi.db")
	firstStore, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer firstStore.Close()
	if err := firstStore.Init(ctx); err != nil {
		t.Fatal(err)
	}
	board, err := firstStore.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote", Workdir: t.TempDir(), TicketBackend: KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	blocking := &gatedBackend{kind: KindGitHub, started: make(chan int64, 1), release: make(chan struct{})}
	first := NewManager(firstStore)
	first.Registry = NewRegistry(LocalBackend{}, blocking)
	secondBackend := &recordingBackend{kind: KindGitHub}
	second := NewManager(secondStore)
	second.Registry = NewRegistry(LocalBackend{}, secondBackend)
	done := make(chan error, 1)
	go func() { _, err := first.SyncBoard(ctx, board); done <- err }()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("first sync did not start")
	}
	if _, err := second.SyncBoard(ctx, board); !errors.Is(err, storage.ErrBoardSyncInProgress) {
		t.Fatalf("second sync error=%v", err)
	}
	if len(secondBackend.boards) != 0 {
		t.Fatal("second provider ran despite lease")
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := second.SyncBoard(ctx, board); err != nil {
		t.Fatal(err)
	}
	if len(secondBackend.boards) != 1 {
		t.Fatal("lease was not released")
	}
}

func TestManagerStartRunsInitialSyncAsynchronously(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	remote, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote", Workdir: t.TempDir(), TicketBackend: KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	backend := &blockingBackend{
		kind:    KindGitHub,
		started: make(chan int64, 1),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	manager := &Manager{Store: store, Registry: NewRegistry(LocalBackend{}, backend), Interval: time.Hour}

	startedAt := time.Now()
	stop := manager.Start(ctx)
	defer stop()
	if elapsed := time.Since(startedAt); elapsed > 50*time.Millisecond {
		t.Fatalf("Start blocked for %s; initial sync should run in the background", elapsed)
	}

	select {
	case boardID := <-backend.started:
		if boardID != remote.ID {
			t.Fatalf("initial sync board id = %d, want %d", boardID, remote.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("initial sync did not start")
	}
	close(backend.release)
	select {
	case <-backend.done:
	case <-time.After(time.Second):
		t.Fatal("initial sync did not finish")
	}
}

type gatedBackend struct {
	kind    string
	started chan int64
	release chan struct{}
	err     error
}

func (b *gatedBackend) Kind() string { return b.kind }
func (b *gatedBackend) Sync(ctx context.Context, _ SyncRepository, board storage.Board) (Result, error) {
	select {
	case b.started <- board.ID:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return Result{}, b.err
}

func TestManagerSyncAllRunsDifferentBoardsIndependently(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote A", Workdir: t.TempDir(), TicketBackend: KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote B", Workdir: t.TempDir(), TicketBackend: KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	backend := &gatedBackend{kind: KindGitHub, started: make(chan int64, 2), release: make(chan struct{})}
	manager := &Manager{Store: store, Registry: NewRegistry(LocalBackend{}, backend)}
	done := make(chan error, 1)
	go func() { done <- manager.SyncAll(ctx) }()

	seen := map[int64]bool{}
	for len(seen) < 2 {
		select {
		case id := <-backend.started:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatalf("unrelated board syncs were serialized; started=%v", seen)
		}
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("started boards=%v, want %d and %d", seen, first.ID, second.ID)
	}
	close(backend.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerStartPersistsPeriodicSyncErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	remote, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Failing Remote", Workdir: t.TempDir(), TicketBackend: KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	backend := &gatedBackend{kind: KindGitHub, started: make(chan int64, 4), err: errors.New("provider unavailable")}
	manager := &Manager{Store: store, Registry: NewRegistry(LocalBackend{}, backend), Interval: 10 * time.Millisecond}
	stop := manager.Start(ctx)
	defer stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		board, err := store.BoardByName(ctx, remote.Name)
		if err != nil {
			t.Fatal(err)
		}
		if board.LastSyncError.Valid && board.LastSyncError.String == "provider unavailable" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background sync error was not persisted for board/CLI/TUI reporting")
}

func TestManagerSerializesRepeatedSyncsForSameBoard(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	remote, err := store.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Remote", Workdir: t.TempDir(), TicketBackend: KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	backend := &gatedBackend{kind: KindGitHub, started: make(chan int64, 2), release: make(chan struct{})}
	manager := &Manager{Store: store, Registry: NewRegistry(LocalBackend{}, backend)}
	done := make(chan error, 2)
	go func() { _, err := manager.SyncBoard(ctx, remote); done <- err }()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("first sync did not start")
	}
	go func() { _, err := manager.SyncBoard(ctx, remote); done <- err }()
	select {
	case <-backend.started:
		t.Fatal("same-board sync started before prior sync completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(backend.release)
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("second same-board sync did not start after release")
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestDefaultRegistryImplementsImplementedBackends(t *testing.T) {
	registry := DefaultRegistry()
	if _, ok := registry.Get(KindLocal); !ok {
		t.Fatal("local backend missing")
	}
	if _, ok := registry.Get(KindGitHub); !ok {
		t.Fatal("github backend missing")
	}
	if _, ok := registry.Get(KindAtlassian); !ok {
		t.Fatal("atlassian backend missing")
	}
}
