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
	projectMarker = "schema_version = 1\n"
)

type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

var codexHookEvents = []string{
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

var claudeHookEvents = append(append([]string(nil), codexHookEvents...), "PostToolUseFailure")

type providerConfig struct {
	provider     Provider
	directory    string
	filename     string
	command      string
	events       []string
	description  string
	inlineConfig bool
}

var providerConfigs = map[Provider]providerConfig{
	ProviderCodex: {
		provider: ProviderCodex, directory: ".codex", filename: "hooks.json",
		command: "whydiff internal ingest codex", events: codexHookEvents,
		description: "WhyDiff provenance capture hooks.", inlineConfig: true,
	},
	ProviderClaude: {
		provider: ProviderClaude, directory: ".claude", filename: "settings.json",
		command: "whydiff internal ingest claude", events: claudeHookEvents,
	},
}

type HookResult struct {
	Provider Provider
	Path     string
	Changed  bool
	Removed  bool
}

type Result struct {
	RepositoryRoot string
	HooksPath      string
	MarkerCreated  bool
	HooksChanged   bool
	Hooks          []HookResult
}

type DisableResult struct {
	RepositoryRoot string
	HooksPath      string
	HooksChanged   bool
	HooksRemoved   bool
	MarkerRemoved  bool
	Hooks          []HookResult
}

// Inspection is a read-only view of the repository capture configuration.
type Inspection struct {
	RepositoryRoot   string
	MarkerPath       string
	HooksPath        string
	MarkerValid      bool
	HooksValid       bool
	ClaudeHooksPath  string
	ClaudeHooksValid bool
}

// Inspect checks the configuration written by Run without modifying it.
func Inspect(ctx context.Context, cwd string) (Inspection, error) {
	location, err := repository.Locate(ctx, cwd)
	if err != nil {
		return Inspection{}, err
	}
	inspection := Inspection{
		RepositoryRoot:  location.WorktreeRoot,
		MarkerPath:      filepath.Join(location.WorktreeRoot, ".whydiff.toml"),
		HooksPath:       filepath.Join(location.WorktreeRoot, ".codex", "hooks.json"),
		ClaudeHooksPath: filepath.Join(location.WorktreeRoot, ".claude", "settings.json"),
	}

	marker, err := os.ReadFile(inspection.MarkerPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return inspection, fmt.Errorf("read WhyDiff project marker: %w", err)
		}
	} else {
		inspection.MarkerValid = hasSupportedSchemaVersion(marker)
	}

	codexConfig := providerConfigs[ProviderCodex]
	if err := validateConfigurationDirectory(filepath.Dir(inspection.HooksPath), codexConfig); err != nil {
		return inspection, err
	}
	inlineConfigPath := filepath.Join(filepath.Dir(inspection.HooksPath), "config.toml")
	if hasInlineHooks, err := containsInlineHooks(inlineConfigPath); err != nil {
		return inspection, err
	} else if hasInlineHooks {
		return inspection, fmt.Errorf("%s defines inline Codex hooks instead of the WhyDiff hooks file", inlineConfigPath)
	}
	_, _, changed, err := prepareHooksFile(inspection.HooksPath, codexConfig)
	if err != nil {
		return inspection, err
	}
	inspection.HooksValid = !changed

	claudeConfig := providerConfigs[ProviderClaude]
	if err := validateConfigurationDirectory(filepath.Dir(inspection.ClaudeHooksPath), claudeConfig); err != nil {
		return inspection, err
	}
	_, _, changed, err = prepareHooksFile(inspection.ClaudeHooksPath, claudeConfig)
	if err != nil {
		return inspection, err
	}
	inspection.ClaudeHooksValid = !changed
	return inspection, nil
}

func hasSupportedSchemaVersion(contents []byte) bool {
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return strings.Join(strings.Fields(line), " ") == "schema_version = 1"
	}
	return false
}

func Run(ctx context.Context, cwd string) (Result, error) {
	return RunProviders(ctx, cwd, []Provider{ProviderCodex})
}

