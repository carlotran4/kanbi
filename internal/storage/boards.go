package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/carlotran4/kanbi/internal/kanban"
	"os"
	"path/filepath"

	"strings"
	"time"
)

const boardSelectSQL = `select id, name, coalesce(uuid,''), coalesce(workdir,''), coalesce(ticket_backend,'local'), coalesce(backend_query,''), coalesce(backend_config,''), last_sync_at, last_sync_error, archived_at, coalesce(sync_enabled,1), source_export_uuid, coalesce(worktree_mode,'off') from boards`

type boardScanner interface {
	Scan(dest ...any) error
}

func scanBoard(row boardScanner, b *Board) error {
	var syncEnabled int
	if err := row.Scan(&b.ID, &b.Name, &b.UUID, &b.Workdir, &b.TicketBackend, &b.BackendQuery, &b.BackendConfig, &b.LastSyncAt, &b.LastSyncError, &b.ArchivedAt, &syncEnabled, &b.SourceExportUUID, &b.WorktreeMode); err != nil {
		return err
	}
	b.SyncEnabled = syncEnabled != 0
	if b.WorktreeMode == "" {
		b.WorktreeMode = WorktreeModeOff
	}
	return nil
}

func (s *Store) DefaultBoard(ctx context.Context) (Board, error) {
	var b Board
	err := scanBoard(s.db.QueryRowContext(ctx, boardSelectSQL+` where archived_at is null order by id limit 1`), &b)
	if errors.Is(err, sql.ErrNoRows) {
		// Fall back to any board so imported/archived-only databases still resolve.
		err = scanBoard(s.db.QueryRowContext(ctx, boardSelectSQL+` order by id limit 1`), &b)
	}
	return b, err
}

func (s *Store) BoardByID(ctx context.Context, id int64) (Board, error) {
	var b Board
	err := scanBoard(s.db.QueryRowContext(ctx, boardSelectSQL+` where id=?`, id), &b)
	return b, err
}

func (s *Store) BoardByUUID(ctx context.Context, uuid string) (Board, error) {
	var b Board
	err := scanBoard(s.db.QueryRowContext(ctx, boardSelectSQL+` where uuid=?`, strings.TrimSpace(uuid)), &b)
	return b, err
}

func (s *Store) BoardByName(ctx context.Context, name string) (Board, error) {
	var b Board
	err := scanBoard(s.db.QueryRowContext(ctx, boardSelectSQL+` where lower(name)=lower(?) order by id limit 1`, strings.TrimSpace(name)), &b)
	return b, err
}

func (s *Store) RenameBoard(ctx context.Context, boardID int64, name string) error {
	var err error
	name, err = normalizeBoardName(name)
	if err != nil {
		return err
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
	if current, err := s.countCurrentWorkspaces(ctx, boardID); err != nil {
		return err
	} else if current > 0 {
		return ErrBoardHasCurrentWorkspaces
	}
	res, err := s.db.ExecContext(ctx, `update boards set workdir=?, updated_at=? where id=?`, nullableString(workdir), time.Now().UTC(), boardID)
	return requireAffected(res, err)
}

func (s *Store) countCurrentWorkspaces(ctx context.Context, boardID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `select count(*) from ticket_workspaces where board_id=? and (is_current=1 or state=?)`, boardID, WorkspaceStateCleanupReq).Scan(&n)
	return n, err
}

