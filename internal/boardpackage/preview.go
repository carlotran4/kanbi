package boardpackage

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
)

// Preview validates a package without mutating SQLite or attachments.
func Preview(ctx context.Context, store *storage.Store, archivePath string) (Report, error) {
	manifest, doc, attachmentNames, err := readPackage(archivePath)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Manifest:         manifest,
		BoardName:        doc.Board.Name,
		TicketBackend:    doc.Board.TicketBackend,
		TicketCount:      len(doc.Tickets),
		NoteCount:        len(doc.Notes),
		SessionCount:     len(doc.Sessions),
		AttachmentCount:  len(doc.Attachments),
		SchemaCompatible: manifest.SchemaVersion >= MinSupportedSchemaVersion && manifest.SchemaVersion <= storage.CurrentSchemaVersion(),
	}
	if !report.SchemaCompatible {
		report.Warnings = append(report.Warnings, fmt.Sprintf("schema_version %d not in supported range [%d,%d]", manifest.SchemaVersion, MinSupportedSchemaVersion, storage.CurrentSchemaVersion()))
	}
	for _, ses := range doc.Sessions {
		if ses.IsActive {
			report.ActiveSessionCount++
		}
	}
	if report.ActiveSessionCount > 0 {
		report.Warnings = append(report.Warnings, "package includes active session rows; import will force them inactive")
	}
	if strings.TrimSpace(doc.Board.BackendConfig) != "" {
		report.Warnings = append(report.Warnings, "package includes backend_config which may contain provider credentials; import starts sync-disabled")
	}
	inventoryIssues, err := validateAttachmentInventory(doc.Attachments, attachmentNames)
	if err != nil {
		return Report{}, err
	}
	report.MissingAttachments = inventoryIssues.Missing
	report.UnlistedAttachments = inventoryIssues.Unlisted
	report.InvalidAttachments = inventoryIssues.Invalid
	if store != nil {
		if board, err := store.BoardByName(ctx, doc.Board.Name); err == nil && board.ID != 0 {
			report.NameCollision = true
			report.Warnings = append(report.Warnings, "board name collides; import requires --name")
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Report{}, err
		}
		if uuid := strings.TrimSpace(manifest.SourceBoardUUID); uuid != "" {
			boards, err := store.ListBoardsFiltered(ctx, true)
			if err == nil {
				for _, b := range boards {
					if b.SourceExportUUID.Valid && b.SourceExportUUID.String == uuid {
						report.Warnings = append(report.Warnings, "another board already imported from this package source uuid")
						break
					}
				}
			}
		}
	}
	return report, nil
}

type inventoryIssues struct {
	Missing  []string
	Unlisted []string
	Invalid  []string
	// byZipPath maps "attachments/<ticket>/<file>" -> inventory entry
	byZipPath map[string]AttachmentEntry
}

func validateAttachmentInventory(entries []AttachmentEntry, attachmentNames map[string]bool) (inventoryIssues, error) {
	out := inventoryIssues{byZipPath: map[string]AttachmentEntry{}}
	seenKeys := map[string]bool{}
	for i, entry := range entries {
		if entry.SourceTicketID <= 0 {
			out.Invalid = append(out.Invalid, fmt.Sprintf("attachments[%d]: invalid source_ticket_id", i))
			continue
		}
		rel, err := flatAttachmentName(entry.RelativePath)
		if err != nil {
			out.Invalid = append(out.Invalid, fmt.Sprintf("attachments[%d]: %v", i, err))
			continue
		}
		if strings.TrimSpace(entry.SHA256) == "" {
			out.Invalid = append(out.Invalid, fmt.Sprintf("attachments/%d/%s: blank checksum", entry.SourceTicketID, rel))
			continue
		}
		if entry.Size < 0 || entry.Size > maxAttachmentBytes {
			out.Invalid = append(out.Invalid, fmt.Sprintf("attachments/%d/%s: invalid size %d", entry.SourceTicketID, rel, entry.Size))
			continue
		}
		key := strconv.FormatInt(entry.SourceTicketID, 10) + "/" + rel
		if seenKeys[key] {
			out.Invalid = append(out.Invalid, fmt.Sprintf("attachments/%s: duplicate inventory entry", key))
			continue
		}
		seenKeys[key] = true
		want := "attachments/" + key
		entry.RelativePath = rel
		out.byZipPath[want] = entry
		if !attachmentNames[want] {
			out.Missing = append(out.Missing, want)
		}
	}
	for name := range attachmentNames {
		if _, ok := out.byZipPath[name]; !ok {
			out.Unlisted = append(out.Unlisted, name)
		}
	}
	return out, nil
}

func readPackage(archivePath string) (Manifest, Document, map[string]bool, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return Manifest{}, Document{}, nil, err
	}
	defer zr.Close()

	var manifest Manifest
	var doc Document
	haveManifest, haveBoard := false, false
	attachmentNames := map[string]bool{}

	for _, zf := range zr.File {
		name := filepath.ToSlash(zf.Name)
		switch {
		case name == "manifest.json":
			data, err := readLimited(zf, 1<<20)
			if err != nil {
				return Manifest{}, Document{}, nil, err
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return Manifest{}, Document{}, nil, err
			}
			haveManifest = true
		case name == "board.json":
			data, err := readLimited(zf, 64<<20)
			if err != nil {
				return Manifest{}, Document{}, nil, err
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				return Manifest{}, Document{}, nil, err
			}
			haveBoard = true
		case strings.HasPrefix(name, "attachments/"):
			if strings.HasSuffix(name, "/") {
				continue
			}
			rel := strings.TrimPrefix(name, "attachments/")
			safe, err := safeRelativePath(rel)
			if err != nil {
				return Manifest{}, Document{}, nil, err
			}
			parts := strings.SplitN(safe, "/", 2)
			if len(parts) != 2 {
				return Manifest{}, Document{}, nil, fmt.Errorf("invalid attachment layout %q", name)
			}
			if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
				return Manifest{}, Document{}, nil, fmt.Errorf("invalid attachment ticket id in %q", name)
			}
			if _, err := flatAttachmentName(parts[1]); err != nil {
				return Manifest{}, Document{}, nil, err
			}
			attachmentNames[name] = true
		case name == "" || strings.HasSuffix(name, "/"):
			// directory entries ignored
		default:
			return Manifest{}, Document{}, nil, fmt.Errorf("unexpected package entry %q", name)
		}
	}
	if !haveManifest || manifest.Format != FormatName || manifest.Version != FormatVersion {
		return Manifest{}, Document{}, nil, errors.New("unsupported or missing Kanbi board package manifest")
	}
	if !haveBoard {
		return Manifest{}, Document{}, nil, errors.New("board package has no board.json")
	}
	return manifest, doc, attachmentNames, nil
}
