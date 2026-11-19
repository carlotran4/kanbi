package runtime

import (
	"context"
	"errors"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/poke/adapters"
)

func (m *Manager) prepareMessageLaunch(command []string, id int64, harnessName, launchCWD string) ([]string, error) {
	return adapters.PrepareLaunch(command, id, harnessName, launchCWD, adapters.LaunchOptions{
		Database: m.Store.DatabasePath(), PluginName: "kanbi-messages", CLIEnv: "KANBI_MAILBOX_CLI", EndpointEnv: "KANBI_MESSAGE_ENDPOINT_FILE", ClaudeHook: `"$KANBI_MAILBOX_CLI" messages register-claude`,
	})
}

// Called only by the recipient's own CLI/native receiver. Expose a small
// notification claim surface without accepting arbitrary sender identities.
func ClaimNativeMessage(ctx context.Context, store *storage.Store, recipient, id int64) (bool, error) {
	unlock, err := store.LockRuntime(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	ses, ok, err := store.SessionByID(ctx, recipient)
	if err != nil {
		return false, err
	}
	if !ok || !ses.IsActive || ses.Harness != "pi" {
		return false, errors.New("native message claims require the owning Pi session")
	}
	endpoint, err := readMessageEndpoint(MessageEndpointPath(store.DatabasePath(), ses.ID))
	if err != nil || endpoint.Disabled || endpoint.SessionID != ses.ID || endpoint.NativeRef == "" || ses.HarnessSessionRef.Valid && ses.HarnessSessionRef.String != endpoint.NativeRef {
		return false, errors.New("Pi native conversation identity is not registered for this attempt")
	}
	if ses.OperatorID != 0 {
		recipient = -ses.OperatorID
	}
	return store.ClaimMessageNotification(ctx, recipient, id, "pi_extension")
}
