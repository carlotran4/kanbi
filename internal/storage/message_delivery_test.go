package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationClaimFencesProcessesAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err = first.Init(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := messageEndpoints(t, first, ctx)
	m, err := first.SendSessionMessage(ctx, a, b, "one", "work", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = second.Init(ctx); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := first
			if i%2 == 0 {
				store = second
			}
			claimed, err := store.ClaimMessageNotification(ctx, b, m.ID, "test")
			if err != nil {
				t.Error(err)
			}
			if claimed {
				count.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("duplicate notification claim", count.Load())
	}
	if err = second.Init(ctx); err != nil {
		t.Fatal(err)
	}
	claimed, err := second.ClaimMessageNotification(ctx, b, m.ID, "test")
	if err != nil || claimed {
		t.Fatal("uncertain handoff automatically repeated", claimed, err)
	}
	blocked, err := second.SendSessionMessage(ctx, a, b, "two", "more", 0)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = second.ClaimMessageNotification(ctx, b, blocked.ID, "test")
	if err != nil || claimed {
		t.Fatal("outstanding wake did not apply backpressure", claimed, err)
	}
	if err = second.ReceiptMessageRead(ctx, b, m.ID); err != nil {
		t.Fatal(err)
	}
	current, _ := second.SessionMessage(ctx, m.ID)
	if current.State != "notified" || current.NotificationStatus != "notified" {
		t.Fatal(current)
	}
	claimed, err = second.ClaimMessageNotification(ctx, b, blocked.ID, "test")
	if err != nil || !claimed {
		t.Fatal("positive receipt did not release the next handoff", claimed, err)
	}
	if err = second.AcknowledgeSessionMessage(ctx, b, m.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = second.ClaimMessageNotification(ctx, b, blocked.ID, "test")
	if err != nil || claimed {
		t.Fatal("ack caused a duplicate handoff", claimed, err)
	}
}
func TestNotificationEligibilityAndExplicitRetry(t *testing.T) {
	s, ctx := newTestStore(t)
	a, b := messageEndpoints(t, s, ctx)
	m, err := s.SendSessionMessage(ctx, a, b, "key", "work", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeferMessageNotification(ctx, b, m.ID, "native", "busy"); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimMessageNotification(ctx, b, m.ID, "test")
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if err = s.RetryMessageNotification(ctx, a, m.ID); err == nil {
		t.Fatal("retried in-flight notification")
	}
	if _, err = s.db.ExecContext(ctx, `update session_messages set notification_attempted_at=? where id=?`, time.Now().UTC().Add(-time.Minute), m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryMessageNotification(ctx, 99999, m.ID); err == nil {
		t.Fatal("nonparticipant retried")
	}
	if err = s.RetryMessageNotification(ctx, a, m.ID); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"starting", "closing"} {
		if err = s.UpdateSessionRuntime(ctx, b, state, "test", "", "", false); err != nil {
			t.Fatal(err)
		}
		claimed, err = s.ClaimMessageNotification(ctx, b, m.ID, "test")
		if err != nil || claimed {
			t.Fatal(state, claimed, err)
		}
	}
	if err = s.MarkSessionClosed(ctx, b, "closed", "test", ""); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimMessageNotification(ctx, b, m.ID, "test")
	if err != nil || claimed {
		t.Fatal("closed", claimed, err)
	}
}
func TestMessageDeliveryMigrationPreservesExistingMessages(t *testing.T) {
	s, ctx := newTestStore(t)
	a, b := messageEndpoints(t, s, ctx)
	m, err := s.SendSessionMessage(ctx, a, b, "key", "history", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AcknowledgeSessionMessage(ctx, b, m.ID); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"notification_status", "notification_transport", "notification_error", "notification_attempted_at"} {
		if _, err = s.db.ExecContext(ctx, `alter table session_messages drop column `+column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.ExecContext(ctx, `delete from schema_migrations where version=12`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Init(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.SessionMessage(ctx, m.ID)
	if err != nil || got.Body != "history" || got.State != "acknowledged" || got.NotificationStatus != "notified" {
		t.Fatal(fmt.Sprint(got), err)
	}
}
