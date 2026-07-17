package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
)

type Config struct {
	Binary            string
	Session           string
	WorkspaceStrategy string
	FocusOnOpen       bool
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type ExecRunner struct{ Session string }

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if r.Session != "" {
		cmd.Env = append(cmd.Environ(), "HERDR_SESSION="+r.Session)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

type Adapter struct {
	Config Config
	Runner Runner
}

func NewAdapter(cfg Config) *Adapter {
	if cfg.Binary == "" {
		cfg.Binary = "herdr"
	}
	if cfg.Session == "" {
		cfg.Session = "default"
	}
	if cfg.WorkspaceStrategy == "" {
		cfg.WorkspaceStrategy = "board"
	}
	return &Adapter{Config: cfg, Runner: ExecRunner{Session: cfg.Session}}
}

var _ multiplexer.Interface = (*Adapter)(nil)

func (a *Adapter) Kind() multiplexer.Kind { return multiplexer.KindHerdr }

func (a *Adapter) Ensure(ctx context.Context, namespace string) error {
	if namespace == "" {
		return nil
	}
	_, err := a.run(ctx, "workspace", "get", namespace)
	return err
}

// CurrentWorkspace returns the workspace ID of the Herdr pane this process is
// running in, so ticket panes can be opened as new tabs alongside it instead
// of spawning a separate workspace.
func (a *Adapter) CurrentWorkspace(ctx context.Context) (string, error) {
	out, err := a.run(ctx, "pane", "current")
	if err != nil {
		return "", err
	}
	obj := parseObject(out)
	workspaceID := firstString(obj, "result.pane.workspace_id", "pane.workspace_id", "workspace_id", "workspaceId")
	if workspaceID == "" {
		return "", errors.New("herdr pane current: missing workspace id")
	}
	return workspaceID, nil
}

func (a *Adapter) Launch(ctx context.Context, spec multiplexer.LaunchSpec) (multiplexer.ContainerRef, error) {
	workspaceID := spec.Namespace
	if workspaceID == "" {
		var err error
		workspaceID, err = a.workspaceForLaunch(ctx, spec)
		if err != nil {
			return multiplexer.ContainerRef{}, err
		}
	}
	name := spec.Name
	if name == "" {
		name = "kanbi-agent"
	}
	args := []string{"agent", "start", name}
	if spec.CWD != "" {
		args = append(args, "--cwd", spec.CWD)
	}
	if workspaceID != "" {
		args = append(args, "--workspace", workspaceID)
	}
	// Always start in the background; the agent is moved into its own tab
	// below, and focus (if requested) is applied to that tab instead.
	args = append(args, "--no-focus")
	args = append(args, "--")
	args = append(args, spec.Command...)
	out, err := a.run(ctx, args...)
	if err != nil {
		return multiplexer.ContainerRef{}, err
	}
	info := parseObject(out)
	if workspaceID == "" {
		workspaceID = firstString(info, "result.workspace.workspace_id", "result.workspace.id", "result.agent.workspace_id", "result.workspace_id", "result.workspaceId", "workspace_id", "workspaceId", "workspace.id")
	}
	paneID := firstString(info, "result.agent.pane_id", "result.pane_id", "result.paneId", "pane_id", "paneId", "pane.id")
	if paneID != "" {
		info["pane_id"] = paneID
		moveOut, moveErr := a.moveToNewTab(ctx, paneID, workspaceID, name)
		if moveErr == nil {
			info = mergeObjects(info, moveOut)
		}
	}
	agentTarget := firstString(info, "target", "agent_target", "agentTarget", "agent.name", "name", "result.agent.name")
	if agentTarget == "" {
		agentTarget = name
	}
	metadata := mergeMetadata(spec.Metadata, info)
	return multiplexer.ContainerRef{Kind: multiplexer.KindHerdr, Namespace: workspaceID, ID: agentTarget, Name: name, Metadata: metadata}, nil
}

func (a *Adapter) Validate(ctx context.Context, ref multiplexer.ContainerRef) (bool, error) {
	detection, err := a.Detect(ctx, ref)
	if err != nil {
		return false, err
	}
	if detection.Source != multiplexer.DetectionSourceUnknown {
		return true, nil
	}
	if _, err := a.Read(ctx, ref, multiplexer.ReadOptions{Lines: 1}); err != nil {
		if errors.Is(err, multiplexer.ErrContainerNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// moveToNewTab relocates a freshly started agent pane into its own new tab
// within the same workspace, so opening a ticket behaves like a new tmux
// window rather than splitting the pane the user was already looking at.
func (a *Adapter) moveToNewTab(ctx context.Context, paneID, workspaceID, label string) (map[string]any, error) {
	args := []string{"pane", "move", paneID, "--new-tab"}
	if workspaceID != "" {
		args = append(args, "--workspace", workspaceID)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	if a.Config.FocusOnOpen {
		args = append(args, "--focus")
	} else {
		args = append(args, "--no-focus")
	}
	out, err := a.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseObject(out), nil
}

func (a *Adapter) Focus(ctx context.Context, ref multiplexer.ContainerRef) error {
	target := ref.Target()
	if target == "" {
		return errors.New("herdr focus target is empty")
	}
	if _, err := a.run(ctx, "agent", "focus", target); err == nil {
		return nil
	}
	paneID := refMeta(ref, "pane_id", "paneId", "result.agent.pane_id", "result.pane_id")
	if paneID == "" {
		paneID = target
	}
	_, err := a.run(ctx, "pane", "focus", "--pane", paneID)
	return err
}

func (a *Adapter) Read(ctx context.Context, ref multiplexer.ContainerRef, opts multiplexer.ReadOptions) (string, error) {
	target := ref.Target()
	if target == "" {
		return "", errors.New("herdr read target is empty")
	}
	args := []string{"agent", "read", target, "--source", "recent-unwrapped"}
	if opts.Lines > 0 {
		args = append(args, "--lines", fmt.Sprint(opts.Lines))
	}
	out, err := a.run(ctx, args...)
	if err == nil {
		return readText(out), nil
	}
	paneID := refMeta(ref, "pane_id", "paneId", "result.agent.pane_id", "result.pane_id")
	if paneID == "" {
		paneID = target
	}
	args = []string{"pane", "read", paneID, "--source", "recent-unwrapped"}
	if opts.Lines > 0 {
		args = append(args, "--lines", fmt.Sprint(opts.Lines))
	}
	out, err = a.run(ctx, args...)
	if err != nil {
		if isNotFoundResponse(out, err) {
			return out, fmt.Errorf("%w: %s", multiplexer.ErrContainerNotFound, strings.TrimSpace(out))
		}
		return out, err
	}
	return readText(out), nil
}

func isNotFoundResponse(out string, err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(out + " " + err.Error())
	return strings.Contains(text, "agent_not_found") ||
		strings.Contains(text, "pane_not_found") ||
		strings.Contains(text, "agent target") && strings.Contains(text, "not found") ||
		strings.Contains(text, "pane ") && strings.Contains(text, "not found")
}

func (a *Adapter) SendKeys(ctx context.Context, ref multiplexer.ContainerRef, keys ...string) error {
	paneID := refMeta(ref, "pane_id", "paneId", "result.agent.pane_id", "result.pane_id")
	if paneID == "" {
		paneID = ref.Target()
	}
	if paneID == "" {
		return errors.New("herdr send target is empty")
	}
	args := append([]string{"pane", "send-keys", paneID}, herdrKeys(keys)...)
	_, err := a.run(ctx, args...)
	return err
}

func (a *Adapter) SendText(ctx context.Context, ref multiplexer.ContainerRef, text string) error {
	paneID := refMeta(ref, "pane_id", "paneId", "result.agent.pane_id", "result.pane_id")
	if paneID == "" {
		paneID = ref.Target()
	}
	if paneID == "" {
		return errors.New("herdr send target is empty")
	}
	_, err := a.run(ctx, "pane", "send-text", paneID, text)
	return err
}

func (a *Adapter) Close(ctx context.Context, ref multiplexer.ContainerRef) error {
	paneID := refMeta(ref, "pane_id", "paneId", "result.agent.pane_id", "result.pane_id")
	if paneID == "" {
		paneID = ref.Target()
	}
	if paneID == "" {
		return errors.New("herdr close target is empty")
	}
	_, err := a.run(ctx, "pane", "close", paneID)
	return err
}

func (a *Adapter) Detect(ctx context.Context, ref multiplexer.ContainerRef) (multiplexer.Detection, error) {
	target := ref.Target()
	if target == "" {
		return unknownDetection("missing Herdr target"), nil
	}
	out, err := a.run(ctx, "agent", "get", target)
	if err != nil {
		return unknownDetection(err.Error()), nil
	}
	obj := parseObject(out)
	state := strings.ToLower(firstString(obj, "result.agent.agent_status", "result.agent.status", "result.agent.state", "state", "status", "agent_state", "agentState", "agent.state", "agent.status"))
	if state == "" || state == "unknown" {
		return unknownDetection("Herdr returned unknown agent state"), nil
	}
	reason := firstString(obj, "result.agent.message", "result.agent.custom_status", "result.agent.customStatus", "message", "reason", "custom_status", "customStatus", "agent.message", "agent.custom_status")
	mapped, confidence := mapState(state, reason)
	if mapped == "" {
		return unknownDetection("Herdr returned unsupported agent state: " + state), nil
	}
	return multiplexer.Detection{State: mapped, Reason: reasonFor(reason, "Herdr agent state: "+state), Source: multiplexer.DetectionSourceNative, Confidence: confidence, ObservedAt: time.Now().UTC()}, nil
}

func (a *Adapter) workspaceForLaunch(ctx context.Context, spec multiplexer.LaunchSpec) (string, error) {
	label := workspaceLabel(spec)
	out, err := a.run(ctx, "workspace", "list")
	if err == nil {
		if id := findWorkspace(out, spec.CWD, label); id != "" {
			return id, nil
		}
	}
	args := []string{"workspace", "create"}
	if spec.CWD != "" {
		args = append(args, "--cwd", spec.CWD)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	if a.Config.FocusOnOpen {
		args = append(args, "--focus")
	} else {
		args = append(args, "--no-focus")
	}
	out, err = a.run(ctx, args...)
	if err != nil {
		return "", err
	}
	obj := parseObject(out)
	return firstString(obj, "result.workspace.workspace_id", "result.workspace.id", "result.workspace_id", "result.workspaceId", "workspace_id", "workspaceId", "workspace.id", "id"), nil
}

func (a *Adapter) run(ctx context.Context, args ...string) (string, error) {
	if a.Runner == nil {
		a.Runner = ExecRunner{Session: a.Config.Session}
	}
	bin := a.Config.Binary
	if bin == "" {
		bin = "herdr"
	}
	return a.Runner.Run(ctx, bin, args...)
}

func mapState(state, reason string) (string, multiplexer.Confidence) {
	switch state {
	case "working", "running":
		return kanban.StateRunning, multiplexer.ConfidenceHigh
	case "blocked":
		if hasPermissionEvidence(reason) {
			return kanban.StateNeedsPermission, multiplexer.ConfidenceHigh
		}
		return kanban.StateWaitingForUser, multiplexer.ConfidenceHigh
	case "done":
		return kanban.StateWaitingForUser, multiplexer.ConfidenceHigh
	case "idle":
		if strings.TrimSpace(reason) != "" {
			return kanban.StateWaitingForUser, multiplexer.ConfidenceMedium
		}
		return kanban.StateIdleUnknown, multiplexer.ConfidenceLow
	}
	return "", multiplexer.ConfidenceUnknown
}

func hasPermissionEvidence(s string) bool {
	s = strings.ToLower(s)
	for _, needle := range []string{"permission", "approval", "approve", "allow", "authorize", "confirmation", "confirm"} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func unknownDetection(reason string) multiplexer.Detection {
	return multiplexer.Detection{Reason: reason, Source: multiplexer.DetectionSourceUnknown, Confidence: multiplexer.ConfidenceUnknown, ObservedAt: time.Now().UTC()}
}

func reasonFor(reason, fallback string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return fallback
}

func herdrKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		switch strings.ToLower(k) {
		case "enter":
			out = append(out, "enter")
		case "esc", "escape":
			out = append(out, "esc")
		case "c-c", "ctrl-c":
			out = append(out, "ctrl+c")
		default:
			out = append(out, k)
		}
	}
	return out
}

func workspaceLabel(spec multiplexer.LaunchSpec) string {
	if spec.CWD != "" {
		parts := strings.Split(strings.TrimRight(spec.CWD, "/"), "/")
		return parts[len(parts)-1]
	}
	return "kanbi"
}

func parseObject(out string) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err == nil {
		return obj
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err == nil && len(arr) > 0 {
		return map[string]any{"items": arr}
	}
	return map[string]any{}
}

func findWorkspace(out, cwd, label string) string {
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		var obj map[string]any
		if json.Unmarshal([]byte(out), &obj) != nil {
			return ""
		}
		for _, key := range []string{"workspaces", "result.workspaces"} {
			if v, ok := dotted(obj, key).([]any); ok {
				for _, item := range v {
					if m, ok := item.(map[string]any); ok && workspaceMatches(m, cwd, label) {
						return firstString(m, "id", "workspace_id", "workspaceId")
					}
				}
			}
		}
		return ""
	}
	for _, m := range arr {
		if workspaceMatches(m, cwd, label) {
			return firstString(m, "id", "workspace_id", "workspaceId")
		}
	}
	return ""
}

func workspaceMatches(m map[string]any, cwd, label string) bool {
	if cwd != "" && firstString(m, "cwd", "foreground_cwd", "workspace.cwd", "root_pane.cwd") == cwd {
		return true
	}
	return label != "" && firstString(m, "label", "name") == label
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := dotted(m, key); v != nil {
			switch x := v.(type) {
			case string:
				if x != "" {
					return x
				}
			case fmt.Stringer:
				return x.String()
			}
		}
	}
	return ""
}

func mergeObjects(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func dotted(m map[string]any, key string) any {
	cur := any(m)
	for _, part := range strings.Split(key, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[part]
	}
	return cur
}

func mergeMetadata(existing string, obj map[string]any) string {
	base := map[string]any{}
	if existing != "" {
		_ = json.Unmarshal([]byte(existing), &base)
	}
	for k, v := range obj {
		base[k] = v
	}
	b, err := json.Marshal(base)
	if err != nil || string(b) == "{}" {
		return existing
	}
	return string(b)
}

func readText(out string) string {
	obj := parseObject(out)
	if text := firstString(obj, "result.read.text", "read.text", "text"); text != "" {
		return text
	}
	return out
}

func refMeta(ref multiplexer.ContainerRef, keys ...string) string {
	if ref.Metadata == "" {
		return ""
	}
	obj := map[string]any{}
	if json.Unmarshal([]byte(ref.Metadata), &obj) != nil {
		return ""
	}
	return firstString(obj, keys...)
}
