package repository_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/prsuyal/why-diff/internal/repository"
)

func TestLocateUsesGitCommonDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	location, err := repository.Locate(context.Background(), root)
	if err != nil {
		t.Fatalf("Locate() error = %v", err)
	}
	wantGitDir, err := filepath.EvalSymlinks(filepath.Join(root, ".git"))
	if err != nil {
		t.Fatalf("resolve expected Git directory: %v", err)
	}
	if location.CommonGitDir != wantGitDir {
		t.Fatalf("CommonGitDir = %q, want %q", location.CommonGitDir, wantGitDir)
	}
	if repository.DataRoot(location) != filepath.Join(wantGitDir, "whydiff") {
		t.Fatalf("DataRoot() = %q", repository.DataRoot(location))
	}
	if location.RepositoryID == "" || location.WorktreeID == "" {
		t.Fatalf("local identifiers were not assigned: %+v", location)
	}
}
