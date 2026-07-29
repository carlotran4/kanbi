package storage

import "database/sql"

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
