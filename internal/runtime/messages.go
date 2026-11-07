package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Keep one env prefix so Herdr still recognizes canonical agent executables.
// Replace inherited identities, including wrapper-provided stale values.
func commandWithMessageIdentity(command []string, sessionID int64, dbPath string) []string {
	result := []string{"env", fmt.Sprintf("KANBI_SESSION_ID=%d", sessionID), "KANBI_DB=" + dbPath}
	if len(command) > 0 && command[0] == "env" {
		command = command[1:]
		for len(command) > 0 && strings.Contains(command[0], "=") {
			if !strings.HasPrefix(command[0], "KANBI_SESSION_ID=") && !strings.HasPrefix(command[0], "KANBI_DB=") {
				result = append(result, command[0])
			}
			command = command[1:]
		}
	}
	// Codex may run tools in its daemon, whose inherited environment belongs to
	// another client. Pin identity in this thread's explicit shell policy as well.
	// Global -c options precede resume subcommands and positional ticket prompts.
	if len(command) > 0 && command[0] == "codex" {
		sessionValue, _ := json.Marshal(strconv.FormatInt(sessionID, 10))
		dbValue, _ := json.Marshal(dbPath)
		result = append(result, "codex", "-c", "shell_environment_policy.set.KANBI_SESSION_ID="+string(sessionValue), "-c", "shell_environment_policy.set.KANBI_DB="+string(dbValue))
		return append(result, command[1:]...)
	}
	return append(result, command...)
}
