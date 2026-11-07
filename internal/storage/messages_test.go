package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func messageEndpoints(t *testing.T, s *Store, ctx context.Context) (int64, int64) {
	t.Helper()
	v := defaultBoardView(t, ctx, s)
	ids := []int64{}
	for _, h := range []string{"codex", "pi"} {
		ticket := createTicket(t, ctx, s, v.Columns[0].ID, h, "", h)
		id, err := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: h, Multiplexer: "herdr", Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids[0], ids[1]
}
func TestSessionMailboxRestartReplyAndStates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := messageEndpoints(t, s, ctx)
	m, err := s.SendSessionMessage(ctx, a, b, "schema-v1", "schema ready", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	inbox, err := s.SessionInbox(ctx, b, false)
	if err != nil || len(inbox) != 1 || inbox[0].State != "queued" {
		t.Fatal(inbox, err)
	}
	retry, err := s.SendSessionMessage(ctx, a, b, "schema-v1", "schema ready", 0)
	if err != nil || retry.ID != m.ID {
		t.Fatal(retry, err)
	}
	if _, err = s.SendSessionMessage(ctx, a, b, "schema-v1", "different", 0); err == nil {
		t.Fatal("key conflict accepted")
	}
	if err = s.MarkMessageNotified(ctx, a, m.ID); err == nil {
		t.Fatal("wrong recipient notified")
	}
	if err = s.MarkMessageNotified(ctx, b, m.ID); err != nil {
		t.Fatal(err)
	}
	observed, _ := s.SessionMessage(ctx, m.ID)
	if observed.State != "notified" {
		t.Fatal(observed)
	}
	if err = s.AcknowledgeSessionMessage(ctx, a, m.ID); err == nil {
		t.Fatal("wrong recipient acknowledged")
	}
	if err = s.AcknowledgeSessionMessage(ctx, b, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkMessageNotified(ctx, b, m.ID); err != nil {
		t.Fatal(err)
	}
	observed, _ = s.SessionMessage(ctx, m.ID)
	if observed.State != "acknowledged" {
		t.Fatal("state regressed")
	}
	reply, err := s.SendSessionMessage(ctx, b, 0, "reply-1", "updated", m.ID)
	if err != nil || reply.Recipient != a || reply.Depth != 1 {
		t.Fatal(reply, err)
	}
	if _, err = s.SendSessionMessage(ctx, a, 0, "forged", "no", m.ID); err == nil {
		t.Fatal("non-recipient replied")
	}
	for depth := 2; depth <= 8; depth++ {
		reply, err = s.SendSessionMessage(ctx, reply.Recipient, 0, fmt.Sprint("chain-", depth), "bounded", reply.ID)
		if err != nil {
			t.Fatal(depth, err)
		}
	}
	if _, err = s.SendSessionMessage(ctx, reply.Recipient, 0, "too-deep", "no", reply.ID); err == nil {
		t.Fatal("loop limit bypassed")
	}
}
func TestSessionMailboxConcurrentRetriesAndClosedRecipient(t *testing.T) {
	s, ctx := newTestStore(t)
	a, b := messageEndpoints(t, s, ctx)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.SendSessionMessage(ctx, a, b, "same", "one", 0); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	inbox, err := s.SessionInbox(ctx, b, false)
	if err != nil || len(inbox) != 1 {
		t.Fatal(inbox, err)
	}
	if err = s.MarkSessionClosed(ctx, b, "closed", "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendSessionMessage(ctx, a, b, "offline", "saved offline", 0); err != nil {
		t.Fatal(err)
	}
	pending, err := s.SessionNotificationInbox(ctx, b)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	if err = s.MarkMessageNotified(ctx, b, inbox[0].ID); err == nil {
		t.Fatal("closed recipient notified")
	}
	inbox, err = s.SessionInbox(ctx, b, false)
	if err != nil || len(inbox) != 2 {
		t.Fatal(inbox, err)
	}
	if _, err = s.SendSessionMessage(ctx, b, a, "closed-sender", "no", 0); err == nil {
		t.Fatal("closed sender sent")
	}
	if _, err = s.SendSessionMessage(ctx, a, 999999, "missing", "no", 0); err == nil {
		t.Fatal("missing recipient accepted")
	}
	if _, err = s.SendSessionMessage(ctx, a, a, "self", "no", 0); err == nil {
		t.Fatal("self-loop accepted")
	}
}
func TestSessionMailboxPauseAndFreshAttempt(t *testing.T) {
	s, ctx := newTestStore(t)
	a, b := messageEndpoints(t, s, ctx)
	m, err := s.SendSessionMessage(ctx, a, b, "draft", "queued", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `update tickets set focus_paused=1 where id=(select ticket_id from sessions where id=?)`, b); err != nil {
		t.Fatal(err)
	}
	pending, err := s.SessionNotificationInbox(ctx, b)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	if err = s.MarkMessageNotified(ctx, b, m.ID); err == nil {
		t.Fatal("paused recipient notified")
	}
	old, _, _ := s.SessionByID(ctx, b)
	fresh, err := s.UpsertActiveSession(ctx, old.TicketID, Session{Harness: "pi", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := s.SessionInbox(ctx, fresh, false)
	if err != nil || len(inbox) != 0 {
		t.Fatal("mail silently moved", inbox, err)
	}
	inbox, err = s.SessionInbox(ctx, b, false)
	if err != nil || len(inbox) != 1 {
		t.Fatal("history lost", inbox, err)
	}
}

func TestSessionMailboxMigrationFromV10PreservesHistory(t *testing.T) {
	s, ctx := newTestStore(t)
	a, b := messageEndpoints(t, s, ctx)
	if _, err := s.db.ExecContext(ctx, `drop table session_messages; delete from schema_migrations where version>=11`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Init(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SendSessionMessage(ctx, a, b, "migrated", "history retained", 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `select count(*) from sessions`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestSessionMailboxRetainsCrossBoardCorrespondence(t *testing.T) {
	s, ctx := newTestStore(t)
	a, _ := messageEndpoints(t, s, ctx)
	board, err := s.CreateBoardWithWorkdir(ctx, "Other board", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	view := boardViewByID(t, ctx, s, board.ID)
	ticket := createTicket(t, ctx, s, view.Columns[0].ID, "recipient", "", "pi")
	b, err := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.SendSessionMessage(ctx, a, b, "cross-board", "retained", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkSessionClosed(ctx, b, "closed", "test", ""); err != nil {
		t.Fatal(err)
	}
	recipient, err := s.MessageRecipient(ctx, b)
	if err != nil || recipient.Delivery != "closed_unresumable" {
		t.Fatal(recipient, err)
	}
	if err = s.DeleteBoard(ctx, board.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := s.SessionMessage(ctx, m.ID)
	if err != nil || retained.Body != "retained" || retained.Sender != a {
		t.Fatal(retained, err)
	}
	if _, err = s.SendSessionMessage(ctx, a, b, "after-delete", "no", 0); err == nil {
		t.Fatal("deleted recipient accepted")
	}
}
