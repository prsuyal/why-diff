// Package checkpoint captures repository state without changing the user's
// branch, working tree, or Git index.
package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/repository"
)

type Result struct {
	State    event.Checkpoint
	Warnings []string
}

func Capture(ctx context.Context, location repository.Location) (Result, error) {
	result := Result{}
	result.State.CapturedAt = time.Now().UTC()

	head, err := gitOutput(ctx, location.WorktreeRoot, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err == nil {
		result.State.HeadCommit = head
	} else if !isExitError(err) {
		result.Warnings = append(result.Warnings, "could not inspect HEAD: "+err.Error())
	}

	indexTree, err := captureIndexTree(ctx, location)
	if err != nil {
		result.Warnings = append(result.Warnings, "could not capture index tree: "+err.Error())
	} else {
		result.State.IndexTree = indexTree
	}

	worktreeTree, err := captureWorktreeTree(ctx, location)
	if err != nil {
		return Result{}, fmt.Errorf("capture working tree: %w", err)
	}
	result.State.WorktreeTree = worktreeTree
	return result, nil
}

func captureIndexTree(ctx context.Context, location repository.Location) (string, error) {
	indexPath, err := gitOutput(ctx, location.WorktreeRoot, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	temporary, cleanup, err := temporaryIndex(location.CommonGitDir)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if err := copyIfExists(indexPath, temporary); err != nil {
		return "", err
	}
	environment := gitEnvironment(temporary)
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		if _, err := gitOutput(ctx, location.WorktreeRoot, environment, "read-tree", "--empty"); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect Git index: %w", err)
	}
	return gitOutput(ctx, location.WorktreeRoot, environment, "write-tree")
}

func captureWorktreeTree(ctx context.Context, location repository.Location) (string, error) {
	temporary, cleanup, err := temporaryIndex(location.CommonGitDir)
	if err != nil {
		return "", err
	}
	defer cleanup()

	environment := gitEnvironment(temporary)
	if _, err := gitOutput(ctx, location.WorktreeRoot, environment, "read-tree", "--empty"); err != nil {
		return "", err
	}
	if _, err := gitOutput(ctx, location.WorktreeRoot, environment, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	return gitOutput(ctx, location.WorktreeRoot, environment, "write-tree")
}

func temporaryIndex(commonGitDir string) (string, func(), error) {
	directory := filepath.Join(commonGitDir, "whydiff", "tmp")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", nil, fmt.Errorf("create checkpoint temporary directory: %w", err)
	}
	file, err := os.CreateTemp(directory, "index-*")
	if err != nil {
		return "", nil, fmt.Errorf("allocate temporary Git index: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", nil, fmt.Errorf("close temporary Git index placeholder: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", nil, fmt.Errorf("prepare temporary Git index: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func copyIfExists(source, destination string) error {
	input, err := os.Open(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Git index: %w", err)
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary Git index: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return fmt.Errorf("copy Git index: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close temporary Git index: %w", err)
	}
	return nil
}

func gitEnvironment(indexPath string) []string {
	return append(os.Environ(), "GIT_INDEX_FILE="+indexPath, "GIT_OPTIONAL_LOCKS=0")
}

func gitOutput(ctx context.Context, cwd string, environment []string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, arguments...)...)
	if environment != nil {
		command.Env = environment
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, message)
	}
	return strings.TrimSpace(string(output)), nil
}

func isExitError(err error) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError)
}
