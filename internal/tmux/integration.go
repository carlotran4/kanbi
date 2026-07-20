package tmux

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/carlotran4/kanbi/internal/harness"
	integrationpkg "github.com/carlotran4/kanbi/internal/integration"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
)

// LaunchIntegration launches a repository-scoped integration agent without a
// synthetic ticket. Runtime identity remains owned by integration_runs.
func (m *Manager) LaunchIntegration(ctx context.Context, spec integrationpkg.LaunchSpec) (storage.IntegrationRun, error) {
	command, promptSent, err := harness.StartCommandWithPrompt(m.Config.Harnesses, spec.Harness, spec.Prompt, true)
	if err != nil {
		return storage.IntegrationRun{}, err
	}
	if !promptSent {
		return storage.IntegrationRun{}, fmt.Errorf("integration harness %s must support prompt arguments", spec.Harness)
	}
	executable, err := os.Executable()
	if err != nil {
		return storage.IntegrationRun{}, err
	}
	command = append([]string{"env", "KANBI_INTEGRATION_RUN_ID=" + spec.PublicID, "KANBI_INTEGRATION_TOKEN=" + spec.Token, "KANBI_INTEGRATION_KANBI_BIN=" + executable, "KANBI_DB=" + m.Config.DBPath}, command...)
	kind := m.defaultMultiplexerKind()
	name := spec.Name
	if kind == multiplexer.KindHerdr {
		adapter := m.herdrAdapter()
		ref, err := adapter.Launch(ctx, multiplexer.LaunchSpec{Name: name, CWD: spec.CWD, Command: command, Namespace: m.currentHerdrWorkspace(ctx)})
		if err != nil {
			return storage.IntegrationRun{}, err
		}
		if err := adapter.Focus(ctx, ref); err != nil {
			_ = adapter.Close(context.Background(), ref)
			return storage.IntegrationRun{}, err
		}
		return storage.IntegrationRun{Multiplexer: sql.NullString{String: string(kind), Valid: true}, MuxNamespace: sql.NullString{String: ref.Namespace, Valid: ref.Namespace != ""}, MuxContainerID: sql.NullString{String: ref.ID, Valid: ref.ID != ""}, MuxContainerName: sql.NullString{String: ref.Name, Valid: ref.Name != ""}, MuxMetadata: sql.NullString{String: ref.Metadata, Valid: ref.Metadata != ""}}, nil
	}
	if err := m.EnsureSession(ctx); err != nil {
		return storage.IntegrationRun{}, err
	}
	name, err = m.availableWindowNameInSession(ctx, m.Config.TmuxSession, name)
	if err != nil {
		return storage.IntegrationRun{}, err
	}
	out, err := m.run(ctx, append(newWindowArgs(m.Config.TmuxSession, name, spec.CWD), ShellCommand(command))...)
	if err != nil {
		return storage.IntegrationRun{}, err
	}
	id := strings.TrimSpace(out)
	if err := m.switchWindow(ctx, m.Config.TmuxSession, id); err != nil {
		_ = NewMultiplexerAdapter(m).Close(context.Background(), multiplexer.ContainerRef{Kind: multiplexer.KindTmux, Namespace: m.Config.TmuxSession, ID: id, Name: name})
		return storage.IntegrationRun{}, err
	}
	return storage.IntegrationRun{Multiplexer: sql.NullString{String: "tmux", Valid: true}, MuxNamespace: sql.NullString{String: m.Config.TmuxSession, Valid: true}, MuxContainerID: sql.NullString{String: id, Valid: id != ""}, MuxContainerName: sql.NullString{String: name, Valid: true}}, nil
}

func (m *Manager) CloseIntegration(ctx context.Context, run storage.IntegrationRun) error {
	kind := multiplexer.Kind(run.Multiplexer.String)
	ref := multiplexer.ContainerRef{Kind: kind, Namespace: run.MuxNamespace.String, ID: run.MuxContainerID.String, Name: run.MuxContainerName.String, Metadata: run.MuxMetadata.String}
	adapter, err := m.multiplexerAdapter(kind)
	if err != nil {
		return err
	}
	valid, err := adapter.Validate(ctx, ref)
	if err != nil {
		return err
	}
	if !valid {
		return nil
	}
	return adapter.Close(ctx, ref)
}

func (m *Manager) RefreshIntegrationRuns(ctx context.Context) error {
	if m.Store == nil {
		return nil
	}
	runs, err := m.Store.ListIntegrationRuns(ctx, 0)
	if err != nil {
		return err
	}
	for _, run := range runs {
		switch run.State {
		case storage.IntegrationStateRunning, storage.IntegrationStateWaitingForUser, storage.IntegrationStateNeedsPermission:
		default:
			continue
		}
		kind := multiplexer.Kind(run.Multiplexer.String)
		ref := multiplexer.ContainerRef{Kind: kind, Namespace: run.MuxNamespace.String, ID: run.MuxContainerID.String, Name: run.MuxContainerName.String, Metadata: run.MuxMetadata.String}
		adapter, err := m.multiplexerAdapter(kind)
		if err != nil {
			continue
		}
		out, err := adapter.Read(ctx, ref, multiplexer.ReadOptions{Lines: 200})
		if err != nil {
			continue
		}
		state, _, _, _, _ := harness.DetectState(out, "", 0)
		next := storage.IntegrationStateRunning
		switch state {
		case "waiting_for_user":
			next = storage.IntegrationStateWaitingForUser
		case "needs_permission":
			next = storage.IntegrationStateNeedsPermission
		}
		if next != run.State {
			if err := m.Store.UpdateIntegrationRuntime(ctx, run.PublicID, next, run); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) FocusIntegration(ctx context.Context, run storage.IntegrationRun) error {
	kind := multiplexer.Kind(run.Multiplexer.String)
	ref := multiplexer.ContainerRef{Kind: kind, Namespace: run.MuxNamespace.String, ID: run.MuxContainerID.String, Name: run.MuxContainerName.String, Metadata: run.MuxMetadata.String}
	if kind == multiplexer.KindHerdr {
		return m.herdrAdapter().Focus(ctx, ref)
	}
	return m.switchWindow(ctx, run.MuxNamespace.String, ref.Target())
}
