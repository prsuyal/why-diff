// Package query derives user-facing views from captured evidence.
package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/provenance"
	"github.com/prsuyal/why-diff/internal/repository"
	"github.com/prsuyal/why-diff/internal/store"
)

var hunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)

type Service struct {
	location repository.Location
	store    *store.Store
}

type SessionSummary struct {
	ID          string
	StartedAt   time.Time
	LastEventAt time.Time
	EventCount  int
	Ended       bool
	Prompt      string
}

type Attribution struct {
	SessionID        string
	Target           string
	Line             int
	Prompt           string
	Tool             string
	ToolSummary      string
	StartedEventID   string
	CompletedEventID string
	BeforeTree       string
	AfterTree        string
	Patch            string
}

type ToolChange struct {
	SessionID        string
	Prompt           string
	Tool             string
	ToolSummary      string
	StartedEventID   string
	CompletedEventID string
	BeforeTree       string
	AfterTree        string
	Files            []string
	Patch            string
}

func New(ctx context.Context, cwd string) (*Service, error) {
	location, err := repository.Locate(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return &Service{
		location: location,
		store:    store.New(repository.DataRoot(location)),
	}, nil
}

func (s *Service) Sessions(ctx context.Context) ([]store.Session, error) {
	return s.allSessions(ctx)
}

func (s *Service) Summaries(ctx context.Context) ([]SessionSummary, error) {
	sessions, err := s.allSessions(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		summary := SessionSummary{ID: session.ID, EventCount: len(session.Events)}
		if len(session.Events) > 0 {
			summary.StartedAt = session.Events[0].ObservedAt
			summary.LastEventAt = session.Events[len(session.Events)-1].ObservedAt
		}
		for _, captured := range session.Events {
			switch captured.Kind {
			case event.KindPromptSubmitted:
				if summary.Prompt == "" {
					summary.Prompt = promptText(captured)
				}
			case event.KindSessionEnded:
				summary.Ended = true
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (s *Service) Session(ctx context.Context, selector string) (store.Session, error) {
	sessions, err := s.allSessions(ctx)
	if err != nil {
		return store.Session{}, err
	}
	return resolveSession(sessions, selector)
}

func (s *Service) Finalize(ctx context.Context, selector string) (provenance.Archive, error) {
	session, err := s.Session(ctx, selector)
	if err != nil {
		return provenance.Archive{}, err
	}
	return provenance.Finalize(ctx, s.location, session)
}

func (s *Service) Why(ctx context.Context, target, sessionSelector string) (Attribution, error) {
	path, line, err := parseTarget(target)
	if err != nil {
		return Attribution{}, err
	}
	sessions, err := s.allSessions(ctx)
	if err != nil {
		return Attribution{}, err
	}
	if sessionSelector != "" {
		selected, err := resolveSession(sessions, sessionSelector)
		if err != nil {
			return Attribution{}, err
		}
		sessions = []store.Session{selected}
	}

	for _, session := range sessions {
		matches, err := s.attributionsForSession(ctx, session, path, line)
		if err != nil {
			return Attribution{}, err
		}
		if len(matches) > 0 {
			return matches[len(matches)-1], nil
		}
	}
	if line > 0 {
		return Attribution{}, fmt.Errorf("no captured tool checkpoint changed %s at post-change line %d", path, line)
	}
	return Attribution{}, fmt.Errorf("no captured tool checkpoint changed %s", path)
}

func (s *Service) allSessions(ctx context.Context) ([]store.Session, error) {
	live, err := s.store.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	archived, err := provenance.Sessions(ctx, s.location)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]store.Session, len(live)+len(archived))
	for _, session := range archived {
		byID[session.ID] = session
	}
	for _, session := range live {
		// The live projection may contain events captured after a manual
		// finalize, so prefer it while it exists.
		if previous, ok := byID[session.ID]; !ok || len(session.Events) >= len(previous.Events) {
			byID[session.ID] = session
		}
	}
	sessions := make([]store.Session, 0, len(byID))
	for _, session := range byID {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		left, right := sessions[i].StartedAt(), sessions[j].StartedAt()
		if left.Equal(right) {
			return sessions[i].ID < sessions[j].ID
		}
		return left.After(right)
	})
	return sessions, nil
}

func (s *Service) Changes(ctx context.Context, sessionSelector string) (store.Session, []ToolChange, error) {
	session, err := s.Session(ctx, sessionSelector)
	if err != nil {
		return store.Session{}, nil, err
	}
	started := map[string]event.Event{}
	var changes []ToolChange
	for _, captured := range session.Events {
		switch captured.Kind {
		case event.KindToolStarted:
			if captured.Context.ToolCallID != "" && captured.Checkpoint != nil {
				started[captured.Context.ToolCallID] = captured
			}
		case event.KindToolCompleted:
			before, ok := started[captured.Context.ToolCallID]
			if !ok || before.Checkpoint == nil || captured.Checkpoint == nil {
				continue
			}
			files, patch, err := s.diffTrees(ctx, before.Checkpoint.WorktreeTree, captured.Checkpoint.WorktreeTree)
			if err != nil {
				return store.Session{}, nil, err
			}
			if len(files) == 0 {
				continue
			}
			tool, summary := toolDescription(before)
			changes = append(changes, ToolChange{
				SessionID:        session.ID,
				Prompt:           precedingPrompt(session.Events, before),
				Tool:             tool,
				ToolSummary:      summary,
				StartedEventID:   before.EventID,
				CompletedEventID: captured.EventID,
				BeforeTree:       before.Checkpoint.WorktreeTree,
				AfterTree:        captured.Checkpoint.WorktreeTree,
				Files:            files,
				Patch:            patch,
			})
		}
	}
	return session, changes, nil
}

func (s *Service) attributionsForSession(ctx context.Context, session store.Session, path string, line int) ([]Attribution, error) {
	started := map[string]event.Event{}
	var matches []Attribution
	for _, captured := range session.Events {
		switch captured.Kind {
		case event.KindToolStarted:
			if captured.Context.ToolCallID != "" && captured.Checkpoint != nil {
				started[captured.Context.ToolCallID] = captured
			}
		case event.KindToolCompleted:
			if captured.Context.ToolCallID == "" || captured.Checkpoint == nil {
				continue
			}
			before, ok := started[captured.Context.ToolCallID]
			if !ok || before.Checkpoint == nil {
				continue
			}
			patch, err := s.diffPath(ctx, before.Checkpoint.WorktreeTree, captured.Checkpoint.WorktreeTree, path)
			if err != nil {
				return nil, err
			}
			if patch == "" || (line > 0 && !patchTouchesLine(patch, line)) {
				continue
			}
			tool, summary := toolDescription(before)
			matches = append(matches, Attribution{
				SessionID:        session.ID,
				Target:           path,
				Line:             line,
				Prompt:           precedingPrompt(session.Events, before),
				Tool:             tool,
				ToolSummary:      summary,
				StartedEventID:   before.EventID,
				CompletedEventID: captured.EventID,
				BeforeTree:       before.Checkpoint.WorktreeTree,
				AfterTree:        captured.Checkpoint.WorktreeTree,
				Patch:            patch,
			})
		}
	}
	return matches, nil
}

func (s *Service) diffPath(ctx context.Context, before, after, path string) (string, error) {
	command := exec.CommandContext(ctx, "git", "-C", s.location.WorktreeRoot,
		"diff", "--no-ext-diff", "--unified=3", before, after, "--", path)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("diff checkpoint trees for %s: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (s *Service) diffTrees(ctx context.Context, before, after string) ([]string, string, error) {
	nameCommand := exec.CommandContext(ctx, "git", "-C", s.location.WorktreeRoot,
		"diff", "--no-ext-diff", "--name-only", "-z", before, after)
	nameOutput, err := nameCommand.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("list checkpoint changes: %w: %s", err, strings.TrimSpace(string(nameOutput)))
	}
	var files []string
	for _, name := range strings.Split(string(nameOutput), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	if len(files) == 0 {
		return nil, "", nil
	}
	patchCommand := exec.CommandContext(ctx, "git", "-C", s.location.WorktreeRoot,
		"diff", "--no-ext-diff", "--unified=3", before, after)
	patchOutput, err := patchCommand.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("diff checkpoint trees: %w: %s", err, strings.TrimSpace(string(patchOutput)))
	}
	return files, strings.TrimSpace(string(patchOutput)), nil
}

func resolveSession(sessions []store.Session, selector string) (store.Session, error) {
	if len(sessions) == 0 {
		return store.Session{}, errors.New("no captured WhyDiff sessions")
	}
	if selector == "" || selector == "latest" {
		return sessions[0], nil
	}
	for _, session := range sessions {
		if session.ID == selector {
			return session, nil
		}
	}
	var matches []store.Session
	for _, session := range sessions {
		if strings.HasPrefix(session.ID, selector) {
			matches = append(matches, session)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return store.Session{}, fmt.Errorf("session prefix %q is ambiguous", selector)
	}
	return store.Session{}, fmt.Errorf("session %q was not found", selector)
}

func parseTarget(target string) (string, int, error) {
	path := target
	line := 0
	if colon := strings.LastIndex(target, ":"); colon >= 0 {
		if parsed, err := strconv.Atoi(target[colon+1:]); err == nil {
			path = target[:colon]
			line = parsed
			if line <= 0 {
				return "", 0, errors.New("target line must be positive")
			}
		}
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == "" || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
		return "", 0, fmt.Errorf("target must be a repository-relative file path: %q", target)
	}
	return path, line, nil
}

func patchTouchesLine(patch string, target int) bool {
	for _, line := range strings.Split(patch, "\n") {
		match := hunkHeader.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		count := 1
		if match[2] != "" {
			count, _ = strconv.Atoi(match[2])
		}
		if count > 0 && target >= start && target < start+count {
			return true
		}
	}
	return false
}

func precedingPrompt(events []event.Event, tool event.Event) string {
	var sameTurn, latest string
	for _, captured := range events {
		if captured.Sequence >= tool.Sequence {
			break
		}
		if captured.Kind != event.KindPromptSubmitted {
			continue
		}
		text := promptText(captured)
		if text == "" {
			continue
		}
		latest = text
		if tool.Context.TurnID != "" && captured.Context.TurnID == tool.Context.TurnID {
			sameTurn = text
		}
	}
	if sameTurn != "" {
		return sameTurn
	}
	return latest
}

func promptText(captured event.Event) string {
	var payload event.PromptSubmittedPayload
	if err := json.Unmarshal(captured.Payload, &payload); err != nil {
		return ""
	}
	return payload.Text
}

func toolDescription(captured event.Event) (string, string) {
	var payload event.ToolStartedPayload
	if err := json.Unmarshal(captured.Payload, &payload); err != nil {
		return "unknown", ""
	}
	var input struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(payload.Input, &input)
	if input.Command != "" {
		return payload.Tool, input.Command
	}
	compact := strings.TrimSpace(string(payload.Input))
	if len(compact) > 160 {
		compact = compact[:160] + "…"
	}
	return payload.Tool, compact
}

// DescribeEvent returns a compact observed-fact description for show output.
func DescribeEvent(captured event.Event) string {
	switch captured.Kind {
	case event.KindPromptSubmitted:
		return "prompt: " + promptText(captured)
	case event.KindToolStarted:
		tool, summary := toolDescription(captured)
		if summary != "" {
			return fmt.Sprintf("tool started: %s — %s", tool, summary)
		}
		return "tool started: " + tool
	case event.KindToolCompleted:
		var payload event.ToolCompletedPayload
		if json.Unmarshal(captured.Payload, &payload) == nil {
			return "tool completed: " + payload.Tool
		}
	}
	return strings.ReplaceAll(string(captured.Kind), "_", " ")
}
