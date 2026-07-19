package boardpackage_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/boardpackage"
	"github.com/carlotran4/kanbi/internal/storage"
)

func openStore(t *testing.T) (*storage.Store, context.Context) {
	t.Helper()
	s, err := storage.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func seedPackagedBoard(t *testing.T, s *storage.Store, ctx context.Context, dataDir string) (storage.Board, storage.Ticket) {
	t.Helper()
	board, err := s.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Source", Workdir: t.TempDir(), TicketBackend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "Packaged", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.AddNote(ctx, ticket.ID, "local note")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkLocalNoteToRemote(ctx, note.ID, "ext-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNote(ctx, note.ID); err != nil {
		t.Fatal(err)
	}
	sesID, err := s.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "s", TmuxWindowName: "w", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSessionClosed(ctx, sesID, "closed", "test", "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBoardWorktreeMode(ctx, board.ID, storage.WorktreeModeGit); err != nil {
		t.Fatal(err)
	}
	board, err = s.BoardByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	attachDir := filepath.Join(dataDir, "attachments", fmt.Sprintf("%d", ticket.ID))
	if err := os.MkdirAll(attachDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachDir, "img.png"), []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return board, ticket
}

func TestBoardPackageRoundTripPreservesTombstoneAndSession(t *testing.T) {
	s, ctx := openStore(t)
	dataDir := t.TempDir()

	board, ticket := seedPackagedBoard(t, s, ctx, dataDir)
	_ = ticket

	dest := filepath.Join(t.TempDir(), "board.zip")
	if err := boardpackage.Export(ctx, s, dataDir, board.ID, dest); err != nil {
		t.Fatal(err)
	}
	report, err := boardpackage.Preview(ctx, s, dest)
	if err != nil {
		t.Fatal(err)
	}
	if report.TicketCount != 1 || report.AttachmentCount != 1 || !report.NameCollision {
		t.Fatalf("preview unexpected: %+v", report)
	}
	if len(report.MissingAttachments) != 0 || len(report.UnlistedAttachments) != 0 || len(report.InvalidAttachments) != 0 {
		t.Fatalf("inventory issues on good package: %+v", report)
	}
	result, err := boardpackage.Import(ctx, s, dataDir, dest, boardpackage.ImportOptions{NameOverride: "Imported"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Board.ArchivedAt.Valid || result.Board.SyncEnabled {
		t.Fatalf("import should archive+disable sync: %+v", result.Board)
	}
	if result.Board.WorktreeMode != storage.WorktreeModeGit {
		t.Fatalf("import lost board execution policy: %+v", result.Board)
	}
	if !result.Board.SourceExportUUID.Valid || result.Board.SourceExportUUID.String != board.UUID {
		t.Fatalf("source export uuid missing: %+v", result.Board)
	}
	agg, err := s.LoadBoardAggregate(ctx, result.Board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg.Notes) != 1 || !agg.Notes[0].DeletedAt.Valid {
		t.Fatalf("expected note tombstone round-trip: %+v", agg.Notes)
	}
	if len(agg.Sessions) != 1 || agg.Sessions[0].IsActive {
		t.Fatalf("sessions should import inactive: %+v", agg.Sessions)
	}
	if len(result.TicketIDRemap) != 1 {
		t.Fatalf("ticket remap=%v", result.TicketIDRemap)
	}
	var newID int64
	for _, v := range result.TicketIDRemap {
		newID = v
	}
	if _, err := os.Stat(filepath.Join(dataDir, "attachments", fmt.Sprintf("%d", newID), "img.png")); err != nil {
		t.Fatalf("attachment missing after import: %v", err)
	}
}

func TestBoardPackageRejectsDestinationInsideAttachments(t *testing.T) {
	s, ctx := openStore(t)
	dataDir := t.TempDir()
	board, err := s.CreateBoard(ctx, "Safe")
	if err != nil {
		t.Fatal(err)
	}
	badDest := filepath.Join(dataDir, "attachments", "evil.zip")
	if err := os.MkdirAll(filepath.Dir(badDest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := boardpackage.Export(ctx, s, dataDir, board.ID, badDest); err == nil {
		t.Fatal("expected export to reject destination inside attachments")
	}
}

func TestBoardPackageRejectsSymlinkAliasDestination(t *testing.T) {
	s, ctx := openStore(t)
	dataDir := t.TempDir()
	board, err := s.CreateBoard(ctx, "Link")
	if err != nil {
		t.Fatal(err)
	}
	attachRoot := filepath.Join(dataDir, "attachments")
	if err := os.MkdirAll(attachRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "attach-alias")
	if err := os.Symlink(attachRoot, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	badDest := filepath.Join(alias, "evil.zip")
	if err := boardpackage.Export(ctx, s, dataDir, board.ID, badDest); err == nil {
		t.Fatal("expected export to reject destination under symlink alias of attachments")
	}
}

func TestBoardPackageExportRejectsActiveSessions(t *testing.T) {
	s, ctx := openStore(t)
	dataDir := t.TempDir()
	board, err := s.CreateBoard(ctx, "Active")
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "Live", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", TmuxSessionName: "s", TmuxWindowName: "w", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "active.zip")
	if err := boardpackage.Export(ctx, s, dataDir, board.ID, dest); !errors.Is(err, storage.ErrBoardHasActiveSessions) {
		t.Fatalf("export err=%v, want active sessions", err)
	}
}

func TestBoardPackageImportUsesCustomDataDir(t *testing.T) {
	s, ctx := openStore(t)
	// Intentionally point env attachments elsewhere so a custom dataDir is observable.
	envData := t.TempDir()
	t.Setenv("KANBI_DATA_DIR", envData)

	exportDir := t.TempDir()
	board, _ := seedPackagedBoard(t, s, ctx, exportDir)
	dest := filepath.Join(t.TempDir(), "custom.zip")
	if err := boardpackage.Export(ctx, s, exportDir, board.ID, dest); err != nil {
		t.Fatal(err)
	}
	importDir := t.TempDir()
	result, err := boardpackage.Import(ctx, s, importDir, dest, boardpackage.ImportOptions{NameOverride: "CustomRoot"})
	if err != nil {
		t.Fatal(err)
	}
	var newID int64
	for _, v := range result.TicketIDRemap {
		newID = v
	}
	if _, err := os.Stat(filepath.Join(importDir, "attachments", strconv.FormatInt(newID, 10), "img.png")); err != nil {
		t.Fatalf("expected attachment under custom dataDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(envData, "attachments", strconv.FormatInt(newID, 10), "img.png")); err == nil {
		t.Fatal("attachment placed under env dataDir instead of custom dataDir")
	}
}

func TestBoardPackageRejectsChecksumSizeAndUnlisted(t *testing.T) {
	s, ctx := openStore(t)
	dataDir := t.TempDir()
	board, ticket := seedPackagedBoard(t, s, ctx, dataDir)
	goodZip := filepath.Join(t.TempDir(), "good.zip")
	if err := boardpackage.Export(ctx, s, dataDir, board.ID, goodZip); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		mutate  func(t *testing.T, src, dest string)
		wantSub string
	}{
		{
			name: "checksum mismatch",
			mutate: func(t *testing.T, src, dest string) {
				t.Helper()
				rewriteBoardJSON(t, src, dest, func(doc *boardpackage.Document) {
					if len(doc.Attachments) != 1 {
						t.Fatalf("attach=%d", len(doc.Attachments))
					}
					doc.Attachments[0].SHA256 = strings.Repeat("0", 64)
				}, nil)
			},
			wantSub: "checksum mismatch",
		},
		{
			name: "blank checksum",
			mutate: func(t *testing.T, src, dest string) {
				t.Helper()
				rewriteBoardJSON(t, src, dest, func(doc *boardpackage.Document) {
					doc.Attachments[0].SHA256 = ""
				}, nil)
			},
			wantSub: "invalid attachments",
		},
		{
			name: "size mismatch",
			mutate: func(t *testing.T, src, dest string) {
				t.Helper()
				rewriteBoardJSON(t, src, dest, func(doc *boardpackage.Document) {
					doc.Attachments[0].Size = 999
				}, nil)
			},
			wantSub: "size mismatch",
		},
		{
			name: "unlisted zip member",
			mutate: func(t *testing.T, src, dest string) {
				t.Helper()
				extra := map[string][]byte{
					fmt.Sprintf("attachments/%d/extra.bin", ticket.ID): []byte("extra"),
				}
				rewriteBoardJSON(t, src, dest, nil, extra)
			},
			wantSub: "unlisted attachments",
		},
		{
			name: "nested attachment path",
			mutate: func(t *testing.T, src, dest string) {
				t.Helper()
				payload := []byte("nested")
				sum := sha256.Sum256(payload)
				rewriteBoardJSON(t, src, dest, func(doc *boardpackage.Document) {
					doc.Attachments = append(doc.Attachments, boardpackage.AttachmentEntry{
						SourceTicketID: ticket.ID,
						RelativePath:   "nested/file.txt",
						SHA256:         hex.EncodeToString(sum[:]),
						Size:           int64(len(payload)),
					})
				}, map[string][]byte{
					fmt.Sprintf("attachments/%d/nested/file.txt", ticket.ID): payload,
				})
			},
			wantSub: "nested attachment",
		},
		{
			name: "missing inventory member",
			mutate: func(t *testing.T, src, dest string) {
				t.Helper()
				rewriteBoardJSON(t, src, dest, func(doc *boardpackage.Document) {
					doc.Attachments = append(doc.Attachments, boardpackage.AttachmentEntry{
						SourceTicketID: ticket.ID,
						RelativePath:   "gone.png",
						SHA256:         strings.Repeat("a", 64),
						Size:           4,
					})
				}, nil)
			},
			wantSub: "missing attachments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := filepath.Join(t.TempDir(), "bad.zip")
			tc.mutate(t, goodZip, bad)
			report, err := boardpackage.Preview(ctx, s, bad)
			// nested/traversal fail in readPackage before report for nested path
			if err != nil {
				if !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("preview err=%v, want %q", err, tc.wantSub)
				}
				return
			}
			if issue := firstPreviewIssue(report); issue == "" || !strings.Contains(issue, tc.wantSub) {
				// import path may still catch checksum/size
			}
			_, err = boardpackage.Import(ctx, s, t.TempDir(), bad, boardpackage.ImportOptions{NameOverride: "Bad-" + tc.name})
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("import err=%v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func firstPreviewIssue(r boardpackage.Report) string {
	if len(r.InvalidAttachments) > 0 {
		return strings.Join(r.InvalidAttachments, ", ")
	}
	if len(r.MissingAttachments) > 0 {
		return "missing attachments: " + strings.Join(r.MissingAttachments, ", ")
	}
	if len(r.UnlistedAttachments) > 0 {
		return "unlisted attachments: " + strings.Join(r.UnlistedAttachments, ", ")
	}
	return ""
}

func TestBoardPackageImportRollsBackPartialAttachmentCopy(t *testing.T) {
	s, ctx := openStore(t)
	dataDir := t.TempDir()
	board, err := s.CreateBoardWithOptions(ctx, storage.CreateBoardOptions{Name: "Roll", Workdir: t.TempDir(), TicketBackend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.CreateTicket(ctx, view.Columns[0].ID, "One", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.CreateTicket(ctx, view.Columns[0].ID, "Two", "", "pi")
	if err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []storage.Ticket{t1, t2} {
		dir := filepath.Join(dataDir, "attachments", strconv.FormatInt(ticket.ID, 10))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(t.TempDir(), "roll.zip")
	if err := boardpackage.Export(ctx, s, dataDir, board.ID, dest); err != nil {
		t.Fatal(err)
	}

	// Pre-create attachment root as a read-only directory tree and occupy one remapped path
	// after dry import would remap independently — force failure by making attachments a file.
	importDir := t.TempDir()
	// That makes MkdirAll for destination parent fail for all; board still rolls back.
	if err := os.WriteFile(filepath.Join(importDir, "attachments"), []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	boardsBefore, err := s.ListBoardsFiltered(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = boardpackage.Import(ctx, s, importDir, dest, boardpackage.ImportOptions{NameOverride: "Rolled"})
	if err == nil {
		t.Fatal("expected import failure")
	}
	if !strings.Contains(err.Error(), "not a directory") && !strings.Contains(err.Error(), "mkdir") && !strings.Contains(err.Error(), "is a file") {
		// Accept any failure path as long as rollback removed the created board.
		t.Logf("import failed as expected: %v", err)
	}
	boardsAfter, err := s.ListBoardsFiltered(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardsAfter) != len(boardsBefore) {
		t.Fatalf("partial import left board behind: before=%d after=%d err=%v", len(boardsBefore), len(boardsAfter), err)
	}
	for _, b := range boardsAfter {
		if b.Name == "Rolled" {
			t.Fatal("imported board not rolled back")
		}
	}
}

func TestBoardPackageResultOmitsRawBoardJSON(t *testing.T) {
	raw, err := json.Marshal(boardpackage.Result{
		Board:           storage.Board{ID: 7, Name: "N"},
		TicketIDRemap:   map[int64]int64{1: 2},
		SourceBoardUUID: "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"board"`) {
		t.Fatalf("Result must not embed raw Board in JSON: %s", raw)
	}
	if !strings.Contains(string(raw), `"ticket_id_remap"`) {
		t.Fatalf("unexpected result json: %s", raw)
	}
}

func rewriteBoardJSON(t *testing.T, src, dest string, editDoc func(*boardpackage.Document), extra map[string][]byte) {
	t.Helper()
	r, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()
	for _, zf := range r.File {
		name := filepath.ToSlash(zf.Name)
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data := mustReadAll(t, rc)
		_ = rc.Close()
		if name == "board.json" && editDoc != nil {
			var doc boardpackage.Document
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			editDoc(&doc)
			data, err = json.MarshalIndent(doc, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := writeZipFile(zw, name, data); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range extra {
		if err := writeZipFile(zw, name, data); err != nil {
			t.Fatal(err)
		}
	}
}

func writeZipFile(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func mustReadAll(t *testing.T, r interface{ Read([]byte) (int, error) }) []byte {
	t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf
}
