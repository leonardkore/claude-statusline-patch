package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leonardkore/claude-statusline-patch/internal/fileutil"
)

const appDirName = "claude-statusline-patch"

const (
	stateDirMode   = 0o700
	metadataMode   = 0o600
	backupFileMode = 0o400
	schemaVersion  = 1
)

type Metadata struct {
	SchemaVersion   int    `json:"schema_version"`
	CanonicalPath   string `json:"canonical_path"`
	DisplayPath     string `json:"display_path"`
	PathKey         string `json:"path_key"`
	DetectedVersion string `json:"detected_version"`
	OriginalSHA256  string `json:"original_sha256"`
	PatchedSHA256   string `json:"patched_sha256"`
	IntervalMS      int    `json:"interval_ms"`
	BackupPath      string `json:"backup_path"`
	FileMode        uint32 `json:"file_mode"`
	UpdatedAt       string `json:"updated_at"`
}

func StateRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("XDG_STATE_HOME must be an absolute path: %s", root)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		rel, err := filepath.Rel(home, root)
		if err != nil {
			return "", fmt.Errorf("resolve XDG_STATE_HOME relative to home dir: %w", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("XDG_STATE_HOME must stay within %s: %s", home, root)
		}
		return filepath.Join(root, appDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", appDirName), nil
}

func TargetDir(canonicalPath string) (string, error) {
	root, err := StateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "targets", PathKey(canonicalPath)), nil
}

func PathKey(canonicalPath string) string {
	sum := sha256.Sum256([]byte(canonicalPath))
	return hex.EncodeToString(sum[:])
}

func SHA256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func SHA256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ExpectedBackupPath(canonicalPath, originalHash string) (string, error) {
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(targetDir, fmt.Sprintf("backup-%s.bin", originalHash)), nil
}

func MetadataPath(canonicalPath, originalHash string) (string, error) {
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(targetDir, fmt.Sprintf("metadata-%s.json", originalHash)), nil
}

func EnsureBackup(canonicalPath, originalHash string, data []byte) (string, bool, error) {
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(targetDir, stateDirMode); err != nil {
		return "", false, fmt.Errorf("create target state dir: %w", err)
	}

	backupPath, err := ExpectedBackupPath(canonicalPath, originalHash)
	if err != nil {
		return "", false, err
	}
	if info, err := os.Stat(backupPath); err == nil {
		if !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("existing backup is not a regular file: %s", backupPath)
		}
		if info.Size() != int64(len(data)) {
			return "", false, fmt.Errorf("existing backup size mismatch for %s: expected %d, found %d", backupPath, len(data), info.Size())
		}
		return backupPath, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("stat existing backup: %w", err)
	}

	if err := writeAtomic(backupPath, data, backupFileMode); err != nil {
		return "", false, err
	}
	return backupPath, true, nil
}

func SaveMetadata(meta Metadata) error {
	targetDir, err := TargetDir(meta.CanonicalPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, stateDirMode); err != nil {
		return fmt.Errorf("create target state dir: %w", err)
	}

	backupPath, err := ExpectedBackupPath(meta.CanonicalPath, meta.OriginalSHA256)
	if err != nil {
		return err
	}

	meta.SchemaVersion = schemaVersion
	meta.PathKey = PathKey(meta.CanonicalPath)
	meta.BackupPath = backupPath
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	path, err := MetadataPath(meta.CanonicalPath, meta.OriginalSHA256)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	payload = append(payload, '\n')
	return writeAtomic(path, payload, metadataMode)
}

func DeleteMetadata(canonicalPath, originalHash string) error {
	path, err := MetadataPath(canonicalPath, originalHash)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove metadata %s: %w", path, err)
	}
	return syncDir(filepath.Dir(path))
}

func DeleteBackup(canonicalPath, originalHash string) error {
	path, err := ExpectedBackupPath(canonicalPath, originalHash)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove backup %s: %w", path, err)
	}
	return syncDir(filepath.Dir(path))
}

func LoadMatchingRecord(canonicalPath, currentHash string) (*Metadata, error) {
	records, err := loadRecords(canonicalPath)
	if err != nil {
		return nil, err
	}

	var matches []*Metadata
	for i := range records {
		record := records[i]
		if record.OriginalSHA256 == currentHash || record.PatchedSHA256 == currentHash {
			recordCopy := record
			matches = append(matches, &recordCopy)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches[len(matches)-1], nil
}

func loadRecords(canonicalPath string) ([]Metadata, error) {
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read target state dir: %w", err)
	}

	var records []Metadata
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "metadata-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		payload, readErr := os.ReadFile(filepath.Join(targetDir, entry.Name()))
		if readErr != nil {
			continue
		}
		var meta Metadata
		if err := json.Unmarshal(payload, &meta); err != nil {
			continue
		}
		if err := validateMetadata(meta, canonicalPath); err != nil {
			continue
		}
		records = append(records, meta)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].UpdatedAt < records[j].UpdatedAt
	})
	return records, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temp file for %s: %w", path, err)
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("chmod temp file for %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync temp file for %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp file for %s: %w", path, err)
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	if err := fileutil.ReplaceFile(tempName, path); err != nil {
		return fmt.Errorf("rename temp file for %s: %w", path, err)
	}
	committed = true
	if err := syncDir(dir); err != nil {
		return err
	}
	return nil
}

func validateMetadata(meta Metadata, canonicalPath string) error {
	if meta.SchemaVersion != 0 && meta.SchemaVersion != schemaVersion {
		return fmt.Errorf("unsupported schema version %d", meta.SchemaVersion)
	}
	if meta.CanonicalPath != canonicalPath {
		return fmt.Errorf("metadata canonical path mismatch: %s", meta.CanonicalPath)
	}
	expectedPathKey := PathKey(canonicalPath)
	if meta.PathKey != "" && meta.PathKey != expectedPathKey {
		return fmt.Errorf("metadata path key mismatch: %s", meta.PathKey)
	}
	expectedBackupPath, err := ExpectedBackupPath(canonicalPath, meta.OriginalSHA256)
	if err != nil {
		return err
	}
	if filepath.Clean(meta.BackupPath) != filepath.Clean(expectedBackupPath) {
		return fmt.Errorf("metadata backup path mismatch: %s", meta.BackupPath)
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
