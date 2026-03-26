package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const appDirName = "claude-statusline-patch"

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
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(data), nil
}

func EnsureBackup(canonicalPath, originalHash string, data []byte, mode os.FileMode) (string, error) {
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create target state dir: %w", err)
	}

	backupPath := filepath.Join(targetDir, fmt.Sprintf("backup-%s.bin", originalHash))
	if _, err := os.Stat(backupPath); err == nil {
		hash, hashErr := SHA256File(backupPath)
		if hashErr != nil {
			return "", fmt.Errorf("hash existing backup: %w", hashErr)
		}
		if hash != originalHash {
			return "", fmt.Errorf("existing backup hash mismatch for %s", backupPath)
		}
		return backupPath, nil
	}

	if err := writeAtomic(backupPath, data, mode); err != nil {
		return "", err
	}
	return backupPath, nil
}

func SaveMetadata(meta Metadata) error {
	targetDir, err := TargetDir(meta.CanonicalPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create target state dir: %w", err)
	}

	meta.SchemaVersion = 1
	meta.PathKey = PathKey(meta.CanonicalPath)
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	path := filepath.Join(targetDir, fmt.Sprintf("metadata-%s.json", meta.OriginalSHA256))
	payload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	payload = append(payload, '\n')
	return writeAtomic(path, payload, 0o644)
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
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple managed records match %s", canonicalPath)
	}
	return matches[0], nil
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
			return nil, fmt.Errorf("read metadata %s: %w", entry.Name(), readErr)
		}
		var meta Metadata
		if err := json.Unmarshal(payload, &meta); err != nil {
			return nil, fmt.Errorf("decode metadata %s: %w", entry.Name(), err)
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

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
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("rename temp file for %s: %w", path, err)
	}
	return nil
}
