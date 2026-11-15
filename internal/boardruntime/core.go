package boardruntime

import (
	"context"
	"fmt"
	"os"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/multiplexer/herdrcore"
)

// LaunchCoreBoard runs the unchanged board in an independent core terminal and
// presents it in the already-running app. A failed presentation closes only the
// newly owned board terminal, never an existing agent or the core server.
func LaunchCoreBoard(cfg config.Config, exe string) error {
	c := cfg.Multiplexer.HerdrCore
	a := herdrcore.NewAdapter(herdrcore.Config{Binary: c.Binary, Session: c.Session, AppBinary: c.AppBinary})
	ctx := context.Background()
	command := []string{"env", "KANBI_INNER=1", "KANBI_MULTIPLEXER=herdr-core"}
	for _, key := range []string{"KANBI_CONFIG", "KANBI_DB", "KANBI_DATA_DIR", "KANBI_STATE_DIR", "KANBI_DEV_RELOAD", "KANBI_DEV_ROOT"} {
		if val := os.Getenv(key); val != "" {
			command = append(command, key+"="+val)
		}
	}
	command = append(command, exe, "--board")
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ref, err := a.LaunchBoard(ctx, multiplexer.LaunchSpec{Name: "Kanbi", CWD: cwd, Command: command})
	if err != nil {
		return err
	}
	if err = a.Focus(ctx, ref); err != nil {
		_ = a.Close(ctx, ref)
		return fmt.Errorf("show Kanbi in Herdr app: %w", err)
	}
	return nil
}
