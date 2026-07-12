package storage

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenUsesPrivateSQLitePermissionsWithPermissiveUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	path := filepath.Join(t.TempDir(), "private", "kanbi.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Dir(path), path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode=%o want=%o", p, info.Mode().Perm(), want)
		}
	}
}

func TestOpenDoesNotChangeExistingDatabaseParentPermissions(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(shared, "kanbi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := os.Stat(shared)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("existing parent mode changed to %o", info.Mode().Perm())
	}
}

func TestBoardSyncLeaseSerializesStoresAndRecoversStaleLease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanbi.db")
	a, _ := Open(path)
	defer a.Close()
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := Open(path)
	defer b.Close()
	board, _ := a.DefaultBoard(ctx)
	ok, err := a.AcquireBoardSyncLease(ctx, board.ID, "a", 20*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("first lease=%v err=%v", ok, err)
	}
	if ok, err := b.AcquireBoardSyncLease(ctx, board.ID, "b", time.Minute); err != nil || ok {
		t.Fatalf("concurrent lease=%v err=%v", ok, err)
	}
	time.Sleep(30 * time.Millisecond)
	if ok, err := b.AcquireBoardSyncLease(ctx, board.ID, "b", time.Minute); err != nil || !ok {
		t.Fatalf("stale recovery=%v err=%v", ok, err)
	}
	if err := b.ReleaseBoardSyncLease(ctx, board.ID, "b"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedRemoteNoteTombstoneSurvivesRestartAndBlocksReimport(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanbi.db")
	s, _ := Open(path)
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	ticket, _ := s.CreateTicket(ctx, view.Columns[0].ID, "ticket", "", "pi")
	now := time.Now().UTC()
	if err := s.UpsertRemoteNote(ctx, ticket.ID, "remote-1", "comment", now); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.ListNotes(ctx, ticket.ID)
	if err := s.DeleteNote(ctx, notes[0].ID); err != nil {
		t.Fatal(err)
	}
	if notes, _ := s.ListNotes(ctx, ticket.ID); len(notes) != 0 {
		t.Fatalf("deleted note visible: %+v", notes)
	}
	_ = s.Close()
	s, _ = Open(path)
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRemoteNote(ctx, ticket.ID, "remote-1", "remote again", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if notes, _ := s.ListNotes(ctx, ticket.ID); len(notes) != 0 {
		t.Fatalf("tombstone reimported: %+v", notes)
	}
	all, _ := s.ListNotesForSync(ctx, ticket.ID)
	if len(all) != 1 || !all[0].DeletedAt.Valid {
		t.Fatalf("missing tombstone: %+v", all)
	}
}
