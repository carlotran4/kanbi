package runtime

import (
	"context"
	"github.com/carlotran4/kanbi/internal/storage"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTicketWakeReusesConversationAndDoesNotFocus(t *testing.T) {
	m, sid, original, log := deliveryFixture(t, "copilot")
	ctx := context.WithValue(context.Background(), operatorWakeContextKey{}, true)
	if err := m.Store.AcknowledgeSessionMessage(ctx, sid, original.ID); err != nil {
		t.Fatal(err)
	}
	ses, _, _ := m.Store.SessionByID(ctx, sid)
	if err := m.Store.ScheduleTicketPrompt(ctx, ses.TicketID, "follow up", time.Now().Add(10*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	if err := m.refreshTicketWakeups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshTicketWakeups(ctx); err != nil {
		t.Fatal(err)
	}
	ticket, err := m.Store.TicketByID(ctx, ses.TicketID)
	if err != nil || ticket.SessionID.Int64 != sid {
		t.Fatal(ticket, err)
	}
	wakes, _ := m.Store.TicketWakeups(ctx, ses.TicketID)
	if len(wakes) != 1 || wakes[0].State != "queued" {
		t.Fatal(wakes)
	}
	mail, err := m.Store.SessionInbox(ctx, sid, false)
	if err != nil || len(mail) != 1 || mail[0].Body != "follow up" {
		t.Fatal(mail, err)
	}
	output, _ := os.ReadFile(log)
	if strings.Contains(string(output), "agent focus") || strings.Contains(string(output), "tab focus") || strings.Contains(string(output), "pane focus") || strings.Contains(string(output), "agent start") {
		t.Fatal(string(output))
	}
}

func TestTicketWakeInterruptedActivationFailsWithoutRelaunch(t *testing.T) {
	m, sid, _, log := deliveryFixture(t, "copilot")
	ctx := context.Background()
	ses, _, _ := m.Store.SessionByID(ctx, sid)
	if err := m.Store.ScheduleTicketPrompt(ctx, ses.TicketID, "follow up", time.Now().Add(10*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	wakes, _ := m.Store.TicketWakeups(ctx, ses.TicketID)
	if ok, err := m.Store.ClaimTicketWake(ctx, wakes[0].ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := m.refreshTicketWakeups(ctx); err != nil {
		t.Fatal(err)
	}
	wakes, _ = m.Store.TicketWakeups(ctx, ses.TicketID)
	if wakes[0].State != "failed" || !strings.Contains(wakes[0].Error, "interrupted") {
		t.Fatal(wakes)
	}
	output, _ := os.ReadFile(log)
	if len(output) > 0 {
		t.Fatal(string(output))
	}
}

func TestTicketWakeStartResumeAndRepairPreserveHistory(t *testing.T) {
	for _, mode := range []string{"new", "resume", "repair"} {
		t.Run(mode, func(t *testing.T) {
			m, log := testHerdrManager(t)
			ctx := context.WithValue(t.Context(), operatorWakeContextKey{}, true)
			view := defaultBoardView(t, ctx, m.Store)
			ticket := createTicket(t, ctx, m.Store, view.Columns[0].ID, "Scheduled task", "normal ticket context", "pi")
			var oldID int64
			if mode != "new" {
				ses := storage.Session{Harness: "pi", Status: "running", Multiplexer: "herdr"}
				if mode == "resume" {
					ses.HarnessSessionRef = sqlString("verified-native-ref")
				}
				var err error
				oldID, err = m.Store.UpsertActiveSession(ctx, ticket.ID, ses)
				if err != nil {
					t.Fatal(err)
				}
				if err = m.Store.MarkSessionClosed(ctx, oldID, "closed", "test", ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Store.ScheduleTicketPrompt(ctx, ticket.ID, "check status", time.Now().Add(10*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(15 * time.Millisecond)
			if err := m.refreshTicketWakeups(ctx); err != nil {
				t.Fatal(err)
			}
			wakes, _ := m.Store.TicketWakeups(ctx, ticket.ID)
			latest, err := m.Store.TicketByID(ctx, ticket.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "repair" {
				if wakes[0].State != "failed" || latest.SessionID.Int64 != oldID {
					t.Fatal(wakes, latest)
				}
				if err = m.refreshTicketWakeups(ctx); err != nil {
					t.Fatal(err)
				}
				again, _ := m.Store.TicketByID(ctx, ticket.ID)
				if again.SessionID.Int64 != oldID {
					t.Fatal("failed activation retried")
				}
			} else {
				if wakes[0].State != "queued" || !latest.SessionActive || latest.SessionID.Int64 == oldID {
					t.Fatal(wakes, latest)
				}
				inbox, err := m.Store.SessionInbox(ctx, latest.SessionID.Int64, false)
				if err != nil || len(inbox) != 1 || inbox[0].Body != "check status" {
					t.Fatal(inbox, err)
				}
			}
			if oldID != 0 {
				old, ok, err := m.Store.SessionByID(ctx, oldID)
				if err != nil || !ok || old.IsActive {
					t.Fatal("old history changed", old, err)
				}
			}
			output, _ := os.ReadFile(log)
			if strings.Contains(string(output), "agent focus") || strings.Contains(string(output), "tab focus") || strings.Contains(string(output), "pane focus") {
				t.Fatal("scheduled activation stole focus", string(output))
			}
			if mode == "resume" && !strings.Contains(string(output), "verified-native-ref") {
				t.Fatal(string(output))
			}
		})
	}
}
