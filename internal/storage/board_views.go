package storage

import (
	"context"
	"database/sql"
	"fmt"
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
	return s.cachedBoardView(ctx, fmt.Sprintf("board:%d", boardID), func(reader *sql.Conn) (BoardView, error) {
		var board Board
		if err := scanBoard(reader.QueryRowContext(ctx, boardSelectSQL+` where id=?`, boardID), &board); err != nil {
			return BoardView{}, err
		}
		return boardViewFor(ctx, reader, board)
	})
}

func (s *Store) MasterBoardView(ctx context.Context) (BoardView, error) {
	return s.MasterBoardViewWithFilter(ctx, MasterFilter{})
}

func (s *Store) MasterBoardViewWithFilter(ctx context.Context, filter MasterFilter) (BoardView, error) {
	return s.cachedBoardView(ctx, masterProjectionCacheKey(filter), func(reader *sql.Conn) (BoardView, error) { return masterBoardViewFrom(ctx, reader, filter) })
}

func masterBoardViewFrom(ctx context.Context, reader projectionReader, filter MasterFilter) (BoardView, error) {
	// Aggregate by workflow_key across non-archived boards. Representative display
	// name is the Name of the min-position column among boards contributing that key.
	rows, err := reader.QueryContext(ctx, `
select c.workflow_key,
       (select c2.name from columns c2
         join boards b2 on b2.id=c2.board_id
        where b2.archived_at is null
          and c2.workflow_key=c.workflow_key
        order by c2.position, lower(c2.name), c2.id
        limit 1) as display_name
  from columns c
  join boards b on b.id=c.board_id
 where b.archived_at is null
 group by c.workflow_key
 order by min(c.position), lower(c.workflow_key)`)
	if err != nil {
		return BoardView{}, err
	}
	defer rows.Close()
	view := BoardView{Board: Board{ID: 0, Name: "Master"}}
	pos := 0
	for rows.Next() {
		var key, displayName string
		if err := rows.Scan(&key, &displayName); err != nil {
			return BoardView{}, err
		}
		if displayName == "" {
			displayName = key
		}
		view.Columns = append(view.Columns, Column{
			ID:          -int64(pos + 1),
			BoardID:     0,
			Name:        displayName,
			WorkflowKey: key,
			Position:    pos,
		})
		pos++
	}
	if err := rows.Err(); err != nil {
		return BoardView{}, err
	}
	labelCounts := make(map[string]int, len(view.Columns))
	for _, col := range view.Columns {
		labelCounts[strings.ToLower(col.Name)]++
	}
	usedLabels := make(map[string]bool, len(view.Columns))
	for i := range view.Columns {
		candidate := view.Columns[i].Name
		if labelCounts[strings.ToLower(candidate)] > 1 {
			candidate = fmt.Sprintf("%s [%s]", candidate, view.Columns[i].WorkflowKey)
		}
		base := candidate
		for suffix := 2; usedLabels[strings.ToLower(candidate)]; suffix++ {
			candidate = fmt.Sprintf("%s #%d", base, suffix)
		}
		view.Columns[i].Name = candidate
		usedLabels[strings.ToLower(candidate)] = true
	}
	for i := range view.Columns {
		suffix, args := masterFilterQuery(view.Columns[i].WorkflowKey, filter)
		tickets, err := queryProjectedTicketsFrom(ctx, reader, suffix, args...)
		if err != nil {
			return BoardView{}, err
		}
		view.Columns[i].Tickets = tickets
	}
	return view, nil
}

func boardViewFor(ctx context.Context, reader projectionReader, board Board) (BoardView, error) {
	rows, err := reader.QueryContext(ctx, `select id, board_id, name, coalesce(workflow_key,name), position from columns where board_id=? order by position`, board.ID)
	if err != nil {
		return BoardView{}, err
	}
	defer rows.Close()
	view := BoardView{Board: board}
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.ID, &c.BoardID, &c.Name, &c.WorkflowKey, &c.Position); err != nil {
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
	tickets, err := queryProjectedTicketsFrom(ctx, reader, `where t.board_id=? and t.archived_at is null order by t.column_id,t.position`, board.ID)
	if err != nil {
		return BoardView{}, err
	}
	columns := make(map[int64]int, len(view.Columns))
	for i, column := range view.Columns {
		columns[column.ID] = i
	}
	for _, ticket := range tickets {
		if i, ok := columns[ticket.ColumnID]; ok {
			view.Columns[i].Tickets = append(view.Columns[i].Tickets, ticket)
		}
	}
	return view, nil
}

func masterFilterQuery(workflowKey string, filter MasterFilter) (string, []any) {
	clauses := []string{`c.workflow_key=?`, `b.archived_at is null`}
	args := []any{workflowKey}
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
	return `join columns c on c.id=t.column_id join boards b on b.id=t.board_id where ` + strings.Join(clauses, ` and `) + ` order by t.board_id,t.position`, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}
