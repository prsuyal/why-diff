package checkpoint_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/prsuyal/why-diff/internal/checkpoint"
	"github.com/prsuyal/why-diff/internal/repository"
)

func TestCapturePreservesDirtyGitState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "WhyDiff Test")
	git(t, root, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(root, "tracked.txt"), "committed\n")
	git(t, root, "add", "tracked.txt")
	git(t, root, "commit", "--quiet", "-m", "initial")

	writeFile(t, filepath.Join(root, "tracked.txt"), "staged\n")
	git(t, root, "add", "tracked.txt")
	writeFile(t, filepath.Join(root, "tracked.txt"), "working\n")
	writeFile(t, filepath.Join(root, "untracked.txt"), "untracked\n")
	beforeStatus := git(t, root, "status", "--porcelain=v1")
	beforeIndex := git(t, root, "write-tree")
	beforeHead := git(t, root, "rev-parse", "HEAD")

	location, err := repository.Locate(context.Background(), root)
	if err != nil {
		t.Fatalf("Locate() error = %v", err)
	}
	result, err := checkpoint.Capture(context.Background(), location)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if result.State.HeadCommit != beforeHead {
		t.Fatalf("HeadCommit = %q, want %q", result.State.HeadCommit, beforeHead)
	}
	if result.State.IndexTree != beforeIndex {
		t.Fatalf("IndexTree = %q, want %q", result.State.IndexTree, beforeIndex)
	}
	if got := git(t, root, "show", result.State.WorktreeTree+":tracked.txt"); got != "working" {
		t.Fatalf("checkpoint tracked.txt = %q, want working", got)
	}
	if got := git(t, root, "show", result.State.WorktreeTree+":untracked.txt"); got != "untracked" {
		t.Fatalf("checkpoint untracked.txt = %q, want untracked", got)
	}

	afterStatus := git(t, root, "status", "--porcelain=v1")
	afterIndex := git(t, root, "write-tree")
	afterHead := git(t, root, "rev-parse", "HEAD")
	if afterStatus != beforeStatus || afterIndex != beforeIndex || afterHead != beforeHead {
		t.Fatalf("capture changed Git state:\nstatus before=%q after=%q\nindex before=%q after=%q\nHEAD before=%q after=%q", beforeStatus, afterStatus, beforeIndex, afterIndex, beforeHead, afterHead)
	}
}

func TestCaptureSupportsUnbornRepository(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	writeFile(t, filepath.Join(root, "new.txt"), "new\n")
	location, err := repository.Locate(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := checkpoint.Capture(context.Background(), location)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if result.State.HeadCommit != "" {
		t.Fatalf("HeadCommit = %q in unborn repository", result.State.HeadCommit)
	}
	if got := git(t, root, "show", result.State.WorktreeTree+":new.txt"); got != "new" {
		t.Fatalf("checkpoint new.txt = %q, want new", got)
	}
}

func git(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(bytesTrimSpace(output))
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
