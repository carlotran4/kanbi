package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestFleetReferenceAndJournalCommitTogetherWithoutChangingHistory(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "fleet", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := s.UpsertActiveSession(ctx, ticket.ID, Session{Harness: "pi", Status: "running", Multiplexer: "herdr", MuxNamespace: sql.NullString{String: "w1", Valid: true}, MuxContainerID: sql.NullString{String: "old", Valid: true}, HarnessSessionRef: sql.NullString{String: "native-ref", Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveFleetJournal(ctx, "before", true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `create trigger fleet_write_failure before update on fleet_journal begin select raise(abort,'injected write failure'); end`); err != nil {
		t.Fatal(err)
	}
	if err = s.PersistFleetReference(ctx, sid, "w2", "new", "metadata", "after"); err == nil {
		t.Fatal("expected journal write failure")
	}
	ses, _, _ := s.ActiveSession(ctx, ticket.ID)
	if ses.MuxContainerID.String != "old" {
		t.Fatal("reference committed without journal")
	}
	journal, _ := s.FleetJournal(ctx)
	if journal != "before" {
		t.Fatal("journal changed")
	}
	if _, err = s.db.ExecContext(ctx, `drop trigger fleet_write_failure`); err != nil {
		t.Fatal(err)
	}
	if err = s.PersistFleetReference(ctx, sid, "w2", "new", "metadata", "after"); err != nil {
		t.Fatal(err)
	}
	ses, _, _ = s.ActiveSession(ctx, ticket.ID)
	if ses.ID != sid || ses.HarnessSessionRef.String != "native-ref" || ses.Status != "running" || ses.MuxContainerID.String != "new" {
		t.Fatalf("attempt changed: %+v", ses)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `select count(*) from sessions where ticket_id=?`, ticket.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("history changed", count, err)
	}
	if err = s.SaveFleetJournal(ctx, "duplicate", true); err == nil {
		t.Fatal("second journal accepted")
	}
	if err = s.BackupTo(ctx, filepath.Join(t.TempDir(), "backup.db")); err == nil {
		t.Fatal("portable snapshot included machine-local Fleet journal")
	}
	if err = s.DeleteFleetJournal(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.BackupTo(ctx, filepath.Join(t.TempDir(), "backup.db")); err != nil {
		t.Fatal(err)
	}
}
func TestFleetMigrationFromV9IsIdempotent(t *testing.T) {
	s, ctx := newTestStore(t)
	if _, err := s.db.ExecContext(ctx, `drop table fleet_journal;delete from schema_migrations where version=10`); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFleetJournal(ctx, "recovery", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := s.FleetJournal(context.Background())
	if err != nil || j != "recovery" {
		t.Fatal(j, err)
	}
}
