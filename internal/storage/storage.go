package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"kanbi/internal/kanban"
)

type Store struct {
	db *sql.DB
}

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

func Open(path string) (*Store, error) {
	if err := ensureParent(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Store{db: db}, nil
}

func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	return s.ensureDefaultBoard(ctx)
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
	_, err := s.db.ExecContext(ctx, `update boards set name=?, updated_at=? where id=?`, name, time.Now().UTC(), boardID)
	return err
}

func (s *Store) SetBoardWorkdir(ctx context.Context, boardID int64, workdir string) error {
	workdir, err := normalizeWorkdir(workdir)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `update boards set workdir=?, updated_at=? where id=?`, nullableString(workdir), time.Now().UTC(), boardID)
	return err
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
}

func (s *Store) DeleteBoard(ctx context.Context, boardID int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `select count(*) from boards`).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return errors.New("cannot delete the last board")
	}
	var active int
	if err := s.db.QueryRowContext(ctx, `select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.board_id=? and s.is_active=1`, boardID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("cannot delete board with active sessions")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from sessions where ticket_id in (select id from tickets where board_id=?)`, boardID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from tickets where board_id=?`, boardID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from columns where board_id=?`, boardID); err != nil {
		return err
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
	backend := strings.TrimSpace(opts.TicketBackend)
	if backend == "" {
		backend = "local"
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
	if title == "" {
		return Ticket{}, errors.New("ticket title is required")
	}
	if harnessName == "" {
		harnessName = "pi"
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
	if strings.TrimSpace(name) == "" {
		return Column{}, errors.New("column name is required")
	}
	if boardID == 0 {
		board, err := s.DefaultBoard(ctx)
		if err != nil {
			return Column{}, err
		}
		boardID = board.ID
	}
	pos, err := columnOrder.nextPosition(ctx, s.db, boardID)
	if err != nil {
		return Column{}, err
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `insert into columns(board_id,name,position,created_at,updated_at) values(?,?,?,?,?)`, boardID, strings.TrimSpace(name), pos, now, now)
	if err != nil {
		return Column{}, err
	}
	id, _ := res.LastInsertId()
	return Column{ID: id, BoardID: boardID, Name: strings.TrimSpace(name), Position: pos}, nil
}

func (s *Store) RenameColumn(ctx context.Context, columnID int64, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("column name is required")
	}
	_, err := s.db.ExecContext(ctx, `update columns set name=?, updated_at=? where id=?`, strings.TrimSpace(name), time.Now().UTC(), columnID)
	return err
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
	_, err := s.db.ExecContext(ctx, `update tickets set title=?, body=?, harness=?, updated_at=? where id=?`, title, body, harnessName, time.Now().UTC(), id)
	return err
}

func (s *Store) ArchiveTicket(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var columnID int64
	if err := tx.QueryRowContext(ctx, `select column_id from tickets where id=?`, id).Scan(&columnID); err != nil {
		return err
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `update sessions set is_active=0, updated_at=? where ticket_id=?`, time.Now().UTC(), ticketID); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	status := session.Status
	if status == "" {
		status = kanban.StateRunning
	}
	res, err := tx.ExecContext(ctx, `insert into sessions(ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,status,is_active,started_at,last_seen_tmux_at,last_state_change_at,last_detected_state,last_detection_source,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ticketID, session.Harness, nullableString(session.HarnessSessionRef.String), nullableString(session.HarnessSessionName.String), session.TmuxSessionName, nullableString(session.TmuxWindowID.String), session.TmuxWindowName, status, 1, now, now, now, status, "system", now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

func (s *Store) ActiveSession(ctx context.Context, ticketID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, `select id,ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,status,is_active,started_at,closed_at,last_seen_tmux_at,last_output_at,last_state_change_at,last_detected_state,last_attention_reason,last_detection_source,last_observed_excerpt from sessions where ticket_id=? and is_active=1 order by id desc limit 1`, ticketID)
}

func (s *Store) LatestSession(ctx context.Context, ticketID int64) (Session, bool, error) {
	return s.sessionByQuery(ctx, `select id,ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,status,is_active,started_at,closed_at,last_seen_tmux_at,last_output_at,last_state_change_at,last_detected_state,last_attention_reason,last_detection_source,last_observed_excerpt from sessions where ticket_id=? order by id desc limit 1`, ticketID)
}

func (s *Store) sessionByQuery(ctx context.Context, query string, ticketID int64) (Session, bool, error) {
	var ses Session
	var active int
	err := s.db.QueryRowContext(ctx, query, ticketID).
		Scan(&ses.ID, &ses.TicketID, &ses.Harness, &ses.HarnessSessionRef, &ses.HarnessSessionName, &ses.TmuxSessionName, &ses.TmuxWindowID, &ses.TmuxWindowName, &ses.Status, &active, &ses.StartedAt, &ses.ClosedAt, &ses.LastSeenTmuxAt, &ses.LastOutputAt, &ses.LastStateChangeAt, &ses.LastDetectedState, &ses.LastAttentionReason, &ses.LastDetectionSource, &ses.LastObservedExcerpt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	ses.IsActive = active == 1
	return ses, true, nil
}

func (s *Store) RenameSessionWindow(ctx context.Context, ticketID int64, name string) error {
	_, err := s.db.ExecContext(ctx, `update sessions set tmux_window_name=?, updated_at=? where ticket_id=? and is_active=1`, name, time.Now().UTC(), ticketID)
	return err
}

func (s *Store) UpdateSessionRef(ctx context.Context, sessionID int64, ref string) error {
	_, err := s.db.ExecContext(ctx, `update sessions set harness_session_ref=?, updated_at=? where id=?`, nullableString(ref), time.Now().UTC(), sessionID)
	return err
}

// Note CRUD

func (s *Store) AddNote(ctx context.Context, ticketID int64, body string) (Note, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `insert into ticket_notes(ticket_id,body,created_at,updated_at) values(?,?,?,?)`, ticketID, body, now, now)
	if err != nil {
		return Note{}, err
	}
	id, _ := res.LastInsertId()
	return Note{ID: id, TicketID: ticketID, Body: body, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdateNote(ctx context.Context, noteID int64, body string) error {
	_, err := s.db.ExecContext(ctx, `update ticket_notes set body=?, updated_at=? where id=?`, body, time.Now().UTC(), noteID)
	return err
}

func (s *Store) DeleteNote(ctx context.Context, noteID int64) error {
	_, err := s.db.ExecContext(ctx, `delete from ticket_notes where id=?`, noteID)
	return err
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
	result := map[string]int64{}
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		var id int64
		err := s.db.QueryRowContext(ctx, `select id from columns where board_id=? and name=? order by position limit 1`, boardID, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			now := time.Now().UTC()
			insertPos, err := columnOrder.nextPosition(ctx, s.db, boardID)
			if err != nil {
				return nil, err
			}
			res, err := s.db.ExecContext(ctx, `insert into columns(board_id,name,position,created_at,updated_at) values(?,?,?,?,?)`, boardID, name, insertPos, now, now)
			if err != nil {
				return nil, err
			}
			id, _ = res.LastInsertId()
		} else if err != nil {
			return nil, err
		}
		result[name] = id
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
	existingErr := s.db.QueryRowContext(ctx, `select id from tickets where board_id=? and external_id=?`, rt.BoardID, rt.ExternalID).Scan(&id)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return Ticket{}, existingErr
	}
	displayNumber, err := s.remoteDisplayNumber(ctx, rt.BoardID, rt.DisplayNumber, id)
	if err != nil {
		return Ticket{}, err
	}
	if errors.Is(existingErr, sql.ErrNoRows) {
		pos, err := visibleTicketOrder.nextPosition(ctx, s.db, rt.ColumnID)
		if err != nil {
			return Ticket{}, err
		}
		res, err := s.db.ExecContext(ctx, `insert into tickets(board_id,column_id,external_id,external_url,external_updated_at,sync_version,display_id,display_number,title,body,harness,position,archived_at,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			rt.BoardID, rt.ColumnID, rt.ExternalID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, "pi", pos, archived, now, rt.ExternalUpdatedAt)
		if err != nil {
			return Ticket{}, err
		}
		id, _ = res.LastInsertId()
	} else {
		_, err = s.db.ExecContext(ctx, `update tickets set column_id=?, external_url=?, external_updated_at=?, sync_version=?, display_id=?, display_number=?, title=?, body=?, archived_at=?, updated_at=? where id=?`,
			rt.ColumnID, nullableString(rt.ExternalURL), rt.ExternalUpdatedAt, rt.ExternalUpdatedAt.Format(time.RFC3339Nano), displayID, displayNumber, rt.Title, rt.Body, archived, rt.ExternalUpdatedAt, id)
		if err != nil {
			return Ticket{}, err
		}
	}
	if _, err := s.db.ExecContext(ctx, `update boards set next_ticket_number=max(next_ticket_number, ?) where id=?`, displayNumber+1, rt.BoardID); err != nil {
		return Ticket{}, err
	}
	return s.TicketByID(ctx, id)
}

func (s *Store) remoteDisplayNumber(ctx context.Context, boardID int64, desired int, currentTicketID int64) (int, error) {
	if desired <= 0 {
		if err := s.db.QueryRowContext(ctx, `select next_ticket_number from boards where id=?`, boardID).Scan(&desired); err != nil {
			return 0, err
		}
	}
	var existing int64
	err := s.db.QueryRowContext(ctx, `select id from tickets where board_id=? and display_number=?`, boardID, desired).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) || existing == currentTicketID {
		return desired, nil
	}
	if err != nil {
		return 0, err
	}
	var next int
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(display_number),0)+1 from tickets where board_id=?`, boardID).Scan(&next); err != nil {
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

func (s *Store) migrate(ctx context.Context) error {
	boardColumns, err := tableColumns(ctx, s.db, "boards")
	if err != nil {
		return err
	}
	if !boardColumns["workdir"] {
		if _, err := s.db.ExecContext(ctx, `alter table boards add column workdir text`); err != nil {
			return err
		}
	}
	for _, col := range []struct {
		name string
		typ  string
	}{
		{"ticket_backend", "text not null default 'local'"},
		{"backend_query", "text"},
		{"backend_config", "text"},
		{"last_sync_at", "datetime"},
		{"last_sync_error", "text"},
	} {
		if !boardColumns[col.name] {
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`alter table boards add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		if _, err := s.db.ExecContext(ctx, `update boards set workdir=? where workdir is null or workdir=''`, cwd); err != nil {
			return err
		}
	}
	ticketColumns, err := tableColumns(ctx, s.db, "tickets")
	if err != nil {
		return err
	}
	for _, col := range []struct {
		name string
		typ  string
	}{
		{"external_id", "text"},
		{"external_url", "text"},
		{"external_updated_at", "datetime"},
		{"sync_version", "text"},
	} {
		if !ticketColumns[col.name] {
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`alter table tickets add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	columns, err := tableColumns(ctx, s.db, "sessions")
	if err != nil {
		return err
	}
	add := func(name, typ string) error {
		if columns[name] {
			return nil
		}
		_, err := s.db.ExecContext(ctx, fmt.Sprintf(`alter table sessions add column %s %s`, name, typ))
		return err
	}
	for _, col := range []struct {
		name string
		typ  string
	}{
		{"started_at", "datetime"},
		{"closed_at", "datetime"},
		{"last_output_at", "datetime"},
		{"last_state_change_at", "datetime"},
		{"last_detected_state", "text"},
		{"last_attention_reason", "text"},
		{"last_detection_source", "text"},
		{"last_observed_excerpt", "text"},
	} {
		if err := add(col.name, col.typ); err != nil {
			return err
		}
	}
	// Create ticket_notes table for existing databases that predate it.
	if _, err := s.db.ExecContext(ctx, `create table if not exists ticket_notes (
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
	noteColumns, err := tableColumns(ctx, s.db, "ticket_notes")
	if err != nil {
		return err
	}
	for _, col := range []struct {
		name string
		typ  string
	}{
		{"external_id", "text"},
		{"external_updated_at", "datetime"},
		{"sync_version", "text"},
	} {
		if !noteColumns[col.name] {
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`alter table ticket_notes add column %s %s`, col.name, col.typ)); err != nil {
				return err
			}
		}
	}
	return nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
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
