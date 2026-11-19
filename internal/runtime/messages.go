package runtime

import "github.com/carlotran4/poke/adapters"

func commandWithMessageIdentity(command []string, id int64, path string) []string {
	return adapters.WithIdentity(command, id, path, "KANBI_SESSION_ID", "KANBI_DB")
}
