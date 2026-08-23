// Package repository locates the Git storage boundary for a working tree.
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Location struct {
	CommonGitDir string
	WorktreeRoot string
	RepositoryID string
	WorktreeID   string
}

func Locate(ctx context.Context, cwd string) (Location, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Location{}, fmt.Errorf("get working directory: %w", err)
		}
	}

	common, err := gitPath(ctx, cwd, "--git-common-dir")
	if err != nil {
		return Location{}, err
	}
	worktree, err := gitPath(ctx, cwd, "--show-toplevel")
	if err != nil {
		return Location{}, err
	}

	return Location{
		CommonGitDir: common,
		WorktreeRoot: worktree,
		RepositoryID: localID("repo", common),
		WorktreeID:   localID("worktree", worktree),
	}, nil
}

func DataRoot(location Location) string {
	return filepath.Join(location.CommonGitDir, "whydiff")
}

func LocalID(kind, path string) string {
	return localID(kind, canonicalPath(path))
}

func gitPath(ctx context.Context, cwd, argument string) (string, error) {
	command := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", argument)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("locate Git repository with %s: %w", argument, err)
	}
	path := strings.TrimSpace(string(output))
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return canonicalPath(path), nil
}

func canonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
}

func localID(kind, path string) string {
	digest := sha256.Sum256([]byte(path))
	return kind + "_" + hex.EncodeToString(digest[:16])
}
