package storage

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"

	"github.com/mattn/go-sqlite3"
)

const projectionCacheEntries = 8
const projectionCacheBytes = 64 << 20

type projectionRevision struct{ Data, Schema, Changes int64 }
type projectionCacheEntry struct {
	key   string
	view  BoardView
	bytes int64
}
type projectionCache struct {
	connection *sqlite3.SQLiteConn
	revision   projectionRevision
	pendingKey string
	entries    []projectionCacheEntry // most recently used first
	bytes      int64
}

func masterProjectionCacheKey(filter MasterFilter) string {
	// Length-prefix every raw string. JSON would replace invalid UTF-8 bytes,
	// allowing distinct SQL filters to collide. This key also owns its bytes.
	var key strings.Builder
	key.WriteString("master:")
	part := func(value string) {
		key.WriteString(strconv.Itoa(len(value)))
		key.WriteByte(':')
		key.WriteString(value)
	}
	part(strconv.Itoa(len(filter.BoardIDs)))
	for _, id := range filter.BoardIDs {
		part(strconv.FormatInt(id, 10))
	}
	for _, values := range [][]string{filter.Runtimes, filter.Harnesses} {
		part(strconv.Itoa(len(values)))
		for _, value := range values {
			part(value)
		}
	}
	part(filter.Search)
	if filter.IncludeArchived {
		key.WriteByte('1')
	} else {
		key.WriteByte('0')
	}
	return key.String()
}

func readProjectionRevision(ctx context.Context, conn *sql.Conn, ownWrites bool) (projectionRevision, error) {
	var revision projectionRevision
	var err error
	if ownWrites {
		err = conn.QueryRowContext(ctx, `select data_version,schema_version,total_changes() from pragma_data_version,pragma_schema_version`).Scan(&revision.Data, &revision.Schema, &revision.Changes)
	} else {
		// A query-only physical reader cannot make its own commits. All data
		// and schema changes arrive from other connections and advance this.
		err = conn.QueryRowContext(ctx, `pragma data_version`).Scan(&revision.Data)
	}
	return revision, err
}

func (s *Store) cachedBoardView(ctx context.Context, key string, load func(*sql.Conn) (BoardView, error)) (BoardView, error) {
	conn, err := s.reader().Conn(ctx)
	if err != nil {
		return BoardView{}, err
	}
	defer conn.Close()
	// Lease the sole reader before taking the cache mutex. Waiting for the
	// database connection honors ctx; a mutex wait would ignore cancellation.
	// Release the mutex before returning the reader to its one-connection pool.
	s.projectionMu.Lock()
	defer s.projectionMu.Unlock()
	var identity *sqlite3.SQLiteConn
	if err = conn.Raw(func(raw any) error { identity = raw.(*sqlite3.SQLiteConn); return nil }); err != nil {
		return BoardView{}, err
	}
	before, err := readProjectionRevision(ctx, conn, s.readDB == nil)
	if err != nil {
		return BoardView{}, err
	}
	cache := &s.projectionCache
	// data_version is connection-local. total_changes also detects own-connection
	// mutations in OpenMemory; schema_version covers its own DDL. Replacement of
	// a pooled physical connection always invalidates all retained snapshots.
	if cache.connection != identity || cache.revision != before {
		*cache = projectionCache{connection: identity, revision: before}
	}
	for i, entry := range cache.entries {
		if entry.key == key {
			copy(cache.entries[1:i+1], cache.entries[:i])
			cache.entries[0] = entry
			return cloneBoardView(entry.view), nil
		}
	}
	view, err := load(conn)
	if err != nil {
		return BoardView{}, err
	}
	after, err := readProjectionRevision(ctx, conn, s.readDB == nil)
	if err != nil {
		return BoardView{}, err
	}
	// A commit during the load must never label an earlier projection with a
	// newer revision. Return the ordinary read result, but retain nothing.
	if before != after {
		*cache = projectionCache{connection: identity, revision: after}
		return view, nil
	}
	// Admit only after the same key is requested twice without a database
	// revision change. Continuously changing/heartbeat-heavy boards therefore
	// keep the ordinary SQL path without allocating a retained snapshot.
	if len(key) > 16<<10 {
		return view, nil
	}
	if cache.pendingKey != key {
		cache.pendingKey = key
		return view, nil
	}
	size := projectionSize(view) + int64(len(key))
	if size > projectionCacheBytes {
		return view, nil
	}
	for len(cache.entries) > 0 && (len(cache.entries) >= projectionCacheEntries || cache.bytes+size > projectionCacheBytes) {
		last := len(cache.entries) - 1
		cache.bytes -= cache.entries[last].bytes
		cache.entries[last] = projectionCacheEntry{}
		cache.entries = cache.entries[:last]
	}
	// Cache owns its mutable slices/checkpoint pointers. The original fresh
	// result can be returned directly; cache hits receive another isolated copy.
	entry := projectionCacheEntry{key: key, view: cloneBoardView(view), bytes: size}
	cache.entries = append(cache.entries, projectionCacheEntry{})
	copy(cache.entries[1:], cache.entries[:len(cache.entries)-1])
	cache.entries[0] = entry
	cache.bytes += size
	return view, nil
}

func cloneBoardView(view BoardView) BoardView {
	if view.Columns == nil {
		return view
	}
	columns := make([]Column, len(view.Columns))
	copy(columns, view.Columns)
	view.Columns = columns
	for i := range view.Columns {
		tickets := view.Columns[i].Tickets
		if tickets == nil {
			continue
		}
		isolated := make([]Ticket, len(tickets))
		copy(isolated, tickets)
		tickets = isolated
		for j := range tickets {
			if tickets[j].LatestCheckpoint != nil {
				checkpoint := *tickets[j].LatestCheckpoint
				tickets[j].LatestCheckpoint = &checkpoint
			}
		}
		view.Columns[i].Tickets = tickets
	}
	return view
}

// Count the retained struct/slice and string data conservatively. Strings may
// share backing storage, but charging each occurrence avoids undercounting
// arbitrary bodies, excerpts, provider data, and workspace JSON.
func projectionSize(view BoardView) int64 {
	size := int64(reflect.TypeOf(view).Size()) + projectionStringBytes(reflect.ValueOf(view.Board))
	for _, column := range view.Columns {
		size += int64(reflect.TypeOf(column).Size()) + int64(len(column.Name)+len(column.WorkflowKey))
		for _, ticket := range column.Tickets {
			size += int64(reflect.TypeOf(ticket).Size()) + projectionStringBytes(reflect.ValueOf(ticket))
			if ticket.LatestCheckpoint != nil {
				size += int64(reflect.TypeOf(*ticket.LatestCheckpoint).Size()) + projectionStringBytes(reflect.ValueOf(*ticket.LatestCheckpoint))
			}
		}
	}
	return size
}

func projectionStringBytes(value reflect.Value) int64 {
	switch value.Kind() {
	case reflect.String:
		return int64(value.Len())
	case reflect.Struct:
		var size int64
		for i := 0; i < value.NumField(); i++ {
			size += projectionStringBytes(value.Field(i))
		}
		return size
	default:
		return 0 // Only checkpoint pointers are owned; time zones are shared.
	}
}
