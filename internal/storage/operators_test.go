package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func operatorFixture(t *testing.T) (*Store, context.Context, Operator) {
	t.Helper()
	s, ctx := newTestStore(t)
	o, e := s.CreateOperator(ctx, Operator{Name: "Intake", Harness: "copilot", CWD: t.TempDir(), Timezone: "UTC"})
	if e != nil {
		t.Fatal(e)
	}
	return s, ctx, o
}
func TestOperatorIdentityAndHistory(t *testing.T) {
	s, ctx, o := operatorFixture(t)
	a, _ := messageEndpoints(t, s, ctx)
	msg, e := s.SendSessionMessage(ctx, a, -o.ID, "request", "inspect all boards", 0)
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.ClaimOperatorSession(ctx, o.ID, Session{Harness: o.Harness, LaunchCWD: sql.NullString{String: o.CWD, Valid: true}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimOperatorSession(ctx, o.ID, Session{Harness: o.Harness}); e == nil {
		t.Fatal("duplicate active operator attempt")
	}
	if e = s.MarkSessionClosed(ctx, first, "closed", "test", ""); e != nil {
		t.Fatal(e)
	}
	second, e := s.ClaimOperatorSession(ctx, o.ID, Session{Harness: o.Harness})
	if e != nil || second == first {
		t.Fatal(second, e)
	}
	inbox, e := s.SessionInbox(ctx, -o.ID, false)
	if e != nil || len(inbox) != 1 || inbox[0].ID != msg.ID {
		t.Fatal(inbox, e)
	}
	reply, e := s.SendSessionMessage(ctx, -o.ID, 0, "reply", "found related work", msg.ID)
	if e != nil || reply.Recipient != a {
		t.Fatal(reply, e)
	}
	old, ok, e := s.SessionByID(ctx, first)
	if e != nil || !ok || old.IsActive || old.OperatorID != o.ID || old.TicketID != 0 {
		t.Fatal(old, e)
	}
	o.Harness = "pi"
	if e = s.UpdateOperator(ctx, o); e == nil {
		t.Fatal("harness changed")
	}
	if e = s.DeleteOperator(ctx, o.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SendSessionMessage(ctx, -o.ID, a, "retired-sender", "reply", 0); e == nil {
		t.Fatal("retired Operator address sent mail")
	}
	if _, e = s.SendSessionMessage(ctx, a, -o.ID, "after-delete", "wake", 0); e == nil {
		t.Fatal("deleted address accepted")
	}
	active, e := s.ActiveRuntimeTickets(ctx)
	if e != nil {
		t.Fatal(e)
	}
	observed := false
	for _, tkt := range active {
		if tkt.ID == -o.ID {
			observed = true
		}
	}
	if !observed {
		t.Fatal("retired Operator's retained terminal was dropped from observation")
	}
	old, ok, e = s.SessionByID(ctx, second)
	if e != nil || !ok || !old.IsActive {
		t.Fatal("delete terminated attempt", old, e)
	}
	if e = s.Init(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestOperatorScheduleRecoveryPauseAndReceipts(t *testing.T) {
	s, ctx, o := operatorFixture(t)
	now := time.Now().UTC()
	past := now.Add(-48 * time.Hour)
	next := now.Add(time.Hour)
	o.Cron = "0 * * * *"
	o.NextWake = &past
	if e := s.UpdateOperator(ctx, o); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if e := s.MaterializeRecurring(ctx, o, now, next); e != nil {
			t.Fatal(e)
		}
	}
	w, e := s.OperatorWakeups(ctx, o.ID)
	if e != nil || len(w) != 1 {
		t.Fatal(w, e)
	}
	if e = s.EnqueueOperatorWakeMessage(ctx, w[0]); e != nil {
		t.Fatal(e)
	}
	// A second occurrence while the first check is still queued must coalesce.
	if e = s.MaterializeRecurring(ctx, o, next, next.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	w, _ = s.OperatorWakeups(ctx, o.ID)
	if len(w) != 1 {
		t.Fatal(w)
	}
	follow, e := s.QueueOperatorWake(ctx, o.ID, "one-time follow-up", "keep this exact prompt", past)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PauseOperator(ctx, o.ID, true, &next); e != nil {
		t.Fatal(e)
	}
	w, _ = s.OperatorWakeups(ctx, o.ID)
	if e = s.EnqueueOperatorWakeMessage(ctx, w[1]); e != nil {
		t.Fatal(e)
	}
	after, _ := s.OperatorWakeups(ctx, o.ID)
	if after[1].State != "pending" || after[1].ID != follow {
		t.Fatal(after)
	}
	if e = s.PauseOperator(ctx, o.ID, false, &next); e != nil {
		t.Fatal(e)
	}
	if e = s.EnqueueOperatorWakeMessage(ctx, after[1]); e != nil {
		t.Fatal(e)
	}
	if e = s.EnqueueOperatorWakeMessage(ctx, after[1]); e != nil {
		t.Fatal(e)
	}
	mail, _ := s.SessionInbox(ctx, -o.ID, false)
	if len(mail) != 1 {
		t.Fatal(mail)
	}
	for _, m := range mail {
		if e = s.ReceiptMessageRead(ctx, -o.ID, m.ID); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.ReconcileOperatorReceipts(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ = s.OperatorWakeups(ctx, o.ID)
	for _, w := range after {
		if w.State != "completed" && w.State != "cancelled" {
			t.Fatal(after)
		}
	}
	if e = s.EnqueueOperatorWakeMessage(ctx, after[1]); e != nil {
		t.Fatal(e)
	}
	mail, _ = s.SessionInbox(ctx, -o.ID, false)
	if len(mail) != 1 {
		t.Fatal("completed followup replayed", mail)
	}
}
func TestOperatorConcurrentClaimAndWakeCommit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "operators.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Init(ctx); e != nil {
		t.Fatal(e)
	}
	o, e := s.CreateOperator(ctx, Operator{Name: "Intake", Harness: "pi", CWD: t.TempDir(), Timezone: "UTC"})
	if e != nil {
		t.Fatal(e)
	}
	other, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	wid, e := s.QueueOperatorWake(ctx, o.ID, "one-time follow-up", "distinct", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	ws, _ := s.OperatorWakeups(ctx, o.ID)
	if ws[0].ID != wid {
		t.Fatal(ws)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, db := range []*Store{s, other} {
		wg.Add(1)
		go func(db *Store) { defer wg.Done(); errs <- db.EnqueueOperatorWakeMessage(ctx, ws[0]) }(db)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	inbox, e := s.SessionInbox(ctx, -o.ID, false)
	if e != nil || len(inbox) != 1 {
		t.Fatal(inbox, e)
	}
}
func TestOperatorPausedTimersDoNotBlockAgentMail(t *testing.T) {
	s, ctx, o := operatorFixture(t)
	a, _ := messageEndpoints(t, s, ctx)
	sid, e := s.ClaimOperatorSession(ctx, o.ID, Session{Harness: o.Harness})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteSessionLaunch(ctx, sid, Session{Multiplexer: "herdr"}); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateSessionRuntime(ctx, sid, "completed", "native", "done", "", false); e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterOperatorRunner(ctx, "test", os.Getpid()); e != nil {
		t.Fatal(e)
	}
	wid, _ := s.QueueOperatorWake(ctx, o.ID, "one-time follow-up", "later", time.Now())
	w, _ := s.OperatorWakeups(ctx, o.ID)
	if w[0].ID != wid {
		t.Fatal(w)
	}
	if e = s.EnqueueOperatorWakeMessage(ctx, w[0]); e != nil {
		t.Fatal(e)
	}
	if e = s.PauseOperator(ctx, o.ID, true, nil); e != nil {
		t.Fatal(e)
	}
	m, e := s.SendSessionMessage(ctx, a, -o.ID, "urgent", "wake despite pause", 0)
	if e != nil {
		t.Fatal(e)
	}
	pending, e := s.SessionNotificationInbox(ctx, -o.ID)
	if e != nil || len(pending) != 1 || pending[0].ID != m.ID {
		t.Fatal(pending, e)
	}
	claimed, e := s.ClaimMessageNotification(ctx, -o.ID, m.ID, "test")
	if e != nil || !claimed {
		t.Fatal(claimed, e)
	}
	if e = s.RemoveOperatorRunner(ctx, "test"); e != nil {
		t.Fatal(e)
	}
	pending, e = s.SessionNotificationInbox(ctx, -o.ID)
	if e != nil || len(pending) != 0 {
		t.Fatal("activation after Kanbi exit", pending, e)
	}
}

func TestOperatorMigrationPreservesSessionIDHighWater(t *testing.T) {
	s, e := OpenMemory()
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if _, e = s.db.ExecContext(ctx, schema); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, `insert into sqlite_sequence(name,seq) values('sessions',9000)`); e != nil {
		t.Fatal(e)
	}
	if e = s.Init(ctx); e != nil {
		t.Fatal(e)
	}
	view, _ := s.BoardView(ctx)
	ticket, e := s.CreateTicket(ctx, view.Columns[0].ID, "new owner", "", "pi")
	if e != nil {
		t.Fatal(e)
	}
	sid, e := s.ClaimSession(ctx, ticket.ID, Session{Harness: "pi"}, false)
	if e != nil || sid <= 9000 {
		t.Fatal("historical mailbox address could be reused", sid, e)
	}
}

func TestOperatorNativeReceiptDoesNotRequireTicketOwnership(t *testing.T) {
	s, ctx, o := operatorFixture(t)
	sid, e := s.ClaimOperatorSession(ctx, o.ID, Session{Harness: o.Harness})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteSessionLaunch(ctx, sid, Session{Multiplexer: "herdr"}); e != nil {
		t.Fatal(e)
	}
	_, e = s.QueueOperatorWake(ctx, o.ID, "run now", "test", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	w, _ := s.OperatorWakeups(ctx, o.ID)
	if e = s.EnqueueOperatorWakeMessage(ctx, w[0]); e != nil {
		t.Fatal(e)
	}
	mail, _ := s.SessionInbox(ctx, -o.ID, false)
	if e = s.MarkMessageNotified(ctx, -o.ID, mail[0].ID); e != nil {
		t.Fatal(e)
	}
	m, e := s.SessionMessage(ctx, mail[0].ID)
	if e != nil || m.State != "notified" || m.NotificationStatus != "notified" {
		t.Fatal(m, e)
	}
	if e = s.ReconcileOperatorReceipts(ctx); e != nil {
		t.Fatal(e)
	}
	w, _ = s.OperatorWakeups(ctx, o.ID)
	if w[0].State != "completed" {
		t.Fatal(w)
	}
}
