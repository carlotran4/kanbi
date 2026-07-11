package storage

import (
	"context"

	"strings"
)

func (f MasterFilter) Empty() bool {
	return len(f.BoardIDs) == 0 && len(f.Runtimes) == 0 && len(f.Harnesses) == 0 && strings.TrimSpace(f.Search) == "" && !f.IncludeArchived
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
