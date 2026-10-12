package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

// FleetPane's terminal identity survives moves; public pane IDs do not.
type FleetPane struct {
	ID        string            `json:"pane_id"`
	Terminal  string            `json:"terminal_id"`
	Workspace string            `json:"workspace_id"`
	Tab       string            `json:"tab_id"`
	CWD       string            `json:"cwd"`
	Label     string            `json:"label"`
	Tokens    map[string]string `json:"tokens,omitempty"`
	Scroll    struct {
		Rows int `json:"viewport_rows"`
	} `json:"scroll"`
}
type FleetRect struct{ Width, Height int }
type FleetLayout struct {
	Focused string    `json:"focused_pane_id"`
	Zoomed  bool      `json:"zoomed"`
	Area    FleetRect `json:"area"`
	Panes   []struct {
		ID   string    `json:"pane_id"`
		Rect FleetRect `json:"rect"`
	} `json:"panes"`
}

func (a *Adapter) FleetServer(ctx context.Context) (string, error) {
	out, err := a.run(ctx, "status", "--json")
	if err != nil {
		return "", err
	}
	var s struct {
		Server struct {
			Socket              string
			Running, Compatible bool
		}
	}
	if err = json.Unmarshal([]byte(out), &s); err != nil {
		return "", err
	}
	if !s.Server.Running || !s.Server.Compatible || s.Server.Socket == "" {
		return "", errors.New("Fleet requires a compatible local Herdr server")
	}
	return s.Server.Socket, nil
}
func (a *Adapter) FleetPane(ctx context.Context, id string) (FleetPane, error) {
	out, err := a.run(ctx, "pane", "get", id)
	if err != nil {
		if isNotFoundResponse(out, err) {
			return FleetPane{}, fmt.Errorf("%w: %s", multiplexer.ErrContainerNotFound, strings.TrimSpace(out))
		}
		return FleetPane{}, herdrCommandError("inspect Fleet pane", out, err)
	}
	var r struct{ Result struct{ Pane FleetPane } }
	err = json.Unmarshal([]byte(out), &r)
	if err == nil && r.Result.Pane.Terminal == "" {
		err = errors.New("Herdr did not supply a stable terminal identity")
	}
	return r.Result.Pane, err
}
func (a *Adapter) FleetPanes(ctx context.Context) ([]FleetPane, error) {
	out, err := a.run(ctx, "pane", "list")
	if err != nil {
		return nil, herdrCommandError("list Fleet panes", out, err)
	}
	var r struct{ Result struct{ Panes []FleetPane } }
	err = json.Unmarshal([]byte(out), &r)
	return r.Result.Panes, err
}
func (a *Adapter) FleetLayout(ctx context.Context, id string) (FleetLayout, error) {
	out, err := a.run(ctx, "pane", "layout", "--pane", id)
	if err != nil {
		return FleetLayout{}, herdrCommandError("read Fleet layout", out, err)
	}
	var r struct{ Result struct{ Layout FleetLayout } }
	err = json.Unmarshal([]byte(out), &r)
	return r.Result.Layout, err
}
func (a *Adapter) FleetSplit(ctx context.Context, id, direction string, ratio float64, marker string) (FleetPane, error) {
	out, err := a.run(ctx, "pane", "split", id, "--direction", direction, "--ratio", fmt.Sprint(ratio), "--cwd", os.TempDir(), "--env", "KANBI_FLEET_ANCHOR="+marker, "--no-focus")
	if err != nil {
		return FleetPane{}, herdrCommandError("create Fleet anchor", out, err)
	}
	obj := parseObject(out)
	p := firstString(obj, "result.new_pane.pane_id", "result.pane.pane_id")
	if p == "" {
		return FleetPane{}, fmt.Errorf("Fleet split returned no pane: %s", out)
	}
	return a.FleetPane(ctx, p)
}
func (a *Adapter) FleetMove(ctx context.Context, id, tab, target, direction string, ratio float64) (FleetPane, error) {
	out, err := a.run(ctx, "pane", "move", id, "--tab", tab, "--target-pane", target, "--split", direction, "--ratio", fmt.Sprint(ratio), "--no-focus")
	if err != nil {
		return FleetPane{}, herdrCommandError("move Fleet pane", out, err)
	}
	var r struct {
		Result struct {
			Move struct{ Pane FleetPane } `json:"move_result"`
		}
	}
	err = json.Unmarshal([]byte(out), &r)
	if err == nil && r.Result.Move.Pane.Terminal == "" {
		err = errors.New("Fleet move returned no terminal identity")
	}
	return r.Result.Move.Pane, err
}
func (a *Adapter) FleetPark(ctx context.Context, p FleetPane, token, label string) error {
	if out, err := a.run(ctx, "pane", "report-metadata", p.ID, "--source", "kanbi-fleet", "--token", "kanbi_fleet="+token, "--title", label); err != nil {
		return herdrCommandError("mark Fleet anchor", out, err)
	}
	// An owned inert terminal keeps the BSP slot and source tab alive after an
	// agent exits. Never run a harness in these anchors.
	out, err := a.run(ctx, "pane", "run", p.ID, "printf '%s\\n' "+shellQuote(label)+"; exec sleep 2147483647")
	if err != nil {
		return herdrCommandError("park Fleet anchor", out, err)
	}
	return nil
}
func (a *Adapter) FleetFocus(ctx context.Context, id string) error {
	p, err := a.FleetPane(ctx, id)
	if err != nil {
		return err
	}
	out, err := a.run(ctx, "tab", "focus", p.Tab)
	if err != nil {
		return herdrCommandError("focus Fleet tab", out, err)
	}
	layout, err := a.FleetLayout(ctx, id)
	if err != nil {
		return err
	}
	if len(layout.Panes) == 1 {
		return nil
	}
	for _, pair := range [][2]string{{"down", "up"}, {"up", "down"}, {"right", "left"}, {"left", "right"}} {
		out, err = a.run(ctx, "pane", "neighbor", "--pane", id, "--direction", pair[0])
		if err != nil {
			continue
		}
		neighbor := firstString(parseObject(out), "result.neighbor.neighbor_pane_id")
		if neighbor == "" {
			continue
		}
		out, err = a.run(ctx, "pane", "focus", "--pane", neighbor, "--direction", pair[1])
		if err != nil {
			return herdrCommandError("focus Fleet pane", out, err)
		}
		actual, err := a.FleetLayout(ctx, id)
		if err != nil {
			return err
		}
		if actual.Focused == id {
			return nil
		}
	}
	return errors.New("Herdr could not focus the requested pane through its neighbors")

}
func (a *Adapter) FleetUnzoom(ctx context.Context, id string) error {
	_, err := a.run(ctx, "pane", "zoom", id, "--off")
	return err
}
func (a *Adapter) FleetClose(ctx context.Context, id string) error {
	out, err := a.run(ctx, "pane", "close", id)
	if err != nil {
		return herdrCommandError("remove Fleet anchor", out, err)
	}
	return nil
}