// SetBoardWorktreeMode migrates a board's durable execution policy. The board
// row serializes this change against lifecycle claims in other processes.
func (s *Store) SetBoardWorktreeMode(ctx context.Context, boardID int64, mode string) error {
	var err error
	mode, err = normalizeWorktreeMode(mode, false)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=?`, boardID); err != nil {
		return err
	}
	var currentMode string
	if err := tx.QueryRowContext(ctx, `select coalesce(worktree_mode,'off') from boards where id=?`, boardID).Scan(&currentMode); err != nil {
		return err
	}
	if currentMode == mode {
		return tx.Commit()
	}
	if mode == WorktreeModeGit {
		if active, err := s.boardHasActiveSessions(ctx, tx, boardID); err != nil {
			return err
		} else if active {
			return ErrBoardHasActiveSessions
		}
	}
	if mode == WorktreeModeOff {
		var history int
		if err := tx.QueryRowContext(ctx, `select count(*) from ticket_workspaces where board_id=?`, boardID).Scan(&history); err != nil {
			return err
		}
		if history > 0 {
			return errors.New("cannot disable Git worktrees after workspace history exists; create a shared-directory board instead")
		}
	}
	res, err := tx.ExecContext(ctx, `update boards set worktree_mode=?, updated_at=? where id=?`, mode, time.Now().UTC(), boardID)
	if err := requireAffected(res, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkBoardSync(ctx context.Context, boardID int64, syncErr error) error {
	now := time.Now().UTC()
	var errText any
	if syncErr != nil {
		errText = RedactSecretText(syncErr.Error())
	}
	_, err := s.db.ExecContext(ctx, `update boards set last_sync_at=?, last_sync_error=?, updated_at=? where id=?`, now, errText, now, boardID)
	return err
}

func (s *Store) boardHasActiveIntegrationRuns(ctx context.Context, querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, boardID int64) (bool, error) {
	var active int
	if err := querier.QueryRowContext(ctx, `select count(*) from integration_runs where board_id=? and state not in (?,?,?)`, boardID, IntegrationStatePromoted, IntegrationStateFailed, IntegrationStateCancelled).Scan(&active); err != nil {
		return false, err
	}
	return active > 0, nil
}

func (s *Store) boardHasActiveSessions(ctx context.Context, querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, boardID int64) (bool, error) {
	var active int
	if err := querier.QueryRowContext(ctx, `select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.board_id=? and s.is_active=1`, boardID).Scan(&active); err != nil {
		return false, err
	}
	return active > 0, nil
}

func (s *Store) ArchiveBoard(ctx context.Context, boardID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize against concurrent claimSession / archive checks via row lock.
	var exist int
	if err := tx.QueryRowContext(ctx, `select count(*) from boards where id=?`, boardID).Scan(&exist); err != nil {
		return err
	}
	if exist == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=?`, boardID); err != nil {
		return err
	}
	active, err := s.boardHasActiveSessions(ctx, tx, boardID)
	if err != nil {
		return err
	}
	if active {
		return ErrBoardHasActiveSessions
	}
	integrationActive, err := s.boardHasActiveIntegrationRuns(ctx, tx, boardID)
	if err != nil {
		return err
	}
	if integrationActive {
		return ErrBoardHasActiveIntegrationRuns
	}
	var currentWorkspaces int
	if err := tx.QueryRowContext(ctx, `select count(*) from ticket_workspaces where board_id=? and is_current=1 and state<>?`, boardID, WorkspaceStateIntegrated).Scan(&currentWorkspaces); err != nil {
		return err
	}
	if currentWorkspaces > 0 {
		return ErrBoardHasCurrentWorkspaces
	}
	now := time.Now().UTC()
	var syncing int
	if err := tx.QueryRowContext(ctx, `select count(*) from board_sync_leases where board_id=? and expires_at>?`, boardID, now).Scan(&syncing); err != nil {
		return err
	}
	if syncing > 0 {
		return ErrBoardSyncInProgress
	}
	res, err := tx.ExecContext(ctx, `update boards set archived_at=?, sync_enabled=0, updated_at=? where id=?`, now, now, boardID)
	if err := requireAffected(res, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UnarchiveBoard(ctx context.Context, boardID int64) error {
	// Clears archive only; sync stays disabled until EnableBoardSync.
	res, err := s.db.ExecContext(ctx, `update boards set archived_at=null, updated_at=? where id=?`, time.Now().UTC(), boardID)
	return requireAffected(res, err)
}

func (s *Store) SetBoardSyncEnabled(ctx context.Context, boardID int64, enabled bool) error {
	board, err := s.BoardByID(ctx, boardID)
	if err != nil {
		return err
	}
	if enabled && board.ArchivedAt.Valid {
		return errors.New("cannot enable sync on an archived board; unarchive first")
	}
	flag := 0
	if enabled {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx, `update boards set sync_enabled=?, updated_at=? where id=?`, flag, time.Now().UTC(), boardID)
	return requireAffected(res, err)
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
	active, err := s.boardHasActiveSessions(ctx, tx, boardID)
	if err != nil {
		return err
	}
	if active {
		return errors.New("cannot delete board with active sessions")
	}
	integrationActive, err := s.boardHasActiveIntegrationRuns(ctx, tx, boardID)
	if err != nil {
		return err
	}
	if integrationActive {
		return ErrBoardHasActiveIntegrationRuns
	}
	var currentWorkspaces int
	if err := tx.QueryRowContext(ctx, `select count(*) from ticket_workspaces where board_id=? and (is_current=1 or state=?)`, boardID, WorkspaceStateCleanupReq).Scan(&currentWorkspaces); err != nil {
		return err
	}
	if currentWorkspaces > 0 {
		return ErrBoardHasCurrentWorkspaces
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

// ListBoards returns active (non-archived) boards by default.
func (s *Store) ListBoards(ctx context.Context) ([]Board, error) {
	return s.ListBoardsFiltered(ctx, false)
}

// ListBoardsFiltered lists boards; when includeArchived is false, archived boards are omitted.
func (s *Store) ListBoardsFiltered(ctx context.Context, includeArchived bool) ([]Board, error) {
	query := boardSelectSQL
	if !includeArchived {
		query += ` where archived_at is null`
	}
	query += ` order by lower(name), id`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []Board
	for rows.Next() {
		var b Board
		if err := scanBoard(rows, &b); err != nil {
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
	return s.CreateBoardWithOptions(ctx, CreateBoardOptions{Name: name, Workdir: workdir, WorktreeMode: WorktreeModeOff, TicketBackend: "local"})
}

func (s *Store) CreateBoardWithOptions(ctx context.Context, opts CreateBoardOptions) (Board, error) {
	name, err := normalizeBoardName(opts.Name)
	if err != nil {
		return Board{}, err
	}
	backend, err := normalizeTicketBackend(opts.TicketBackend, true)
	if err != nil {
		return Board{}, err
	}
	query := strings.TrimSpace(opts.BackendQuery)
	backendConfig := strings.TrimSpace(opts.BackendConfig)
	worktreeMode, err := normalizeWorktreeMode(opts.WorktreeMode, true)
	if err != nil {
		return Board{}, err
	}
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
	uuid, err := NewUUIDv4()
	if err != nil {
		return Board{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Board{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `insert into boards(name,uuid,workdir,next_ticket_number,ticket_backend,backend_query,backend_config,sync_enabled,worktree_mode,created_at,updated_at) values(?,?,?,1,?,?,?,1,?,?,?)`, name, uuid, nullableString(workdir), backend, nullableString(query), nullableString(backendConfig), worktreeMode, now, now)
	if err != nil {
		return Board{}, err
	}
	boardID, _ := res.LastInsertId()
	for _, col := range kanban.DefaultColumns() {
		if _, err := tx.ExecContext(ctx, `insert into columns(board_id,name,workflow_key,position,created_at,updated_at) values(?,?,?,?,?,?)`, boardID, col.Name, col.Name, col.Position, now, now); err != nil {
			return Board{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Board{}, err
	}
	return Board{ID: boardID, Name: name, UUID: uuid, Workdir: workdir, TicketBackend: backend, BackendQuery: query, BackendConfig: backendConfig, SyncEnabled: true, WorktreeMode: worktreeMode}, nil
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
	uuid, err := NewUUIDv4()
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `insert into boards(name,uuid,workdir,next_ticket_number,ticket_backend,sync_enabled,worktree_mode,created_at,updated_at) values('Default',?,?,1,'local',1,?,?,?)`, uuid, nullableString(cwd), WorktreeModeOff, now, now)
	if err != nil {
		return err
	}
	boardID, _ := res.LastInsertId()
	for _, col := range kanban.DefaultColumns() {
		if _, err := tx.ExecContext(ctx, `insert into columns(board_id,name,workflow_key,position,created_at,updated_at) values(?,?,?,?,?,?)`, boardID, col.Name, col.Name, col.Position, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
