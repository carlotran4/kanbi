package backup

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
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

func TestExportRejectsDestinationUnderSymlinkAliasOfAttachments(t *testing.T) {
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
	attachmentRoot := filepath.Join(root, "data", "attachments")
	if err := os.MkdirAll(attachmentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "attachments-alias")
	if err := os.Symlink(attachmentRoot, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := Export(ctx, s, filepath.Join(root, "data"), filepath.Join(alias, "backup.kanbi")); err == nil {
		t.Fatal("destination under symlink alias of attachments accepted")
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

func TestRestoreRejectsNonRegularAndOversizedMembersBeforeReplacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{"symlink database", os.ModeSymlink | 0o777, []byte("target")},
		{"directory database", os.ModeDir | 0o755, nil},
		{"oversized manifest", 0o600, []byte("ignored")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			archivePath := filepath.Join(root, "bad.zip")
			f, err := os.Create(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(f)
			if tc.name == "oversized manifest" {
				w, err := zw.Create("manifest.json")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte(strings.Repeat("x", 1<<20+1))); err != nil {
					t.Fatal(err)
				}
			} else {
				w, err := zw.Create("manifest.json")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte(`{"format":"kanbi-backup","version":1}`)); err != nil {
					t.Fatal(err)
				}
				h := &zip.FileHeader{Name: "database/kanbi.db", Method: zip.Deflate}
				h.SetMode(tc.mode)
				w, err = zw.CreateHeader(h)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write(tc.data); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}

			dbPath := filepath.Join(root, "target.db")
			if err := os.WriteFile(dbPath, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Restore(context.Background(), archivePath, dbPath, filepath.Join(root, "data"), true); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			data, err := os.ReadFile(dbPath)
			if err != nil || string(data) != "original" {
				t.Fatalf("database was replaced: %q, %v", data, err)
			}
		})
	}
}
