package herdr

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"kanbi/internal/kanban"
	"kanbi/internal/multiplexer"
)

type fakeRunner struct {
	calls [][]string
	out   map[string]string
	err   map[string]error
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(args, " ")
	if err := f.err[key]; err != nil {
		return f.out[key], err
	}
	return f.out[key], nil
}

func TestLaunchCreatesWorkspaceAndAgent(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"workspace list": `[]`,
		"workspace create --cwd /repo --label repo --no-focus":                             `{"id":"ws-1","cwd":"/repo"}`,
		"agent start b1-T-001-demo --cwd /repo --workspace ws-1 --no-focus -- codex hello": `{"pane_id":"pane-1","agent":{"name":"agent-1"},"tab_id":"tab-1"}`,
	}}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test", FocusOnOpen: false})
	adapter.Runner = r

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{Name: "b1-T-001-demo", CWD: "/repo", Command: []string{"codex", "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != multiplexer.KindHerdr || ref.Namespace != "ws-1" || ref.ID != "agent-1" || !strings.Contains(ref.Metadata, "pane-1") {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestLaunchParsesRealHerdrCLIEnvelope(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"workspace list": `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"repo","active_tab_id":"w1:t1"}]}}`,
		"agent start b1-T-001-demo --cwd /repo --workspace w1 --no-focus -- codex hello": `{"id":"cli:agent:start","result":{"type":"agent_started","agent":{"name":"agent-1","pane_id":"w1:p2","tab_id":"w1:t1","terminal_id":"term_123","workspace_id":"w1"}}}`,
	}}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test", FocusOnOpen: false})
	adapter.Runner = r

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{Name: "b1-T-001-demo", CWD: "/repo", Command: []string{"codex", "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Namespace != "w1" || ref.ID != "agent-1" || refMeta(ref, "result.agent.pane_id") != "w1:p2" {
		t.Fatalf("unexpected ref from real Herdr envelope: %+v", ref)
	}
}

func TestDetectMapsNativeHerdrStates(t *testing.T) {
	cases := []struct {
		name   string
		json   string
		state  string
		reason string
	}{
		{"working", `{"state":"working"}`, kanban.StateRunning, "Herdr agent state: working"},
		{"blocked", `{"state":"blocked","message":"waiting for input"}`, kanban.StateWaitingForUser, "waiting for input"},
		{"permission", `{"state":"blocked","message":"permission required"}`, kanban.StateNeedsPermission, "permission required"},
		{"done", `{"state":"done","message":"finished"}`, kanban.StateWaitingForUser, "finished"},
		{"idle", `{"state":"idle"}`, kanban.StateIdleUnknown, "Herdr agent state: idle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{out: map[string]string{"agent get agent-1": tc.json}}
			adapter := NewAdapter(Config{})
			adapter.Runner = r
			d, err := adapter.Detect(context.Background(), multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, ID: "agent-1"})
			if err != nil {
				t.Fatal(err)
			}
			if d.Source != multiplexer.DetectionSourceNative || d.State != tc.state || d.Reason != tc.reason {
				t.Fatalf("detection = %+v", d)
			}
		})
	}
}

func TestDetectParsesRealHerdrAgentEnvelope(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"agent get agent-1": `{"id":"cli:agent:get","result":{"agent":{"agent_status":"idle","name":"agent-1"},"type":"agent_info"}}`}}
	adapter := NewAdapter(Config{})
	adapter.Runner = r
	d, err := adapter.Detect(context.Background(), multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, ID: "agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != multiplexer.DetectionSourceNative || d.State != kanban.StateIdleUnknown {
		t.Fatalf("detection = %+v", d)
	}
}

func TestDetectUnknownAllowsFallback(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"agent get agent-1": `{"state":"unknown"}`}}
	adapter := NewAdapter(Config{})
	adapter.Runner = r
	d, err := adapter.Detect(context.Background(), multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, ID: "agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != multiplexer.DetectionSourceUnknown || d.Confidence != multiplexer.ConfidenceUnknown {
		t.Fatalf("expected unknown fallback detection, got %+v", d)
	}
}

func TestReadExtractsTextFromHerdrCLIEnvelope(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"agent read agent-1 --source recent-unwrapped --lines 50": `{"id":"cli:agent:read","result":{"read":{"text":"PROMPT_READY\nSESSION_REF=fake-ref","pane_id":"w1:p2"},"type":"pane_read"}}`,
	}}
	adapter := NewAdapter(Config{})
	adapter.Runner = r
	out, err := adapter.Read(context.Background(), multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, ID: "agent-1"}, multiplexer.ReadOptions{Lines: 50})
	if err != nil {
		t.Fatal(err)
	}
	if out != "PROMPT_READY\nSESSION_REF=fake-ref" {
		t.Fatalf("read text = %q", out)
	}
}

func TestControlCommandsUsePaneFallbacks(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"agent read agent-1 --source recent-unwrapped --lines 50": "hello",
	}, err: map[string]error{
		"agent focus agent-1": errors.New("not an agent"),
	}}
	adapter := NewAdapter(Config{})
	adapter.Runner = r
	ref := multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, ID: "agent-1", Metadata: `{"pane_id":"pane-1"}`}
	if err := adapter.Focus(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	out, err := adapter.Read(context.Background(), ref, multiplexer.ReadOptions{Lines: 50})
	if err != nil || out != "hello" {
		t.Fatalf("Read = %q, %v", out, err)
	}
	if err := adapter.SendKeys(context.Background(), ref, "C-c", "Enter"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SendText(context.Background(), ref, "exit"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	gotLast := r.calls[len(r.calls)-3:]
	want := [][]string{{"herdr", "pane", "send-keys", "pane-1", "ctrl+c", "enter"}, {"herdr", "pane", "send-text", "pane-1", "exit"}, {"herdr", "pane", "close", "pane-1"}}
	if !reflect.DeepEqual(gotLast, want) {
		t.Fatalf("last calls = %#v, want %#v", gotLast, want)
	}
}
