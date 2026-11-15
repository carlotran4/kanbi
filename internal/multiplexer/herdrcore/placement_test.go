package herdrcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

func placementFixture(t *testing.T) (*Adapter, *terminal, *[]map[string]any) {
	t.Helper()
	t.Setenv("HC_TERMINAL_ID", "")
	a := NewAdapter(Config{Session: "test", AppBinary: "/usr/bin/true", CallerTerminal: "core-one/t9"})
	origin := &terminal{Address: "t9", State: "live", PID: 99, Labels: map[string]string{"herdr.space": "my-project", "herdr.tab": "board-tab"}}
	runs := []map[string]any{}
	terminals := map[string]terminal{}
	a.Call = func(ctx context.Context, method string, params, result any) error {
		p, _ := params.(map[string]any)
		var out any
		switch method {
		case "hello":
			out = map[string]any{"generation": 1, "core": map[string]string{"id": "core-one"}}
		case "terminal.get":
			id := p["terminal"].(string)
			if id == "t9" {
				out = origin
			} else {
				v, ok := terminals[id]
				if !ok {
					return errors.New("unknown test terminal")
				}
				out = v
			}
		case "terminal.run":
			runs = append(runs, p)
			id := fmt.Sprintf("t%d", len(runs))
			v := terminal{Address: id, State: "live", PID: uint32(len(runs) + 10), Created: uint64(len(runs)), Labels: p["labels"].(map[string]string)}
			terminals[id] = v
			out = v
		case "terminal.label":
			id := p["terminal"].(string)
			v := terminals[id]
			for k, x := range p["labels"].(map[string]string) {
				v.Labels[k] = x
			}
			terminals[id] = v
			out = v
		case "terminal.keep":
			out = map[string]any{}
		default:
			t.Fatalf("unexpected RPC %s", method)
		}
		raw, _ := json.Marshal(out)
		return json.Unmarshal(raw, result)
	}
	a.Query = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte(`{"space":"unrelated-focus"}`), nil
		}
		if args[0] != "ls" {
			t.Fatal(args)
		}
		var spaces []any
		for _, v := range terminals {
			spaces = append(spaces, map[string]any{"id": v.Labels["herdr.space"], "tabs": []any{map[string]any{"id": v.Labels["herdr.tab"], "terminals": []any{map[string]string{"terminal": v.Address, "pane": "p-" + v.Address}}}}})
		}
		spaces = append(spaces, map[string]any{"id": "unrelated-focus", "tabs": []any{}})
		return json.Marshal(map[string]any{"spaces": spaces})
	}
	return a, origin, &runs
}
func placeLaunch(t *testing.T, a *Adapter) multiplexer.ContainerRef {
	t.Helper()
	r, err := a.Launch(context.Background(), multiplexer.LaunchSpec{Command: []string{"fake-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestLaunchInheritsCallingBoardSpace(t *testing.T) {
	a, origin, runs := placementFixture(t)
	first := placeLaunch(t, a)
	labels := (*runs)[0]["labels"].(map[string]string)
	if labels["herdr.space"] != "my-project" || labels["herdr.tab"] == "board-tab" || labels["herdr.tab"] == "" {
		t.Fatal(labels)
	}
	var m metadata
	if err := json.Unmarshal([]byte(first.Metadata), &m); err != nil || m.Space != "my-project" {
		t.Fatal(m, err)
	}
	// GUI selection is elsewhere. Reopen must not move or relaunch the agent.
	if err := a.Focus(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if len(*runs) != 1 || labels["herdr.space"] != "my-project" {
		t.Fatal("focus changed placement", *runs)
	}
	// Moving the board changes future destinations, not already-live tickets.
	origin.Labels["herdr.space"] = "moved-project"
	second := placeLaunch(t, a)
	if (*runs)[1]["labels"].(map[string]string)["herdr.space"] != "moved-project" {
		t.Fatal(*runs)
	}
	if first.ID == second.ID || labels["herdr.space"] != "my-project" {
		t.Fatal("new launch changed existing ticket")
	}
}
func TestCallerPlacementFailureDoesNotLaunch(t *testing.T) {
	for _, scenario := range []string{"foreign-core", "closed", "mismatched-id", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			a, origin, runs := placementFixture(t)
			switch scenario {
			case "foreign-core":
				a.Config.CallerTerminal = "other-core/t9"
			case "closed":
				origin.State = "finished"
			case "mismatched-id":
				origin.Address = "t-other"
			case "unavailable":
				a.Config.CallerTerminal = "t-missing"
			}
			_, err := a.Launch(context.Background(), multiplexer.LaunchSpec{Command: []string{"fake"}})
			if err == nil || len(*runs) != 0 {
				t.Fatal("launch must fail before creating a process", err, *runs)
			}
		})
	}
}
func TestLaunchBoardUsesSelectedSpaceBeforeSpawn(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			a, _, runs := placementFixture(t)
			a.Config.CallerTerminal = ""
			query := a.Query
			if empty {
				a.Query = func(ctx context.Context, args ...string) ([]byte, error) {
					if args[0] == "status" {
						return []byte(`{"space":null}`), nil
					}
					return query(ctx, args...)
				}
			}
			r, err := a.LaunchBoard(context.Background(), multiplexer.LaunchSpec{Command: []string{"kanbi", "--board"}})
			if err != nil {
				t.Fatal(err)
			}
			labels := (*runs)[0]["labels"].(map[string]string)
			if empty {
				if labels["herdr.space"] == "" || labels["herdr.space"] == "kanbi-managed" {
					t.Fatal(labels)
				}
			} else if labels["herdr.space"] != "unrelated-focus" {
				t.Fatal(labels)
			}
			var m metadata
			_ = json.Unmarshal([]byte(r.Metadata), &m)
			if m.Space != labels["herdr.space"] {
				t.Fatal(m, labels)
			}
		})
	}
}
func TestHeadlessLaunchRequiresNoApp(t *testing.T) {
	a, _, runs := placementFixture(t)
	a.Config.CallerTerminal = ""
	a.Query = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("headless launch queried app")
		return nil, nil
	}
	_ = placeLaunch(t, a)
	if _, ok := (*runs)[0]["labels"].(map[string]string)["herdr.space"]; ok {
		t.Fatal("headless launch fabricated GUI space")
	}
}
func TestSelectedRemoteOrMissingSpaceRejectedBeforeBoardLaunch(t *testing.T) {
	for _, world := range []string{`{"spaces":[]}`, `{"spaces":[{"id":"unrelated-focus","machine":"remote"}]}`} {
		t.Run(world, func(t *testing.T) {
			a, _, runs := placementFixture(t)
			a.Config.CallerTerminal = ""
			a.Query = func(ctx context.Context, args ...string) ([]byte, error) {
				if args[0] == "status" {
					return []byte(`{"space":"unrelated-focus"}`), nil
				}
				return []byte(world), nil
			}
			_, err := a.LaunchBoard(context.Background(), multiplexer.LaunchSpec{Command: []string{"kanbi"}})
			if err == nil || len(*runs) != 0 {
				t.Fatal(err, *runs)
			}
		})
	}
}