// RunProviders initializes one or more agent integrations without replacing
// unrelated project settings. Run remains the backward-compatible Codex setup.
func RunProviders(ctx context.Context, cwd string, providers []Provider) (Result, error) {
	location, err := repository.Locate(ctx, cwd)
	if err != nil {
		return Result{}, err
	}

	result := Result{RepositoryRoot: location.WorktreeRoot}
	markerPath := filepath.Join(location.WorktreeRoot, ".whydiff.toml")
	markerMissing, markerMode, err := missingRegularFile(markerPath, 0o644)
	if err != nil {
		return Result{}, err
	}

	type pendingHook struct {
		config  providerConfig
		path    string
		encoded []byte
		mode    os.FileMode
		changed bool
	}
	pending := make([]pendingHook, 0, len(providers))
	seen := make(map[Provider]bool)
	for _, provider := range providers {
		config, ok := providerConfigs[provider]
		if !ok {
			return Result{}, fmt.Errorf("unsupported capture provider %q", provider)
		}
		if seen[provider] {
			continue
		}
		seen[provider] = true
		directory := filepath.Join(location.WorktreeRoot, config.directory)
		path := filepath.Join(directory, config.filename)
		if err := validateConfigurationDirectory(directory, config); err != nil {
			return Result{}, err
		}
		if config.inlineConfig {
			inlineConfigPath := filepath.Join(directory, "config.toml")
			if hasInlineHooks, inlineErr := containsInlineHooks(inlineConfigPath); inlineErr != nil {
				return Result{}, inlineErr
			} else if hasInlineHooks {
				return Result{}, fmt.Errorf("%s already defines inline Codex hooks; refusing to create a second hook source in the same config layer", inlineConfigPath)
			}
		}
		merged, mode, changed, prepareErr := prepareHooksFile(path, config)
		if prepareErr != nil {
			return Result{}, prepareErr
		}
		pending = append(pending, pendingHook{config: config, path: path, encoded: merged, mode: mode, changed: changed})
	}

	for _, hook := range pending {
		entry := HookResult{Provider: hook.config.provider, Path: hook.path, Changed: hook.changed}
		if result.HooksPath == "" {
			result.HooksPath = hook.path
		}
		if hook.changed {
			if err := os.MkdirAll(filepath.Dir(hook.path), 0o755); err != nil {
				return Result{}, fmt.Errorf("create %s directory: %w", hook.config.directory, err)
			}
			if err := writeFileAtomic(hook.path, hook.encoded, hook.mode); err != nil {
				return Result{}, err
			}
			result.HooksChanged = true
		}
		result.Hooks = append(result.Hooks, entry)
	}
	if markerMissing {
		if err := writeFileAtomic(markerPath, []byte(projectMarker), markerMode); err != nil {
			return Result{}, err
		}
		result.MarkerCreated = true
	}

	return result, nil
}

// Disable removes WhyDiff's repository hook handlers and project marker. It
// deliberately retains captured provenance and unrelated provider settings.
func Disable(ctx context.Context, cwd string) (DisableResult, error) {
	location, err := repository.Locate(ctx, cwd)
	if err != nil {
		return DisableResult{}, err
	}
	result := DisableResult{RepositoryRoot: location.WorktreeRoot}
	markerPath := filepath.Join(location.WorktreeRoot, ".whydiff.toml")
	markerPresent := false
	markerInfo, markerErr := os.Lstat(markerPath)
	if markerErr == nil {
		if !markerInfo.Mode().IsRegular() {
			return DisableResult{}, fmt.Errorf("WhyDiff project marker is not a regular file: %s", markerPath)
		}
		markerPresent = true
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return DisableResult{}, fmt.Errorf("inspect WhyDiff project marker: %w", markerErr)
	}

	for _, provider := range []Provider{ProviderCodex, ProviderClaude} {
		config := providerConfigs[provider]
		path := filepath.Join(location.WorktreeRoot, config.directory, config.filename)
		if result.HooksPath == "" {
			result.HooksPath = path
		}
		encoded, mode, changed, removeFile, prepareErr := prepareDisabledHooksFile(path, config)
		if prepareErr != nil {
			return DisableResult{}, prepareErr
		}
		entry := HookResult{Provider: provider, Path: path, Changed: changed, Removed: removeFile}
		if changed {
			if removeFile {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return DisableResult{}, fmt.Errorf("remove WhyDiff-only hooks file: %w", err)
				}
				result.HooksRemoved = true
			} else if err := writeFileAtomic(path, encoded, mode); err != nil {
				return DisableResult{}, err
			}
			result.HooksChanged = true
		}
		result.Hooks = append(result.Hooks, entry)
	}

	if markerPresent {
		if err := os.Remove(markerPath); err != nil {
			return DisableResult{}, fmt.Errorf("remove WhyDiff project marker: %w", err)
		}
		result.MarkerRemoved = true
	}
	return result, nil
}

func prepareDisabledHooksFile(path string, config providerConfig) ([]byte, os.FileMode, bool, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, false, nil
	}
	if err != nil {
		return nil, 0, false, false, fmt.Errorf("inspect existing %s hooks: %w", config.provider, err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, false, fmt.Errorf("existing %s hooks path is not a regular file: %s", config.provider, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, false, fmt.Errorf("read existing %s hooks: %w", config.provider, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		if err == nil {
			err = errors.New("value is null")
		}
		return nil, 0, false, false, fmt.Errorf("parse existing %s hooks without modifying it: %w", config.provider, err)
	}
	hooks := map[string]json.RawMessage{}
	if hooksRaw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &hooks); err != nil || hooks == nil {
			if err == nil {
				err = errors.New("value is null")
			}
			return nil, 0, false, false, fmt.Errorf("existing hooks field must be a JSON object: %w", err)
		}
	}

	changed := false
	for eventName, groupsRaw := range hooks {
		groups, err := decodeGroups(groupsRaw, eventName)
		if err != nil {
			return nil, 0, false, false, err
		}
		kept := make([]json.RawMessage, 0, len(groups))
		for _, groupRaw := range groups {
			group, groupChanged, drop, err := removeWhyDiffFromGroup(groupRaw, eventName, config.command)
			if err != nil {
				return nil, 0, false, false, err
			}
			changed = changed || groupChanged
			if !drop {
				kept = append(kept, group)
			}
		}
		if len(kept) == 0 {
			delete(hooks, eventName)
		} else {
			hooks[eventName] = mustJSON(kept)
		}
	}
	if !changed {
		return nil, info.Mode().Perm(), false, false, nil
	}
	top["hooks"] = mustJSON(hooks)
	removeFile := generatedHooksFileIsEmpty(top, hooks, config)
	if removeFile {
		return nil, info.Mode().Perm(), true, true, nil
	}
	encoded, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, 0, false, false, fmt.Errorf("encode hooks without WhyDiff handlers: %w", err)
	}
	return append(encoded, '\n'), info.Mode().Perm(), true, false, nil
}

