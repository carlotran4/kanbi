package runtime

import (
	"context"

	"github.com/carlotran4/kanbi/internal/storage"
)

// CloseLegacyIntegration closes only the recorded runtime container. Candidate
// checkouts and all durable history remain available for manual recovery.
func (m *Manager) CloseLegacyIntegration(ctx context.Context, run storage.IntegrationRun) error {
	if !run.MuxContainerID.Valid {
		return nil
	}
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
