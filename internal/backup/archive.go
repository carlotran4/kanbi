package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

const FormatVersion = 1

type Manifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

func Export(ctx context.Context, store *storage.Store, dataDir, destination string) (err error) {
	attachmentRoot := filepath.Join(dataDir, "attachments")
	if inside, pathErr := pathWithin(attachmentRoot, destination); pathErr != nil {
		return pathErr
	} else if inside {
		return errors.New("backup destination must not be inside the attachment directory")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "kanbi-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	dbSnapshot := filepath.Join(tmpDir, "kanbi.db")
	if err := store.BackupTo(ctx, dbSnapshot); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	zw := zip.NewWriter(f)
	defer func() {
		if closeErr := zw.Close(); err == nil {
			err = closeErr
		}
	}()
	manifest, _ := json.MarshalIndent(Manifest{Format: "kanbi-backup", Version: FormatVersion, CreatedAt: time.Now().UTC()}, "", "  ")
	if err := writeBytes(zw, "manifest.json", manifest, 0o600); err != nil {
		return err
	}
	if err := writeFile(zw, "database/kanbi.db", dbSnapshot); err != nil {
		return err
	}
	root := attachmentRoot
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported attachment file: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return writeFile(zw, filepath.ToSlash(filepath.Join("attachments", rel)), path)
	})
}

func pathWithin(root, candidate string) (bool, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func writeBytes(zw *zip.Writer, name string, data []byte, mode os.FileMode) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
func writeFile(zw *zip.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(0o600)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// Restore validates and stages all content before replacing existing data.
// The caller must ensure no Kanbi process has the target database open.
func Restore(ctx context.Context, archivePath, dbPath, dataDir string, force bool) error {
	if _, err := os.Stat(dbPath); err == nil && !force {
		return errors.New("database already exists; rerun with --force after stopping all Kanbi processes")
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	stage, err := os.MkdirTemp(filepath.Dir(dbPath), ".kanbi-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	stageDB := filepath.Join(stage, "kanbi.db")
	stageAttachments := filepath.Join(stage, "attachments")
	var manifest Manifest
	haveManifest, haveDB := false, false
	for _, zf := range zr.File {
		name := filepath.ToSlash(zf.Name)
		if name == "manifest.json" {
			data, err := readLimited(zf, 1<<20)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return err
			}
			haveManifest = true
			continue
		}
		var target string
		if name == "database/kanbi.db" {
			target = stageDB
			haveDB = true
		} else if strings.HasPrefix(name, "attachments/") {
			rel := strings.TrimPrefix(name, "attachments/")
			if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "..") {
				return fmt.Errorf("unsafe backup path %q", name)
			}
			target = filepath.Join(stageAttachments, filepath.FromSlash(rel))
		} else {
			return fmt.Errorf("unexpected backup entry %q", name)
		}
		if zf.UncompressedSize64 > 10<<30 {
			return fmt.Errorf("backup entry too large: %s", name)
		}
		if err := extract(zf, target); err != nil {
			return err
		}
	}
	if !haveManifest || manifest.Format != "kanbi-backup" || manifest.Version != FormatVersion {
		return errors.New("unsupported or missing Kanbi backup manifest")
	}
	if !haveDB {
		return errors.New("backup has no database")
	}
	if err := storage.ValidateDatabase(ctx, stageDB); err != nil {
		return err
	}
	if err := os.Chmod(stageDB, 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	oldDB := dbPath + ".restore-old"
	oldAttachments := filepath.Join(dataDir, "attachments.restore-old")
	attachments := filepath.Join(dataDir, "attachments")
	_ = os.Remove(oldDB)
	_ = os.RemoveAll(oldAttachments)
	for _, sidecar := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			return fmt.Errorf("refusing restore while SQLite sidecar exists: %s; stop Kanbi and checkpoint/close the database", sidecar)
		}
	}
	dbExisted := false
	if _, err := os.Stat(dbPath); err == nil {
		dbExisted = true
		if err := os.Rename(dbPath, oldDB); err != nil {
			return err
		}
	}
	if err := os.Rename(stageDB, dbPath); err != nil {
		if dbExisted {
			_ = os.Rename(oldDB, dbPath)
		}
		return err
	}
	attachmentsExisted := false
	if _, err := os.Stat(attachments); err == nil {
		attachmentsExisted = true
		if err := os.Rename(attachments, oldAttachments); err != nil {
			_ = os.Remove(dbPath)
			if dbExisted {
				_ = os.Rename(oldDB, dbPath)
			}
			return err
		}
	}
	if _, err := os.Stat(stageAttachments); err == nil {
		if err := os.Rename(stageAttachments, attachments); err != nil {
			_ = os.Remove(dbPath)
			if dbExisted {
				_ = os.Rename(oldDB, dbPath)
			}
			if attachmentsExisted {
				_ = os.Rename(oldAttachments, attachments)
			}
			return err
		}
	} else {
		_ = os.MkdirAll(attachments, 0o700)
	}
	_ = os.Remove(oldDB)
	_ = os.RemoveAll(oldAttachments)
	return nil
}

func readLimited(zf *zip.File, limit int64) ([]byte, error) {
	r, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, limit))
}
func extract(zf *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	r, err := zf.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
