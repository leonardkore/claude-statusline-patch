package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const defaultBinaryPath = "~/.local/bin/claude"

var versionPattern = regexp.MustCompile(`\b(\d+\.\d+\.\d+)\b`)

type ResolvedBinary struct {
	InputPath     string
	DisplayPath   string
	CanonicalPath string
	Version       string
	Mode          os.FileMode
}

func Resolve(binaryPath string) (*ResolvedBinary, error) {
	displayPath, err := expandPath(binaryPath)
	if err != nil {
		return nil, err
	}

	canonicalPath, err := filepath.EvalSymlinks(displayPath)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", displayPath, err)
	}

	info, err := os.Stat(canonicalPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", canonicalPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("target is a directory: %s", canonicalPath)
	}

	return &ResolvedBinary{
		InputPath:     binaryPath,
		DisplayPath:   displayPath,
		CanonicalPath: canonicalPath,
		Version:       detectVersion(canonicalPath),
		Mode:          info.Mode(),
	}, nil
}

func expandPath(binaryPath string) (string, error) {
	path := strings.TrimSpace(binaryPath)
	if path == "" {
		path = defaultBinaryPath
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		switch {
		case path == "~":
			path = home
		case strings.HasPrefix(path, "~/"):
			path = filepath.Join(home, path[2:])
		default:
			return "", fmt.Errorf("unsupported home-relative path: %s", path)
		}
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Abs(path)
}

func detectVersion(canonicalPath string) string {
	if version := detectVersionFromPath(canonicalPath); version != "" {
		return version
	}
	version, _ := detectVersionFromBinary(canonicalPath)
	return version
}

func detectVersionFromPath(canonicalPath string) string {
	parts := strings.Split(filepath.Clean(canonicalPath), string(filepath.Separator))
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "versions" && versionPattern.MatchString(parts[i+1]) {
			return parts[i+1]
		}
	}
	return ""
}

func detectVersionFromBinary(canonicalPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, canonicalPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("timed out running %s --version", canonicalPath)
		}
		return "", err
	}

	match := versionPattern.FindSubmatch(output)
	if len(match) != 2 {
		return "", fmt.Errorf("could not parse version from %s --version output", canonicalPath)
	}
	return string(match[1]), nil
}