// FleetRatio updates the existing BSP tree. layout.apply is deliberately absent.
func (a *Adapter) FleetRatio(ctx context.Context, socket, tab string, path []bool, ratio float64) error {
	if runner, ok := a.Runner.(interface {
		FleetRatio(context.Context, string, string, []bool, float64) error
	}); ok {
		return runner.FleetRatio(ctx, socket, tab, path, ratio)
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	_ = conn.SetDeadline(deadline)
	req := map[string]any{"id": "kanbi-fleet-ratio", "method": "layout.set_split_ratio", "params": map[string]any{"tab_id": tab, "path": path, "ratio": ratio}}
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	line, err := bufio.NewReaderSize(conn, 65536).ReadBytes('\n')
	if err != nil {
		return err
	}
	var r struct{ Error json.RawMessage }
	if err = json.Unmarshal(line, &r); err != nil {
		return err
	}
	if len(r.Error) > 0 && string(r.Error) != "null" {
		return fmt.Errorf("Herdr ratio: %s", r.Error)
	}
	return nil
}
func FleetPaneID(metadata, id string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(metadata), &m)
	if p, ok := m["pane_id"].(string); ok && p != "" {
		return p
	}
	if strings.Contains(id, ":p") {
		return id
	}
	return ""
}

func (a *Adapter) FleetRename(ctx context.Context, id, name string) error {
	out, err := a.run(ctx, "pane", "rename", id, name)
	if err != nil {
		return herdrCommandError("rename Fleet pane", out, err)
	}
	return nil
}

