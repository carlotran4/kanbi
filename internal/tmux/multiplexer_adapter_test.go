package tmux

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
)

func TestMultiplexerAdapterLaunchMapsTmuxWindowToContainerRef(t *testing.T) {
	cfg := config.Defaults(config.Paths{})
	cfg.TmuxSession = "kanbi-test"
	runner := &fakeRunner{}
	manager := NewManager(cfg, nil)
	manager.Runner = runner
	adapter := NewMultiplexerAdapter(manager)

	ref, err := adapter.Launch(context.Background(), multiplexer.LaunchSpec{
		Namespace: "kanbi-test",
		Name:      "b1-T-001-test",
		CWD:       "/tmp",
		Command:   []string{"echo", "hello world"},
	})
	if err != nil {
		t.Fatalf("Launch returned error: %v", err)
	}
	if ref.Kind != multiplexer.KindTmux || ref.Namespace != "kanbi-test" || ref.ID != "@7" || ref.Name != "b1-T-001-test" {
		t.Fatalf("unexpected container ref: %+v", ref)
	}
	last := runner.calls[len(runner.calls)-1]
	wantPrefix := []string{"new-window", "-d", "-P", "-F", "#{window_id}", "-c", "/tmp", "-t", "kanbi-test", "-n", "b1-T-001-test"}
	if !reflect.DeepEqual(last.args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("new-window args prefix = %#v, want %#v", last.args[:len(wantPrefix)], wantPrefix)
	}
	if got := last.args[len(last.args)-1]; got != "echo 'hello world'" {
		t.Fatalf("shell command = %q", got)
	}
}

func TestMultiplexerAdapterControlsMappedContainerRef(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults(config.Paths{})
	cfg.TmuxSession = "fallback"
	runner := &fakeRunner{windows: map[string]string{"@9": "ticket-window"}, pane: "pane output"}
	manager := NewManager(cfg, nil)
	manager.Runner = runner
	adapter := NewMultiplexerAdapter(manager)
	ref := multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: "runtime", ID: "@9", Name: "ticket-window"}

	if err := adapter.Focus(ctx, ref); err != nil {
		t.Fatalf("Focus returned error: %v", err)
	}
	out, err := adapter.Read(ctx, ref, multiplexer.ReadOptions{Lines: 50})
	if err != nil || out != "pane output" {
		t.Fatalf("Read = %q, %v", out, err)
	}
	if err := adapter.SendKeys(ctx, ref, "C-c", "Enter"); err != nil {
		t.Fatalf("SendKeys returned error: %v", err)
	}
	if err := adapter.Close(ctx, ref); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	detection, err := adapter.Detect(ctx, ref)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if detection.Source != multiplexer.DetectionSourceTmux || detection.Confidence != multiplexer.ConfidenceMedium {
		t.Fatalf("unexpected detection metadata: %+v", detection)
	}

	assertCalledWithTarget(t, runner.calls, "select-window", "runtime:@9")
	assertCalledWithTarget(t, runner.calls, "capture-pane", "runtime:@9")
	assertCalledWithTarget(t, runner.calls, "send-keys", "runtime:@9")
	assertCalledWithTarget(t, runner.calls, "kill-window", "runtime:@9")
}

func TestMultiplexerAdapterValidateRequiresStoredIDNameMatchInNamespace(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults(config.Paths{})
	cfg.TmuxSession = "fallback"
	runner := &fakeRunner{windows: map[string]string{"@9": "ticket-window", "@10": "other-window"}}
	manager := NewManager(cfg, nil)
	manager.Runner = runner
	adapter := NewMultiplexerAdapter(manager)

	valid, err := adapter.Validate(ctx, multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: "stored-runtime", ID: "@9", Name: "ticket-window"})
	if err != nil || !valid {
		t.Fatalf("Validate(matching) = %v, %v, want true, nil", valid, err)
	}
	valid, err = adapter.Validate(ctx, multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: "stored-runtime", ID: "@10", Name: "ticket-window"})
	if err != nil || valid {
		t.Fatalf("Validate(wrong name) = %v, %v, want false, nil", valid, err)
	}
	assertCalledWithTarget(t, runner.calls, "display-message", "stored-runtime:@9")
	assertCalledWithTarget(t, runner.calls, "display-message", "stored-runtime:@10")
}

func TestMultiplexerAdapterValidateByNameAndSendTextUseStoredNamespace(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults(config.Paths{})
	cfg.TmuxSession = "fallback"
	runner := &fakeRunner{windows: map[string]string{"@9": "ticket-window"}}
	manager := NewManager(cfg, nil)
	manager.Runner = runner
	adapter := NewMultiplexerAdapter(manager)
	ref := multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: "stored-runtime", Name: "ticket-window"}

	valid, err := adapter.Validate(ctx, ref)
	if err != nil || !valid {
		t.Fatalf("Validate(name) = %v, %v, want true, nil", valid, err)
	}
	if err := adapter.SendText(ctx, ref, "multi-line\nprompt ' text"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	assertCalledWithTarget(t, runner.calls, "paste-buffer", "stored-runtime:ticket-window")
	foundBuffer := false
	for _, call := range runner.calls {
		if reflect.DeepEqual(call.args, []string{"set-buffer", "--", "multi-line\nprompt ' text"}) {
			foundBuffer = true
		}
	}
	if !foundBuffer {
		t.Fatalf("SendText did not pass text directly to tmux buffer: %#v", runner.calls)
	}
}

func TestContainerRefFromSessionUsesGenericMuxFieldsWhenPresent(t *testing.T) {
	session := storage.Session{
		Multiplexer:      "herdr",
		TmuxSessionName:  "tmux-session",
		TmuxWindowID:     sql.NullString{String: "@1", Valid: true},
		TmuxWindowName:   "tmux-window",
		MuxNamespace:     sql.NullString{String: "workspace", Valid: true},
		MuxContainerID:   sql.NullString{String: "tab-123", Valid: true},
		MuxContainerName: sql.NullString{String: "Tab", Valid: true},
		MuxMetadata:      sql.NullString{String: `{"native":true}`, Valid: true},
	}
	ref := ContainerRefFromSession(session)
	if ref.Kind != multiplexer.KindHerdr || ref.Namespace != "workspace" || ref.ID != "tab-123" || ref.Name != "Tab" || ref.Metadata != `{"native":true}` {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestApplyContainerRefToTmuxSessionRecordsLegacyAndGenericFields(t *testing.T) {
	var session storage.Session
	ApplyContainerRefToSession(&session, multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: "runtime", ID: "@42", Name: "ticket"})
	if session.Multiplexer != "tmux" || session.TmuxSessionName != "runtime" || !session.TmuxWindowID.Valid || session.TmuxWindowID.String != "@42" || session.TmuxWindowName != "ticket" {
		t.Fatalf("legacy tmux fields not populated: %+v", session)
	}
	if !session.MuxNamespace.Valid || session.MuxNamespace.String != "runtime" || !session.MuxContainerID.Valid || session.MuxContainerID.String != "@42" || !session.MuxContainerName.Valid || session.MuxContainerName.String != "ticket" {
		t.Fatalf("generic mux fields not populated: %+v", session)
	}
}

func assertCalledWithTarget(t *testing.T, calls []call, command, target string) {
	t.Helper()
	for _, call := range calls {
		if len(call.args) == 0 || call.args[0] != command {
			continue
		}
		for i := 0; i < len(call.args)-1; i++ {
			if call.args[i] == "-t" && call.args[i+1] == target {
				return
			}
		}
	}
	t.Fatalf("did not find %s call targeting %s in %#v", command, target, calls)
}
