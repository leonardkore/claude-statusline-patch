package verifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const DefaultDurationSeconds = 8

var ErrUnavailable = errors.New("claude-statusline-verify unavailable")

type Result struct {
	Mode                   string `json:"mode"`
	RunID                  string `json:"run_id"`
	DurationSeconds        int    `json:"duration_seconds"`
	EventsFile             string `json:"events_file"`
	PaneCaptureFile        string `json:"pane_capture_file"`
	EventCount             int    `json:"event_count"`
	DistinctSessionSeconds []int  `json:"distinct_session_seconds"`
	Passed                 bool   `json:"passed"`
}

var (
	lookPath       = exec.LookPath
	commandContext = exec.CommandContext
)

func Verify(ctx context.Context, mode string, durationSeconds int) (Result, error) {
	if strings.TrimSpace(mode) == "" {
		return Result{}, fmt.Errorf("verify mode is required")
	}
	if durationSeconds <= 0 {
		return Result{}, fmt.Errorf("verify duration must be positive")
	}

	path, err := lookPath("claude-statusline-verify")
	if err != nil {
		return Result{}, ErrUnavailable
	}

	cmd := commandContext(ctx, path, mode, strconv.Itoa(durationSeconds))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// Try parsing JSON from stdout first — the verifier script writes
	// structured output even on non-zero exit (exit 1 = passed: false).
	var result Result
	if json.Unmarshal(stdout.Bytes(), &result) == nil {
		return result, nil
	}

	// No valid JSON — treat the run error as a real failure.
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) {
			return Result{}, ErrUnavailable
		}
		if strings.TrimSpace(stderr.String()) == "" {
			return Result{}, fmt.Errorf("run verifier: %w", runErr)
		}
		return Result{}, fmt.Errorf("run verifier: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	return Result{}, fmt.Errorf("parse verifier output: empty or invalid JSON")
}
