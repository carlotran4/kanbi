package boardpackage

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/archiveutil"
	"github.com/carlotran4/kanbi/internal/storage"
)

// Export writes a create-new portable board package for boardID to destPath.
// Active-session rejection runs again inside LoadBoardAggregate's consistent
// snapshot path so concurrent lifecycle transitions cannot sneak into the
// package after the initial check.
func Export(ctx context.Context, store *storage.Store, dataDir string, boardID int64, destPath string) (err error) {
	if store == nil {
		return errors.New("store is required")
	}
	if strings.TrimSpace(dataDir) == "" {
		return errors.New("dataDir is required")
	}
	attachmentRoot := filepath.Join(dataDir, "attachments")
	if inside, pathErr := archiveutil.PathWithin(attachmentRoot, destPath); pathErr != nil {
		return pathErr
	} else if inside {
		return errors.New("board package destination must not be inside the attachment directory")
	}
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("board package destination already exists: %s", destPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// LoadBoardAggregate re-checks active sessions under one consistent read path.
	agg, err := store.LoadBoardAggregate(ctx, boardID)
	if err != nil {
		return err
	}

	doc := documentFromAggregate(agg)
	// Collect attachment inventory and paths.
	type fileRef struct {
		zipName string
		src     string
		entry   AttachmentEntry
	}
	var files []fileRef
	for sourceTicketID := range ticketSourceIDs(agg) {
		ticketDir := filepath.Join(attachmentRoot, strconv.FormatInt(sourceTicketID, 10))
		entries, walkErr := os.ReadDir(ticketDir)
		if errors.Is(walkErr, os.ErrNotExist) {
			continue
		}
		if walkErr != nil {
			return walkErr
		}
		for _, ent := range entries {
			if ent.IsDir() {
				// Nested attachment trees are unsupported and would be silently dropped on import.
				return fmt.Errorf("nested attachment path rejected: %s", filepath.Join(ticketDir, ent.Name()))
			}
			src := filepath.Join(ticketDir, ent.Name())
			info, err := os.Lstat(src)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported attachment file: %s", src)
			}
			rel, err := flatAttachmentName(ent.Name())
			if err != nil {
				return err
			}
			sum, size, err := hashFile(src)
			if err != nil {
				return err
			}
			zipName := filepath.ToSlash(filepath.Join("attachments", strconv.FormatInt(sourceTicketID, 10), rel))
			entry := AttachmentEntry{SourceTicketID: sourceTicketID, RelativePath: rel, SHA256: sum, Size: size}
			doc.Attachments = append(doc.Attachments, entry)
			files = append(files, fileRef{zipName: zipName, src: src, entry: entry})
		}
	}

	manifest := Manifest{
		Format:          FormatName,
		Version:         FormatVersion,
		SchemaVersion:   storage.CurrentSchemaVersion(),
		CreatedAt:       time.Now().UTC(),
		SourceBoardUUID: agg.Board.UUID,
		SourceBoardName: agg.Board.Name,
		TicketCount:     len(doc.Tickets),
		NoteCount:       len(doc.Notes),
		SessionCount:    len(doc.Sessions),
		CheckpointCount: len(doc.Checkpoints),
		AttachmentCount: len(doc.Attachments),
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destPath)
		}
	}()
	zw := zip.NewWriter(f)
	defer func() {
		if closeErr := zw.Close(); err == nil {
			err = closeErr
		}
	}()

	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := writeBytes(zw, "manifest.json", manifestBytes, 0o600); err != nil {
		return err
	}
	boardBytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := writeBytes(zw, "board.json", boardBytes, 0o600); err != nil {
		return err
	}
	for _, file := range files {
		if err := writeFile(zw, file.zipName, file.src); err != nil {
			return err
		}
	}
	return nil
}

