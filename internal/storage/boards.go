package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"kanbi/internal/kanban"
	"os"
	"path/filepath"

	"strings"
	"time"
)

const boardSelectSQL = `select id, name, coalesce(workdir,''), coalesce(ticket_backend,'local'), coalesce(backend_query,''), coalesce(backend_config,''), last_sync_at, last_sync_error from boards`

func boardScanDest(b *Board) []any {
	return []any{&b.ID, &b.Name, &b.Workdir, &b.TicketBackend, &b.BackendQuery, &b.BackendConfig, &b.LastSyncAt, &b.LastSyncError}
}

func (s *Store) DefaultBoard(ctx context.Context) (Board, error) {
	var b Board
	err := s.db.QueryRowContext(ctx, boardSelectSQL+` order by id limit 1`).Scan(boardScanDest(&b)...)
	return b, err
}

func (s *Store) BoardByName(ctx context.Context, name string) (Board, error) {
	var b Board
	err := s.db.QueryRowContext(ctx, boardSelectSQL+` where lower(name)=lower(?) order by id limit 1`, strings.TrimSpace(name)).Scan(boardScanDest(&b)...)
	return b, err
}

func (s *Store) RenameBoard(ctx context.Context, boardID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("board name is required")
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from boards where lower(name)=lower(?) and id<>?`, name, boardID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return errors.New("board name already exists")
	}
	res, err := s.db.ExecContext(ctx, `update boards set name=?, updated_at=? where id=?`, name, time.Now().UTC(), boardID)
	return requireAffected(res, err)
}

func (s *Store) SetBoardWorkdir(ctx context.Context, boardID int64, workdir string) error {
	workdir, err := normalizeWorkdir(workdir)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `update boards set workdir=?, updated_at=? where id=?`, nullableString(workdir), time.Now().UTC(), boardID)
	return requireAffected(res, err)
}

func (s *Store) MarkBoardSync(ctx context.Context, boardID int64, syncErr error) error {
	now := time.Now().UTC()
	var errText any
	if syncErr != nil {
		errText = syncErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `update boards set last_sync_at=?, last_sync_error=?, updated_at=? where id=?`, now, errText, now, boardID)
	return err
}

func (s *Store) DeleteBoard(ctx context.Context, boardID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `select count(*) from boards`).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return errors.New("cannot delete the last board")
	}
	var active int
	if err := tx.QueryRowContext(ctx, `select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.board_id=? and s.is_active=1`, boardID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("cannot delete board with active sessions")
	}

	res, err := tx.ExecContext(ctx, `delete from boards where id=?`, boardID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) ListBoards(ctx context.Context) ([]Board, error) {
	rows, err := s.db.QueryContext(ctx, boardSelectSQL+` order by lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []Board
	for rows.Next() {
		var b Board
		if err := rows.Scan(boardScanDest(&b)...); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func (s *Store) CreateBoard(ctx context.Context, name string) (Board, error) {
	cwd, _ := os.Getwd()
	return s.CreateBoardWithWorkdir(ctx, name, cwd)
}

func normalizeWorkdir(workdir string) (string, error) {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return "", nil
	}
	if workdir == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		workdir = home
	} else if strings.HasPrefix(workdir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		workdir = filepath.Join(home, strings.TrimPrefix(workdir, "~/"))
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", errors.New("board workdir must be a directory")
	}
	return abs, nil
}

func (s *Store) CreateBoardWithWorkdir(ctx context.Context, name, workdir string) (Board, error) {
	return s.CreateBoardWithOptions(ctx, CreateBoardOptions{Name: name, Workdir: workdir, TicketBackend: "local"})
}

func (s *Store) CreateBoardWithOptions(ctx context.Context, opts CreateBoardOptions) (Board, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return Board{}, errors.New("board name is required")
	}
	backend := strings.ToLower(strings.TrimSpace(opts.TicketBackend))
	if backend == "" {
		backend = "local"
	}
	if !validTicketBackend(backend) {
		return Board{}, fmt.Errorf("unsupported ticket backend %q", backend)
	}
	query := strings.TrimSpace(opts.BackendQuery)
	backendConfig := strings.TrimSpace(opts.BackendConfig)
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from boards where lower(name)=lower(?)`, name).Scan(&existing); err != nil {
		return Board{}, err
	}
	if existing > 0 {
		return Board{}, errors.New("board name already exists")
	}
	workdir, err := normalizeWorkdir(opts.Workdir)
	if err != nil {
		return Board{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Board{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `insert into boards(name,workdir,next_ticket_number,ticket_backend,backend_query,backend_config,created_at,updated_at) values(?,?,1,?,?,?,?,?)`, name, nullableString(workdir), backend, nullableString(query), nullableString(backendConfig), now, now)
	if err != nil {
		return Board{}, err
	}
	boardID, _ := res.LastInsertId()
	for _, col := range kanban.DefaultColumns() {
		if _, err := tx.ExecContext(ctx, `insert into columns(board_id,name,position,created_at,updated_at) values(?,?,?,?,?)`, boardID, col.Name, col.Position, now, now); err != nil {
			return Board{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Board{}, err
	}
	return Board{ID: boardID, Name: name, Workdir: workdir, TicketBackend: backend, BackendQuery: query, BackendConfig: backendConfig}, nil
}

func (s *Store) ensureDefaultBoard(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `select count(*) from boards`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return tx.Commit()
	}
	now := time.Now().UTC()
	cwd, _ := os.Getwd()
	res, err := tx.ExecContext(ctx, `insert into boards(name,workdir,next_ticket_number,ticket_backend,created_at,updated_at) values('Default',?,1,'local',?,?)`, nullableString(cwd), now, now)
	if err != nil {
		return err
	}
	boardID, _ := res.LastInsertId()
	for _, col := range kanban.DefaultColumns() {
		if _, err := tx.ExecContext(ctx, `insert into columns(board_id,name,position,created_at,updated_at) values(?,?,?,?,?)`, boardID, col.Name, col.Position, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
