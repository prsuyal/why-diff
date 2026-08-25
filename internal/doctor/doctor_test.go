package doctor_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/prsuyal/why-diff/internal/doctor"
	"github.com/prsuyal/why-diff/internal/initialize"
)

func TestRunDistinguishesInitializationFromCaptureReadiness(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")

	uninitialized := doctor.Run(context.Background(), root, doctor.Options{
		LookupExecutable: func(string) (string, error) { return "", errors.New("not found") },
	})
	if uninitialized.Ready() {
		t.Fatalf("uninitialized report = %+v, want errors", uninitialized)
	}

	if _, err := initialize.Run(context.Background(), root); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	initialized := doctor.Run(context.Background(), root, doctor.Options{
		LookupExecutable: func(string) (string, error) { return "/usr/local/bin/whydiff", nil },
	})
	if !initialized.Ready() {
		t.Fatalf("initialized report = %+v, want ready with only no-session warning", initialized)
	}
	if !hasCheck(initialized, "Provenance data", doctor.StatusWarning) {
		t.Fatalf("initialized report = %+v, want no-session warning", initialized)
	}
}

func TestRunReportsACompleteCorruptRecordAsAnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	if _, err := initialize.Run(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(root, ".git", "whydiff", "active", "corrupt-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := doctor.Run(context.Background(), root, doctor.Options{
		LookupExecutable: func(string) (string, error) { return "/usr/local/bin/whydiff", nil },
	})
	if report.Ready() || !hasCheck(report, "Provenance data", doctor.StatusError) {
		t.Fatalf("corrupt report = %+v, want provenance error", report)
	}
}

func TestRunAcceptsClaudeAsTheOnlyConfiguredProvider(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	if _, err := initialize.RunProviders(context.Background(), root, []initialize.Provider{initialize.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	report := doctor.Run(context.Background(), root, doctor.Options{
		LookupExecutable: func(string) (string, error) { return "/usr/local/bin/whydiff", nil },
	})
	if !report.Ready() || !hasCheck(report, "Claude Code hooks", doctor.StatusOK) || !hasCheck(report, "Codex hooks", doctor.StatusWarning) {
		t.Fatalf("report = %+v", report)
	}
}

func hasCheck(report doctor.Report, name string, status doctor.Status) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}

func git(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
