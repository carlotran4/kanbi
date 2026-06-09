package ticketbackend

import (
	"context"
	"testing"
	"time"

	"agent-kanban/internal/storage"
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

func TestDefaultRegistryImplementsLocalAndGitHub(t *testing.T) {
	registry := DefaultRegistry()
	if _, ok := registry.Get(KindLocal); !ok {
		t.Fatal("local backend missing")
	}
	if _, ok := registry.Get(KindGitHub); !ok {
		t.Fatal("github backend missing")
	}
	if _, ok := registry.Get(KindAtlassian); ok {
		t.Fatal("atlassian should not be implemented yet")
	}
}
