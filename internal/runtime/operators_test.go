package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestOperatorFixedClockSchedule(t *testing.T) {
	at := time.Date(2026, 11, 7, 12, 7, 0, 0, time.UTC)
	for _, test := range []struct {
		spec, zone string
		want       time.Time
	}{{"15m", "UTC", time.Date(2026, 11, 7, 12, 15, 0, 0, time.UTC)}, {"daily", "America/Chicago", time.Date(2026, 11, 7, 15, 0, 0, 0, time.UTC)}, {"0 * * * *", "UTC", time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)}} {
		next, e := NextOperatorWake(test.spec, test.zone, at)
		if e != nil || !next.Equal(test.want) {
			t.Fatal(test, next, e)
		}
	}
	for _, spec := range []string{"@every 15m", "bad", "60 * * * *"} {
		if _, e := NextOperatorWake(spec, "UTC", at); e == nil {
			t.Fatal(spec)
		}
	}
	if _, e := NextOperatorWake("15m", "", at); e == nil {
		t.Fatal("missing timezone accepted")
	}
}
func TestOperatorInstructionsReloadAndFreshHistory(t *testing.T) {
	s, ctx := newRuntimeTestStore(t)
	cfg := config.Defaults(config.Paths{ConfigFile: filepath.Join(t.TempDir(), "custom", "config.yaml")})
	m := NewManager(cfg, s)
	defer m.Close()
	o, e := m.SaveOperator(ctx, storage.Operator{Name: "Intake", Harness: "pi", CWD: t.TempDir(), Timezone: "UTC"}, "first")
	if e != nil {
		t.Fatal(e)
	}
	path := m.OperatorInstructionsPath(o.ID)
	if !filepath.IsAbs(path) || !strings.Contains(path, "custom/operators/") {
		t.Fatal(path)
	}
	if e = os.WriteFile(path, []byte("direct file edit"), 0600); e != nil {
		t.Fatal(e)
	}
	text, e := m.ReadOperatorInstructions(o.ID)
	if e != nil || text != "direct file edit" {
		t.Fatal(text, e)
	}
	for _, msg := range []storage.SessionMessage{{ID: 1, Recipient: -o.ID, Sender: 0, Body: "Wake reason: scheduled check\ncheck requests"}, {ID: 2, Recipient: -o.ID, Sender: 9, Body: "incoming"}} {
		notice := m.OperatorMessageNotice(o.ID, msg)
		if !strings.Contains(notice, path) || !strings.Contains(notice, "before acting") || !strings.Contains(notice, "Wake reason:") {
			t.Fatal(notice)
		}
	}
	o.Harness = "codex"
	if _, e = m.SaveOperator(ctx, o, "changed"); e == nil {
		t.Fatal("harness changed")
	}
	text, _ = m.ReadOperatorInstructions(o.ID)
	if text != "direct file edit" {
		t.Fatal(text)
	}
}
func TestOperatorCopilotDeliveryUsesExistingAdapterAndDefersBlocked(t *testing.T) {
	for _, state := range []string{"done", "ready", "working", "blocked", "idle"} {
		t.Run(state, func(t *testing.T) {
			m, _, original, _ := deliveryFixture(t, "copilot")
			if e := m.Store.MarkSessionClosed(context.Background(), original.Sender, "closed", "test", ""); e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			if e := m.Store.AcknowledgeSessionMessage(ctx, original.Recipient, original.ID); e != nil {
				t.Fatal(e)
			}
			response := `{"state":"` + state + `","message":"test phase"}`
			if state == "ready" {
				response = `{"state":"idle","interactive_ready":true}`
			}
			bin, log := writeFakeHerdr(t, map[string]string{"agent get": response, "agent read": "prompt"})
			m.Config.Multiplexer.Herdr.Binary = bin
			o, e := m.Store.CreateOperator(ctx, storage.Operator{Name: "Intake", Harness: "copilot", CWD: t.TempDir(), Timezone: "UTC"})
			if e != nil {
				t.Fatal(e)
			}
			sid, e := m.Store.ClaimOperatorSession(ctx, o.ID, storage.Session{Harness: o.Harness})
			if e != nil {
				t.Fatal(e)
			}
			if e = m.Store.CompleteSessionLaunch(ctx, sid, storage.Session{Multiplexer: "herdr", MuxContainerID: sqlString("agent-1"), MuxMetadata: sqlString(`{"pane_id":"pane-1"}`)}); e != nil {
				t.Fatal(e)
			}
			if e = m.RefreshRuntime(ctx); e != nil {
				t.Fatal(e)
			}
			ses, _, _ := m.Store.OperatorSession(ctx, o.ID)
			if state == "done" && ses.Status != "completed" {
				t.Fatal(ses)
			}
			if e = m.Store.RegisterOperatorRunner(ctx, "test", os.Getpid()); e != nil {
				t.Fatal(e)
			}
			_, e = m.Store.QueueOperatorWake(ctx, o.ID, "run now", "inspect requests", time.Now())
			if e != nil {
				t.Fatal(e)
			}
			w, _ := m.Store.OperatorWakeups(ctx, o.ID)
			if e = m.Store.EnqueueOperatorWakeMessage(ctx, w[0]); e != nil {
				t.Fatal(e)
			}
			if e = m.NotifySessionMessages(ctx, -o.ID); e != nil {
				t.Fatal(e)
			}
			if e = m.NotifySessionMessages(ctx, -o.ID); e != nil {
				t.Fatal(e)
			}
			output, _ := os.ReadFile(log)
			count := strings.Count(string(output), "pane send-text")
			if state == "done" || state == "ready" {
				if count != 1 || !strings.Contains(string(output), "instructions.md") {
					t.Fatal(string(output))
				}
			} else if count != 0 {
				t.Fatal("unsafe phase received input", string(output))
			}
		})
	}
}
func TestOperatorUnresumableActivationFailsWithoutFreshReplacement(t *testing.T) {
	s, ctx := newRuntimeTestStore(t)
	cfg := config.Defaults(config.Paths{ConfigFile: filepath.Join(t.TempDir(), "config.yaml")})
	m := NewManager(cfg, s)
	defer m.Close()
	o, e := m.SaveOperator(ctx, storage.Operator{Name: "Intake", Harness: "pi", CWD: t.TempDir(), Timezone: "UTC"}, "instructions")
	if e != nil {
		t.Fatal(e)
	}
	sid, e := s.ClaimOperatorSession(ctx, o.ID, storage.Session{Harness: o.Harness})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.MarkSessionClosed(ctx, sid, "closed", "test", ""); e != nil {
		t.Fatal(e)
	}
	if e = m.OpenOperator(ctx, o.ID, false); e == nil {
		t.Fatal("unresumable session replaced")
	}
	latest, _, _ := s.OperatorSession(ctx, o.ID)
	if latest.ID != sid {
		t.Fatal(latest)
	}
}

