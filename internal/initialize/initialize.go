// Package initialize creates WhyDiff's repository-local configuration.
package initialize

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prsuyal/why-diff/internal/repository"
)

const (
	hookCommand   = "whydiff internal ingest codex"
	projectMarker = "schema_version = 1\n"
)

var hookEvents = []string{
	"SessionStart",
	"SessionEnd",
	"PreToolUse",
	"PermissionRequest",
	"PostToolUse",
	"PreCompact",
	"PostCompact",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
}

type Result struct {
	RepositoryRoot string
	HooksPath      string
	MarkerCreated  bool
	HooksChanged   bool
}

func Run(ctx context.Context, cwd string) (Result, error) {
	location, err := repository.Locate(ctx, cwd)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		RepositoryRoot: location.WorktreeRoot,
		HooksPath:      filepath.Join(location.WorktreeRoot, ".codex", "hooks.json"),
	}
	markerPath := filepath.Join(location.WorktreeRoot, ".whydiff.toml")
	codexDirectory := filepath.Join(location.WorktreeRoot, ".codex")
	inlineConfigPath := filepath.Join(codexDirectory, "config.toml")

	if err := validateConfigurationDirectory(codexDirectory); err != nil {
		return Result{}, err
	}

	if hasInlineHooks, err := containsInlineHooks(inlineConfigPath); err != nil {
		return Result{}, err
	} else if hasInlineHooks {
		return Result{}, fmt.Errorf("%s already defines inline Codex hooks; refusing to create a second hook source in the same config layer", inlineConfigPath)
	}

	merged, hooksMode, hooksChanged, err := prepareHooksFile(result.HooksPath)
	if err != nil {
		return Result{}, err
	}
	markerMissing, markerMode, err := missingRegularFile(markerPath, 0o644)
	if err != nil {
		return Result{}, err
	}

	if hooksChanged {
		if err := os.MkdirAll(codexDirectory, 0o755); err != nil {
			return Result{}, fmt.Errorf("create .codex directory: %w", err)
		}
		if err := writeFileAtomic(result.HooksPath, merged, hooksMode); err != nil {
			return Result{}, err
		}
		result.HooksChanged = true
	}
	if markerMissing {
		if err := writeFileAtomic(markerPath, []byte(projectMarker), markerMode); err != nil {
			return Result{}, err
		}
		result.MarkerCreated = true
	}

	return result, nil
}

func prepareHooksFile(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		top := map[string]json.RawMessage{
			"description": mustJSON("WhyDiff provenance capture hooks."),
			"hooks":       mustJSON(map[string]json.RawMessage{}),
		}
		merged, changed, mergeErr := mergeHooks(top)
		return merged, 0o644, changed, mergeErr
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("inspect existing Codex hooks: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("existing Codex hooks path is not a regular file: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read existing Codex hooks: %w", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, 0, false, fmt.Errorf("parse existing Codex hooks without modifying it: %w", err)
	}
	if top == nil {
		return nil, 0, false, errors.New("existing Codex hooks must contain a JSON object")
	}
	merged, changed, err := mergeHooks(top)
	return merged, info.Mode().Perm(), changed, err
}

func validateConfigurationDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect .codex directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf(".codex path is not a regular directory: %s", path)
	}
	return nil
}

func mergeHooks(top map[string]json.RawMessage) ([]byte, bool, error) {
	hooks := map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			if err == nil {
				err = errors.New("value is null")
			}
			return nil, false, fmt.Errorf("existing hooks field must be a JSON object: %w", err)
		}
	}

	changed := false
	for _, eventName := range hookEvents {
		groups, err := decodeGroups(hooks[eventName], eventName)
		if err != nil {
			return nil, false, err
		}
		found := false
		for _, group := range groups {
			hasHandler, err := groupContainsWhyDiff(group, eventName)
			if err != nil {
				return nil, false, err
			}
			if hasHandler {
				found = true
			}
		}
		if found {
			continue
		}
		groups = append(groups, mustJSON(map[string]any{
			"hooks": []map[string]any{{
				"type":    "command",
				"command": hookCommand,
				"timeout": 2,
			}},
		}))
		hooks[eventName] = mustJSON(groups)
		changed = true
	}

	if !changed {
		return nil, false, nil
	}
	top["hooks"] = mustJSON(hooks)
	encoded, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode merged Codex hooks: %w", err)
	}
	encoded = append(encoded, '\n')
	return encoded, true, nil
}

func decodeGroups(raw json.RawMessage, eventName string) ([]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("existing %s hook groups must be a JSON array: %w", eventName, err)
	}
	return groups, nil
}

func groupContainsWhyDiff(raw json.RawMessage, eventName string) (bool, error) {
	var group map[string]json.RawMessage
	if err := json.Unmarshal(raw, &group); err != nil || group == nil {
		if err == nil {
			err = errors.New("value is null")
		}
		return false, fmt.Errorf("existing %s hook group must be a JSON object: %w", eventName, err)
	}
	handlersRaw, ok := group["hooks"]
	if !ok {
		return false, fmt.Errorf("existing %s hook group has no hooks array", eventName)
	}
	var handlers []map[string]json.RawMessage
	if err := json.Unmarshal(handlersRaw, &handlers); err != nil {
		return false, fmt.Errorf("existing %s handlers must be a JSON array of objects: %w", eventName, err)
	}
	for _, handler := range handlers {
		var command string
		if rawCommand, ok := handler["command"]; ok {
			if err := json.Unmarshal(rawCommand, &command); err != nil {
				return false, fmt.Errorf("existing %s hook command must be a string: %w", eventName, err)
			}
		}
		if command == hookCommand {
			return true, nil
		}
	}
	return false, nil
}

func containsInlineHooks(path string) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Codex config: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[hooks]") || strings.HasPrefix(line, "[[hooks.") {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("scan Codex config: %w", err)
	}
	return false, nil
}

func missingRegularFile(path string, defaultMode os.FileMode) (bool, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, defaultMode, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, 0, fmt.Errorf("path is not a regular file: %s", path)
	}
	return false, info.Mode().Perm(), nil
}

func writeFileAtomic(path string, contents []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".whydiff-init-*")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set configuration permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish configuration: %w", err)
	}
	return nil
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
