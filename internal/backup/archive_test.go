package backup

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"kanbi/internal/storage"
)

func TestExportRestoreRoundTripDatabaseAndAttachments(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourceData := filepath.Join(root, "source-data")
	sourceDB := filepath.Join(root, "source.db")
	s, err := storage.Open(sourceDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ := s.BoardView(ctx)
	if _, err := s.CreateTicket(ctx, view.Columns[0].ID, "preserved", "body", "pi"); err != nil {
		t.Fatal(err)
	}
	attachment := filepath.Join(sourceData, "attachments", "1", "image.png")
	if err := os.MkdirAll(filepath.Dir(attachment), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachment, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "backup.kanbi")
	if err := Export(ctx, s, sourceData, archive); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	targetData := filepath.Join(root, "target-data")
	targetDB := filepath.Join(root, "target.db")
	if err := os.WriteFile(targetDB, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, archive, targetDB, targetData, false); err == nil {
		t.Fatal("restore must refuse overwrite without --force")
	}
	if err := Restore(ctx, archive, targetDB, targetData, true); err != nil {
		t.Fatal(err)
	}
	restored, err := storage.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Init(ctx); err != nil {
		t.Fatal(err)
	}
	tickets, _ := restored.ListTickets(ctx, false)
	found := false
	for _, ticket := range tickets {
		found = found || ticket.Title == "preserved"
	}
	if !found {
		t.Fatal("restored ticket missing")
	}
	data, err := os.ReadFile(filepath.Join(targetData, "attachments", "1", "image.png"))
	if err != nil || string(data) != "image" {
		t.Fatalf("attachment=%q err=%v", data, err)
	}
}

func TestExportRejectsDestinationInsideAttachments(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := storage.Open(filepath.Join(root, "kanbi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "data", "attachments", "backup.kanbi")
	if err := Export(ctx, s, filepath.Join(root, "data"), destination); err == nil {
		t.Fatal("destination inside attachments accepted")
	}
}

func TestRestoreRejectsUnsafeArchivePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.zip")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("manifest.json")
	_, _ = w.Write([]byte(`{"format":"kanbi-backup","version":1}`))
	w, _ = zw.Create("attachments/../../escape")
	_, _ = w.Write([]byte("bad"))
	_ = zw.Close()
	_ = f.Close()
	if err := Restore(context.Background(), path, filepath.Join(t.TempDir(), "db"), t.TempDir(), false); err == nil {
		t.Fatal("unsafe path accepted")
	}
}
