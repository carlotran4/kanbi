package ticketbackend

import (
	"context"
	"testing"
	"time"

	"kanbi/internal/storage"
)

type recordingBackend struct {
	kind   string
	boards []int64
}

func (b *recordingBackend) Kind() string { return b.kind }
func (b *recordingBackend) Sync(ctx context.Context, store *storage.Store, board storage.Board) (Result, error) {
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
func (b *blockingBackend) Sync(ctx context.Context, store *storage.Store, board storage.Board) (Result, error) {
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
