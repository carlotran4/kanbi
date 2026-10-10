package herdr

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
)

type fakeRunner struct {
	calls [][]string
	out   map[string]string
	err   map[string]error
}

type busyThenReadyRunner struct {
	fakeRunner
	startKey     string
	busyAttempts int
	starts       int
}

func (r *busyThenReadyRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if key := strings.Join(args, " "); key == r.startKey {
		r.calls = append(r.calls, append([]string{name}, args...))
		r.starts++
		if r.starts <= r.busyAttempts {
			return `{"error":{"code":"agent_pane_busy","message":"agent target pane w1:p2 is not an available shell"}}`, errors.New("exit status 1")
		}
		return `{"result":{"agent":{"name":"agent-1","pane_id":"w1:p2"}}}`, nil
	}
	return r.fakeRunner.Run(ctx, name, args...)
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

func TestLaunchUsesPaneFirstAgentStartWhenSupported(t *testing.T) {
	agentName := paneFirstAgentName("b1-T-001-demo", "w1:p2")
	r := &fakeRunner{out: map[string]string{
		"agent start --help": `Usage: herdr agent start <NAME> --kind <KIND> --pane <ID>`,
		"tab create --workspace w1 --label b1-T-001-demo --cwd /repo --env TOKEN=secret --no-focus": `{"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2","workspace_id":"w1"}}}`,
		"agent start " + agentName + " --kind codex --pane w1:p2 -- hello":                          `{"result":{"agent":{"name":"agent-1","pane_id":"w1:p2","workspace_id":"w1"}}}`,
	}}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test", FocusOnOpen: false})
	adapter.Runner = r

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{
		Name: "b1-T-001-demo", CWD: "/repo", Namespace: "w1", AgentKind: "codex",
		Command: []string{"env", "TOKEN=secret", "codex", "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Namespace != "w1" || ref.ID != "agent-1" || refMeta(ref, "pane_id") != "w1:p2" || refMeta(ref, "tab_id") != "w1:t2" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	for _, call := range r.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "pane split") || strings.Contains(joined, "pane move") || strings.Contains(joined, " --focus") {
			t.Fatalf("launch must leave board layout and focus unchanged: %s", joined)
		}
		if strings.Contains(joined, "agent start") && (strings.Contains(joined, "--cwd") || strings.Contains(joined, "--workspace")) {
			t.Fatalf("new Herdr launch used removed options: %s", joined)
		}
	}
}

func TestLaunchPaneFirstNormalizesAgentNameForCurrentHerdr(t *testing.T) {
	agentName := paneFirstAgentName("b3-GH-296-release-qualification-publish-the-next-beta", "w1:p2")
	r := &fakeRunner{out: map[string]string{
		"agent start --help": `--kind <KIND> --pane <ID>`,
		"tab create --workspace w1 --label b3-GH-296-release-qualification-publish-the-next-beta --cwd /repo --no-focus": `{"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2","workspace_id":"w1"}}}`,
		"agent start " + agentName + " --kind pi --pane w1:p2 -- --session ref-1":                                        `{"result":{"agent":{"name":"` + agentName + `","pane_id":"w1:p2"}}}`,
	}}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test"})
	adapter.Runner = r

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{
		Name: "b3-GH-296-release-qualification-publish-the-next-beta", CWD: "/repo", Namespace: "w1", AgentKind: "pi",
		Command: []string{"pi", "--session", "ref-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != agentName || ref.Name != "b3-GH-296-release-qualification-publish-the-next-beta" {
		t.Fatalf("ref = %+v, want normalized agent target and original display name", ref)
	}
}

func TestPaneFirstAgentNameIsBoundedAndUniquePerPane(t *testing.T) {
	label := "b3-GH-296-release-qualification-publish-the-next-beta"
	first := paneFirstAgentName(label, "w1:p2")
	second := paneFirstAgentName(label, "w1:p3")
	if first == second {
		t.Fatalf("agent names collide across panes: %q", first)
	}
	for _, name := range []string{first, second} {
		if len(name) > 32 || name != strings.ToLower(name) {
			t.Fatalf("invalid normalized agent name %q", name)
		}
	}
}

func TestLaunchPaneFirstRetriesNewPaneUntilHerdrReportsShellReady(t *testing.T) {
	startKey := "agent start " + paneFirstAgentName("b1-T-001-demo", "w1:p2") + " --kind pi --pane w1:p2 --"
	r := &busyThenReadyRunner{
		fakeRunner: fakeRunner{out: map[string]string{
			"agent start --help": `--kind <KIND> --pane <ID>`,
			"tab create --workspace w1 --label b1-T-001-demo --cwd /repo --no-focus": `{"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2","workspace_id":"w1"}}}`,
		}},
		startKey: startKey, busyAttempts: 2,
	}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test", FocusOnOpen: true})
	adapter.Runner = r

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{
		Name: "b1-T-001-demo", CWD: "/repo", Namespace: "w1", AgentKind: "pi", Command: []string{"pi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.starts != 3 || ref.ID != "agent-1" {
		t.Fatalf("starts=%d ref=%+v, want two busy retries and a new agent", r.starts, ref)
	}
	for _, call := range r.calls {
		if strings.Contains(strings.Join(call, " "), " --focus") {
			t.Fatalf("launch focused the new pane before the agent was ready: calls=%v", r.calls)
		}
	}
}

func TestLaunchPaneFirstCleansUpPaneWhenAgentStartFails(t *testing.T) {
	startKey := "agent start " + paneFirstAgentName("b1-T-001-demo", "w1:p2") + " --kind pi --pane w1:p2 -- --session ref-1"
	r := &fakeRunner{
		out: map[string]string{
			"agent start --help": `--kind <KIND> --pane <ID>`,
			"tab create --workspace w1 --label b1-T-001-demo --cwd /repo --no-focus": `{"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2","workspace_id":"w1"}}}`,
			startKey: `launch rejected`,
		},
		err: map[string]error{startKey: errors.New("exit status 2")},
	}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test"})
	adapter.Runner = r

	_, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{
		Name: "b1-T-001-demo", CWD: "/repo", Namespace: "w1", AgentKind: "pi",
		Command: []string{"pi", "--session", "ref-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "launch rejected") {
		t.Fatalf("Launch error = %v, want Herdr output", err)
	}
	if got := strings.Join(r.calls[len(r.calls)-1], " "); got != "herdr pane close w1:p2" {
		t.Fatalf("last call = %q, want pane cleanup", got)
	}
	starts := 0
	for _, call := range r.calls {
		if strings.Join(call[1:], " ") == startKey {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("non-transient launch error retried %d times; calls=%v", starts, r.calls)
	}
}

func TestLaunchCustomExecutableInRawPane(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"agent start --help": "--kind <KIND> --pane <ID>", "tab create --workspace w1 --label demo --no-focus": `{"result":{"root_pane":{"pane_id":"w1:p1"},"tab":{"tab_id":"w1:t1"}}}`}}
	adapter := NewAdapter(Config{})
	adapter.Runner = r
	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{Name: "demo", Namespace: "w1", AgentKind: "pi", Command: []string{"/opt/wrappers/pi", "hello ' literal"}})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "w1:p1" {
		t.Fatalf("ref=%+v", ref)
	}
	found := false
	for _, call := range r.calls {
		if strings.Contains(strings.Join(call, " "), "pane run w1:p1 exec") {
			found = true
		}
		if len(call) > 2 && call[1] == "agent" && call[2] == "start" && call[len(call)-1] != "--help" {
			t.Fatalf("replaced custom command: %v", call)
		}
	}
	if !found {
		t.Fatalf("no raw launch: %v", r.calls)
	}
}

func TestLaunchFallsBackToLegacyAgentStart(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"agent start --help": `Usage: herdr agent start <NAME> --cwd <PATH> --workspace <ID>`,
		"agent start b1-T-001-demo --cwd /repo --workspace ws-1 --no-focus -- codex hello": `{"pane_id":"w1:p2","agent":{"name":"agent-1"},"tab_id":"w1:t1"}`,
		"pane move w1:p2 --new-tab --workspace ws-1 --label b1-T-001-demo --no-focus":      `{"tab_id":"w1:t9"}`,
	}}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test", FocusOnOpen: false})
	adapter.Runner = r

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{Name: "b1-T-001-demo", CWD: "/repo", Command: []string{"codex", "hello"}, AgentKind: "codex", Namespace: "ws-1"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "agent-1" || ref.Namespace != "ws-1" {
		t.Fatalf("unexpected legacy ref: %+v", ref)
	}
}

func TestLaunchCreatesWorkspaceWithoutFocusingBeforeAgentIsReady(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"workspace list": `[]`,
		"workspace create --cwd /repo --label repo --no-focus":                             `{"id":"ws-1","cwd":"/repo"}`,
		"agent start b1-T-001-demo --cwd /repo --workspace ws-1 --no-focus -- codex hello": `{"pane_id":"pane-1","agent":{"name":"agent-1"},"tab_id":"tab-1"}`,
	}}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test", FocusOnOpen: true})
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

