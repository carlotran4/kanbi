package runtime

import (
	"context"
	"fmt"
	"os"

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
	kind := m.defaultMultiplexerKind()
	herdrPaneFirst := m.herdrAdapter().UsesAgentStart(ctx, command, spec.Harness)
	if herdrPaneFirst {
		command, err = harness.StartCommand(m.Config.Harnesses, spec.Harness)
		if err != nil {
			return storage.IntegrationRun{}, err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return storage.IntegrationRun{}, err
	}
	command = append([]string{"env", "KANBI_INTEGRATION_RUN_ID=" + spec.PublicID, "KANBI_INTEGRATION_TOKEN=" + spec.Token, "KANBI_INTEGRATION_KANBI_BIN=" + executable, "KANBI_DB=" + m.Config.DBPath}, command...)
	name := spec.Name
	namespace := m.currentHerdrWorkspace(ctx)
	adapter, err := m.multiplexerAdapter(kind)
	if err != nil {
		return storage.IntegrationRun{}, err
	}
	ref, err := adapter.Launch(ctx, multiplexer.LaunchSpec{Name: name, CWD: spec.CWD, Command: command, AgentKind: spec.Harness, Namespace: namespace})
	if err != nil {
		return storage.IntegrationRun{}, err
	}
	if herdrPaneFirst {
		if err := m.herdrAdapter().SubmitPrompt(ctx, ref, spec.Prompt, m.Config.PromptReadyTimeout); err != nil {
			_ = adapter.Close(context.Background(), ref)
			return storage.IntegrationRun{}, err
		}
	}
	if err := adapter.Focus(ctx, ref); err != nil {
		_ = adapter.Close(context.Background(), ref)
		return storage.IntegrationRun{}, err
	}
	runtime := storage.IntegrationRun{}
	ApplyContainerRefToIntegrationRun(&runtime, ref)
	return runtime, nil
}

func (m *Manager) CloseIntegration(ctx context.Context, run storage.IntegrationRun) error {
	ref := ContainerRefFromIntegrationRun(run)
	adapter, err := m.multiplexerAdapter(ref.Kind)
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
		ref := ContainerRefFromIntegrationRun(run)
		adapter, err := m.multiplexerAdapter(ref.Kind)
		if err != nil {
			continue
		}
		valid, err := adapter.Validate(ctx, ref)
		if err != nil || !valid {
			// Observation failures and stale refs leave durable state unchanged.
			continue
		}

		// Native state is authoritative only when the provider gives a concrete
		// native result. Integration runs intentionally have no idle_unknown;
		// all other live native states remain running.
		detection, err := adapter.Detect(ctx, ref)
		if err != nil {
			continue
		}
		if detection.Source == multiplexer.DetectionSourceNative && detection.State != "" {
			next := storage.IntegrationStateRunning
			switch detection.State {
			case storage.IntegrationStateWaitingForUser:
				next = storage.IntegrationStateWaitingForUser
			case storage.IntegrationStateNeedsPermission:
				next = storage.IntegrationStateNeedsPermission
			}
			if next != run.State {
				if err := m.Store.UpdateIntegrationRuntime(ctx, run.PublicID, next, run); err != nil {
					return err
				}
			}
			continue
		}

		out, err := adapter.Read(ctx, ref, multiplexer.ReadOptions{Lines: 200})
		if err != nil {
			continue
		}
		state, _, _, _, _ := harness.DetectState(out, "", 0)
		next := storage.IntegrationStateRunning
		switch state {
		case storage.IntegrationStateWaitingForUser:
			next = storage.IntegrationStateWaitingForUser
		case storage.IntegrationStateNeedsPermission:
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
	ref := ContainerRefFromIntegrationRun(run)
	adapter, err := m.multiplexerAdapter(ref.Kind)
	if err != nil {
		return err
	}
	valid, err := adapter.Validate(ctx, ref)
	if err != nil {
		return err
	}
	if !valid {
		return multiplexer.ErrContainerNotFound
	}
	return adapter.Focus(ctx, ref)
}
