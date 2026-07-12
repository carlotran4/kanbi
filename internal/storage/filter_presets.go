package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Store) ListFilterPresets(ctx context.Context) ([]MasterFilterPreset, error) {
	rows, err := s.db.QueryContext(ctx, `select id, name, payload_json, created_at, updated_at from master_filter_presets order by lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var presets []MasterFilterPreset
	for rows.Next() {
		p, err := scanFilterPreset(rows)
		if err != nil {
			return nil, err
		}
		presets = append(presets, p)
	}
	return presets, rows.Err()
}

func (s *Store) FilterPresetByID(ctx context.Context, id int64) (MasterFilterPreset, error) {
	return scanFilterPreset(s.db.QueryRowContext(ctx, `select id, name, payload_json, created_at, updated_at from master_filter_presets where id=?`, id))
}

func (s *Store) SaveFilterPreset(ctx context.Context, name string, filter DurableMasterFilter) (MasterFilterPreset, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return MasterFilterPreset{}, errors.New("filter preset name is required")
	}
	payload, err := json.Marshal(normalizeDurableFilter(filter))
	if err != nil {
		return MasterFilterPreset{}, err
	}
	now := time.Now().UTC()
	var id int64
	err = s.db.QueryRowContext(ctx, `select id from master_filter_presets where name=? collate nocase`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := s.db.ExecContext(ctx, `insert into master_filter_presets(name,payload_json,created_at,updated_at) values(?,?,?,?)`, name, string(payload), now, now)
		if err != nil {
			return MasterFilterPreset{}, err
		}
		id, _ = res.LastInsertId()
	} else if err != nil {
		return MasterFilterPreset{}, err
	} else {
		if _, err := s.db.ExecContext(ctx, `update master_filter_presets set name=?, payload_json=?, updated_at=? where id=?`, name, string(payload), now, id); err != nil {
			return MasterFilterPreset{}, err
		}
	}
	return s.FilterPresetByID(ctx, id)
}

func (s *Store) DeleteFilterPreset(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `delete from master_filter_presets where id=?`, id)
	return requireAffected(res, err)
}

// ResolveMasterFilter maps durable board UUIDs onto current integer board IDs.
// Unresolved UUIDs are returned without failing the whole filter.
func (s *Store) ResolveMasterFilter(ctx context.Context, d DurableMasterFilter) (MasterFilter, []string, error) {
	out := MasterFilter{
		Runtimes:        append([]string(nil), d.Runtimes...),
		Harnesses:       append([]string(nil), d.Harnesses...),
		Search:          d.Search,
		IncludeArchived: d.IncludeArchived,
	}
	var missing []string
	for _, uuid := range d.BoardUUIDs {
		uuid = strings.TrimSpace(uuid)
		if uuid == "" {
			continue
		}
		board, err := s.BoardByUUID(ctx, uuid)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, uuid)
			continue
		}
		if err != nil {
			return MasterFilter{}, nil, err
		}
		out.BoardIDs = append(out.BoardIDs, board.ID)
	}
	return out, missing, nil
}

// DurableFromMasterFilter converts a runtime filter into durable form using board UUIDs.
func (s *Store) DurableFromMasterFilter(ctx context.Context, f MasterFilter) (DurableMasterFilter, error) {
	out := DurableMasterFilter{
		Runtimes:        append([]string(nil), f.Runtimes...),
		Harnesses:       append([]string(nil), f.Harnesses...),
		Search:          f.Search,
		IncludeArchived: f.IncludeArchived,
	}
	for _, id := range f.BoardIDs {
		board, err := s.BoardByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return DurableMasterFilter{}, err
		}
		if board.UUID != "" {
			out.BoardUUIDs = append(out.BoardUUIDs, board.UUID)
		}
	}
	return out, nil
}

func scanFilterPreset(row boardScanner) (MasterFilterPreset, error) {
	var p MasterFilterPreset
	var payload string
	if err := row.Scan(&p.ID, &p.Name, &payload, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return MasterFilterPreset{}, err
	}
	if err := json.Unmarshal([]byte(payload), &p.Filter); err != nil {
		return MasterFilterPreset{}, err
	}
	p.Filter = normalizeDurableFilter(p.Filter)
	return p, nil
}

func normalizeDurableFilter(f DurableMasterFilter) DurableMasterFilter {
	out := DurableMasterFilter{
		Search:          strings.TrimSpace(f.Search),
		IncludeArchived: f.IncludeArchived,
	}
	seenUUID := map[string]bool{}
	for _, uuid := range f.BoardUUIDs {
		uuid = strings.TrimSpace(uuid)
		if uuid == "" || seenUUID[uuid] {
			continue
		}
		seenUUID[uuid] = true
		out.BoardUUIDs = append(out.BoardUUIDs, uuid)
	}
	seenRT := map[string]bool{}
	for _, runtime := range f.Runtimes {
		runtime = strings.TrimSpace(runtime)
		if runtime == "" || seenRT[runtime] {
			continue
		}
		seenRT[runtime] = true
		out.Runtimes = append(out.Runtimes, runtime)
	}
	seenH := map[string]bool{}
	for _, harness := range f.Harnesses {
		harness = strings.TrimSpace(harness)
		if harness == "" || seenH[harness] {
			continue
		}
		seenH[harness] = true
		out.Harnesses = append(out.Harnesses, harness)
	}
	return out
}