func TestLaunchMovesAgentIntoNewTab(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"agent start b1-T-001-demo --cwd /repo --workspace ws-1 --no-focus -- codex hello": `{"pane_id":"w1:p2","agent":{"name":"agent-1"},"tab_id":"w1:t1"}`,
		"pane move w1:p2 --new-tab --workspace ws-1 --label b1-T-001-demo --no-focus":      `{"tab_id":"w1:t9"}`,
	}}
	adapter := NewAdapter(Config{Binary: "herdr", Session: "test", FocusOnOpen: false})
	adapter.Runner = r

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{Name: "b1-T-001-demo", CWD: "/repo", Command: []string{"codex", "hello"}, Namespace: "ws-1"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Namespace != "ws-1" || ref.ID != "agent-1" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	foundMove := false
	for _, call := range r.calls {
		if strings.Join(call, " ") == "herdr pane move w1:p2 --new-tab --workspace ws-1 --label b1-T-001-demo --no-focus" {
			foundMove = true
		}
		if len(call) >= 2 && call[0] == "herdr" && call[1] == "workspace" {
			t.Fatalf("Launch with a namespace should not touch workspace list/create, got %v", call)
		}
	}
	if !foundMove {
		t.Fatalf("expected pane move --new-tab call, got calls: %#v", r.calls)
	}
}

