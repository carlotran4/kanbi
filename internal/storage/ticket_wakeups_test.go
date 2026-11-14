package storage

import (
	"testing"
	"time"
)

func TestTicketWakeAtomicDeliveryAndCancellation(t *testing.T) {
	s, ctx := newTestStore(t)
	a, _ := messageEndpoints(t, s, ctx)
	ses, _, _ := s.SessionByID(ctx, a)
	if err := s.ScheduleTicketPrompt(ctx, ses.TicketID, "check tomorrow", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	wakes, err := s.TicketWakeups(ctx, ses.TicketID)
	if err != nil || len(wakes) != 1 {
		t.Fatal(wakes, err)
	}
	id := wakes[0].ID
	if claimed, err := s.ClaimTicketWake(ctx, id); err != nil || claimed {
		t.Fatal("claimed early", claimed, err)
	}
	if _, err = s.db.ExecContext(ctx, `update ticket_wakeups set due_at=? where id=?`, time.Now().Add(-time.Hour), id); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimTicketWake(ctx, id); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if claimed, err := s.ClaimTicketWake(ctx, id); err != nil || claimed {
		t.Fatal("claimed twice", claimed, err)
	}
	if err = s.EnqueueTicketWake(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = s.EnqueueTicketWake(ctx, id); err == nil {
		t.Fatal("handoff repeated")
	}
	mail, err := s.SessionInbox(ctx, a, false)
	if err != nil || len(mail) != 1 || mail[0].Body != "check tomorrow" || mail[0].Sender != 0 {
		t.Fatal(mail, err)
	}
	if err = s.CancelTicketWake(ctx, ses.TicketID+1, id); err == nil {
		t.Fatal("wrong ticket cancelled wake")
	}
	claimed, err := s.ClaimMessageNotification(ctx, a, mail[0].ID, "test")
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if err = s.CancelTicketWake(ctx, ses.TicketID, id); err == nil {
		t.Fatal("cancelled uncertain delivery")
	}
	wakes, _ = s.TicketWakeups(ctx, ses.TicketID)
	if wakes[0].Delivery != "uncertain" {
		t.Fatal(wakes)
	}
}

func TestTicketWakeValidationPauseAndCancel(t *testing.T) {
	s, ctx := newTestStore(t)
	v := defaultBoardView(t, ctx, s)
	ticket := createTicket(t, ctx, s, v.Columns[0].ID, "Later", "", "pi")
	for _, prompt := range []string{"", "   "} {
		if err := s.ScheduleTicketPrompt(ctx, ticket.ID, prompt, time.Now().Add(time.Hour)); err == nil {
			t.Fatal("empty prompt accepted")
		}
	}
	if err := s.ScheduleTicketPrompt(ctx, ticket.ID, "past", time.Now().Add(-time.Hour)); err == nil {
		t.Fatal("past accepted")
	}
	if err := s.ScheduleTicketPrompt(ctx, ticket.ID, "later", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	wakes, _ := s.TicketWakeups(ctx, ticket.ID)
	id := wakes[0].ID
	if _, err := s.db.ExecContext(ctx, `update ticket_wakeups set due_at=? where id=?`, time.Now().Add(-time.Hour), id); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`update tickets set focus_paused=1 where id=?`, `update tickets set focus_paused=0,archived_at=CURRENT_TIMESTAMP where id=?`} {
		if _, err := s.db.ExecContext(ctx, query, ticket.ID); err != nil {
			t.Fatal(err)
		}
		if claimed, err := s.ClaimTicketWake(ctx, id); err != nil || claimed {
			t.Fatal("claimed unavailable ticket", claimed, err)
		}
	}
	if err := s.CancelTicketWake(ctx, ticket.ID, id); err != nil {
		t.Fatal(err)
	}
	wakes, _ = s.TicketWakeups(ctx, ticket.ID)
	if wakes[0].State != "cancelled" {
		t.Fatal(wakes)
	}
}

func TestQueuedPromptProjectionTracksDeliveryAcrossBoardViews(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	ticket := createTicket(t, ctx, s, view.Columns[0].ID, "Scheduled badge", "", "pi")
	check := func(want bool) {
		t.Helper()
		got, err := s.TicketByID(ctx, ticket.ID)
		if err != nil || got.HasQueuedPrompt != want {
			t.Fatalf("ticket queued=%v want %v: %v", got.HasQueuedPrompt, want, err)
		}
		// Read twice to exercise cache admission as well as fresh SQL.
		for i := 0; i < 2; i++ {
			named, err := s.BoardViewByID(ctx, ticket.BoardID)
			if err != nil {
				t.Fatal(err)
			}
			master, err := s.MasterBoardView(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []BoardView{named, master} {
				count := 0
				for _, c := range v.Columns {
					for _, got := range c.Tickets {
						if got.ID == ticket.ID {
							count++
							if got.HasQueuedPrompt != want {
								t.Fatalf("%s queued=%v want %v", v.Board.Name, got.HasQueuedPrompt, want)
							}
						}
					}
				}
				if count != 1 {
					t.Fatalf("%s duplicate/missing projected ticket: %d", v.Board.Name, count)
				}
			}
		}
	}
	check(false)
	if err := s.ScheduleTicketPrompt(ctx, ticket.ID, "check later", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	check(true)
	wakes, _ := s.TicketWakeups(ctx, ticket.ID)
	wid := wakes[0].ID
	if err := s.CancelTicketWake(ctx, ticket.ID, wid); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := s.ScheduleTicketPrompt(ctx, ticket.ID, "another check", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	check(true)
	wakes, _ = s.TicketWakeups(ctx, ticket.ID)
	wid = wakes[len(wakes)-1].ID
	if _, err := s.db.ExecContext(ctx, `update ticket_wakeups set state='activating' where id=?`, wid); err != nil {
		t.Fatal(err)
	}
	check(true)
	sid, err := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", Multiplexer: "herdr", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnqueueTicketWake(ctx, wid); err != nil {
		t.Fatal(err)
	}
	check(true)
	mail, err := s.SessionInbox(ctx, sid, false)
	if err != nil || len(mail) != 1 {
		t.Fatal(mail, err)
	}
	for _, state := range []string{"deferred", "uncertain", "submitted"} {
		if _, err = s.db.ExecContext(ctx, `update session_messages set notification_status=? where id=?`, state, mail[0].ID); err != nil {
			t.Fatal(err)
		}
		check(true)
	}
	if err = s.MarkMessageNotified(ctx, sid, mail[0].ID); err != nil {
		t.Fatal(err)
	}
	check(false)
	if _, err = s.db.ExecContext(ctx, `update ticket_wakeups set state='failed' where id=?`, wid); err != nil {
		t.Fatal(err)
	}
	check(false)
}
