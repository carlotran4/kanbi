package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"kanbi/internal/harness"
	"kanbi/internal/kanban"
)

type Store struct {
	db *sql.DB
}

var ErrActiveSessionExists = errors.New("ticket already has an active session")

var ErrTicketHasActiveSession = errors.New("cannot archive ticket with an active session; close it first")

const boardSelectSQL = `select id, name, coalesce(workdir,''), coalesce(ticket_backend,'local'), coalesce(backend_query,''), coalesce(backend_config,''), last_sync_at, last_sync_error from boards`

func boardScanDest(b *Board) []any {
	return []any{&b.ID, &b.Name, &b.Workdir, &b.TicketBackend, &b.BackendQuery, &b.BackendConfig, &b.LastSyncAt, &b.LastSyncError}
}

type Board struct {
	ID            int64
	Name          string
	Workdir       string
	TicketBackend string
	BackendQuery  string
	BackendConfig string
	LastSyncAt    sql.NullTime
	LastSyncError sql.NullString
}

type Column struct {
	ID       int64
	BoardID  int64
	Name     string
	Position int
	Tickets  []Ticket
}

type Ticket struct {
	ID                  int64
	BoardID             int64
	BoardName           string
	BoardWorkdir        string
	ColumnID            int64
	ExternalID          sql.NullString
	ExternalURL         sql.NullString
	ExternalUpdatedAt   sql.NullTime
	SyncVersion         sql.NullString
	DisplayID           string
	DisplayNum          int
	Title               string
	Body                string
	Harness             string
	Position            int
	ArchivedAt          sql.NullTime
	Runtime             string
	SessionActive       bool
	TmuxSessionName     sql.NullString
	WindowID            sql.NullString
	WindowName          sql.NullString
	Multiplexer         sql.NullString
	MuxNamespace        sql.NullString
	MuxContainerID      sql.NullString
	MuxContainerName    sql.NullString
	MuxMetadata         sql.NullString
	SessionID           sql.NullInt64
	SessionRef          sql.NullString
	LastOutputAt        sql.NullTime
	LastStateChangeAt   sql.NullTime
	LastDetectedState   sql.NullString
	LastAttentionReason sql.NullString
	LastDetectionSource sql.NullString
	LastObservedExcerpt sql.NullString
	NoteCount           int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Note struct {
	ID                int64
	TicketID          int64
	ExternalID        sql.NullString
	ExternalUpdatedAt sql.NullTime
	SyncVersion       sql.NullString
	Body              string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Session struct {
	ID                  int64
	TicketID            int64
	Harness             string
	HarnessSessionRef   sql.NullString
	HarnessSessionName  sql.NullString
	TmuxSessionName     string
	TmuxWindowID        sql.NullString
	TmuxWindowName      string
	Multiplexer         string
	MuxNamespace        sql.NullString
	MuxContainerID      sql.NullString
	MuxContainerName    sql.NullString
	MuxMetadata         sql.NullString
	Status              string
	IsActive            bool
	StartedAt           sql.NullTime
	ClosedAt            sql.NullTime
	LastSeenTmuxAt      sql.NullTime
	LastOutputAt        sql.NullTime
	LastStateChangeAt   sql.NullTime
	LastDetectedState   sql.NullString
	LastAttentionReason sql.NullString
	LastDetectionSource sql.NullString
	LastObservedExcerpt sql.NullString
}

type BoardView struct {
	Board   Board
	Columns []Column
}

// MasterFilter describes query controls for the cross-board Master view.
// Empty slices mean "all" for that dimension. Filters are intentionally
// runtime-only UI state; callers decide whether to persist them.
type MasterFilter struct {
	BoardIDs        []int64
	Runtimes        []string
	Harnesses       []string
	Search          string
	IncludeArchived bool
}

func (f MasterFilter) Empty() bool {
	return len(f.BoardIDs) == 0 && len(f.Runtimes) == 0 && len(f.Harnesses) == 0 && strings.TrimSpace(f.Search) == "" && !f.IncludeArchived
}

const sqliteBusyTimeout = 5 * time.Second

func Open(path string) (*Store, error) {
	if err := ensureParent(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", sqliteDSN(path, false))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite3", sqliteDSN(":memory:", true))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func sqliteDSN(path string, memory bool) string {
	if memory {
		return fmt.Sprintf(":memory:?_foreign_keys=on&_busy_timeout=%d", sqliteBusyTimeout.Milliseconds())
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	u := url.URL{Scheme: "file", Path: absolute}
	query := u.Query()
	query.Set("_foreign_keys", "on")
	query.Set("_busy_timeout", strconv.FormatInt(sqliteBusyTimeout.Milliseconds(), 10))
	query.Set("_journal_mode", "WAL")
	query.Set("_synchronous", "NORMAL")
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) verifyForeignKeys(ctx context.Context) error {
	var enabled int
	if err := s.db.QueryRowContext(ctx, `pragma foreign_keys`).Scan(&enabled); err != nil {
		return fmt.Errorf("read SQLite foreign-key setting: %w", err)
	}
	if enabled != 1 {
		return errors.New("SQLite foreign-key enforcement is disabled")
	}
	rows, err := s.db.QueryContext(ctx, `pragma foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check SQLite foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return err
		}
		return fmt.Errorf("foreign-key violation in table %s row %v referencing %s (constraint %d)", table, rowID, parent, foreignKeyID)
	}
	return rows.Err()
}

func (s *Store) Init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	if err := s.ensureDefaultBoard(ctx); err != nil {
		return err
	}
	return s.verifyForeignKeys(ctx)
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

// RemoteTicket is the ticket metadata projection written by external ticket
// backends. It intentionally excludes local runtime/session fields.
type RemoteTicket struct {
	BoardID           int64
	ColumnID          int64
	ExternalID        string
	ExternalURL       string
	ExternalUpdatedAt time.Time
	DisplayID         string
	DisplayNumber     int
	Title             string
	Body              string
	ArchivedAt        *time.Time
	// SourceTicketID optionally links a just-created remote issue back to the
	// local placeholder ticket that produced it, instead of inserting a second
	// ticket with the remote display ID.
	SourceTicketID int64
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
	// The schema owns deletion order through foreign-key cascades. Keeping the
	// operation at the board aggregate avoids duplicating child-table knowledge.
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

type CreateBoardOptions struct {
	Name          string
	Workdir       string
	TicketBackend string
	BackendQuery  string
	BackendConfig string
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

func (s *Store) BoardView(ctx context.Context) (BoardView, error) {
	board, err := s.DefaultBoard(ctx)
	if err != nil {
		return BoardView{}, err
	}
	return s.BoardViewByID(ctx, board.ID)
}

func (s *Store) BoardViewByID(ctx context.Context, boardID int64) (BoardView, error) {
	var board Board
	if err := s.db.QueryRowContext(ctx, boardSelectSQL+` where id=?`, boardID).Scan(boardScanDest(&board)...); err != nil {
		return BoardView{}, err
	}
	return s.boardViewFor(ctx, board)
}

func (s *Store) MasterBoardView(ctx context.Context) (BoardView, error) {
	return s.MasterBoardViewWithFilter(ctx, MasterFilter{})
}

func (s *Store) MasterBoardViewWithFilter(ctx context.Context, filter MasterFilter) (BoardView, error) {
	rows, err := s.db.QueryContext(ctx, `select name from columns group by name order by min(position), lower(name)`)
	if err != nil {
		return BoardView{}, err
	}
	defer rows.Close()
	view := BoardView{Board: Board{ID: 0, Name: "Master"}}
	pos := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return BoardView{}, err
		}
		view.Columns = append(view.Columns, Column{ID: -int64(pos + 1), BoardID: 0, Name: name, Position: pos})
		pos++
	}
	if err := rows.Err(); err != nil {
		return BoardView{}, err
	}
	for i := range view.Columns {
		suffix, args := masterFilterQuery(view.Columns[i].Name, filter)
		tickets, err := s.queryProjectedTickets(ctx, suffix, args...)
		if err != nil {
			return BoardView{}, err
		}
		view.Columns[i].Tickets = tickets
	}
	return view, nil
}

func (s *Store) boardViewFor(ctx context.Context, board Board) (BoardView, error) {
	rows, err := s.db.QueryContext(ctx, `select id, board_id, name, position from columns where board_id=? order by position`, board.ID)
	if err != nil {
		return BoardView{}, err
	}
	defer rows.Close()
	view := BoardView{Board: board}
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.ID, &c.BoardID, &c.Name, &c.Position); err != nil {
			return BoardView{}, err
		}
		view.Columns = append(view.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return BoardView{}, err
	}
	if err := rows.Close(); err != nil {
		return BoardView{}, err
	}
	for i := range view.Columns {
		tickets, err := s.TicketsForColumn(ctx, view.Columns[i].ID)
		if err != nil {
			return BoardView{}, err
		}
		view.Columns[i].Tickets = tickets
	}
	return view, nil
}

func masterFilterQuery(columnName string, filter MasterFilter) (string, []any) {
	clauses := []string{`c.name=?`}
	args := []any{columnName}
	if !filter.IncludeArchived {
		clauses = append(clauses, `t.archived_at is null`)
	}
	if len(filter.BoardIDs) > 0 {
		clauses = append(clauses, `t.board_id in (`+placeholders(len(filter.BoardIDs))+`)`)
		for _, id := range filter.BoardIDs {
			args = append(args, id)
		}
	}
	if len(filter.Runtimes) > 0 {
		clauses = append(clauses, ticketProjectionRuntimeSQL+` in (`+placeholders(len(filter.Runtimes))+`)`)
		for _, runtime := range filter.Runtimes {
			args = append(args, runtime)
		}
	}
	if len(filter.Harnesses) > 0 {
		clauses = append(clauses, `lower(t.harness) in (`+placeholders(len(filter.Harnesses))+`)`)
		for _, harness := range filter.Harnesses {
			args = append(args, strings.ToLower(harness))
		}
	}
	if q := strings.ToLower(strings.TrimSpace(filter.Search)); q != "" {
		like := "%" + q + "%"
		clauses = append(clauses, `(lower(t.display_id) like ? or lower(t.title) like ? or lower(t.body) like ? or lower((select name from boards where id=t.board_id)) like ? or lower(t.harness) like ?)`)
		args = append(args, like, like, like, like, like)
	}
	return `join columns c on c.id=t.column_id where ` + strings.Join(clauses, ` and `) + ` order by t.board_id,t.position`, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func (s *Store) TicketsForColumn(ctx context.Context, columnID int64) ([]Ticket, error) {
	return s.queryProjectedTickets(ctx, `where t.column_id=? and t.archived_at is null order by t.position`, columnID)
}

func (s *Store) ListTickets(ctx context.Context, includeArchived bool) ([]Ticket, error) {
	where := `where t.archived_at is null`
	if includeArchived {
		where = ``
	}
	return s.queryProjectedTickets(ctx, where+` order by t.display_number`)
}

func (s *Store) TicketByDisplayID(ctx context.Context, displayID string) (Ticket, error) {
	tickets, err := s.queryProjectedTickets(ctx, `where t.display_id=?`, strings.ToUpper(displayID))
	if err != nil {
		return Ticket{}, err
	}
	if len(tickets) == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	if len(tickets) > 1 {
		return Ticket{}, errors.New("display id is ambiguous; specify a board")
	}
	return tickets[0], nil
}

func (s *Store) TicketByDisplayIDInBoard(ctx context.Context, displayID string, boardID int64) (Ticket, error) {
	tickets, err := s.queryProjectedTickets(ctx, `where t.display_id=? and t.board_id=?`, strings.ToUpper(displayID), boardID)
	if err != nil {
		return Ticket{}, err
	}
	if len(tickets) == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	return tickets[0], nil
}

func (s *Store) TicketByID(ctx context.Context, id int64) (Ticket, error) {
	tickets, err := s.queryProjectedTickets(ctx, `where t.id=?`, id)
	if err != nil {
		return Ticket{}, err
	}
	if len(tickets) == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	return tickets[0], nil
}

func (s *Store) CreateTicket(ctx context.Context, columnID int64, title, body, harnessName string) (Ticket, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Ticket{}, errors.New("ticket title is required")
	}
	harnessName = strings.ToLower(strings.TrimSpace(harnessName))
	if harnessName == "" {
		harnessName = "pi"
	}
	if !validHarness(harnessName) {
		return Ticket{}, fmt.Errorf("unsupported harness %q", harnessName)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()

	var boardID int64
	if columnID == 0 {
		if err := tx.QueryRowContext(ctx, `select id from columns order by board_id, position limit 1`).Scan(&columnID); err != nil {
			return Ticket{}, err
		}
	}
	if err := tx.QueryRowContext(ctx, `select board_id from columns where id=?`, columnID).Scan(&boardID); err != nil {
		return Ticket{}, err
	}
	var next int
	if err := tx.QueryRowContext(ctx, `select next_ticket_number from boards where id=?`, boardID).Scan(&next); err != nil {
		return Ticket{}, err
	}
	pos, err := visibleTicketOrder.nextPosition(ctx, tx, columnID)
	if err != nil {
		return Ticket{}, err
	}
	displayID := fmt.Sprintf("T-%03d", next)
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `insert into tickets(board_id,column_id,display_id,display_number,title,body,harness,position,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?)`,
		boardID, columnID, displayID, next, title, body, harnessName, pos, now, now)
	if err != nil {
		return Ticket{}, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `update boards set next_ticket_number=next_ticket_number+1 where id=?`, boardID); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}
	return s.TicketByID(ctx, id)
}

func (s *Store) AddColumn(ctx context.Context, boardID int64, name string) (Column, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Column{}, errors.New("column name is required")
	}
	if boardID == 0 {
		board, err := s.DefaultBoard(ctx)
		if err != nil {
			return Column{}, err
		}
		boardID = board.ID
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from columns where board_id=? and name=?`, boardID, name).Scan(&existing); err != nil {
		return Column{}, err
	}
	if existing > 0 {
		return Column{}, errors.New("column name already exists on this board")
	}
	pos, err := columnOrder.nextPosition(ctx, s.db, boardID)
	if err != nil {
		return Column{}, err
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `insert into columns(board_id,name,position,created_at,updated_at) values(?,?,?,?,?)`, boardID, name, pos, now, now)
	if err != nil {
		return Column{}, err
	}
	id, _ := res.LastInsertId()
	return Column{ID: id, BoardID: boardID, Name: name, Position: pos}, nil
}

func (s *Store) RenameColumn(ctx context.Context, columnID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("column name is required")
	}
	var boardID int64
	if err := s.db.QueryRowContext(ctx, `select board_id from columns where id=?`, columnID).Scan(&boardID); err != nil {
		return err
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from columns where board_id=? and name=? and id<>?`, boardID, name, columnID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return errors.New("column name already exists on this board")
	}
	res, err := s.db.ExecContext(ctx, `update columns set name=?, updated_at=? where id=?`, name, time.Now().UTC(), columnID)
	return requireAffected(res, err)
}

func (s *Store) DeleteColumn(ctx context.Context, columnID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var boardID int64
	var pos int
	if err := tx.QueryRowContext(ctx, `select board_id, position from columns where id=?`, columnID).Scan(&boardID, &pos); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `select count(*) from tickets where column_id=? and archived_at is null`, columnID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("Cannot delete non-empty column. Move tickets first.")
	}
	var fallbackColumnID int64
	if err := tx.QueryRowContext(ctx, `select id from columns where board_id=? and id<>? order by abs(position-?), position limit 1`, boardID, columnID, pos).Scan(&fallbackColumnID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("Cannot delete the last column.")
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `update tickets set column_id=?, updated_at=? where column_id=?`, fallbackColumnID, time.Now().UTC(), columnID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from columns where id=?`, columnID); err != nil {
		return err
	}
	if err := columnOrder.compact(ctx, tx, boardID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReorderColumn(ctx context.Context, columnID int64, delta int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := columnOrder.swapByDelta(ctx, tx, columnID, delta); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateTicket(ctx context.Context, id int64, title, body, harnessName string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("ticket title is required")
	}
	harnessName = strings.ToLower(strings.TrimSpace(harnessName))
	if !validHarness(harnessName) {
		return fmt.Errorf("unsupported harness %q", harnessName)
	}
	res, err := s.db.ExecContext(ctx, `update tickets set title=?, body=?, harness=?, updated_at=? where id=?`, title, body, harnessName, time.Now().UTC(), id)
	return requireAffected(res, err)
}

func (s *Store) ArchiveTicket(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var columnID int64
	var activeSessions int
	if err := tx.QueryRowContext(ctx, `select t.column_id, count(s.id) from tickets t left join sessions s on s.ticket_id=t.id and s.is_active=1 where t.id=? group by t.id`, id).Scan(&columnID, &activeSessions); err != nil {
		return err
	}
	if activeSessions > 0 {
		return ErrTicketHasActiveSession
	}
	if _, err := tx.ExecContext(ctx, `update tickets set archived_at=?, updated_at=? where id=?`, time.Now().UTC(), time.Now().UTC(), id); err != nil {
		return err
	}
	if err := visibleTicketOrder.compact(ctx, tx, columnID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ColumnIDByBoardAndName(ctx context.Context, boardID int64, name string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `select id from columns where board_id=? and name=? order by position limit 1`, boardID, name).Scan(&id)
	return id, err
}

func (s *Store) MoveTicket(ctx context.Context, ticketID, toColumnID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ticketBoardID, columnBoardID int64
	if err := tx.QueryRowContext(ctx, `select board_id from tickets where id=?`, ticketID).Scan(&ticketBoardID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `select board_id from columns where id=?`, toColumnID).Scan(&columnBoardID); err != nil {
		return err
	}
	if ticketBoardID != columnBoardID {
		return errors.New("cannot move a ticket to a column on another board")
	}
	if err := visibleTicketOrder.moveToFront(ctx, tx, ticketID, toColumnID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReorderTicket(ctx context.Context, ticketID int64, delta int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := visibleTicketOrder.swapByDelta(ctx, tx, ticketID, delta); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertActiveSession(ctx context.Context, ticketID int64, session Session) (int64, error) {
	return s.claimSession(ctx, ticketID, session, true)
}

// ClaimSession durably reserves the ticket before a runtime container is
// launched. A second caller cannot claim the same ticket while this attempt is
// active, even when it is using another Store/process.
func (s *Store) ClaimSession(ctx context.Context, ticketID int64, session Session, replaceActive bool) (int64, error) {
	session.Status = kanban.StateStarting
	return s.claimSession(ctx, ticketID, session, replaceActive)
}

func (s *Store) claimSession(ctx context.Context, ticketID int64, session Session, replaceActive bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if replaceActive {
		if _, err := tx.ExecContext(ctx, `update sessions set is_active=0, updated_at=? where ticket_id=? and is_active=1`, now, ticketID); err != nil {
			return 0, err
		}
	} else {
		var active int
		if err := tx.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=? and is_active=1`, ticketID).Scan(&active); err != nil {
			return 0, err
		}
		if active > 0 {
			return 0, ErrActiveSessionExists
		}
	}
	status := session.Status
	if status == "" {
		status = kanban.StateRunning
	}
	applySessionMuxDefaults(&session)
	res, err := tx.ExecContext(ctx, `insert into sessions(ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,multiplexer,mux_namespace,mux_container_id,mux_container_name,mux_metadata,status,is_active,started_at,last_seen_tmux_at,last_state_change_at,last_detected_state,last_detection_source,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ticketID, session.Harness, nullableString(session.HarnessSessionRef.String), nullableString(session.HarnessSessionName.String), session.TmuxSessionName, nullableString(session.TmuxWindowID.String), session.TmuxWindowName, session.Multiplexer, nullableString(session.MuxNamespace.String), nullableString(session.MuxContainerID.String), nullableString(session.MuxContainerName.String), nullableString(session.MuxMetadata.String), status, 1, now, now, now, status, "system", now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			return 0, ErrActiveSessionExists
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// CompleteSessionLaunch attaches the newly-created runtime container to the
// durable claim and transitions it from starting to running.
func (s *Store) CompleteSessionLaunch(ctx context.Context, sessionID int64, session Session) error {
	applySessionMuxDefaults(&session)
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `update sessions set harness_session_ref=?,harness_session_name=?,tmux_session_name=?,tmux_window_id=?,tmux_window_name=?,multiplexer=?,mux_namespace=?,mux_container_id=?,mux_container_name=?,mux_metadata=?,status=?,last_seen_tmux_at=?,last_state_change_at=?,last_detected_state=?,last_detection_source='system',updated_at=? where id=? and is_active=1 and status=?`,
		nullableString(session.HarnessSessionRef.String), nullableString(session.HarnessSessionName.String), session.TmuxSessionName, nullableString(session.TmuxWindowID.String), session.TmuxWindowName, session.Multiplexer, nullableString(session.MuxNamespace.String), nullableString(session.MuxContainerID.String), nullableString(session.MuxContainerName.String), nullableString(session.MuxMetadata.String), kanban.StateRunning, now, now, kanban.StateRunning, now, sessionID, kanban.StateStarting)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return errors.New("session launch claim is no longer active")
	}
	return nil
}

func (s *Store) FailSessionLaunch(ctx context.Context, sessionID int64, reason string) error {
	return s.MarkSessionClosed(ctx, sessionID, kanban.StateError, "system", reason)
}

func (s *Store) ActiveSession(ctx context.Context, ticketID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, sessionSelectSQL+` where ticket_id=? and is_active=1 order by id desc limit 1`, ticketID)
}

func (s *Store) LatestSession(ctx context.Context, ticketID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, sessionSelectSQL+` where ticket_id=? order by id desc limit 1`, ticketID)
}

func (s *Store) SessionByID(ctx context.Context, sessionID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, sessionSelectSQL+` where id=?`, sessionID)
}

const sessionSelectSQL = `select id,ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,coalesce(multiplexer,'tmux'),coalesce(mux_namespace,tmux_session_name),coalesce(mux_container_id,tmux_window_id),coalesce(mux_container_name,tmux_window_name),mux_metadata,status,is_active,started_at,closed_at,last_seen_tmux_at,last_output_at,last_state_change_at,last_detected_state,last_attention_reason,last_detection_source,last_observed_excerpt from sessions`

func (s *Store) sessionByQuery(ctx context.Context, query string, ticketID int64) (Session, bool, error) {
	var ses Session
	var active int
	err := s.db.QueryRowContext(ctx, query, ticketID).
		Scan(&ses.ID, &ses.TicketID, &ses.Harness, &ses.HarnessSessionRef, &ses.HarnessSessionName, &ses.TmuxSessionName, &ses.TmuxWindowID, &ses.TmuxWindowName, &ses.Multiplexer, &ses.MuxNamespace, &ses.MuxContainerID, &ses.MuxContainerName, &ses.MuxMetadata, &ses.Status, &active, &ses.StartedAt, &ses.ClosedAt, &ses.LastSeenTmuxAt, &ses.LastOutputAt, &ses.LastStateChangeAt, &ses.LastDetectedState, &ses.LastAttentionReason, &ses.LastDetectionSource, &ses.LastObservedExcerpt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	ses.IsActive = active == 1
	return ses, true, nil
}

func applySessionMuxDefaults(session *Session) {
	if session.Multiplexer == "" {
		session.Multiplexer = "tmux"
	}
	if !session.MuxNamespace.Valid && session.TmuxSessionName != "" {
		session.MuxNamespace = sql.NullString{String: session.TmuxSessionName, Valid: true}
	}
	if !session.MuxContainerID.Valid && session.TmuxWindowID.Valid {
		session.MuxContainerID = session.TmuxWindowID
	}
	if !session.MuxContainerName.Valid && session.TmuxWindowName != "" {
		session.MuxContainerName = sql.NullString{String: session.TmuxWindowName, Valid: true}
	}
}

func (s *Store) RenameSessionWindow(ctx context.Context, ticketID int64, name string) error {
	_, err := s.db.ExecContext(ctx, `update sessions set tmux_window_name=?, mux_container_name=?, updated_at=? where ticket_id=? and is_active=1`, name, name, time.Now().UTC(), ticketID)
	return err
}

func (s *Store) UpdateSessionRef(ctx context.Context, sessionID int64, ref string) error {
	res, err := s.db.ExecContext(ctx, `update sessions set harness_session_ref=?, updated_at=? where id=?`, nullableString(ref), time.Now().UTC(), sessionID)
	return requireAffected(res, err)
}

// Note CRUD

func (s *Store) AddNote(ctx context.Context, ticketID int64, body string) (Note, error) {
	if strings.TrimSpace(body) == "" {
		return Note{}, errors.New("note body is required")
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `insert into ticket_notes(ticket_id,body,created_at,updated_at) values(?,?,?,?)`, ticketID, body, now, now)
	if err != nil {
		return Note{}, err
	}
	id, _ := res.LastInsertId()
	return Note{ID: id, TicketID: ticketID, Body: body, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) NoteByID(ctx context.Context, noteID int64) (Note, error) {
	var n Note
	err := s.db.QueryRowContext(ctx, `select id, ticket_id, external_id, external_updated_at, sync_version, body, created_at, updated_at from ticket_notes where id=?`, noteID).Scan(&n.ID, &n.TicketID, &n.ExternalID, &n.ExternalUpdatedAt, &n.SyncVersion, &n.Body, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

func (s *Store) UpdateNote(ctx context.Context, noteID int64, body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("note body is required")
	}
	res, err := s.db.ExecContext(ctx, `update ticket_notes set body=?, updated_at=? where id=?`, body, time.Now().UTC(), noteID)
	return requireAffected(res, err)
}

func (s *Store) DeleteNote(ctx context.Context, noteID int64) error {
	res, err := s.db.ExecContext(ctx, `delete from ticket_notes where id=?`, noteID)
	return requireAffected(res, err)
}

func (s *Store) ListNotes(ctx context.Context, ticketID int64) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx, `select id, ticket_id, external_id, external_updated_at, sync_version, body, created_at, updated_at from ticket_notes where ticket_id=? order by id asc`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.TicketID, &n.ExternalID, &n.ExternalUpdatedAt, &n.SyncVersion, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// SyncBoardColumns mirrors an external backend's workflow columns for a board
// and returns the local column ID by name. Existing columns are reused by exact
// name; missing columns are created; absent columns are left in place to avoid
// destructive ticket/session history changes.
func (s *Store) SyncBoardColumns(ctx context.Context, boardID int64, names []string) (map[string]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	result := map[string]int64{}
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		var id int64
		err := tx.QueryRowContext(ctx, `select id from columns where board_id=? and name=? order by position limit 1`, boardID, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			now := time.Now().UTC()
			insertPos, err := columnOrder.nextPosition(ctx, tx, boardID)
			if err != nil {
				return nil, err
			}
			res, err := tx.ExecContext(ctx, `insert into columns(board_id,name,position,created_at,updated_at) values(?,?,?,?,?)`, boardID, name, insertPos, now, now)
			if err != nil {
				return nil, err
			}
			id, _ = res.LastInsertId()
		} else if err != nil {
			return nil, err
		}
		result[name] = id
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) SyncTicketsForBoard(ctx context.Context, boardID int64) ([]Ticket, error) {
	return s.queryProjectedTickets(ctx, `where t.board_id=? order by t.position`, boardID)
}

func (s *Store) UpsertRemoteTicket(ctx context.Context, rt RemoteTicket) (Ticket, error) {
	if rt.BoardID == 0 || rt.ColumnID == 0 || strings.TrimSpace(rt.ExternalID) == "" {
		return Ticket{}, errors.New("remote ticket requires board, column, and external id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()
	displayID := strings.TrimSpace(rt.DisplayID)
	if displayID == "" {
		displayID = rt.ExternalID
	}
	if rt.DisplayNumber == 0 {
		if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(displayID), "GH-")); err == nil {
			rt.DisplayNumber = n
		}
	}
	now := time.Now().UTC()
	var archived any
	if rt.ArchivedAt != nil {
		archived = *rt.ArchivedAt
	}
	var id int64
	existingErr := tx.QueryRowContext(ctx, `select id from tickets where board_id=? and external_id=?`, rt.BoardID, rt.ExternalID).Scan(&id)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return Ticket{}, existingErr
	}
	displayNumberCurrentID := id
	if errors.Is(existingErr, sql.ErrNoRows) && rt.SourceTicketID != 0 {
		displayNumberCurrentID = rt.SourceTicketID
	}
	displayNumber, err := remoteDisplayNumber(ctx, tx, rt.BoardID, rt.DisplayNumber, displayNumberCurrentID)
	if err != nil {
		return Ticket{}, err
	}
	if rt.ArchivedAt != nil {
		ticketID := id
		if errors.Is(existingErr, sql.ErrNoRows) {
			ticketID = rt.SourceTicketID
		}
		if ticketID != 0 {
			var active int
			if err := s.db.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=? and is_active=1`, ticketID).Scan(&active); err != nil {
				return Ticket{}, err
			}
			if active > 0 {
				return Ticket{}, ErrTicketHasActiveSession
			}
		}
	}
	if errors.Is(existingErr, sql.ErrNoRows) {
		if rt.SourceTicketID != 0 {
			var sourceBoardID int64
			if err := tx.QueryRowContext(ctx, `select board_id from tickets where id=?`, rt.SourceTicketID).Scan(&sourceBoardID); err != nil {
				return Ticket{}, err
			}
			if sourceBoardID != rt.BoardID {
				return Ticket{}, errors.New("remote ticket source belongs to a different board")
			}
			_, err = tx.ExecContext(ctx, `update tickets set column_id=?, external_id=?, external_url=?, external_updated_at=?, sync_version=?, display_id=?, display_number=?, title=?, body=?, archived_at=?, updated_at=? where id=?`,
				rt.ColumnID, rt.ExternalID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, archived, rt.ExternalUpdatedAt, rt.SourceTicketID)
			if err != nil {
				return Ticket{}, err
			}
			id = rt.SourceTicketID
		} else {
			pos, err := visibleTicketOrder.nextPosition(ctx, tx, rt.ColumnID)
			if err != nil {
				return Ticket{}, err
			}
			res, err := tx.ExecContext(ctx, `insert into tickets(board_id,column_id,external_id,external_url,external_updated_at,sync_version,display_id,display_number,title,body,harness,position,archived_at,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				rt.BoardID, rt.ColumnID, rt.ExternalID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, "pi", pos, archived, now, rt.ExternalUpdatedAt)
			if err != nil {
				return Ticket{}, err
			}
			id, _ = res.LastInsertId()
		}
	} else {
		_, err = tx.ExecContext(ctx, `update tickets set column_id=?, external_url=?, external_updated_at=?, sync_version=?, display_id=?, display_number=?, title=?, body=?, archived_at=?, updated_at=? where id=?`,
			rt.ColumnID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, archived, rt.ExternalUpdatedAt, id)
		if err != nil {
			return Ticket{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `update boards set next_ticket_number=max(next_ticket_number, ?) where id=?`, displayNumber+1, rt.BoardID); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}
	return s.TicketByID(ctx, id)
}

type sqlQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func remoteDisplayNumber(ctx context.Context, q sqlQueryer, boardID int64, desired int, currentTicketID int64) (int, error) {
	if desired <= 0 {
		if err := q.QueryRowContext(ctx, `select next_ticket_number from boards where id=?`, boardID).Scan(&desired); err != nil {
			return 0, err
		}
	}
	var existing int64
	err := q.QueryRowContext(ctx, `select id from tickets where board_id=? and display_number=?`, boardID, desired).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) || existing == currentTicketID {
		return desired, nil
	}
	if err != nil {
		return 0, err
	}
	var next int
	if err := q.QueryRowContext(ctx, `select coalesce(max(display_number),0)+1 from tickets where board_id=?`, boardID).Scan(&next); err != nil {
		return 0, err
	}
	if next <= desired {
		next = desired + 1
	}
	return next, nil
}

func (s *Store) UpsertRemoteNote(ctx context.Context, ticketID int64, externalID, body string, externalUpdatedAt time.Time) error {
	if strings.TrimSpace(externalID) == "" {
		return errors.New("remote note requires external id")
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `select id from ticket_notes where ticket_id=? and external_id=?`, ticketID, externalID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `insert into ticket_notes(ticket_id,external_id,external_updated_at,sync_version,body,created_at,updated_at) values(?,?,?,?,?,?,?)`, ticketID, externalID, externalUpdatedAt, externalUpdatedAt.Format(time.RFC3339Nano), body, time.Now().UTC(), externalUpdatedAt)
		return err
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `update ticket_notes set external_updated_at=?, sync_version=?, body=?, updated_at=? where id=?`, externalUpdatedAt, externalUpdatedAt.Format(time.RFC3339Nano), body, externalUpdatedAt, id)
	return err
}

func (s *Store) LinkLocalNoteToRemote(ctx context.Context, noteID int64, externalID string, externalUpdatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `update ticket_notes set external_id=?, external_updated_at=?, sync_version=?, updated_at=? where id=?`, externalID, externalUpdatedAt, externalUpdatedAt.Format(time.RFC3339Nano), externalUpdatedAt, noteID)
	return err
}

func (s *Store) MarkSessionMissing(ctx context.Context, sessionID int64) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `update sessions set status=?, is_active=0, closed_at=?, last_state_change_at=?, last_detected_state=?, last_attention_reason='tmux window missing', last_detection_source='tmux', updated_at=? where id=?`, kanban.StateError, now, now, kanban.StateError, now, sessionID)
	return err
}

func (s *Store) MarkSessionClosed(ctx context.Context, sessionID int64, status, source, reason string) error {
	if status == "" {
		status = kanban.StateClosed
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `update sessions set status=?, is_active=0, closed_at=?, last_state_change_at=?, last_detected_state=?, last_attention_reason=?, last_detection_source=?, updated_at=? where id=?`,
		status, now, now, status, nullableString(reason), nullableString(source), now, sessionID)
	return err
}

func (s *Store) MarkTicketRuntime(ctx context.Context, ticketID int64, status, source, reason string) error {
	ses, ok, err := s.ActiveSession(ctx, ticketID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("ticket has no active session")
	}
	return s.UpdateSessionRuntime(ctx, ses.ID, status, source, reason, "", false)
}

func (s *Store) UpdateSessionRuntime(ctx context.Context, sessionID int64, status, source, reason, excerpt string, outputChanged bool) error {
	if status == "" {
		return errors.New("runtime status is required")
	}
	now := time.Now().UTC()
	var currentStatus string
	err := s.db.QueryRowContext(ctx, `select status from sessions where id=?`, sessionID).Scan(&currentStatus)
	if err != nil {
		return err
	}
	stateChanged := currentStatus != status
	lastOutput := any(nil)
	if outputChanged {
		lastOutput = now
	}
	lastStateChange := any(nil)
	if stateChanged {
		lastStateChange = now
	}
	_, err = s.db.ExecContext(ctx, `update sessions set
status=?,
last_seen_tmux_at=?,
last_output_at=coalesce(?, last_output_at),
last_state_change_at=coalesce(?, last_state_change_at),
last_detected_state=?,
last_attention_reason=?,
last_detection_source=?,
last_observed_excerpt=coalesce(?, last_observed_excerpt),
updated_at=?
where id=?`,
		status, now, lastOutput, lastStateChange, status, nullableString(reason), nullableString(source), nullableString(excerpt), now, sessionID)
	return err
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

type migration struct {
	version int
	name    string
	apply   func(context.Context, *sql.Tx) error
}

var migrations = []migration{
	{version: 1, name: "legacy schema compatibility", apply: migrateLegacySchema},
	{version: 2, name: "projection and lifecycle indexes", apply: migrateIndexes},
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `create table if not exists schema_migrations (
  version integer primary key,
  name text not null,
  applied_at datetime not null
)`); err != nil {
		return fmt.Errorf("create schema migration ledger: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// This sentinel write serializes migration runners across Kanbi processes.
	if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version,name,applied_at) values(0,'migration lock',?) on conflict(version) do update set applied_at=schema_migrations.applied_at`, time.Now().UTC()); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `select coalesce(max(version),0) from schema_migrations`).Scan(&current); err != nil {
		return err
	}
	latest := migrations[len(migrations)-1].version
	if current > latest {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, latest)
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := m.apply(ctx, tx); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version,name,applied_at) values(?,?,?)`, m.version, m.name, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func migrateLegacySchema(ctx context.Context, tx *sql.Tx) error {
	boardColumns, err := tableColumns(ctx, tx, "boards")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{
		{"workdir", "text"}, {"ticket_backend", "text not null default 'local'"},
		{"backend_query", "text"}, {"backend_config", "text"},
		{"last_sync_at", "datetime"}, {"last_sync_error", "text"},
	} {
		if !boardColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table boards add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		if _, err := tx.ExecContext(ctx, `update boards set workdir=? where workdir is null or workdir=''`, cwd); err != nil {
			return err
		}
	}
	ticketColumns, err := tableColumns(ctx, tx, "tickets")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{{"external_id", "text"}, {"external_url", "text"}, {"external_updated_at", "datetime"}, {"sync_version", "text"}} {
		if !ticketColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table tickets add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	sessionColumns, err := tableColumns(ctx, tx, "sessions")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{
		{"started_at", "datetime"}, {"closed_at", "datetime"}, {"last_output_at", "datetime"},
		{"last_state_change_at", "datetime"}, {"last_detected_state", "text"},
		{"last_attention_reason", "text"}, {"last_detection_source", "text"},
		{"last_observed_excerpt", "text"}, {"multiplexer", "text not null default 'tmux'"},
		{"mux_namespace", "text"}, {"mux_container_id", "text"},
		{"mux_container_name", "text"}, {"mux_metadata", "text"},
	} {
		if !sessionColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table sessions add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	for _, statement := range []string{
		`update sessions set multiplexer='tmux' where multiplexer is null or multiplexer=''`,
		`update sessions set mux_namespace=tmux_session_name where mux_namespace is null and tmux_session_name is not null`,
		`update sessions set mux_container_id=tmux_window_id where mux_container_id is null and tmux_window_id is not null`,
		`update sessions set mux_container_name=tmux_window_name where mux_container_name is null and tmux_window_name is not null`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// Repair legacy duplicate active rows before enforcing the durable invariant.
	if _, err := tx.ExecContext(ctx, `update sessions set is_active=0 where is_active=1 and id not in (select max(id) from sessions where is_active=1 group by ticket_id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create unique index if not exists sessions_one_active_per_ticket on sessions(ticket_id) where is_active=1`); err != nil {
		return err
	}
	// Create ticket_notes table for existing databases that predate it.
	if _, err := tx.ExecContext(ctx, `create table if not exists ticket_notes (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  external_id text,
  external_updated_at datetime,
  sync_version text,
  body text not null default '',
  created_at datetime not null,
  updated_at datetime not null
)`); err != nil {
		return err
	}
	noteColumns, err := tableColumns(ctx, tx, "ticket_notes")
	if err != nil {
		return err
	}
	for _, col := range []struct{ name, typ string }{{"external_id", "text"}, {"external_updated_at", "datetime"}, {"sync_version", "text"}} {
		if !noteColumns[col.name] {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`alter table ticket_notes add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	// These indexes make durable identity rules authoritative across multiple
	// Kanbi processes. If an older database contains ambiguous rows, fail rather
	// than silently choosing or deleting one; the conflicting data must be
	// explicitly renamed before upgrading.
	for _, statement := range []string{
		`create unique index if not exists boards_name_nocase_uq on boards(name collate nocase)`,
		`create unique index if not exists columns_board_name_uq on columns(board_id,name)`,
		`create unique index if not exists tickets_board_external_id_uq on tickets(board_id,external_id) where external_id is not null`,
		`create unique index if not exists notes_ticket_external_id_uq on ticket_notes(ticket_id,external_id) where external_id is not null`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("enforce storage identity invariant: %w", err)
		}
	}
	return nil
}

func migrateIndexes(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`create index if not exists idx_columns_board_position on columns(board_id,position)`,
		`create index if not exists idx_tickets_column_archived_position on tickets(column_id,archived_at,position)`,
		`create index if not exists idx_tickets_board_external_id on tickets(board_id,external_id) where external_id is not null`,
		`create index if not exists idx_sessions_ticket_latest on sessions(ticket_id,id desc)`,
		`create index if not exists idx_sessions_ticket_active on sessions(ticket_id,is_active)`,
		`create index if not exists idx_ticket_notes_ticket on ticket_notes(ticket_id,id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

type contextQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func tableColumns(ctx context.Context, db contextQueryer, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func ensureParent(path string) error {
	if path == ":memory:" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

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

const schema = `
pragma foreign_keys = on;

create table if not exists boards (
  id integer primary key autoincrement,
  name text not null,
  workdir text,
  next_ticket_number integer not null default 1,
  ticket_backend text not null default 'local',
  backend_query text,
  backend_config text,
  last_sync_at datetime,
  last_sync_error text,
  created_at datetime not null,
  updated_at datetime not null
);

create table if not exists columns (
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  name text not null,
  position integer not null,
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, position)
);

create table if not exists tickets (
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  column_id integer not null references columns(id) on delete restrict,
  external_id text,
  external_url text,
  external_updated_at datetime,
  sync_version text,
  display_id text not null,
  display_number integer not null,
  title text not null,
  body text not null default '',
  harness text not null default 'pi',
  position integer not null,
  archived_at datetime,
  created_at datetime not null,
  updated_at datetime not null,
  unique(board_id, display_id),
  unique(board_id, display_number)
);

create table if not exists sessions (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  harness text not null,
  harness_session_ref text,
  harness_session_name text,
  tmux_session_name text not null,
  tmux_window_id text,
  tmux_window_name text not null,
  multiplexer text not null default 'tmux',
  mux_namespace text,
  mux_container_id text,
  mux_container_name text,
  mux_metadata text,
  status text not null,
  is_active integer not null default 1,
  started_at datetime,
  closed_at datetime,
  last_seen_tmux_at datetime,
  last_output_at datetime,
  last_state_change_at datetime,
  last_detected_state text,
  last_attention_reason text,
  last_detection_source text,
  last_observed_excerpt text,
  created_at datetime not null,
  updated_at datetime not null
);

create table if not exists ticket_notes (
  id integer primary key autoincrement,
  ticket_id integer not null references tickets(id) on delete cascade,
  external_id text,
  external_updated_at datetime,
  sync_version text,
  body text not null default '',
  created_at datetime not null,
  updated_at datetime not null
);
`