func TestOperatorLifecycleReusesSessionAttemptsForEveryHarness(t *testing.T) {
	for _, h := range []string{"pi", "codex", "claude", "copilot"} {
		t.Run(h, func(t *testing.T) {
			m, _ := testHerdrManager(t)
			ctx := t.Context()
			m.Config.Paths.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
			m.Config.Harnesses[h] = config.Harness{Start: []string{"/custom/" + h}, Resume: []string{"/custom/" + h, "--session", "{session_ref}"}, PromptMode: "arg"}
			o, e := m.SaveOperator(ctx, storage.Operator{Name: "Intake", Harness: h, CWD: t.TempDir(), Timezone: "UTC"}, "inspect requests")
			if e != nil {
				t.Fatal(e)
			}
			if e = m.OpenOperator(ctx, o.ID, false); e != nil {
				t.Fatal(e)
			}
			first, ok, e := m.Store.OperatorSession(ctx, o.ID)
			if e != nil || !ok || first.TicketID != 0 {
				t.Fatal(first, e)
			}
			if e = m.OpenOperator(ctx, o.ID, false); e != nil {
				t.Fatal(e)
			}
			same, _, _ := m.Store.OperatorSession(ctx, o.ID)
			if same.ID != first.ID {
				t.Fatal("open duplicated attempt")
			}
			if e = m.OpenOperator(ctx, o.ID, true); e == nil {
				t.Fatal("fresh silently closed active session")
			}
			if e = m.Store.UpdateSessionRef(ctx, first.ID, "verified-ref"); e != nil {
				t.Fatal(e)
			}
			ticket, _ := m.Store.OperatorTicket(ctx, o.ID)
			if e = m.CloseSession(ctx, ticket); e != nil {
				t.Fatal(e)
			}
			if e = m.OpenOperator(ctx, o.ID, false); e != nil {
				t.Fatal(e)
			}
			second, _, _ := m.Store.OperatorSession(ctx, o.ID)
			if second.ID == first.ID || second.HarnessSessionRef.String != "verified-ref" {
				t.Fatal(second)
			}
			ticket, _ = m.Store.OperatorTicket(ctx, o.ID)
			if e = m.CloseSession(ctx, ticket); e != nil {
				t.Fatal(e)
			}
			if e = m.OpenOperator(ctx, o.ID, true); e != nil {
				t.Fatal(e)
			}
			fresh, _, _ := m.Store.OperatorSession(ctx, o.ID)
			if fresh.ID == second.ID || fresh.HarnessSessionRef.Valid {
				t.Fatal(fresh)
			}
			text, e := m.ReadOperatorInstructions(o.ID)
			if e != nil || text != "inspect requests" {
				t.Fatal(text, e)
			}
		})
	}
}
