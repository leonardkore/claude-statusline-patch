package verifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultDurationSeconds = 8

var (
	ErrUnavailable    = errors.New("claude-statusline-verify unavailable")
	ErrTargetMismatch = errors.New("verifier target does not match active claude binary")
)

type Options struct {
	Mode            string
	DurationSeconds int
	TargetBinary    string
	ContractVersion int
}

type Result struct {
	Mode                    string `json:"mode"`
	TargetBinary            string `json:"target_binary"`
	RunID                   string `json:"run_id"`
	DurationSeconds         int    `json:"duration_seconds"`
	VerifierContractVersion int    `json:"verifier_contract_version"`
	EventsFile              string `json:"events_file"`
	PaneCaptureFile         string `json:"pane_capture_file"`
	EventCount              int    `json:"event_count"`
	DistinctSessionSeconds  []int  `json:"distinct_session_seconds"`
	Passed                  bool   `json:"passed"`
}

var (
	executablePath = defaultExecutablePath
	commandContext = exec.CommandContext
)

func Verify(ctx context.Context, mode string, durationSeconds int) (Result, error) {
	return VerifyWithOptions(ctx, Options{
		Mode:            mode,
		DurationSeconds: durationSeconds,
	})
}

func VerifyWithOptions(ctx context.Context, options Options) (Result, error) {
	if strings.TrimSpace(options.Mode) == "" {
		return Result{}, fmt.Errorf("verify mode is required")
	}
	if options.DurationSeconds <= 0 {
		return Result{}, fmt.Errorf("verify duration must be positive")
	}

	path, err := executablePath()
	if err != nil {
		return Result{}, err
	}

	cmd := commandContext(ctx, path, options.Mode, strconv.Itoa(options.DurationSeconds))
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env,
		"CLAUDE_STATUSLINE_VERIFY_TARGET="+options.TargetBinary,
		"CLAUDE_STATUSLINE_VERIFY_CONTRACT_VERSION="+strconv.Itoa(options.ContractVersion),
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// Try parsing JSON from stdout first; the verifier script writes
	// structured output even on non-zero exit (exit 1 = passed: false).
	var result Result
	if json.Unmarshal(stdout.Bytes(), &result) == nil {
		if err := validateResult(result, options); err != nil {
			return Result{}, err
		}
		if runErr != nil {
			if result.Passed || !isExpectedVerificationFailure(runErr) {
				return Result{}, verifierRunError(runErr, stderr.String())
			}
		}
		return result, nil
	}

	// No valid JSON — treat the run error as a real failure.
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) {
			return Result{}, ErrUnavailable
		}
		return Result{}, verifierRunError(runErr, stderr.String())
	}
	return Result{}, fmt.Errorf("parse verifier output: empty or invalid JSON")
}

func defaultExecutablePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	path := filepath.Join(home, ".local", "bin", "claude-statusline-verify")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrUnavailable
		}
		return "", fmt.Errorf("stat verifier %s: %w", path, err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "", fmt.Errorf("verifier is not a regular file: %s", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("verifier is not executable: %s", path)
	}
	return path, nil
}

func validateResult(result Result, options Options) error {
	if result.Mode != options.Mode {
		return fmt.Errorf("verifier result mode mismatch: expected %q, got %q", options.Mode, result.Mode)
	}
	if result.DurationSeconds < options.DurationSeconds {
		return fmt.Errorf("verifier result duration too short: expected at least %d, got %d", options.DurationSeconds, result.DurationSeconds)
	}
	if options.TargetBinary != "" && result.TargetBinary != options.TargetBinary {
		return fmt.Errorf("verifier result target mismatch: expected %q, got %q", options.TargetBinary, result.TargetBinary)
	}
	if options.ContractVersion > 0 && result.VerifierContractVersion != options.ContractVersion {
		return fmt.Errorf("verifier contract mismatch: expected %d, got %d", options.ContractVersion, result.VerifierContractVersion)
	}
	if result.Passed && strings.TrimSpace(result.TargetBinary) == "" {
		return fmt.Errorf("verifier passed with empty target binary")
	}
	if result.Passed && result.VerifierContractVersion <= 0 {
		return fmt.Errorf("verifier passed with invalid contract version")
	}
	if result.Passed && strings.TrimSpace(result.RunID) == "" {
		return fmt.Errorf("verifier passed with empty run id")
	}
	if result.Passed && (strings.TrimSpace(result.EventsFile) == "" || strings.TrimSpace(result.PaneCaptureFile) == "") {
		return fmt.Errorf("verifier passed without artifact paths")
	}
	if result.Passed && len(result.DistinctSessionSeconds) < 5 {
		return fmt.Errorf("verifier passed with too few distinct session samples: %d", len(result.DistinctSessionSeconds))
	}
	if result.Passed && result.EventCount <= 0 {
		return fmt.Errorf("verifier passed with no recorded events")
	}
	return nil
}

func isExpectedVerificationFailure(runErr error) bool {
	var exitErr *exec.ExitError
	return errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1
}

func verifierRunError(runErr error, stderr string) error {
	if strings.TrimSpace(stderr) == "" {
		return fmt.Errorf("run verifier: %w", runErr)
	}
	return fmt.Errorf("run verifier: %w: %s", runErr, strings.TrimSpace(stderr))
}
