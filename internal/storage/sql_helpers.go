package storage

import (
	"database/sql"

	"github.com/carlotran4/kanbi/internal/harness"
)

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func requireAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func validHarness(name string) bool {
	_, ok := harness.BuiltinContract(name)
	return ok
}

func validTicketBackend(name string) bool {
	switch name {
	case "local", "github", "atlassian":
		return true
	default:
		return false
	}
}
