package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProjectionCacheTracksMutationsAndProtectsSnapshots(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(fmt.Sprintf("file=%t", file), func(t *testing.T) {
			ctx := context.Background()
			var s, writer *Store
			var err error
			if file {
				s, err = Open(filepath.Join(t.TempDir(), "cache.db"))
			} else {
				s, err = OpenMemory()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.Init(ctx); err != nil {
				t.Fatal(err)
			}
			writer = s
			if file {
				writer, err = Open(sPath(t, s))
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Close()
			}
			initial, err := s.BoardView(ctx)
			if err != nil {
				t.Fatal(err)
			}
			boardID, columnID := initial.Board.ID, initial.Columns[0].ID
			ticket, err := writer.CreateTicket(ctx, columnID, "cached ticket", "search full body", "pi")
			if err != nil {
				t.Fatal(err)
			}
			_, err = writer.db.ExecContext(ctx, `insert into pause_checkpoints(ticket_id,why,completed,next_action,paused_at) values(?,?,?,?,?)`, ticket.ID, "why", "done", "next", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			filters := []MasterFilter{{}, {BoardIDs: []int64{boardID}}, {Search: "search full body"}, {Harnesses: []string{"pi"}}, {Runtimes: []string{"closed"}}, {IncludeArchived: true}}
			check := func() {
				t.Helper()
				var board Board
				if err := scanBoard(s.reader().QueryRowContext(ctx, boardSelectSQL+` where id=?`, boardID), &board); err != nil {
					t.Fatal(err)
				}
				want, err := boardViewFor(ctx, s.reader(), board)
				if err != nil {
					t.Fatal(err)
				}
				for range 3 {
					got, err := s.BoardViewByID(ctx, boardID)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal("named projection differs from fresh SQL")
					}
					// A caller's presentation edits must not contaminate a cached snapshot.
					got.Columns[0].Name = "caller mutation"
					for i := range got.Columns[0].Tickets {
						got.Columns[0].Tickets[i].FocusMember = true
						if cp := got.Columns[0].Tickets[i].LatestCheckpoint; cp != nil {
							cp.Why = "caller mutation"
						}
					}
				}
				for _, filter := range filters {
					want, err := masterBoardViewFrom(ctx, s.reader(), filter)
					if err != nil {
						t.Fatal(err)
					}
					for range 3 {
						got, err := s.MasterBoardViewWithFilter(ctx, filter)
						if err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("Master filter %+v differs from fresh SQL", filter)
						}
					}
				}
			}
			check()
			if err = writer.UpdateTicket(ctx, ticket.ID, "external changed", "changed body", "codex"); err != nil {
				t.Fatal(err)
			}
			check()
			note, err := writer.AddNote(ctx, ticket.ID, "note")
			if err != nil {
				t.Fatal(err)
			}
			check()
			if err = writer.DeleteNote(ctx, note.ID); err != nil {
				t.Fatal(err)
			}
			check()
			wid, err := writer.CreateWorkspaceClaim(ctx, Workspace{TicketID: ticket.ID, BoardID: boardID, Kind: "git_worktree", State: "ready", BranchName: "cache-test", SourceBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			check()
			if err = writer.SaveWorkspaceStatusJSON(ctx, wid, `{"dirty":true}`); err != nil {
				t.Fatal(err)
			}
			check()
			sid, err := writer.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "codex", TmuxSessionName: "fixture", TmuxWindowName: "ticket", Status: "running"})
			if err != nil {
				t.Fatal(err)
			}
			check()
			if err = writer.UpdateSessionRuntime(ctx, sid, "needs_permission", "test", "approval", "new output", true); err != nil {
				t.Fatal(err)
			}
			check()
			if err = writer.UpdateSessionRuntime(ctx, sid, "needs_permission", "test", "approval", "new output", false); err != nil {
				t.Fatal(err)
			}
			check() // heartbeat only
			if err = writer.MarkSessionClosed(ctx, sid, "closed", "test", "done"); err != nil {
				t.Fatal(err)
			}
			check()
			if err = writer.MoveTicket(ctx, ticket.ID, initial.Columns[1].ID); err != nil {
				t.Fatal(err)
			}
			check()
			if err = writer.ArchiveTicket(ctx, ticket.ID); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}

func sPath(t *testing.T, s *Store) string {
	t.Helper()
	var n int
	var name, path string
	if err := s.db.QueryRow(`pragma database_list`).Scan(&n, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectionCacheDoesNotRetainConcurrentCommitOrReplacedConnection(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "before", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := Open(sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	_, err = s.cachedBoardView(ctx, "crossing-commit", func(conn *sql.Conn) (BoardView, error) {
		old, err := boardViewFor(ctx, conn, view.Board)
		if err != nil {
			return old, err
		}
		return old, writer.UpdateTicket(ctx, ticket.ID, "after", "body", "pi")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.projectionCache.entries) != 0 {
		t.Fatal("cached a load crossing a commit")
	}
	got, err := s.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Columns[0].Tickets[0].Title != "after" {
		t.Fatal("external commit was hidden")
	}
	calls := 0
	load := func(*sql.Conn) (BoardView, error) { calls++; return view, nil }
	for range 3 {
		if _, err = s.cachedBoardView(ctx, "connection-probe", load); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("unchanged projection did not hit cache: %d", calls)
	}
	conn, err := s.readDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
	if err != driver.ErrBadConn {
		t.Fatal(err)
	}
	if _, err = s.cachedBoardView(ctx, "connection-probe", load); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("replacement connection reused a connection-local revision")
	}
}

func TestProjectionCacheIsBoundedAndConcurrent(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "stable", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 40 {
				v, err := s.BoardView(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				v.Columns[0].Tickets[0].Title = "mutated"
				if _, err = s.MasterBoardView(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	group.Add(1)
	go func() {
		defer group.Done()
		for i := range 40 {
			if err := s.UpdateTicket(ctx, ticket.ID, fmt.Sprintf("revision %d", i), "body", "pi"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	group.Wait()
	got, err := s.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Columns[0].Tickets[0].Title != "revision 39" {
		t.Fatal("last committed write was hidden")
	}
	payload := strings.Repeat("x", 9<<20)
	large := BoardView{Columns: []Column{{Tickets: []Ticket{{Body: payload}}}}}
	for i := range 24 {
		_, err := s.cachedBoardView(ctx, fmt.Sprintf("large:%d", i/2), func(*sql.Conn) (BoardView, error) { return large, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.projectionCache.bytes > projectionCacheBytes || len(s.projectionCache.entries) > projectionCacheEntries {
		t.Fatal("cache exceeded its retention bounds")
	}
	oversized := large
	oversized.Columns = []Column{{Tickets: []Ticket{{Body: payload}, {Body: payload}, {Body: payload}, {Body: payload}, {Body: payload}, {Body: payload}, {Body: payload}, {Body: payload}}}}
	calls := 0
	for range 2 {
		_, err := s.cachedBoardView(ctx, "oversized", func(*sql.Conn) (BoardView, error) { calls++; return oversized, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("oversized result was retained")
	}
}

func TestProjectionFilterKeyPreservesAllRawFields(t *testing.T) {
	cases := []MasterFilter{
		{}, {BoardIDs: []int64{1}}, {BoardIDs: []int64{12}}, {BoardIDs: []int64{1, 2}},
		{Search: "1:abc"}, {Search: "abc"}, {Search: string([]byte{0xff})}, {Search: string([]byte{0xfe})},
		{Runtimes: []string{"running"}}, {Harnesses: []string{"running"}},
		{Runtimes: []string{"a:b", "c"}}, {Runtimes: []string{"a", "b:c"}},
		{IncludeArchived: true},
	}
	seen := make(map[string]bool)
	for _, filter := range cases {
		key := masterProjectionCacheKey(filter)
		if seen[key] {
			t.Fatalf("distinct filters collided: %+v", filter)
		}
		seen[key] = true
	}
	filter := MasterFilter{Harnesses: []string{"pi"}}
	old := masterProjectionCacheKey(filter)
	filter.Harnesses[0] = "codex"
	if old == masterProjectionCacheKey(filter) {
		t.Fatal("key aliases caller's filter slice")
	}
}

func TestProjectionCacheInvalidatesOwnMemoryDDL(t *testing.T) {
	s, ctx := newTestStore(t)
	calls := 0
	load := func(*sql.Conn) (BoardView, error) { calls++; return BoardView{}, nil }
	for range 3 {
		if _, err := s.cachedBoardView(ctx, "schema", load); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("stable schema did not hit cache: %d", calls)
	}
	if _, err := s.db.ExecContext(ctx, `create table cache_schema_probe(value text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.cachedBoardView(ctx, "schema", load); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("own-connection DDL left stale cache valid")
	}
}
