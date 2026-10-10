package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestAsyncSessionRefCaptureUpdatesExactInsertedAttempt(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, "Attempt binding", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "test", TmuxWindowName: "first", Status: "running",
	})
	if err != nil {
		t.Fatal(err)
	}

	manager := NewManager(config.Config{}, store)
	manager.RefCapturePollInterval = time.Millisecond
	t.Cleanup(manager.Close)
	ready := make(chan struct{})
	manager.startSessionRefCapture(firstID, "pi", time.Second, func() (string, bool) {
		select {
		case <-ready:
			return "ref-for-first-attempt", true
		default:
			return "", false
		}
	})

	if err := store.MarkSessionClosed(ctx, firstID, "closed", "test", "new attempt started"); err != nil {
		t.Fatal(err)
	}
	secondID, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "test", TmuxWindowName: "second", Status: "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	close(ready)

	waitForSessionRef(t, store, firstID, "ref-for-first-attempt")
	second, ok, err := store.LatestSession(ctx, ticket.ID)
	if err != nil || !ok {
		t.Fatalf("latest session: ok=%v err=%v", ok, err)
	}
	if second.ID != secondID {
		t.Fatalf("latest session id=%d, want %d", second.ID, secondID)
	}
	if second.HarnessSessionRef.Valid {
		t.Fatalf("new attempt received old ref %q", second.HarnessSessionRef.String)
	}
}

func TestManagerCloseCancelsSessionRefCapture(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Cancel capture", "", "pi")
	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "test", TmuxWindowName: "capture", Status: "running",
	})
	manager := NewManager(config.Config{}, store)
	manager.RefCapturePollInterval = time.Millisecond
	manager.startSessionRefCapture(sessionID, "pi", time.Minute, func() (string, bool) { return "", false })

	done := make(chan struct{})
	go func() {
		manager.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Manager.Close did not cancel and join capture")
	}
}

func TestAsyncSessionRefPersistenceFailureIsReported(t *testing.T) {
	store, ctx := newRuntimeTestStore(t)
	view := defaultBoardView(t, ctx, store)
	ticket, _ := store.CreateTicket(ctx, view.Columns[0].ID, "Report failure", "", "pi")
	sessionID, _ := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{
		Harness: "pi", TmuxSessionName: "test", TmuxWindowName: "capture", Status: "running",
	})
	manager := NewManager(config.Config{}, store)
	manager.RefCapturePollInterval = time.Millisecond
	errCh := make(chan error, 1)
	manager.BackgroundError = func(err error) { errCh <- err }
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	manager.startSessionRefCapture(sessionID, "pi", time.Second, func() (string, bool) {
		return "captured-ref", true
	})
	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "session ref") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("background persistence error was not reported")
	}
	manager.Close()
}

func waitForSessionRef(t *testing.T, store *storage.Store, sessionID int64, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ses, ok, err := store.SessionByID(context.Background(), sessionID)
		if err == nil && ok && ses.HarnessSessionRef.Valid && ses.HarnessSessionRef.String == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session %d did not receive ref %q", sessionID, want)
}
