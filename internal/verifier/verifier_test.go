package verifier

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestVerifyRejectsPassedJSONFromFailingVerifier(t *testing.T) {
	withVerifierHelper(t, `{"mode":"on","target_binary":"/target","run_id":"run-1","duration_seconds":8,"verifier_contract_version":1,"events_file":"events.jsonl","pane_capture_file":"pane.txt","event_count":5,"distinct_session_seconds":[0,1,2,3,4],"passed":true}`, 1)

	_, err := VerifyWithOptions(context.Background(), Options{Mode: "on", DurationSeconds: 8, TargetBinary: "/target", ContractVersion: 1})
	if err == nil {
		t.Fatalf("expected failing verifier with passed=true to be rejected")
	}
	if !strings.Contains(err.Error(), "run verifier") {
		t.Fatalf("expected run verifier error, got %v", err)
	}
}

func TestVerifyRejectsFailedEnvelopeFromUnexpectedExit(t *testing.T) {
	withVerifierHelper(t, `{"mode":"on","target_binary":"/target","duration_seconds":8,"verifier_contract_version":1,"event_count":1,"distinct_session_seconds":[0],"passed":false}`, 2)

	_, err := VerifyWithOptions(context.Background(), Options{Mode: "on", DurationSeconds: 8, TargetBinary: "/target", ContractVersion: 1})
	if err == nil {
		t.Fatalf("expected unexpected verifier exit to be rejected")
	}
	if !strings.Contains(err.Error(), "run verifier") {
		t.Fatalf("expected run verifier error, got %v", err)
	}
}

func TestVerifyRejectsStructurallyInvalidPassedJSON(t *testing.T) {
	withVerifierHelper(t, `{"passed":true}`, 0)

	_, err := VerifyWithOptions(context.Background(), Options{Mode: "on", DurationSeconds: 8, TargetBinary: "/target", ContractVersion: 1})
	if err == nil {
		t.Fatalf("expected structurally invalid verifier JSON to be rejected")
	}
	if !strings.Contains(err.Error(), "mode mismatch") {
		t.Fatalf("expected mode mismatch, got %v", err)
	}
}

func TestVerifyAcceptsFailedVerificationEnvelope(t *testing.T) {
	withVerifierHelper(t, `{"mode":"on","target_binary":"/target","duration_seconds":8,"verifier_contract_version":1,"event_count":1,"distinct_session_seconds":[0],"passed":false}`, 1)

	result, err := VerifyWithOptions(context.Background(), Options{Mode: "on", DurationSeconds: 8, TargetBinary: "/target", ContractVersion: 1})
	if err != nil {
		t.Fatalf("expected failed verifier envelope to be returned for caller classification, got %v", err)
	}
	if result.Passed {
		t.Fatalf("expected failed verifier result")
	}
}

func TestVerifyPassesTargetAndContractEnvironment(t *testing.T) {
	withVerifierHelper(t, `{"mode":"on","target_binary":"/target","run_id":"run-1","duration_seconds":8,"verifier_contract_version":7,"events_file":"events.jsonl","pane_capture_file":"pane.txt","event_count":5,"distinct_session_seconds":[0,1,2,3,4],"passed":true}`, 0)

	result, err := VerifyWithOptions(context.Background(), Options{Mode: "on", DurationSeconds: 8, TargetBinary: "/target", ContractVersion: 7})
	if err != nil {
		t.Fatalf("VerifyWithOptions failed: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected verifier pass")
	}
}

func TestVerifyRejectsTargetMismatch(t *testing.T) {
	withVerifierHelper(t, `{"mode":"on","target_binary":"/other","run_id":"run-1","duration_seconds":8,"verifier_contract_version":1,"events_file":"events.jsonl","pane_capture_file":"pane.txt","event_count":5,"distinct_session_seconds":[0,1,2,3,4],"passed":true}`, 0)

	_, err := VerifyWithOptions(context.Background(), Options{Mode: "on", DurationSeconds: 8, TargetBinary: "/target", ContractVersion: 1})
	if err == nil {
		t.Fatalf("expected verifier target mismatch")
	}
	if !strings.Contains(err.Error(), "target mismatch") {
		t.Fatalf("expected target mismatch, got %v", err)
	}
}

func TestVerifyRejectsPassedResultMissingRequiredContractFields(t *testing.T) {
	withVerifierHelper(t, `{"mode":"on","duration_seconds":8,"event_count":5,"distinct_session_seconds":[0,1,2,3,4],"passed":true}`, 0)

	_, err := VerifyWithOptions(context.Background(), Options{Mode: "on", DurationSeconds: 8, TargetBinary: "/target", ContractVersion: 1})
	if err == nil {
		t.Fatalf("expected passed verifier result without contract fields to be rejected")
	}
	if !strings.Contains(err.Error(), "target") {
		t.Fatalf("expected target contract error, got %v", err)
	}
}

func withVerifierHelper(t *testing.T, stdout string, exitCode int) {
	t.Helper()

	originalExecutablePath := executablePath
	originalCommandContext := commandContext
	t.Cleanup(func() {
		executablePath = originalExecutablePath
		commandContext = originalCommandContext
	})

	executablePath = func() (string, error) {
		return os.Args[0], nil
	}
	commandContext = func(ctx context.Context, path string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=TestVerifierHelperProcess", "--"}, args...)
		cmd := exec.CommandContext(ctx, path, helperArgs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_VERIFIER_HELPER_PROCESS=1",
			"VERIFIER_HELPER_STDOUT="+stdout,
			"VERIFIER_HELPER_EXIT_CODE="+strconv.Itoa(exitCode),
		)
		return cmd
	}
}

func TestVerifierHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFIER_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("CLAUDE_STATUSLINE_VERIFY_TARGET") != "/target" {
		os.Exit(9)
	}
	if os.Getenv("CLAUDE_STATUSLINE_VERIFY_CONTRACT_VERSION") == "" {
		os.Exit(8)
	}
	if _, err := os.Stdout.WriteString(os.Getenv("VERIFIER_HELPER_STDOUT")); err != nil {
		os.Exit(7)
	}
	switch os.Getenv("VERIFIER_HELPER_EXIT_CODE") {
	case "0":
		os.Exit(0)
	case "1":
		os.Exit(1)
	case "2":
		os.Exit(2)
	default:
		os.Exit(2)
	}
}

func TestDefaultExecutablePathRequiresPinnedVerifier(t *testing.T) {
	original := executablePath
	t.Cleanup(func() {
		executablePath = original
	})
	executablePath = defaultExecutablePath
	_, err := executablePath()
	if err != nil && !errors.Is(err, ErrUnavailable) && !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("unexpected default executable path error: %v", err)
	}
}