func TestCurrentWorkspaceParsesPaneCurrent(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"pane current": `{"id":"cli:pane:current","result":{"pane":{"pane_id":"w4:p2","workspace_id":"w4"},"type":"pane_current"}}`,
	}}
	adapter := NewAdapter(Config{})
	adapter.Runner = r
	id, err := adapter.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id != "w4" {
		t.Fatalf("workspace id = %q, want w4", id)
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

func TestValidateUsesNativeStateThenReadFallback(t *testing.T) {
	tests := []struct {
		name string
		out  map[string]string
		err  map[string]error
		want bool
	}{
		{name: "native state", out: map[string]string{"agent get agent-1": `{"state":"working"}`}, want: true},
		{name: "read fallback", out: map[string]string{"agent get agent-1": `{"state":"unknown"}`, "agent read agent-1 --source recent-unwrapped --lines 1": "output"}, want: true},
		{name: "unreadable", out: map[string]string{"agent get agent-1": `{"state":"unknown"}`}, err: map[string]error{"agent read agent-1 --source recent-unwrapped --lines 1": errors.New("missing"), "pane read agent-1 --source recent-unwrapped --lines 1": errors.New("missing")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{out: tt.out, err: tt.err}
			adapter := NewAdapter(Config{})
			adapter.Runner = runner
			valid, err := adapter.Validate(context.Background(), multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, ID: "agent-1"})
			if valid != tt.want {
				t.Fatalf("Validate() valid = %v, want %v (err %v)", valid, tt.want, err)
			}
			if tt.want && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !tt.want && err == nil {
				t.Fatal("Validate() error = nil, want read failure")
			}
		})
	}
}

func TestValidateTreatsKnownMissingAgentAndPaneAsInvalid(t *testing.T) {
	runner := &fakeRunner{
		out: map[string]string{
			"agent get old-agent": `{"error":{"code":"agent_not_found","message":"agent target old-agent not found"}}`,
			"agent read old-agent --source recent-unwrapped --lines 1": `{"error":{"code":"agent_not_found"}}`,
			"pane read old-pane --source recent-unwrapped --lines 1":   `{"code":"pane_not_found","message":"pane old-pane not found"}`,
		},
		err: map[string]error{
			"agent get old-agent": errors.New("exit status 1"),
			"agent read old-agent --source recent-unwrapped --lines 1": errors.New("exit status 1"),
			"pane read old-pane --source recent-unwrapped --lines 1":   errors.New("exit status 1"),
		},
	}
	adapter := NewAdapter(Config{})
	adapter.Runner = runner
	valid, err := adapter.Validate(context.Background(), multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, ID: "old-agent", Metadata: `{"pane_id":"old-pane"}`})
	if err != nil {
		t.Fatalf("Validate() error = %v, want known absence", err)
	}
	if valid {
		t.Fatal("Validate() = true for missing agent and pane")
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

func TestLaunchPaneFirstTabCreationFailure(t *testing.T) {
	const create = "tab create --workspace w1 --label demo --no-focus"
	for _, tc := range []struct {
		name, output, want  string
		createErr, closeErr error
		wantClose           bool
	}{
		{name: "command failure", output: "creation rejected", createErr: errors.New("exit status 1"), want: "create agent tab"},
		{name: "missing pane", output: `{"result":{"tab":{"tab_id":"w1:t2"}}}`, want: "missing root pane id", wantClose: true},
		{name: "cleanup failure", output: `{"result":{"tab":{"tab_id":"w1:t2"}}}`, closeErr: errors.New("close failed"), want: "clean up agent tab", wantClose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{out: map[string]string{"agent start --help": "--kind --pane", create: tc.output}, err: map[string]error{create: tc.createErr, "tab close w1:t2": tc.closeErr}}
			a := NewAdapter(Config{})
			a.Runner = r
			_, err := a.Launch(context.Background(), multiplexer.LaunchSpec{Name: "demo", Namespace: "w1", AgentKind: "pi", Command: []string{"pi"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Launch error = %v, want %q", err, tc.want)
			}
			closed := false
			for _, call := range r.calls {
				command := strings.Join(call[1:], " ")
				if command == "tab close w1:t2" {
					closed = true
				}
				if command != "agent start --help" && strings.HasPrefix(command, "agent start") {
					t.Fatalf("started agent after tab creation failed: %v", call)
				}
			}
			if closed != tc.wantClose {
				t.Fatalf("closed tab = %v, want %v", closed, tc.wantClose)
			}
		})
	}
}
