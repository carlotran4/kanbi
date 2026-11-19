// Package herdrcore implements the generation-1 public Herdr core API.
package herdrcore

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

type Config struct {
	Binary, Session, AppBinary string
	// CallerTerminal identifies the board/calling terminal, not the terminal that
	// happens to have GUI focus. Read its current labels for every fresh launch.
	CallerTerminal string
}
type Adapter struct {
	Config Config
	Call   func(context.Context, string, any, any) error
	Query  func(context.Context, ...string) ([]byte, error)
}

func NewAdapter(c Config) *Adapter {
	if c.Binary == "" {
		c.Binary = "hc"
	}
	if c.Session == "" {
		c.Session = "default"
	}
	if c.AppBinary == "" {
		c.AppBinary = "herdr-app"
	}
	if c.CallerTerminal == "" {
		c.CallerTerminal = os.Getenv("HC_TERMINAL_ID")
	}
	return &Adapter{Config: c}
}
func (a *Adapter) Kind() multiplexer.Kind { return multiplexer.KindHerdrCore }
func (a *Adapter) Env() []string {
	var out []string
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		switch k {
		case "HC_SESSION", "HC_TERMINAL_ID", "HC_PARENT", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "HERDR_SOCKET_PATH", "HERDR_CLIENT_SOCKET_PATH":
			continue
		}
		out = append(out, v)
	}
	return append(out, "HC_SESSION="+a.Config.Session)
}
func (a *Adapter) command(ctx context.Context, binary string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, binary, args...)
	c.Env = a.Env()
	if f := multiplexer.OperationLock(ctx); f != nil {
		c.ExtraFiles = []*os.File{f}
	}
	return c
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Kind string `json:"kind"`
	} `json:"data"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("core %s: %s", e.Data.Kind, e.Message) }
func decodeReply(data []byte, result any) error {
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Error != nil {
		if r.Error.Data.Kind == "not_found" || r.Error.Data.Kind == "finished" {
			return fmt.Errorf("%w: %s", multiplexer.ErrContainerNotFound, r.Error)
		}
		return r.Error
	}
	if len(r.Result) == 0 {
		return errors.New("core reply missing result")
	}
	return json.Unmarshal(r.Result, result)
}
func (a *Adapter) RPC(ctx context.Context, method string, params, result any) error {
	if a.Call != nil {
		return a.Call(ctx, method, params, result)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	cmd := a.command(ctx, a.Config.Binary, "connect")
	cmd.Stdin = bytes.NewReader(append(raw, '\n'))
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	var stdout limitedBuffer
	cmd.Stdout = &stdout
	err = cmd.Run()
	out := stdout.Bytes()
	if err != nil {
		return fmt.Errorf("%s: %w (%s)", method, err, strings.TrimSpace(stderr.String()))
	}
	scan := bufio.NewScanner(bytes.NewReader(out))
	scan.Buffer(make([]byte, 4096), 8<<20)
	for scan.Scan() {
		var id struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(scan.Bytes(), &id) == nil && id.ID == 1 {
			return decodeReply(scan.Bytes(), result)
		}
	}
	if scan.Err() != nil {
		return scan.Err()
	}
	return fmt.Errorf("%s: no core reply", method)
}

type hello struct {
	Generation int `json:"generation"`
	Core       struct {
		ID string `json:"id"`
	} `json:"core"`
}

func (a *Adapter) identity(ctx context.Context) (string, error) {
	var h hello
	if err := a.RPC(ctx, "hello", map[string]any{"generation": 1}, &h); err != nil {
		return "", err
	}
	if h.Generation != 1 || h.Core.ID == "" {
		return "", errors.New("Herdr core requires protocol generation 1")
	}
	return h.Core.ID, nil
}
func (a *Adapter) Ensure(ctx context.Context, namespace string) error {
	if namespace != "" && namespace != a.Config.Session {
		return errors.New("core session does not match configured namespace")
	}
	_, err := a.identity(ctx)
	return err
}

type metadata struct {
	CoreID  string `json:"core_id"`
	Token   string `json:"token"`
	PID     uint32 `json:"pid"`
	Created uint64 `json:"created_ms"`
	Space   string `json:"space_id,omitempty"`
}
type terminal struct {
	Address    string            `json:"address"`
	State      string            `json:"state"`
	PID        uint32            `json:"pid"`
	Created    uint64            `json:"created_ms"`
	Reason     *string           `json:"reason"`
	Labels     map[string]string `json:"labels"`
	Cols, Rows uint16
	Status     *struct{ State, Kind, Msg string } `json:"status"`
}

func (a *Adapter) Launch(ctx context.Context, s multiplexer.LaunchSpec) (multiplexer.ContainerRef, error) {
	return a.launch(ctx, s, "")
}

// LaunchBoard resolves its destination before starting the UI, so its scheduler
// cannot launch agents before the board's space labels have been assigned.
func (a *Adapter) LaunchBoard(ctx context.Context, s multiplexer.LaunchSpec) (multiplexer.ContainerRef, error) {
	id, err := a.identity(ctx)
	if err != nil {
		return multiplexer.ContainerRef{}, err
	}
	space, err := a.originSpace(ctx, id)
	if err != nil {
		return multiplexer.ContainerRef{}, err
	}
	if space == "" {
		space, err = a.selectedSpace(ctx)
		if err != nil {
			return multiplexer.ContainerRef{}, err
		}
	}
	if space == "" {
		b := make([]byte, 16)
		if _, err = rand.Read(b); err != nil {
			return multiplexer.ContainerRef{}, err
		}
		space = "kanbi-" + hex.EncodeToString(b)
	}
	return a.launch(ctx, s, space)
}

func (a *Adapter) launch(ctx context.Context, s multiplexer.LaunchSpec, space string) (multiplexer.ContainerRef, error) {
	if s.Namespace != "" && s.Namespace != a.Config.Session {
		return multiplexer.ContainerRef{}, errors.New("core launch namespace does not match configuration")
	}
	if len(s.Command) == 0 {
		return multiplexer.ContainerRef{}, errors.New("empty core command")
	}
	id, err := a.identity(ctx)
	if err != nil {
		return multiplexer.ContainerRef{}, err
	}
	if space == "" {
		space, err = a.originSpace(ctx, id)
		if err != nil {
			return multiplexer.ContainerRef{}, err
		}
	}
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return multiplexer.ContainerRef{}, err
	}
	token := hex.EncodeToString(b)
	var t terminal
	childEnv := map[string]string{}
	for _, v := range a.Env() {
		k, val, _ := strings.Cut(v, "=")
		if strings.HasPrefix(k, "HC_") || strings.HasPrefix(k, "HERDR_") || strings.HasPrefix(k, "TERM") || k == "COLORTERM" || k == "LINES" || k == "COLUMNS" {
			continue
		}
		childEnv[k] = val
	}
	labels := map[string]string{"kanbi.owner": token, "kanbi.title": s.Name}
	if space != "" {
		labels["herdr.space"] = space
		labels["herdr.tab"] = "kanbi-" + token
	}
	err = a.RPC(ctx, "terminal.run", map[string]any{"env": childEnv, "argv": s.Command, "cwd": s.CWD, "no_parent": true, "no_notify": true, "keep": false, "labels": labels}, &t)
	if err != nil {
		return multiplexer.ContainerRef{}, err
	}
	raw, _ := json.Marshal(metadata{CoreID: id, Token: token, PID: t.PID, Created: t.Created, Space: space})
	ref := multiplexer.ContainerRef{Kind: a.Kind(), Namespace: a.Config.Session, ID: t.Address, Name: s.Name, Metadata: string(raw)}
	if t.Address == "" || t.State != "live" || t.PID == 0 {
		if t.Address != "" && t.Labels["kanbi.owner"] == token {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			var v any
			_ = a.RPC(cleanup, "terminal.kill", map[string]any{"terminal": t.Address}, &v)
		}
		return ref, errors.New("core program did not become live")
	}
	return ref, nil
}
func (a *Adapter) get(ctx context.Context, r multiplexer.ContainerRef) (terminal, error) {
	var m metadata
	var t terminal
	if r.Kind != a.Kind() || r.Namespace != a.Config.Session || json.Unmarshal([]byte(r.Metadata), &m) != nil || m.Token == "" || m.PID == 0 {
		return t, errors.New("core reference is incomplete or belongs to another namespace")
	}
	id, err := a.identity(ctx)
	if err != nil {
		return t, err
	}
	if id != m.CoreID {
		return t, multiplexer.ErrContainerNotFound
	}
	if err = a.RPC(ctx, "terminal.get", map[string]any{"terminal": r.ID}, &t); err != nil {
		return t, err
	}
	if t.Address != r.ID || t.State != "live" || t.Labels["kanbi.owner"] != m.Token || t.PID != m.PID || t.Created != m.Created || t.Reason != nil {
		return t, multiplexer.ErrContainerNotFound
	}
	return t, nil
}
func (a *Adapter) Validate(ctx context.Context, r multiplexer.ContainerRef) (bool, error) {
	_, err := a.get(ctx, r)
	if errors.Is(err, multiplexer.ErrContainerNotFound) {
		return false, nil
	}
	return err == nil, err
}
func (a *Adapter) Read(ctx context.Context, r multiplexer.ContainerRef, o multiplexer.ReadOptions) (string, error) {
	if _, err := a.get(ctx, r); err != nil {
		return "", err
	}
	if o.Lines <= 0 {
		o.Lines = 200
	}
	if o.Lines > 2000 {
		o.Lines = 2000
	}
	var out struct {
		Text string `json:"text"`
	}
	err := a.RPC(ctx, "terminal.read", map[string]any{"terminal": r.ID, "lines": o.Lines}, &out)
	return out.Text, err
}
func (a *Adapter) SendKeys(ctx context.Context, r multiplexer.ContainerRef, keys ...string) error {
	if _, err := a.get(ctx, r); err != nil {
		return err
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		switch k {
		case "Escape":
			k = "esc"
		case "Enter":
			k = "enter"
		}
		if strings.HasPrefix(k, "C-") {
			k = "ctrl+" + k[2:]
		}
		out[i] = strings.ToLower(k)
	}
	var v any
	return a.RPC(ctx, "terminal.keys", map[string]any{"terminal": r.ID, "keys": out}, &v)
}
func (a *Adapter) Close(ctx context.Context, r multiplexer.ContainerRef) error {
	if _, err := a.get(ctx, r); err != nil {
		if errors.Is(err, multiplexer.ErrContainerNotFound) {
			return nil
		}
		return err
	}
	var v any
	return a.RPC(ctx, "terminal.kill", map[string]any{"terminal": r.ID}, &v)
}
func (a *Adapter) Detect(ctx context.Context, r multiplexer.ContainerRef) (multiplexer.Detection, error) {
	t, err := a.get(ctx, r)
	if err != nil {
		return multiplexer.Detection{}, err
	}
	d := multiplexer.Detection{Source: multiplexer.DetectionSourceUnknown}
	if t.Status == nil {
		return d, nil
	}
	d.NativeState = t.Status.State
	d.Reason = t.Status.Msg
	switch t.Status.State {
	case "working":
		d.State = "running"
	case "idle":
		d.State = "idle_unknown"
	case "done":
		d.State = "waiting_for_user"
	case "blocked":
		d.State = "waiting_for_user"
		if t.Status.Kind == "permission" {
			d.State = "needs_permission"
		}
	case "error":
		d.State = "error"
	}
	if d.State != "" {
		d.Source = multiplexer.DetectionSourceNative
	}
	return d, nil
}
func (a *Adapter) App(ctx context.Context, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := a.command(ctx, a.Config.AppBinary, "do", method, string(raw)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Herdr app %s: %w (%s); open Herdr app using HC_SESSION=%s", method, err, strings.TrimSpace(string(out)), a.Config.Session)
	}
	return nil
}

// Focus preserves existing placement. Unpresented agents use their launch space;
// only callers without a GUI origin fall back to the selected app space.
// Match the random tab id as well as the core-local address before focusing;
// another app home/core can have its own unrelated t1.
func (a *Adapter) Focus(ctx context.Context, r multiplexer.ContainerRef) error {
	t, err := a.get(ctx, r)
	if err != nil {
		return err
	}
	var m metadata
	_ = json.Unmarshal([]byte(r.Metadata), &m)
	space, tab := t.Labels["herdr.space"], t.Labels["herdr.tab"]
	if space == "" || tab == "" {
		if space == "" {
			space = m.Space
		}
		if space == "" {
			space, err = a.selectedSpace(ctx)
			if err != nil {
				return err
			}
		}
		if space == "" {
			space = "kanbi-" + m.Token
		}
		if tab == "" {
			tab = "kanbi-" + m.Token
		}
		var v any
		if err = a.RPC(ctx, "terminal.label", map[string]any{"terminal": r.ID, "labels": map[string]string{"herdr.space": space, "herdr.tab": tab}}, &v); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		pane, e := a.appPane(ctx, space, tab, r.ID)
		if e != nil {
			return e
		}
		if pane != "" {
			if _, err = a.get(ctx, r); err != nil {
				return err
			}
			// App adoption enables keep. Disable it only after adoption has completed.
			var v any
			if err = a.RPC(ctx, "terminal.keep", map[string]any{"terminal": r.ID, "keep": false}, &v); err != nil {
				return err
			}
			if r.Name != "" {
				if err = a.App(ctx, "tab.rename", map[string]any{"id": tab, "name": r.Name}); err != nil {
					return err
				}
			}
			return a.App(ctx, "pane.focus", map[string]any{"pane": pane})
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Herdr app has not adopted this terminal; check matching HC_SESSION and HERDR_APP_HOME: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (a *Adapter) appPane(ctx context.Context, space, tab, address string) (string, error) {
	raw, err := a.appQuery(ctx, "ls")
	if err != nil {
		return "", err
	}
	var world appWorld
	if err = json.Unmarshal(raw, &world); err != nil {
		return "", err
	}
	for _, s := range world.Spaces {
		if s.ID != space {
			continue
		}
		for _, t := range s.Tabs {
			if t.ID != tab {
				continue
			}
			for _, p := range t.Terminals {
				if p.Terminal == address {
					return p.Pane, nil
				}
			}
		}
	}
	return "", nil
}
func (a *Adapter) Rename(ctx context.Context, r multiplexer.ContainerRef, name string) error {
	t, err := a.get(ctx, r)
	if err != nil {
		return err
	}
	var v any
	if err = a.RPC(ctx, "terminal.label", map[string]any{"terminal": r.ID, "labels": map[string]string{"kanbi.title": name}}, &v); err != nil {
		return err
	}
	space, tab := t.Labels["herdr.space"], t.Labels["herdr.tab"]
	if space == "" || tab == "" {
		return nil
	}
	pane, err := a.appPane(ctx, space, tab, r.ID)
	// A closed app does not prevent durable title edits. Focus applies the saved
	// name on its next visit. Never focus or create a pane merely to rename it.
	if err != nil || pane == "" {
		return nil
	}
	return a.App(ctx, "tab.rename", map[string]any{"id": tab, "name": name})
}

var _ multiplexer.Interface = (*Adapter)(nil)

// Drain large responses while bounding retained data, including failed commands.
type limitedBuffer struct{ data bytes.Buffer }

func (b *limitedBuffer) Bytes() []byte  { return b.data.Bytes() }
func (b *limitedBuffer) String() string { return b.data.String() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := (8 << 20) - b.data.Len()
	if left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.data.Write(p)
	}
	return n, nil
}