func (a *Adapter) FleetWorkspaceLabel(ctx context.Context, id string) (string, error) {
	out, err := a.run(ctx, "workspace", "get", id)
	if err != nil {
		return "", herdrCommandError("inspect Fleet source workspace", out, err)
	}
	label := firstString(parseObject(out), "result.workspace.label")
	if label == "" {
		return "", errors.New("Fleet source workspace has no label")
	}
	return label, nil
}
func (a *Adapter) FleetMoveNew(ctx context.Context, id, workspace, workspaceLabel, tabLabel string) (FleetPane, error) {
	args := []string{"pane", "move", id}
	if workspace != "" {
		args = append(args, "--new-tab", "--workspace", workspace, "--label", tabLabel)
	} else {
		args = append(args, "--new-workspace", "--label", workspaceLabel, "--tab-label", tabLabel)
	}
	args = append(args, "--no-focus")
	out, err := a.run(ctx, args...)
	if err != nil {
		return FleetPane{}, herdrCommandError("recreate Fleet source tab", out, err)
	}
	var r struct {
		Result struct {
			Move struct{ Pane FleetPane } `json:"move_result"`
		}
	}
	err = json.Unmarshal([]byte(out), &r)
	if err == nil && r.Result.Move.Pane.Terminal == "" {
		err = errors.New("Fleet return supplied no terminal identity")
	}
	return r.Result.Move.Pane, err
}

// Reject recycled public IDs before any read/input/focus/close operation. Older
// refs without terminal identity retain their existing validation contract.
func (a *Adapter) checkTerminalIdentity(ctx context.Context, ref multiplexer.ContainerRef) error {
	expected := refMeta(ref, "terminal_id")
	if expected == "" {
		return nil
	}
	id := FleetPaneID(ref.Metadata, ref.ID)
	if id == "" {
		return errors.New("stored terminal identity has no pane ID")
	}
	p, err := a.FleetPane(ctx, id)
	if err != nil {
		return err
	}
	if p.Terminal != expected {
		return multiplexer.ErrContainerNotFound
	}
	return nil
}

// The creation nonce is set atomically in the shell's environment by pane.split.
// Read-only process inspection resolves a lost split reply without trusting cwd,
// labels, or a recycled public pane ID. Environment output is never reported.
func (a *Adapter) FleetAnchorOwned(ctx context.Context, p FleetPane, marker string) (bool, error) {
	out, err := a.run(ctx, "pane", "process-info", "--pane", p.ID)
	if err != nil {
		return false, herdrCommandError("inspect pending Fleet anchor", out, err)
	}
	var r struct {
		Result struct {
			Info struct {
				PID int `json:"shell_pid"`
			} `json:"process_info"`
		}
	}
	if err = json.Unmarshal([]byte(out), &r); err != nil {
		return false, err
	}
	if r.Result.Info.PID <= 0 {
		return false, errors.New("pending Fleet anchor has no inspectable shell PID")
	}
	env, err := a.Runner.Run(ctx, "ps", "eww", "-p", strconv.Itoa(r.Result.Info.PID), "-o", "command=")
	if err != nil {
		return false, errors.New("cannot verify pending Fleet anchor creation nonce through local process inspection")
	}
	expected := "KANBI_FLEET_ANCHOR=" + marker
	for _, field := range strings.Fields(env) {
		if field == expected {
			return true, nil
		}
	}
	return false, nil
}
