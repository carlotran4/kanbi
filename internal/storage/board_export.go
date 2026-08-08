package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BoardAggregate is the durable board slice used by board package export/import.
// Integer IDs are board-local source IDs and will be remapped on import.
type BoardAggregate struct {
	Board            Board
	NextTicketNumber int
	Columns          []AggregateColumn
	Templates        []AggregateTemplate
	Tickets          []AggregateTicket
	Notes            []AggregateNote
	Sessions         []AggregateSession
	PauseCheckpoints []AggregatePauseCheckpoint
}

type AggregateTemplate struct {
	SourceID  int64
	Name      string
	Title     string
	Body      string
	Harness   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AggregateColumn struct {
	SourceID    int64
	Name        string
	WorkflowKey string
	Position    int
}

type AggregateTicket struct {
	SourceID              int64
	ColumnSourceID        int64
	ExternalID            sql.NullString
	ExternalURL           sql.NullString
	ExternalUpdatedAt     sql.NullTime
	SyncVersion           sql.NullString
	DisplayID             string
	DisplayNumber         int
	Title                 string
	Body                  string
	Harness               string
	Position              int
	ArchivedAt            sql.NullTime
	FocusPaused           bool
	RemotePushState       sql.NullString
	RemotePushToken       sql.NullString
	RemotePushAttemptedAt sql.NullTime
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type AggregateNote struct {
	SourceID          int64
	TicketSourceID    int64
	ExternalID        sql.NullString
	ExternalUpdatedAt sql.NullTime
	SyncVersion       sql.NullString
	Body              string
	DeletedAt         sql.NullTime
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type AggregatePauseCheckpoint struct {
	SourceID       int64
	TicketSourceID int64
	Why            string
	Completed      string
	NextAction     string
	PausedAt       time.Time
	ResumedAt      sql.NullTime
}

type AggregateSession struct {
	SourceID            int64
	TicketSourceID      int64
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
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// BoardAggregateInsert is the create-new-only package import payload.
type BoardAggregateInsert struct {
	Name             string
	Workdir          string
	WorktreeMode     string
	TicketBackend    string
	BackendQuery     string
	BackendConfig    string
	NextTicketNumber int
	SourceExportUUID string
	Columns          []AggregateColumn
	Templates        []AggregateTemplate
	Tickets          []AggregateTicket
	Notes            []AggregateNote
	Sessions         []AggregateSession
	PauseCheckpoints []AggregatePauseCheckpoint
}

// BoardInsertResult carries remaps needed to place attachment bodies.
type BoardInsertResult struct {
	Board          Board
	TicketIDRemap  map[int64]int64
	ColumnIDRemap  map[int64]int64
	NoteIDRemap    map[int64]int64
	SessionIDRemap map[int64]int64
}

func (s *Store) LoadBoardAggregate(ctx context.Context, boardID int64) (BoardAggregate, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BoardAggregate{}, err
	}
	defer tx.Rollback()

	// Serialize against concurrent lifecycle claims on this board.
	if _, err := tx.ExecContext(ctx, `update boards set updated_at=updated_at where id=?`, boardID); err != nil {
		return BoardAggregate{}, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.board_id=? and s.is_active=1`, boardID).Scan(&active); err != nil {
		return BoardAggregate{}, err
	}
	if active > 0 {
		return BoardAggregate{}, ErrBoardHasActiveSessions
	}
	integrationActive, err := s.boardHasActiveIntegrationRuns(ctx, tx, boardID)
	if err != nil {
		return BoardAggregate{}, err
	}
	if integrationActive {
		return BoardAggregate{}, ErrBoardHasActiveIntegrationRuns
	}
	var currentWorkspaces int
	if err := tx.QueryRowContext(ctx, `select count(*) from ticket_workspaces where board_id=? and (is_current=1 or state=?)`, boardID, WorkspaceStateCleanupReq).Scan(&currentWorkspaces); err != nil {
		return BoardAggregate{}, err
	}
	if currentWorkspaces > 0 {
		return BoardAggregate{}, ErrBoardHasCurrentWorkspaces
	}

	var board Board
	var syncEnabled int
	if err := tx.QueryRowContext(ctx, boardSelectSQL+` where id=?`, boardID).Scan(
		&board.ID, &board.Name, &board.UUID, &board.Workdir, &board.TicketBackend, &board.BackendQuery, &board.BackendConfig,
		&board.LastSyncAt, &board.LastSyncError, &board.ArchivedAt, &syncEnabled, &board.SourceExportUUID, &board.WorktreeMode,
	); err != nil {
		return BoardAggregate{}, err
	}
	board.SyncEnabled = syncEnabled != 0
	var next int
	if err := tx.QueryRowContext(ctx, `select next_ticket_number from boards where id=?`, boardID).Scan(&next); err != nil {
		return BoardAggregate{}, err
	}
	agg := BoardAggregate{Board: board, NextTicketNumber: next}

	colRows, err := tx.QueryContext(ctx, `select id, name, coalesce(workflow_key,name), position from columns where board_id=? order by position, id`, boardID)
	if err != nil {
		return BoardAggregate{}, err
	}
	for colRows.Next() {
		var c AggregateColumn
		if err := colRows.Scan(&c.SourceID, &c.Name, &c.WorkflowKey, &c.Position); err != nil {
			colRows.Close()
			return BoardAggregate{}, err
		}
		agg.Columns = append(agg.Columns, c)
	}
	if err := colRows.Err(); err != nil {
		colRows.Close()
		return BoardAggregate{}, err
	}
	colRows.Close()

	templateRows, err := tx.QueryContext(ctx, `select id,name,title,body,harness,created_at,updated_at from ticket_templates where board_id=? order by name collate nocase,id`, boardID)
	if err != nil {
		return BoardAggregate{}, err
	}
	for templateRows.Next() {
		var tmpl AggregateTemplate
		if err := templateRows.Scan(&tmpl.SourceID, &tmpl.Name, &tmpl.Title, &tmpl.Body, &tmpl.Harness, &tmpl.CreatedAt, &tmpl.UpdatedAt); err != nil {
			templateRows.Close()
			return BoardAggregate{}, err
		}
		agg.Templates = append(agg.Templates, tmpl)
	}
	if err := templateRows.Err(); err != nil {
		templateRows.Close()
		return BoardAggregate{}, err
	}
	templateRows.Close()

	ticketRows, err := tx.QueryContext(ctx, `select id, column_id, external_id, external_url, external_updated_at, sync_version, display_id, display_number, title, body, harness, position, archived_at, coalesce(focus_paused,0), remote_push_state, remote_push_token, remote_push_attempted_at, created_at, updated_at from tickets where board_id=? order by id`, boardID)
	if err != nil {
		return BoardAggregate{}, err
	}
	var ticketIDs []int64
	for ticketRows.Next() {
		var t AggregateTicket
		var focusPaused int
		if err := ticketRows.Scan(&t.SourceID, &t.ColumnSourceID, &t.ExternalID, &t.ExternalURL, &t.ExternalUpdatedAt, &t.SyncVersion, &t.DisplayID, &t.DisplayNumber, &t.Title, &t.Body, &t.Harness, &t.Position, &t.ArchivedAt, &focusPaused, &t.RemotePushState, &t.RemotePushToken, &t.RemotePushAttemptedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			ticketRows.Close()
			return BoardAggregate{}, err
		}
		t.FocusPaused = focusPaused != 0
		agg.Tickets = append(agg.Tickets, t)
		ticketIDs = append(ticketIDs, t.SourceID)
	}
	if err := ticketRows.Err(); err != nil {
		ticketRows.Close()
		return BoardAggregate{}, err
	}
	ticketRows.Close()

	for _, ticketID := range ticketIDs {
		noteRows, err := tx.QueryContext(ctx, `select id, ticket_id, external_id, external_updated_at, sync_version, body, deleted_at, created_at, updated_at from ticket_notes where ticket_id=? order by id`, ticketID)
		if err != nil {
			return BoardAggregate{}, err
		}
		for noteRows.Next() {
			var n AggregateNote
			if err := noteRows.Scan(&n.SourceID, &n.TicketSourceID, &n.ExternalID, &n.ExternalUpdatedAt, &n.SyncVersion, &n.Body, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt); err != nil {
				noteRows.Close()
				return BoardAggregate{}, err
			}
			agg.Notes = append(agg.Notes, n)
		}
		err = noteRows.Err()
		noteRows.Close()
		if err != nil {
			return BoardAggregate{}, err
		}

		checkpointRows, err := tx.QueryContext(ctx, `select id,ticket_id,why,completed,next_action,paused_at,resumed_at from pause_checkpoints where ticket_id=? order by id`, ticketID)
		if err != nil {
			return BoardAggregate{}, err
		}
		for checkpointRows.Next() {
			var checkpoint AggregatePauseCheckpoint
			if err := checkpointRows.Scan(&checkpoint.SourceID, &checkpoint.TicketSourceID, &checkpoint.Why, &checkpoint.Completed, &checkpoint.NextAction, &checkpoint.PausedAt, &checkpoint.ResumedAt); err != nil {
				checkpointRows.Close()
				return BoardAggregate{}, err
			}
			agg.PauseCheckpoints = append(agg.PauseCheckpoints, checkpoint)
		}
		if err := checkpointRows.Err(); err != nil {
			checkpointRows.Close()
			return BoardAggregate{}, err
		}
		checkpointRows.Close()

		sessionRows, err := tx.QueryContext(ctx, `select id, ticket_id, harness, harness_session_ref, harness_session_name, tmux_session_name, tmux_window_id, tmux_window_name, coalesce(multiplexer,'tmux'), mux_namespace, mux_container_id, mux_container_name, mux_metadata, status, is_active, started_at, closed_at, last_seen_tmux_at, last_output_at, last_state_change_at, last_detected_state, last_attention_reason, last_detection_source, last_observed_excerpt, created_at, updated_at from sessions where ticket_id=? order by id`, ticketID)
		if err != nil {
			return BoardAggregate{}, err
		}
		for sessionRows.Next() {
			var ses AggregateSession
			var isActive int
			if err := sessionRows.Scan(&ses.SourceID, &ses.TicketSourceID, &ses.Harness, &ses.HarnessSessionRef, &ses.HarnessSessionName, &ses.TmuxSessionName, &ses.TmuxWindowID, &ses.TmuxWindowName, &ses.Multiplexer, &ses.MuxNamespace, &ses.MuxContainerID, &ses.MuxContainerName, &ses.MuxMetadata, &ses.Status, &isActive, &ses.StartedAt, &ses.ClosedAt, &ses.LastSeenTmuxAt, &ses.LastOutputAt, &ses.LastStateChangeAt, &ses.LastDetectedState, &ses.LastAttentionReason, &ses.LastDetectionSource, &ses.LastObservedExcerpt, &ses.CreatedAt, &ses.UpdatedAt); err != nil {
				sessionRows.Close()
				return BoardAggregate{}, err
			}
			ses.IsActive = isActive == 1
			agg.Sessions = append(agg.Sessions, ses)
		}
		err = sessionRows.Err()
		sessionRows.Close()
		if err != nil {
			return BoardAggregate{}, err
		}
	}
	// Final active-session barrier after reading the full aggregate.
	if err := tx.QueryRowContext(ctx, `select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.board_id=? and s.is_active=1`, boardID).Scan(&active); err != nil {
		return BoardAggregate{}, err
	}
	if active > 0 {
		return BoardAggregate{}, ErrBoardHasActiveSessions
	}
	if err := tx.Commit(); err != nil {
		return BoardAggregate{}, err
	}
	return agg, nil
}

// CountActiveSessionsOnBoard reports whether export/archive should be blocked.
func (s *Store) CountActiveSessionsOnBoard(ctx context.Context, boardID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.board_id=? and s.is_active=1`, boardID).Scan(&n)
	return n, err
}

// InsertBoardAggregate create-new-only imports a board package payload in one TX.
// All sessions are forced inactive so no live container claims go active.
// Imported boards are always archived with sync disabled.
func (s *Store) InsertBoardAggregate(ctx context.Context, in BoardAggregateInsert) (BoardInsertResult, error) {
	name, err := normalizeBoardName(in.Name)
	if err != nil {
		return BoardInsertResult{}, err
	}
	backend, err := normalizeTicketBackend(in.TicketBackend, true)
	if err != nil {
		return BoardInsertResult{}, err
	}
	worktreeMode, err := normalizeWorktreeMode(in.WorktreeMode, true)
	if err != nil {
		return BoardInsertResult{}, err
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from boards where lower(name)=lower(?)`, name).Scan(&existing); err != nil {
		return BoardInsertResult{}, err
	}
	if existing > 0 {
		return BoardInsertResult{}, errors.New("board name already exists")
	}
	maxSafeImportedTicketNumber := int(^uint(0)>>1) / 2
	nextTicketNumber := in.NextTicketNumber
	if nextTicketNumber <= 0 {
		nextTicketNumber = 1
	}
	if nextTicketNumber > maxSafeImportedTicketNumber {
		return BoardInsertResult{}, errors.New("next ticket number is too large")
	}
	normalizedTemplates := make([]TicketTemplate, len(in.Templates))
	for i, tmpl := range in.Templates {
		write, err := normalizeTemplateWrite(tmpl.Name, tmpl.Title, tmpl.Body, tmpl.Harness)
		if err != nil {
			return BoardInsertResult{}, fmt.Errorf("template %d: %w", tmpl.SourceID, err)
		}
		for j := 0; j < i; j++ {
			if strings.EqualFold(normalizedTemplates[j].Name, write.Name) {
				return BoardInsertResult{}, errors.New("template name already exists on this board")
			}
		}
		normalizedTemplates[i] = write
	}
	normalizedTickets := make([]ticketWrite, len(in.Tickets))
	for i, ticket := range in.Tickets {
		write, err := normalizeTicketWrite(ticket.Title, ticket.Body, ticket.Harness, true)
		if err != nil {
			return BoardInsertResult{}, fmt.Errorf("ticket %d: %w", ticket.SourceID, err)
		}
		normalizedTickets[i] = write
		if ticket.DisplayNumber <= 0 {
			return BoardInsertResult{}, fmt.Errorf("ticket %d has invalid display number %d", ticket.SourceID, ticket.DisplayNumber)
		}
		if ticket.DisplayNumber >= maxSafeImportedTicketNumber {
			return BoardInsertResult{}, fmt.Errorf("ticket %d display number is too large", ticket.SourceID)
		}
		if ticket.DisplayNumber >= nextTicketNumber {
			nextTicketNumber = ticket.DisplayNumber + 1
		}
	}
	for _, ses := range in.Sessions {
		if ses.IsActive {
			// Safe sanitize occurs during insert; package export should already reject actives.
			_ = ses
		}
	}

	uuid, err := NewUUIDv4()
	if err != nil {
		return BoardInsertResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BoardInsertResult{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `insert into boards(name,uuid,workdir,next_ticket_number,ticket_backend,backend_query,backend_config,worktree_mode,archived_at,sync_enabled,source_export_uuid,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,0,?,?,?)`,
		name, uuid, nullableString(strings.TrimSpace(in.Workdir)), nextTicketNumber, backend, nullableString(strings.TrimSpace(in.BackendQuery)), nullableString(strings.TrimSpace(in.BackendConfig)), worktreeMode, now, nullableString(strings.TrimSpace(in.SourceExportUUID)), now, now)
	if err != nil {
		return BoardInsertResult{}, err
	}
	boardID, _ := res.LastInsertId()

	columnRemap := map[int64]int64{}
	for _, col := range in.Columns {
		key := strings.TrimSpace(col.WorkflowKey)
		if key == "" {
			key = col.Name
		}
		cres, err := tx.ExecContext(ctx, `insert into columns(board_id,name,workflow_key,position,created_at,updated_at) values(?,?,?,?,?,?)`, boardID, col.Name, key, col.Position, now, now)
		if err != nil {
			return BoardInsertResult{}, err
		}
		id, _ := cres.LastInsertId()
		columnRemap[col.SourceID] = id
	}

	for i, tmpl := range in.Templates {
		createdAt := tmpl.CreatedAt
		updatedAt := tmpl.UpdatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}
		if _, err := tx.ExecContext(ctx, `insert into ticket_templates(board_id,name,title,body,harness,created_at,updated_at) values(?,?,?,?,?,?,?)`, boardID, normalizedTemplates[i].Name, normalizedTemplates[i].Title, normalizedTemplates[i].Body, normalizedTemplates[i].Harness, createdAt, updatedAt); err != nil {
			return BoardInsertResult{}, err
		}
	}

	ticketRemap := map[int64]int64{}
	for i, t := range in.Tickets {
		colID, ok := columnRemap[t.ColumnSourceID]
		if !ok {
			return BoardInsertResult{}, fmt.Errorf("ticket %d references missing column %d", t.SourceID, t.ColumnSourceID)
		}
		focusPaused := 0
		if t.FocusPaused {
			focusPaused = 1
		}
		tres, err := tx.ExecContext(ctx, `insert into tickets(board_id,column_id,external_id,external_url,external_updated_at,sync_version,display_id,display_number,title,body,harness,position,archived_at,focus_paused,remote_push_state,remote_push_token,remote_push_attempted_at,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			boardID, colID, nullStringValue(t.ExternalID), nullStringValue(t.ExternalURL), nullTimeValue(t.ExternalUpdatedAt), nullStringValue(t.SyncVersion), t.DisplayID, t.DisplayNumber, normalizedTickets[i].Title, normalizedTickets[i].Body, normalizedTickets[i].Harness, t.Position, nullTimeValue(t.ArchivedAt), focusPaused, nullStringValue(t.RemotePushState), nullStringValue(t.RemotePushToken), nullTimeValue(t.RemotePushAttemptedAt), t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return BoardInsertResult{}, err
		}
		id, _ := tres.LastInsertId()
		ticketRemap[t.SourceID] = id
	}

	noteRemap := map[int64]int64{}
	for _, n := range in.Notes {
		ticketID, ok := ticketRemap[n.TicketSourceID]
		if !ok {
			return BoardInsertResult{}, fmt.Errorf("note %d references missing ticket %d", n.SourceID, n.TicketSourceID)
		}
		nres, err := tx.ExecContext(ctx, `insert into ticket_notes(ticket_id,external_id,external_updated_at,sync_version,body,deleted_at,created_at,updated_at) values(?,?,?,?,?,?,?,?)`,
			ticketID, nullStringValue(n.ExternalID), nullTimeValue(n.ExternalUpdatedAt), nullStringValue(n.SyncVersion), n.Body, nullTimeValue(n.DeletedAt), n.CreatedAt, n.UpdatedAt)
		if err != nil {
			return BoardInsertResult{}, err
		}
		id, _ := nres.LastInsertId()
		noteRemap[n.SourceID] = id
	}

	for _, checkpoint := range in.PauseCheckpoints {
		ticketID, ok := ticketRemap[checkpoint.TicketSourceID]
		if !ok {
			return BoardInsertResult{}, fmt.Errorf("pause checkpoint %d references missing ticket %d", checkpoint.SourceID, checkpoint.TicketSourceID)
		}
		if _, err := tx.ExecContext(ctx, `insert into pause_checkpoints(ticket_id,why,completed,next_action,paused_at,resumed_at) values(?,?,?,?,?,?)`, ticketID, checkpoint.Why, checkpoint.Completed, checkpoint.NextAction, checkpoint.PausedAt, nullTimeValue(checkpoint.ResumedAt)); err != nil {
			return BoardInsertResult{}, err
		}
	}

	sessionRemap := map[int64]int64{}
	for _, ses := range in.Sessions {
		ticketID, ok := ticketRemap[ses.TicketSourceID]
		if !ok {
			return BoardInsertResult{}, fmt.Errorf("session %d references missing ticket %d", ses.SourceID, ses.TicketSourceID)
		}
		// Force inactive on import; preserve historical status/closed_at fields as-is.
		sres, err := tx.ExecContext(ctx, `insert into sessions(ticket_id,harness,harness_session_ref,harness_session_name,tmux_session_name,tmux_window_id,tmux_window_name,multiplexer,mux_namespace,mux_container_id,mux_container_name,mux_metadata,status,is_active,started_at,closed_at,last_seen_tmux_at,last_output_at,last_state_change_at,last_detected_state,last_attention_reason,last_detection_source,last_observed_excerpt,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?,?,?,?)`,
			ticketID, ses.Harness, nullStringValue(ses.HarnessSessionRef), nullStringValue(ses.HarnessSessionName), ses.TmuxSessionName, nullStringValue(ses.TmuxWindowID), ses.TmuxWindowName, emptyDefault(ses.Multiplexer, "tmux"), nullStringValue(ses.MuxNamespace), nullStringValue(ses.MuxContainerID), nullStringValue(ses.MuxContainerName), nullStringValue(ses.MuxMetadata), emptyDefault(ses.Status, "closed"), nullTimeValue(ses.StartedAt), nullTimeValue(ses.ClosedAt), nullTimeValue(ses.LastSeenTmuxAt), nullTimeValue(ses.LastOutputAt), nullTimeValue(ses.LastStateChangeAt), nullStringValue(ses.LastDetectedState), nullStringValue(ses.LastAttentionReason), nullStringValue(ses.LastDetectionSource), nullStringValue(ses.LastObservedExcerpt), ses.CreatedAt, ses.UpdatedAt)
		if err != nil {
			return BoardInsertResult{}, err
		}
		id, _ := sres.LastInsertId()
		sessionRemap[ses.SourceID] = id
	}

	if err := tx.Commit(); err != nil {
		return BoardInsertResult{}, err
	}
	board, err := s.BoardByID(ctx, boardID)
	if err != nil {
		return BoardInsertResult{}, err
	}
	return BoardInsertResult{
		Board:          board,
		TicketIDRemap:  ticketRemap,
		ColumnIDRemap:  columnRemap,
		NoteIDRemap:    noteRemap,
		SessionIDRemap: sessionRemap,
	}, nil
}

func nullStringValue(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func nullTimeValue(v sql.NullTime) any {
	if !v.Valid {
		return nil
	}
	return v.Time
}

func emptyDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