func TestFocusPreservesActualOrRecordedPlacement(t *testing.T) {
	for _, scenario := range []string{"moved-agent", "missing-tab", "parked", "old-metadata", "headless"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, runs := placementFixture(t)
			if scenario == "headless" {
				a.Config.CallerTerminal = ""
			}
			r := placeLaunch(t, a)
			labels := (*runs)[0]["labels"].(map[string]string)
			want := "my-project"
			switch scenario {
			case "moved-agent":
				labels["herdr.space"] = "moved-agent-space"
				want = "moved-agent-space"
			case "missing-tab":
				labels["herdr.space"] = "moved-agent-space"
				delete(labels, "herdr.tab")
				want = "moved-agent-space"
			case "parked":
				delete(labels, "herdr.space")
				delete(labels, "herdr.tab")
			case "old-metadata":
				var m map[string]any
				_ = json.Unmarshal([]byte(r.Metadata), &m)
				delete(m, "space_id")
				raw, _ := json.Marshal(m)
				r.Metadata = string(raw)
			case "headless":
				want = "unrelated-focus"
			}
			if err := a.Focus(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			if labels["herdr.space"] != want || labels["herdr.tab"] == "" || len(*runs) != 1 {
				t.Fatal("focus moved/recreated existing attempt", labels, *runs)
			}
		})
	}
}
