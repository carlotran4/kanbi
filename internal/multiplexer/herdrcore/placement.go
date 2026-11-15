package herdrcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// originSpace follows the board even after the user focuses another space or
// moves the board to a different space. Never infer ownership from GUI focus.
func (a *Adapter) originSpace(ctx context.Context, coreID string) (string, error) {
	caller := a.Config.CallerTerminal
	if caller == "" {
		return "", nil
	}
	if prefix, local, ok := strings.Cut(caller, "/"); ok {
		if prefix != coreID || strings.Contains(local, "/") {
			return "", fmt.Errorf("calling terminal belongs to another core; use the matching core session")
		}
		caller = local
	}
	var t terminal
	if err := a.RPC(ctx, "terminal.get", map[string]any{"terminal": caller}, &t); err != nil {
		return "", fmt.Errorf("locate calling Kanbi terminal: %w", err)
	}
	if t.Address != caller || t.State != "live" {
		return "", fmt.Errorf("calling Kanbi terminal is no longer live")
	}
	return t.Labels["herdr.space"], nil
}

type appWorld struct {
	Spaces []struct {
		ID      string
		Machine *string
		Tabs    []struct {
			ID        string
			Terminals []struct{ Terminal, Pane string }
		}
	}
}

func (a *Adapter) appQuery(ctx context.Context, args ...string) ([]byte, error) {
	if a.Query != nil {
		return a.Query(ctx, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := a.command(ctx, a.Config.AppBinary, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("Herdr app unavailable: %w; open the app with HC_SESSION=%s and the matching HERDR_APP_HOME", err, a.Config.Session)
	}
	return out, nil
}

// selectedSpace is only for a board launched outside core, or a headless agent
// first shown without a board origin. It never overrides a stored placement.
func (a *Adapter) selectedSpace(ctx context.Context) (string, error) {
	raw, err := a.appQuery(ctx, "status")
	if err != nil {
		return "", err
	}
	var status struct{ Space string }
	if err = json.Unmarshal(raw, &status); err != nil {
		return "", fmt.Errorf("read Herdr app selection: %w", err)
	}
	if status.Space == "" {
		return "", nil
	}
	raw, err = a.appQuery(ctx, "ls")
	if err != nil {
		return "", err
	}
	var world appWorld
	if err = json.Unmarshal(raw, &world); err != nil {
		return "", err
	}
	for _, space := range world.Spaces {
		if space.ID == status.Space {
			if (space.Machine != nil && *space.Machine != "") || strings.Contains(space.ID, "/") {
				return "", fmt.Errorf("selected Herdr space is on another machine; select a local space")
			}
			return space.ID, nil
		}
	}
	return "", fmt.Errorf("selected Herdr space no longer exists; select a space and retry")
}
