package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const verifiedSchemaVersion = 1

type VerifiedOutcome struct {
	SchemaVersion           int    `json:"schema_version"`
	CanonicalPath           string `json:"canonical_path"`
	PathKey                 string `json:"path_key"`
	InstalledSHA256         string `json:"installed_sha256"`
	IntervalMS              int    `json:"interval_ms"`
	PlatformGOOS            string `json:"platform_goos"`
	PlatformGOARCH          string `json:"platform_goarch"`
	VerifierContractVersion int    `json:"verifier_contract_version"`
	DetectedVersion         string `json:"detected_version"`
	VerifiedAt              string `json:"verified_at"`
}

func VerifiedOutcomePath(canonicalPath, installedHash string, intervalMS int, goos, goarch string, verifierContractVersion int) (string, error) {
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		return "", err
	}
	filename := fmt.Sprintf(
		"verified-%s-%d-%s-%s-v%d.json",
		installedHash,
		intervalMS,
		strings.TrimSpace(goos),
		strings.TrimSpace(goarch),
		verifierContractVersion,
	)
	return filepath.Join(targetDir, filename), nil
}

func SaveVerifiedOutcome(record VerifiedOutcome) error {
	targetDir, err := TargetDir(record.CanonicalPath)
	if err != nil {
		return err
	}
	record.SchemaVersion = verifiedSchemaVersion
	record.PathKey = PathKey(record.CanonicalPath)
	record.VerifiedAt = time.Now().UTC().Format(time.RFC3339)

	if err := validateVerifiedOutcome(record); err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, stateDirMode); err != nil {
		return fmt.Errorf("create target state dir: %w", err)
	}
	if err := syncDir(targetDir); err != nil && !os.IsNotExist(err) {
		return err
	}

	path, err := VerifiedOutcomePath(
		record.CanonicalPath,
		record.InstalledSHA256,
		record.IntervalMS,
		record.PlatformGOOS,
		record.PlatformGOARCH,
		record.VerifierContractVersion,
	)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal verified outcome: %w", err)
	}
	payload = append(payload, '\n')
	return writeAtomic(path, payload, metadataMode)
}

func LoadVerifiedOutcome(canonicalPath, installedHash string, intervalMS int, goos, goarch string, verifierContractVersion int) (*VerifiedOutcome, error) {
	path, err := VerifiedOutcomePath(canonicalPath, installedHash, intervalMS, goos, goarch, verifierContractVersion)
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read verified outcome %s: %w", path, err)
	}
	var record VerifiedOutcome
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("parse verified outcome %s: %w", path, err)
	}
	if err := validateVerifiedOutcome(record); err != nil {
		return nil, err
	}
	return &record, nil
}

func LoadAllVerifiedOutcomes(canonicalPath string) ([]VerifiedOutcome, error) {
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read verified outcomes dir: %w", err)
	}

	var records []VerifiedOutcome
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "verified-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		payload, readErr := os.ReadFile(filepath.Join(targetDir, entry.Name()))
		if readErr != nil {
			continue
		}
		var record VerifiedOutcome
		if err := json.Unmarshal(payload, &record); err != nil {
			continue
		}
		if err := validateVerifiedOutcome(record); err != nil {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].VerifiedAt < records[j].VerifiedAt
	})
	return records, nil
}

func validateVerifiedOutcome(record VerifiedOutcome) error {
	if record.SchemaVersion != 0 && record.SchemaVersion != verifiedSchemaVersion {
		return fmt.Errorf("unsupported verified outcome schema version %d", record.SchemaVersion)
	}
	if strings.TrimSpace(record.CanonicalPath) == "" {
		return fmt.Errorf("verified outcome canonical path is required")
	}
	expectedPathKey := PathKey(record.CanonicalPath)
	if record.PathKey != "" && record.PathKey != expectedPathKey {
		return fmt.Errorf("verified outcome path key mismatch: %s", record.PathKey)
	}
	if strings.TrimSpace(record.InstalledSHA256) == "" {
		return fmt.Errorf("verified outcome installed sha256 is required")
	}
	if record.IntervalMS <= 0 {
		return fmt.Errorf("verified outcome interval must be positive")
	}
	if strings.TrimSpace(record.PlatformGOOS) == "" || strings.TrimSpace(record.PlatformGOARCH) == "" {
		return fmt.Errorf("verified outcome platform tuple is required")
	}
	if record.VerifierContractVersion <= 0 {
		return fmt.Errorf("verified outcome verifier contract version must be positive")
	}
	return nil
}
