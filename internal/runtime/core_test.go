package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/multiplexer/herdrcore"
)

func TestCoreLifecycle(t *testing.T) {
	for _, h := range []string{"pi", "codex", "copilot", "claude"} {
		t.Run(h, func(t *testing.T) {
			t.Setenv("HC_TERMINAL_ID", "")
			t.Setenv("CODEX_HOME", t.TempDir())
			store, ctx := newRuntimeTestStore(t)
			view := defaultBoardView(t, ctx, store)
			ticket := createTicket(t, ctx, store, view.Columns[0].ID, "Core", "body", h)
			dir := t.TempDir()
			cfg := config.Defaults(config.Paths{StateDir: dir})
			cfg.Multiplexer.Default = "herdr-core"
			cfg.Multiplexer.HerdrCore.Session = "test"
			cfg.Harnesses[h] = config.Harness{Start: []string{"/fake/" + h}, Resume: []string{"/fake/" + h, "resume", "{session_ref}"}, PromptMode: "arg", SessionRef: "SESSION_REF="}
			app := filepath.Join(dir, "app")
			if err := os.WriteFile(app, []byte("#!/bin/sh\nprintf '%s\\n' '{}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			a := herdrcore.NewAdapter(herdrcore.Config{Session: "test", AppBinary: app})
			term := map[string]any{}
			runs := 0
			native := "working"
			unavailable := false
			a.Call = func(ctx context.Context, method string, params, result any) error {
				p, _ := params.(map[string]any)
				var out any
				if unavailable {
					return errors.New("core unavailable")
				}
				switch method {
				case "hello":
					out = map[string]any{"generation": 1, "core": map[string]string{"id": "core-1"}}
				case "terminal.run":
					runs++
					term = map[string]any{"address": fmt.Sprintf("t%d", runs), "pid": runs + 100, "created_ms": runs, "state": "live", "labels": p["labels"], "cols": 80, "rows": 24}
					out = term
				case "terminal.get":
					term["status"] = map[string]string{"state": native, "kind": "permission"}
					out = term
				case "terminal.read":
					out = map[string]string{"text": "SESSION_REF=verified-fixture-ref"}
				case "terminal.label":
					labels := term["labels"].(map[string]string)
					for k, v := range p["labels"].(map[string]string) {
						labels[k] = v
					}
					world := map[string]any{"spaces": []any{map[string]any{"id": labels["herdr.space"], "tabs": []any{map[string]any{"id": labels["herdr.tab"], "terminals": []any{map[string]string{"terminal": term["address"].(string), "pane": "owned-pane"}}}}}}}
					raw, _ := json.Marshal(world)
					if err := os.WriteFile(app, []byte("#!/bin/sh\nprintf '%s\\n' '"+string(raw)+"'\n"), 0700); err != nil {
						t.Fatal(err)
					}
					out = term
				case "terminal.keep":
					out = term
				case "terminal.keys":
					keys, _ := p["keys"].([]string)
					if len(keys) > 0 && keys[0] == "ctrl+c" {
						term["state"] = "finished"
						return multiplexer.ErrContainerNotFound
					}
					out = term
				case "terminal.kill":
					term["state"] = "finished"
					out = term
				default:
					t.Fatal(method)
				}
				raw, _ := json.Marshal(out)
				return json.Unmarshal(raw, result)
			}
			m := NewManager(cfg, store)
			m.CoreAdapter = a
			m.ResumeCheckAfter = time.Millisecond
			t.Cleanup(m.Close)
			if err := m.OpenTicket(ctx, ticket, true); err != nil {
				t.Fatal(err)
			}
			got, _ := store.TicketByID(ctx, ticket.ID)
			if got.Multiplexer.String != "herdr-core" || !got.SessionRef.Valid || got.SessionRef.String != "verified-fixture-ref" {
				t.Fatal(got)
			}
			first := got.SessionID
			// Switch config while the active row remains core-owned: route by stored kind.
			m.Config.Multiplexer.Default = "herdr"
			if err := m.OpenTicket(ctx, got, false); err != nil {
				t.Fatal(err)
			}
			got, _ = store.TicketByID(ctx, ticket.ID)
			if runs != 1 || got.SessionID != first {
				t.Fatal("duplicate open")
			}
			native = "blocked"
			if err := m.RefreshRuntime(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ = store.TicketByID(ctx, ticket.ID)
			if got.Runtime != "needs_permission" {
				t.Fatal(got.Runtime)
			}
			unavailable = true
			if err := m.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ = store.TicketByID(ctx, ticket.ID)
			if !got.SessionActive {
				t.Fatal("outage ended session")
			}
			unavailable = false
			if err := m.CloseSession(ctx, got); err != nil {
				t.Fatal(err)
			}
			got, _ = store.TicketByID(ctx, ticket.ID)
			if got.SessionActive || got.Runtime != "closed" {
				t.Fatal(got)
			}
			m.Config.Multiplexer.Default = "herdr-core"
			if err := m.OpenTicket(ctx, got, false); err != nil {
				t.Fatal(err)
			}
			got, _ = store.TicketByID(ctx, ticket.ID)
			if runs != 2 || got.SessionID == first || !got.SessionRef.Valid {
				t.Fatal("resume lost history")
			}
			// A recovered shell at the same address must deactivate the original attempt.
			term["pid"] = 9999
			if err := m.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ = store.TicketByID(ctx, ticket.ID)
			if got.SessionActive {
				t.Fatal("accepted replacement shell")
			}
		})
	}
}
func TestCoreFleetRefusesWithoutMutation(t *testing.T) {
	m := NewManager(config.Defaults(config.Paths{}), nil)
	defer m.Close()
	m.Config.Multiplexer.Default = "herdr-core"
	_, _, err := m.fleetPlan(context.Background(), 0, 160, 45)
	if err == nil || !strings.Contains(err.Error(), "Herdr TUI host") {
		t.Fatal(err)
	}
}
