package tui

import (
	"context"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

func newTestStore(t *testing.T) (*storage.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return store, ctx
}

func newTestModel(t *testing.T) (Model, *storage.Store, context.Context) {
	t.Helper()
	store, ctx := newTestStore(t)
	return New(ctx, NewService(store, nil)), store, ctx
}

func defaultBoardView(t *testing.T, ctx context.Context, store *storage.Store) storage.BoardView {
	t.Helper()
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func createTicket(t *testing.T, ctx context.Context, store *storage.Store, columnID int64, title, body, harness string) storage.Ticket {
	t.Helper()
	ticket, err := store.CreateTicket(ctx, columnID, title, body, harness)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}
