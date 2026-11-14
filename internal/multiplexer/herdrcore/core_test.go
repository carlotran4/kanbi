package herdrcore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

func fixture(t *testing.T) (*Adapter, *terminal, *string, *[]string) {
	t.Helper()
	a := NewAdapter(Config{Session: "test"})
	id := "core-one"
	calls := []string{}
	term := &terminal{Address: "t1", PID: 10, Created: 123, State: "live", Cols: 80, Rows: 24, Labels: map[string]string{}}
	a.Call = func(ctx context.Context, method string, params, result any) error {
		calls = append(calls, method)
		var out any
		p, _ := params.(map[string]any)
		switch method {
		case "hello":
			out = map[string]any{"generation": 1, "core": map[string]string{"id": id}}
		case "terminal.run":
			if p["no_parent"] != true || p["keep"] != false || p["no_notify"] != true {
				t.Fatal("unsafe ownership", p)
			}
			if !reflect.DeepEqual(p["argv"], []string{"env", "X=$(touch bad)", "fake", "hello world"}) {
				t.Fatal("argv changed", p)
			}
			term.Labels = p["labels"].(map[string]string)
			out = term
		case "terminal.get":
			out = term
		case "terminal.read":
			out = map[string]string{"text": "PROMPT_READY"}
		case "terminal.kill", "terminal.keep", "terminal.keys", "terminal.label":
			out = map[string]any{}
		default:
			t.Fatal("unexpected method", method)
		}
		raw, _ := json.Marshal(out)
		return json.Unmarshal(raw, result)
	}
	return a, term, &id, &calls
}
func launch(t *testing.T, a *Adapter) multiplexer.ContainerRef {
	t.Helper()
	r, e := a.Launch(context.Background(), multiplexer.LaunchSpec{Name: "ticket", CWD: "/tmp/a b", Command: []string{"env", "X=$(touch bad)", "fake", "hello world"}})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestIdentityFencing(t *testing.T) {
	for _, scenario := range []string{"core", "pid", "created", "owner", "recovered", "finished", "namespace", "metadata"} {
		t.Run(scenario, func(t *testing.T) {
			a, term, id, calls := fixture(t)
			r := launch(t, a)
			ok, e := a.Validate(context.Background(), r)
			if !ok || e != nil {
				t.Fatal(ok, e)
			}
			switch scenario {
			case "core":
				*id = "replacement"
			case "pid":
				term.PID++
			case "created":
				term.Created++
			case "owner":
				term.Labels["kanbi.owner"] = "stranger"
			case "recovered":
				v := "holder_lost"
				term.Reason = &v
			case "finished":
				term.State = "finished"
			case "namespace":
				r.Namespace = "other"
			case "metadata":
				r.Metadata = "{}"
			}
			*calls = nil
			_, err := a.Read(context.Background(), r, multiplexer.ReadOptions{})
			if err == nil {
				t.Fatal("accepted foreign or recovered process")
			}
			for _, m := range *calls {
				if m == "terminal.read" {
					t.Fatal("read unsafe terminal")
				}
			}
			*calls = nil
			if err = a.SendKeys(context.Background(), r, "Enter"); err == nil {
				t.Fatal("sent to unsafe terminal")
			}
			for _, m := range *calls {
				if m == "terminal.keys" {
					t.Fatal("sent unsafe input")
				}
			}
			*calls = nil
			_ = a.Close(context.Background(), r)
			for _, m := range *calls {
				if m == "terminal.kill" {
					t.Fatal("killed unsafe terminal")
				}
			}
		})
	}
}
func TestDetectionAndAbsence(t *testing.T) {
	a, term, _, _ := fixture(t)
	r := launch(t, a)
	ctx := context.Background()
	d, e := a.Detect(ctx, r)
	if e != nil || d.Source != multiplexer.DetectionSourceUnknown {
		t.Fatal(d, e)
	}
	for _, c := range []struct{ state, kind, want string }{{"working", "", "running"}, {"blocked", "permission", "needs_permission"}, {"blocked", "question", "waiting_for_user"}, {"done", "", "waiting_for_user"}, {"idle", "", "idle_unknown"}, {"error", "", "error"}, {"new-state", "", ""}} {
		term.Status = &struct{ State, Kind, Msg string }{c.state, c.kind, "report"}
		d, e = a.Detect(ctx, r)
		if e != nil || d.State != c.want || d.Session != nil {
			t.Fatal(d, e)
		}
	}
	term.State = "finished"
	if valid, e := a.Validate(ctx, r); valid || e != nil {
		t.Fatal(valid, e)
	}
	_, e = a.Read(ctx, r, multiplexer.ReadOptions{})
	if !errors.Is(e, multiplexer.ErrContainerNotFound) {
		t.Fatal(e)
	}
}
func TestErrorsPreserveUnavailable(t *testing.T) {
	for _, c := range []struct {
		kind    string
		missing bool
	}{{"not_found", true}, {"finished", true}, {"unavailable", false}, {"machine_unknown", false}} {
		err := decodeReply([]byte(`{"error":{"code":-32000,"message":"test","data":{"kind":"`+c.kind+`"}}}`), new(any))
		if errors.Is(err, multiplexer.ErrContainerNotFound) != c.missing {
			t.Fatal(c, err)
		}
	}
}
func TestEnvironmentPinsSessionAndDropsParent(t *testing.T) {
	t.Setenv("HC_SESSION", "parent")
	t.Setenv("HC_TERMINAL_ID", "parent/t1")
	t.Setenv("HERDR_SOCKET_PATH", "user-socket")
	a := NewAdapter(Config{Session: "private"})
	for _, v := range a.Env() {
		if v == "HC_SESSION=parent" || v == "HC_TERMINAL_ID=parent/t1" || v == "HERDR_SOCKET_PATH=user-socket" {
			t.Fatal(v)
		}
	}
}

func TestBoundedOutputDrain(t *testing.T) {
	var b limitedBuffer
	data := make([]byte, 9<<20)
	n, err := b.Write(data)
	if n != len(data) || err != nil || len(b.Bytes()) != 8<<20 {
		t.Fatal(n, err, len(b.Bytes()))
	}
}