func ticketSourceIDs(agg storage.BoardAggregate) map[int64]struct{} {
	out := map[int64]struct{}{}
	for _, t := range agg.Tickets {
		out[t.SourceID] = struct{}{}
	}
	return out
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil {
		return "", 0, err
	}
	if n > maxAttachmentBytes {
		return "", 0, fmt.Errorf("attachment too large: %s", path)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func documentFromAggregate(agg storage.BoardAggregate) Document {
	doc := Document{
		Board: BoardPayload{
			SourceID:         agg.Board.ID,
			Name:             agg.Board.Name,
			UUID:             agg.Board.UUID,
			Workdir:          agg.Board.Workdir,
			WorktreeMode:     agg.Board.WorktreeMode,
			TicketBackend:    agg.Board.TicketBackend,
			BackendQuery:     agg.Board.BackendQuery,
			BackendConfig:    agg.Board.BackendConfig,
			NextTicketNumber: agg.NextTicketNumber,
			SyncEnabled:      agg.Board.SyncEnabled,
			SourceExportUUID: nullString(agg.Board.SourceExportUUID),
		},
	}
	if agg.Board.ArchivedAt.Valid {
		t := agg.Board.ArchivedAt.Time
		doc.Board.ArchivedAt = &t
	}
	for _, c := range agg.Columns {
		doc.Columns = append(doc.Columns, ColumnPayload{
			SourceID:    c.SourceID,
			Name:        c.Name,
			WorkflowKey: c.WorkflowKey,
			Position:    c.Position,
		})
	}
	for _, t := range agg.Tickets {
		doc.Tickets = append(doc.Tickets, TicketPayload{
			SourceID:              t.SourceID,
			ColumnSourceID:        t.ColumnSourceID,
			ExternalID:            nullStringPtr(t.ExternalID),
			ExternalURL:           nullStringPtr(t.ExternalURL),
			ExternalUpdatedAt:     nullTimePtr(t.ExternalUpdatedAt),
			SyncVersion:           nullStringPtr(t.SyncVersion),
			DisplayID:             t.DisplayID,
			DisplayNumber:         t.DisplayNumber,
			Title:                 t.Title,
			Body:                  t.Body,
			Harness:               t.Harness,
			Position:              t.Position,
			ArchivedAt:            nullTimePtr(t.ArchivedAt),
			FocusPaused:           t.FocusPaused,
			RemotePushState:       nullStringPtr(t.RemotePushState),
			RemotePushToken:       nullStringPtr(t.RemotePushToken),
			RemotePushAttemptedAt: nullTimePtr(t.RemotePushAttemptedAt),
			CreatedAt:             t.CreatedAt,
			UpdatedAt:             t.UpdatedAt,
		})
	}
	for _, n := range agg.Notes {
		doc.Notes = append(doc.Notes, NotePayload{
			SourceID:          n.SourceID,
			TicketSourceID:    n.TicketSourceID,
			ExternalID:        nullStringPtr(n.ExternalID),
			ExternalUpdatedAt: nullTimePtr(n.ExternalUpdatedAt),
			SyncVersion:       nullStringPtr(n.SyncVersion),
			Body:              n.Body,
			DeletedAt:         nullTimePtr(n.DeletedAt),
			CreatedAt:         n.CreatedAt,
			UpdatedAt:         n.UpdatedAt,
		})
	}
	for _, checkpoint := range agg.PauseCheckpoints {
		doc.Checkpoints = append(doc.Checkpoints, CheckpointPayload{
			SourceID: checkpoint.SourceID, TicketSourceID: checkpoint.TicketSourceID,
			Why: checkpoint.Why, Completed: checkpoint.Completed, NextAction: checkpoint.NextAction,
			PausedAt: checkpoint.PausedAt, ResumedAt: nullTimePtr(checkpoint.ResumedAt),
		})
	}
	for _, ses := range agg.Sessions {
		doc.Sessions = append(doc.Sessions, SessionPayload{
			SourceID:            ses.SourceID,
			TicketSourceID:      ses.TicketSourceID,
			Harness:             ses.Harness,
			HarnessSessionRef:   nullStringPtr(ses.HarnessSessionRef),
			HarnessSessionName:  nullStringPtr(ses.HarnessSessionName),
			TmuxSessionName:     ses.TmuxSessionName,
			TmuxWindowID:        nullStringPtr(ses.TmuxWindowID),
			TmuxWindowName:      ses.TmuxWindowName,
			Multiplexer:         ses.Multiplexer,
			MuxNamespace:        nullStringPtr(ses.MuxNamespace),
			MuxContainerID:      nullStringPtr(ses.MuxContainerID),
			MuxContainerName:    nullStringPtr(ses.MuxContainerName),
			MuxMetadata:         nullStringPtr(ses.MuxMetadata),
			Status:              ses.Status,
			IsActive:            ses.IsActive,
			StartedAt:           nullTimePtr(ses.StartedAt),
			ClosedAt:            nullTimePtr(ses.ClosedAt),
			LastSeenTmuxAt:      nullTimePtr(ses.LastSeenTmuxAt),
			LastOutputAt:        nullTimePtr(ses.LastOutputAt),
			LastStateChangeAt:   nullTimePtr(ses.LastStateChangeAt),
			LastDetectedState:   nullStringPtr(ses.LastDetectedState),
			LastAttentionReason: nullStringPtr(ses.LastAttentionReason),
			LastDetectionSource: nullStringPtr(ses.LastDetectionSource),
			LastObservedExcerpt: nullStringPtr(ses.LastObservedExcerpt),
			CreatedAt:           ses.CreatedAt,
			UpdatedAt:           ses.UpdatedAt,
		})
	}
	return doc
}

func nullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}
