package boardpackage

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

// Import applies a create-new-only board package with attachment staging + rollback.
// Final attachment placement always uses the supplied dataDir so custom roots are honored.
func Import(ctx context.Context, store *storage.Store, dataDir, archivePath string, opts ImportOptions) (Result, error) {
	if store == nil {
		return Result{}, errors.New("store is required")
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return Result{}, errors.New("dataDir is required")
	}
	report, err := Preview(ctx, store, archivePath)
	if err != nil {
		return Result{}, err
	}
	if !report.SchemaCompatible {
		return Result{}, fmt.Errorf("incompatible board package schema_version %d", report.Manifest.SchemaVersion)
	}
	if issue := firstAttachmentIssue(report); issue != "" {
		return Result{}, errors.New(issue)
	}

	manifest, doc, _, err := readPackage(archivePath)
	if err != nil {
		return Result{}, err
	}
	// Binding of ZIP members requires a second inventory pass after re-reading.
	attachmentNames := map[string]bool{}
	{
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return Result{}, err
		}
		for _, zf := range zr.File {
			name := filepath.ToSlash(zf.Name)
			if strings.HasPrefix(name, "attachments/") && !strings.HasSuffix(name, "/") {
				attachmentNames[name] = true
			}
		}
		_ = zr.Close()
	}
	inventory, err := validateAttachmentInventory(doc.Attachments, attachmentNames)
	if err != nil {
		return Result{}, err
	}
	if issue := formatInventoryIssue(inventory); issue != "" {
		return Result{}, errors.New(issue)
	}

	name := strings.TrimSpace(opts.NameOverride)
	if name == "" {
		name = doc.Board.Name
	}
	if report.NameCollision && strings.EqualFold(name, doc.Board.Name) {
		return Result{}, errors.New("board name already exists; pass --name to import under a new name")
	}
	if board, err := store.BoardByName(ctx, name); err == nil && board.ID != 0 {
		return Result{}, errors.New("board name already exists; pass --name to import under a new name")
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Result{}, err
	}
	stage, err := os.MkdirTemp(dataDir, ".kanbi-board-import-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()

	// index zip files for inventory keys
	zipByName := map[string]*zip.File{}
	for _, zf := range zr.File {
		zipByName[filepath.ToSlash(zf.Name)] = zf
	}

	stagedBySource := map[int64]string{}
	for zipPath, entry := range inventory.byZipPath {
		zf, ok := zipByName[zipPath]
		if !ok {
			return Result{}, fmt.Errorf("board package missing attachments: %s", zipPath)
		}
		target := filepath.Join(stage, strconv.FormatInt(entry.SourceTicketID, 10), entry.RelativePath)
		if err := extractRegular(zf, target); err != nil {
			return Result{}, err
		}
		sum, size, err := hashFile(target)
		if err != nil {
			return Result{}, err
		}
		if !strings.EqualFold(sum, entry.SHA256) {
			return Result{}, fmt.Errorf("attachment checksum mismatch for %s", zipPath)
		}
		if size != entry.Size {
			return Result{}, fmt.Errorf("attachment size mismatch for %s: got %d want %d", zipPath, size, entry.Size)
		}
		stagedBySource[entry.SourceTicketID] = filepath.Join(stage, strconv.FormatInt(entry.SourceTicketID, 10))
	}

	insert := storage.BoardAggregateInsert{
		Name:             name,
		Workdir:          doc.Board.Workdir,
		TicketBackend:    doc.Board.TicketBackend,
		BackendQuery:     doc.Board.BackendQuery,
		BackendConfig:    doc.Board.BackendConfig,
		NextTicketNumber: doc.Board.NextTicketNumber,
		SourceExportUUID: manifest.SourceBoardUUID,
	}
	for _, c := range doc.Columns {
		insert.Columns = append(insert.Columns, storage.AggregateColumn{
			SourceID: c.SourceID, Name: c.Name, WorkflowKey: c.WorkflowKey, Position: c.Position,
		})
	}
	for _, t := range doc.Tickets {
		insert.Tickets = append(insert.Tickets, storage.AggregateTicket{
			SourceID: t.SourceID, ColumnSourceID: t.ColumnSourceID,
			ExternalID: ptrToNullString(t.ExternalID), ExternalURL: ptrToNullString(t.ExternalURL),
			ExternalUpdatedAt: ptrToNullTime(t.ExternalUpdatedAt), SyncVersion: ptrToNullString(t.SyncVersion),
			DisplayID: t.DisplayID, DisplayNumber: t.DisplayNumber, Title: t.Title, Body: t.Body,
			Harness: t.Harness, Position: t.Position, ArchivedAt: ptrToNullTime(t.ArchivedAt),
			RemotePushState: ptrToNullString(t.RemotePushState), RemotePushToken: ptrToNullString(t.RemotePushToken),
			RemotePushAttemptedAt: ptrToNullTime(t.RemotePushAttemptedAt), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		})
	}
	for _, n := range doc.Notes {
		insert.Notes = append(insert.Notes, storage.AggregateNote{
			SourceID: n.SourceID, TicketSourceID: n.TicketSourceID,
			ExternalID: ptrToNullString(n.ExternalID), ExternalUpdatedAt: ptrToNullTime(n.ExternalUpdatedAt),
			SyncVersion: ptrToNullString(n.SyncVersion), Body: n.Body, DeletedAt: ptrToNullTime(n.DeletedAt),
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		})
	}
	for _, ses := range doc.Sessions {
		insert.Sessions = append(insert.Sessions, storage.AggregateSession{
			SourceID: ses.SourceID, TicketSourceID: ses.TicketSourceID, Harness: ses.Harness,
			HarnessSessionRef: ptrToNullString(ses.HarnessSessionRef), HarnessSessionName: ptrToNullString(ses.HarnessSessionName),
			TmuxSessionName: ses.TmuxSessionName, TmuxWindowID: ptrToNullString(ses.TmuxWindowID), TmuxWindowName: ses.TmuxWindowName,
			Multiplexer: ses.Multiplexer, MuxNamespace: ptrToNullString(ses.MuxNamespace), MuxContainerID: ptrToNullString(ses.MuxContainerID),
			MuxContainerName: ptrToNullString(ses.MuxContainerName), MuxMetadata: ptrToNullString(ses.MuxMetadata),
			Status: ses.Status, IsActive: ses.IsActive, StartedAt: ptrToNullTime(ses.StartedAt), ClosedAt: ptrToNullTime(ses.ClosedAt),
			LastSeenTmuxAt: ptrToNullTime(ses.LastSeenTmuxAt), LastOutputAt: ptrToNullTime(ses.LastOutputAt),
			LastStateChangeAt: ptrToNullTime(ses.LastStateChangeAt), LastDetectedState: ptrToNullString(ses.LastDetectedState),
			LastAttentionReason: ptrToNullString(ses.LastAttentionReason), LastDetectionSource: ptrToNullString(ses.LastDetectionSource),
			LastObservedExcerpt: ptrToNullString(ses.LastObservedExcerpt), CreatedAt: ses.CreatedAt, UpdatedAt: ses.UpdatedAt,
		})
	}

	ins, err := store.InsertBoardAggregate(ctx, insert)
	if err != nil {
		return Result{}, err
	}

	var written []int64
	fail := func(opErr error) (Result, error) {
		if rbErr := rollbackImport(ctx, store, dataDir, ins.Board.ID, written); rbErr != nil {
			return Result{}, fmt.Errorf("%w; rollback failed: %v", opErr, rbErr)
		}
		return Result{}, opErr
	}

	for sourceID, stagedDir := range stagedBySource {
		newID, ok := ins.TicketIDRemap[sourceID]
		if !ok {
			return fail(fmt.Errorf("attachment source ticket %d not remapped", sourceID))
		}
		destDir := ticketAttachmentDir(dataDir, newID)
		// Track before copy so partial destination trees are cleaned on failure.
		written = append(written, newID)
		if err := os.MkdirAll(filepath.Dir(destDir), 0o700); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(destDir); err == nil {
			return fail(fmt.Errorf("attachment directory already exists for ticket %d", newID))
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
		if err := copyDir(stagedDir, destDir); err != nil {
			return fail(err)
		}
	}

	result := Result{
		Board:           ins.Board,
		TicketIDRemap:   ins.TicketIDRemap,
		SourceBoardUUID: manifest.SourceBoardUUID,
		Warnings:        append([]string(nil), report.Warnings...),
	}
	if uuid := strings.TrimSpace(manifest.SourceBoardUUID); uuid != "" {
		boards, err := store.ListBoardsFiltered(ctx, true)
		if err == nil {
			count := 0
			for _, b := range boards {
				if b.SourceExportUUID.Valid && b.SourceExportUUID.String == uuid {
					count++
				}
			}
			if count > 1 {
				result.DuplicateProvenance = true
				result.Warnings = append(result.Warnings, "duplicate import of the same package source uuid")
			}
		}
	}
	return result, nil
}

func firstAttachmentIssue(report Report) string {
	if len(report.InvalidAttachments) > 0 {
		return "board package invalid attachments: " + strings.Join(report.InvalidAttachments, ", ")
	}
	if len(report.MissingAttachments) > 0 {
		return "board package missing attachments: " + strings.Join(report.MissingAttachments, ", ")
	}
	if len(report.UnlistedAttachments) > 0 {
		return "board package unlisted attachments: " + strings.Join(report.UnlistedAttachments, ", ")
	}
	return ""
}

func formatInventoryIssue(inv inventoryIssues) string {
	if len(inv.Invalid) > 0 {
		return "board package invalid attachments: " + strings.Join(inv.Invalid, ", ")
	}
	if len(inv.Missing) > 0 {
		return "board package missing attachments: " + strings.Join(inv.Missing, ", ")
	}
	if len(inv.Unlisted) > 0 {
		return "board package unlisted attachments: " + strings.Join(inv.Unlisted, ", ")
	}
	return ""
}

func ticketAttachmentDir(dataDir string, ticketID int64) string {
	return filepath.Join(dataDir, "attachments", strconv.FormatInt(ticketID, 10))
}

func rollbackImport(ctx context.Context, store *storage.Store, dataDir string, boardID int64, ticketIDs []int64) error {
	var errs []error
	if err := store.DeleteBoard(ctx, boardID); err != nil {
		errs = append(errs, err)
	}
	for _, id := range ticketIDs {
		if err := os.RemoveAll(ticketAttachmentDir(dataDir, id)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func copyDir(src, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if ent.IsDir() {
			return fmt.Errorf("nested attachment path rejected: %s", filepath.Join(src, ent.Name()))
		}
		from := filepath.Join(src, ent.Name())
		to := filepath.Join(dest, ent.Name())
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

func ptrToNullString(v *string) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}

func ptrToNullTime(v *time.Time) sql.NullTime {
	if v == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *v, Valid: true}
}
