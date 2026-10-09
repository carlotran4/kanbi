package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

// A file-backed Master fixture verifies that the production read-only reader
// sees commits from a separate writer, including heartbeat-only invalidations.
func TestPerformanceFileProjection(t *testing.T) {
	if os.Getenv("KANBI_PERFORMANCE") != "1" {
		t.Skip("opt-in file projection measurements")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "file-projection.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var first storage.Ticket
	for i := range 2000 {
		ticket, err := store.CreateTicket(ctx, view.Columns[i%len(view.Columns)].ID, fmt.Sprintf("File ticket %d", i), strings.Repeat("body ", 1640), "pi")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = ticket
		}
	}
	writer, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	m := New(ctx, NewService(store, nil))
	defer func() {
		if closer, ok := any(m).(interface{ Close() }); ok {
			closer.Close()
		}
	}()
	m.masterBoard = true
	measurePerformance(t, "file-master-projection-warm", 60, func() { m.reload() })
	iteration := 0
	measurePerformance(t, "file-master-projection-external-write", 30, func() {
		iteration++
		if err := writer.UpdateTicket(ctx, first.ID, fmt.Sprintf("File revision %d", iteration), first.Body, first.Harness); err != nil {
			t.Fatal(err)
		}
		m.reload()
	})
	sid, err := writer.UpsertActiveSession(ctx, first.ID, storage.Session{Harness: "pi", TmuxSessionName: "fixture", TmuxWindowName: "ticket", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	measurePerformance(t, "file-master-projection-heartbeat", 30, func() {
		if err := writer.UpdateSessionRuntime(ctx, sid, "running", "test", "", "", false); err != nil {
			t.Fatal(err)
		}
		m.reload()
	})
}