func removeWhyDiffFromGroup(raw json.RawMessage, eventName, hookCommand string) (json.RawMessage, bool, bool, error) {
	var group map[string]json.RawMessage
	if err := json.Unmarshal(raw, &group); err != nil || group == nil {
		if err == nil {
			err = errors.New("value is null")
		}
		return nil, false, false, fmt.Errorf("existing %s hook group must be a JSON object: %w", eventName, err)
	}
	var handlers []json.RawMessage
	if err := json.Unmarshal(group["hooks"], &handlers); err != nil {
		return nil, false, false, fmt.Errorf("existing %s handlers must be a JSON array: %w", eventName, err)
	}
	kept := make([]json.RawMessage, 0, len(handlers))
	changed := false
	for _, handlerRaw := range handlers {
		var handler map[string]json.RawMessage
		if err := json.Unmarshal(handlerRaw, &handler); err != nil || handler == nil {
			if err == nil {
				err = errors.New("value is null")
			}
			return nil, false, false, fmt.Errorf("existing %s handler must be a JSON object: %w", eventName, err)
		}
		var command string
		if commandRaw, ok := handler["command"]; ok {
			if err := json.Unmarshal(commandRaw, &command); err != nil {
				return nil, false, false, fmt.Errorf("existing %s hook command must be a string: %w", eventName, err)
			}
		}
		if command == hookCommand {
			changed = true
			continue
		}
		kept = append(kept, handlerRaw)
	}
	if !changed {
		return raw, false, false, nil
	}
	if len(kept) == 0 && len(group) == 1 {
		return nil, true, true, nil
	}
	group["hooks"] = mustJSON(kept)
	return mustJSON(group), true, false, nil
}

func generatedHooksFileIsEmpty(top map[string]json.RawMessage, hooks map[string]json.RawMessage, config providerConfig) bool {
	if len(hooks) != 0 {
		return false
	}
	if config.description == "" {
		return len(top) == 1
	}
	if len(top) != 2 {
		return false
	}
	var description string
	if err := json.Unmarshal(top["description"], &description); err != nil {
		return false
	}
	return description == config.description
}

func prepareHooksFile(path string, config providerConfig) ([]byte, os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		top := map[string]json.RawMessage{"hooks": mustJSON(map[string]json.RawMessage{})}
		if config.description != "" {
			top["description"] = mustJSON(config.description)
		}
		merged, changed, mergeErr := mergeHooks(top, config)
		return merged, 0o644, changed, mergeErr
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("inspect existing %s hooks: %w", config.provider, err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("existing %s hooks path is not a regular file: %s", config.provider, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read existing %s hooks: %w", config.provider, err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, 0, false, fmt.Errorf("parse existing %s hooks without modifying it: %w", config.provider, err)
	}
	if top == nil {
		return nil, 0, false, fmt.Errorf("existing %s hooks must contain a JSON object", config.provider)
	}
	merged, changed, err := mergeHooks(top, config)
	return merged, info.Mode().Perm(), changed, err
}

func validateConfigurationDirectory(path string, config providerConfig) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s directory: %w", config.directory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s path is not a regular directory: %s", config.directory, path)
	}
	return nil
}

func mergeHooks(top map[string]json.RawMessage, config providerConfig) ([]byte, bool, error) {
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
	for _, eventName := range config.events {
		groups, err := decodeGroups(hooks[eventName], eventName)
		if err != nil {
			return nil, false, err
		}
		found := false
		for _, group := range groups {
			hasHandler, err := groupContainsWhyDiff(group, eventName, config.command)
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
				"command": config.command,
				"timeout": hookTimeout(eventName),
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
		return nil, false, fmt.Errorf("encode merged %s hooks: %w", config.provider, err)
	}
	encoded = append(encoded, '\n')
	return encoded, true, nil
}

func hookTimeout(eventName string) int {
	if eventName == "SessionEnd" {
		return 5
	}
	return 2
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

func groupContainsWhyDiff(raw json.RawMessage, eventName, hookCommand string) (bool, error) {
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
