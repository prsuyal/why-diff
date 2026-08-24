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
	"unicode/utf8"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/provenance"
	"github.com/prsuyal/why-diff/internal/reason"
	"github.com/prsuyal/why-diff/internal/repository"
	"github.com/prsuyal/why-diff/internal/semantic"
	"github.com/prsuyal/why-diff/internal/store"
)

var hunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)

const (
	maxSemanticEvidenceBytes = 96 * 1024
	maxSemanticEventBytes    = 4 * 1024
	maxSemanticPatchBytes    = 64 * 1024
	maxComparisonPatchBytes  = 32 * 1024
	maxSemanticTurnEvents    = 40
)

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
	TurnID           string
	Prompt           string
	PromptEventID    string
	Tool             string
	ToolSummary      string
	StartedEventID   string
	CompletedEventID string
	BeforeTree       string
	AfterTree        string
	Patch            string
	Validation       *reason.ValidationClaim
}

type ToolChange struct {
	SessionID         string
	Prompt            string
	Tool              string
	ToolSummary       string
	StartedEventID    string
	CompletedEventID  string
	StartedSequence   uint64
	CompletedSequence uint64
	BeforeTree        string
	AfterTree         string
	Files             []string
	Patch             string
}

type PromptEvidence struct {
	EventID string
	Text    string
}

type ComparisonAttempt struct {
	SessionID   string
	Prompts     []PromptEvidence
	Changes     []ToolChange
	Files       []string
	Validations []reason.CommandObservation
}

type Comparison struct {
	Left                 ComparisonAttempt
	Right                ComparisonAttempt
	SharedFiles          []string
	LeftOnlyFiles        []string
	RightOnlyFiles       []string
	SharedValidations    []string
	LeftOnlyValidations  []string
	RightOnlyValidations []string
}

type semanticEvidenceBuilder struct {
	packet *semantic.EvidencePacket
	seen   map[string]struct{}
	total  int
}

func newSemanticEvidenceBuilder(packet *semantic.EvidencePacket) *semanticEvidenceBuilder {
	return &semanticEvidenceBuilder{packet: packet, seen: make(map[string]struct{})}
}

func (b *semanticEvidenceBuilder) add(id, kind, summary string, maximum int) {
	if id == "" || summary == "" {
		return
	}
	if _, exists := b.seen[id]; exists {
		return
	}
	remaining := maxSemanticEvidenceBytes - b.total
	if remaining <= 0 {
		b.packet.Truncated = true
		return
	}
	limit := maximum
	if limit > remaining {
		limit = remaining
	}
	trimmed, truncated := truncateBytes(strings.TrimSpace(summary), limit)
	if truncated || maximum > remaining {
		b.packet.Truncated = true
	}
	if trimmed == "" {
		return
	}
	b.seen[id] = struct{}{}
	b.packet.Evidence = append(b.packet.Evidence, semantic.EvidenceItem{ID: id, Kind: kind, Summary: trimmed})
	b.total += len(trimmed)
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
			attribution := matches[len(matches)-1]
			claims, err := s.validationClaimsForSession(ctx, session)
			if err != nil {
				return Attribution{}, err
			}
			for index := len(claims) - 1; index >= 0; index-- {
				if claimSupportsAttribution(claims[index], attribution) {
					claim := claims[index]
					attribution.Validation = &claim
					break
				}
			}
			return attribution, nil
		}
	}
	if line > 0 {
		return Attribution{}, fmt.Errorf("no captured tool checkpoint changed %s at post-change line %d", path, line)
	}
	return Attribution{}, fmt.Errorf("no captured tool checkpoint changed %s", path)
}

// SemanticEvidence builds the bounded, inspectable packet that may be sent to
// a semantic provider. It contains only captured evidence relevant to the
// selected attribution, never the full raw session transcript.
func (s *Service) SemanticEvidence(ctx context.Context, target, sessionSelector string) (Attribution, semantic.EvidencePacket, error) {
	attribution, err := s.Why(ctx, target, sessionSelector)
	if err != nil {
		return Attribution{}, semantic.EvidencePacket{}, err
	}
	session, err := s.Session(ctx, attribution.SessionID)
	if err != nil {
		return Attribution{}, semantic.EvidencePacket{}, err
	}
	packet := semantic.EvidencePacket{
		SchemaVersion: semantic.EvidenceSchemaVersion,
		Operation:     semantic.OperationExplainChange,
		SessionIDs:    []string{attribution.SessionID},
		Target:        target,
	}
	builder := newSemanticEvidenceBuilder(&packet)

	if attribution.PromptEventID != "" {
		builder.add(attribution.PromptEventID, "prompt", attribution.Prompt, maxSemanticEventBytes)
	}
	turnEvents := 0
	for _, captured := range session.Events {
		if attribution.TurnID != "" && captured.Context.TurnID != attribution.TurnID {
			continue
		}
		if captured.Kind != event.KindPromptSubmitted && captured.Kind != event.KindToolStarted && captured.Kind != event.KindToolCompleted {
			continue
		}
		if turnEvents >= maxSemanticTurnEvents {
			packet.Truncated = true
			break
		}
		builder.add(captured.EventID, string(captured.Kind), semanticEventSummary(captured), maxSemanticEventBytes)
		turnEvents++
	}
	changeID := "diff:" + attribution.StartedEventID + ":" + attribution.CompletedEventID
	builder.add(changeID, "checkpoint_diff", fmt.Sprintf("Before tree: %s\nAfter tree: %s\n%s", attribution.BeforeTree, attribution.AfterTree, attribution.Patch), maxSemanticPatchBytes)
	if validation := attribution.Validation; validation != nil {
		builder.add(validation.ClaimID, "deterministic_claim", fmt.Sprintf(
			"Rule %s observed `%s` fail at %s, repository changes to %s, then pass at %s. This supports but does not prove that the changes resolved the failure.",
			validation.RuleID, validation.Command, validation.FailedEventID, strings.Join(validation.Files, ", "), validation.PassedEventID,
		), maxSemanticEventBytes)
	}
	if err := semantic.ValidatePacket(packet); err != nil {
		return Attribution{}, semantic.EvidencePacket{}, err
	}
	return attribution, packet, nil
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
	changes, err := s.changesForSession(ctx, session)
	return session, changes, err
}

func (s *Service) Claims(ctx context.Context, sessionSelector string) (store.Session, []reason.ValidationClaim, error) {
	session, err := s.Session(ctx, sessionSelector)
	if err != nil {
		return store.Session{}, nil, err
	}
	claims, err := s.validationClaimsForSession(ctx, session)
	return session, claims, err
}

// Compare returns an evidence-only comparison of two captured sessions. It
// deliberately reports overlap and divergence without assigning intent.
func (s *Service) Compare(ctx context.Context, leftSelector, rightSelector string) (Comparison, error) {
	leftSession, err := s.Session(ctx, leftSelector)
	if err != nil {
		return Comparison{}, fmt.Errorf("resolve left session: %w", err)
	}
	rightSession, err := s.Session(ctx, rightSelector)
	if err != nil {
		return Comparison{}, fmt.Errorf("resolve right session: %w", err)
	}
	if leftSession.ID == rightSession.ID {
		return Comparison{}, errors.New("comparison requires two different sessions")
	}

	left, err := s.comparisonAttempt(ctx, leftSession)
	if err != nil {
		return Comparison{}, fmt.Errorf("summarize left session: %w", err)
	}
	right, err := s.comparisonAttempt(ctx, rightSession)
	if err != nil {
		return Comparison{}, fmt.Errorf("summarize right session: %w", err)
	}

	sharedFiles, leftOnlyFiles, rightOnlyFiles := partitionStrings(left.Files, right.Files)
	sharedValidations, leftOnlyValidations, rightOnlyValidations := partitionStrings(
		validationCommands(left.Validations),
		validationCommands(right.Validations),
	)
	return Comparison{
		Left:                 left,
		Right:                right,
		SharedFiles:          sharedFiles,
		LeftOnlyFiles:        leftOnlyFiles,
		RightOnlyFiles:       rightOnlyFiles,
		SharedValidations:    sharedValidations,
		LeftOnlyValidations:  leftOnlyValidations,
		RightOnlyValidations: rightOnlyValidations,
	}, nil
}

// ComparisonSemanticEvidence builds the bounded packet used for optional
// semantic interpretation of a deterministic session comparison.
func (s *Service) ComparisonSemanticEvidence(ctx context.Context, leftSelector, rightSelector string) (Comparison, semantic.EvidencePacket, error) {
	comparison, err := s.Compare(ctx, leftSelector, rightSelector)
	if err != nil {
		return Comparison{}, semantic.EvidencePacket{}, err
	}
	packet := semantic.EvidencePacket{
		SchemaVersion: semantic.EvidenceSchemaVersion,
		Operation:     semantic.OperationCompareSessions,
		SessionIDs:    []string{comparison.Left.SessionID, comparison.Right.SessionID},
		Target:        comparison.Left.SessionID + " vs " + comparison.Right.SessionID,
	}
	builder := newSemanticEvidenceBuilder(&packet)
	addComparisonAttemptEvidence(builder, "A", comparison.Left)
	addComparisonAttemptEvidence(builder, "B", comparison.Right)
	builder.add("comparison:files", "deterministic_comparison", fmt.Sprintf(
		"Shared changed files: %s\nOnly attempt A: %s\nOnly attempt B: %s",
		comparisonValue(comparison.SharedFiles),
		comparisonValue(comparison.LeftOnlyFiles),
		comparisonValue(comparison.RightOnlyFiles),
	), maxSemanticEventBytes)
	builder.add("comparison:validations", "deterministic_comparison", fmt.Sprintf(
		"Shared validation commands: %s\nOnly attempt A: %s\nOnly attempt B: %s",
		comparisonValue(comparison.SharedValidations),
		comparisonValue(comparison.LeftOnlyValidations),
		comparisonValue(comparison.RightOnlyValidations),
	), maxSemanticEventBytes)
	if err := semantic.ValidatePacket(packet); err != nil {
		return Comparison{}, semantic.EvidencePacket{}, err
	}
	return comparison, packet, nil
}

func addComparisonAttemptEvidence(builder *semanticEvidenceBuilder, label string, attempt ComparisonAttempt) {
	for _, prompt := range attempt.Prompts {
		builder.add(prompt.EventID, "prompt", "Attempt "+label+" prompt: "+prompt.Text, maxSemanticEventBytes)
	}
	for _, change := range attempt.Changes {
		id := "diff:" + change.StartedEventID + ":" + change.CompletedEventID
		builder.add(id, "checkpoint_diff", fmt.Sprintf(
			"Attempt %s checkpointed change.\nFiles: %s\nBefore tree: %s\nAfter tree: %s\n%s",
			label, comparisonValue(change.Files), change.BeforeTree, change.AfterTree, change.Patch,
		), maxComparisonPatchBytes)
	}
	for _, validation := range attempt.Validations {
		builder.add(validation.EventID, "validation", fmt.Sprintf(
			"Attempt %s observed `%s` with outcome %s. Basis: %s",
			label, validation.Command, validation.Outcome, validation.OutcomeBasis,
		), maxSemanticEventBytes)
	}
}

func comparisonValue(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}

func (s *Service) comparisonAttempt(ctx context.Context, session store.Session) (ComparisonAttempt, error) {
	changes, err := s.changesForSession(ctx, session)
	if err != nil {
		return ComparisonAttempt{}, err
	}
	attempt := ComparisonAttempt{SessionID: session.ID, Changes: changes}
	fileSet := make(map[string]struct{})
	for _, change := range changes {
		for _, file := range change.Files {
			fileSet[file] = struct{}{}
		}
	}
	for file := range fileSet {
		attempt.Files = append(attempt.Files, file)
	}
	sort.Strings(attempt.Files)

	for _, captured := range session.Events {
		if captured.Kind != event.KindPromptSubmitted {
			continue
		}
		if text := promptText(captured); text != "" {
			attempt.Prompts = append(attempt.Prompts, PromptEvidence{EventID: captured.EventID, Text: text})
		}
	}
	for _, observation := range reason.Commands(session.Events) {
		if observation.Category == reason.CategoryTest {
			attempt.Validations = append(attempt.Validations, observation)
		}
	}
	return attempt, nil
}

func validationCommands(observations []reason.CommandObservation) []string {
	commands := make([]string, 0, len(observations))
	for _, observation := range observations {
		commands = append(commands, observation.NormalizedCommand)
	}
	return commands
}

func partitionStrings(left, right []string) (shared, leftOnly, rightOnly []string) {
	leftSet := make(map[string]struct{}, len(left))
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range left {
		leftSet[value] = struct{}{}
	}
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	for value := range leftSet {
		if _, ok := rightSet[value]; ok {
			shared = append(shared, value)
		} else {
			leftOnly = append(leftOnly, value)
		}
	}
	for value := range rightSet {
		if _, ok := leftSet[value]; !ok {
			rightOnly = append(rightOnly, value)
		}
	}
	sort.Strings(shared)
	sort.Strings(leftOnly)
	sort.Strings(rightOnly)
	return shared, leftOnly, rightOnly
}

func (s *Service) validationClaimsForSession(ctx context.Context, session store.Session) ([]reason.ValidationClaim, error) {
	changes, err := s.changesForSession(ctx, session)
	if err != nil {
		return nil, err
	}
	evidence := make([]reason.ChangeEvidence, 0, len(changes))
	for _, change := range changes {
		evidence = append(evidence, reason.ChangeEvidence{
			StartedSequence:   change.StartedSequence,
			CompletedSequence: change.CompletedSequence,
			StartedEventID:    change.StartedEventID,
			CompletedEventID:  change.CompletedEventID,
			Files:             change.Files,
		})
	}
	return reason.ValidationClaims(session.Events, evidence), nil
}

func (s *Service) changesForSession(ctx context.Context, session store.Session) ([]ToolChange, error) {
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
				return nil, err
			}
			if len(files) == 0 {
				continue
			}
			tool, summary := toolDescription(before)
			changes = append(changes, ToolChange{
				SessionID:         session.ID,
				Prompt:            precedingPrompt(session.Events, before),
				Tool:              tool,
				ToolSummary:       summary,
				StartedEventID:    before.EventID,
				CompletedEventID:  captured.EventID,
				StartedSequence:   before.Sequence,
				CompletedSequence: captured.Sequence,
				BeforeTree:        before.Checkpoint.WorktreeTree,
				AfterTree:         captured.Checkpoint.WorktreeTree,
				Files:             files,
				Patch:             patch,
			})
		}
	}
	return changes, nil
}

func claimSupportsAttribution(claim reason.ValidationClaim, attribution Attribution) bool {
	fileFound := false
	for _, file := range claim.Files {
		if file == attribution.Target {
			fileFound = true
			break
		}
	}
	if !fileFound {
		return false
	}
	started, completed := false, false
	for _, eventID := range claim.ChangeEventIDs {
		started = started || eventID == attribution.StartedEventID
		completed = completed || eventID == attribution.CompletedEventID
	}
	return started && completed
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
			promptEvent, _ := precedingPromptEvent(session.Events, before)
			matches = append(matches, Attribution{
				SessionID:        session.ID,
				Target:           path,
				Line:             line,
				TurnID:           before.Context.TurnID,
				Prompt:           promptText(promptEvent),
				PromptEventID:    promptEvent.EventID,
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
	prompt, _ := precedingPromptEvent(events, tool)
	return promptText(prompt)
}

func precedingPromptEvent(events []event.Event, tool event.Event) (event.Event, bool) {
	var sameTurn, latest event.Event
	for _, captured := range events {
		if captured.Sequence >= tool.Sequence {
			break
		}
		if captured.Kind != event.KindPromptSubmitted {
			continue
		}
		if promptText(captured) == "" {
			continue
		}
		latest = captured
		if tool.Context.TurnID != "" && captured.Context.TurnID == tool.Context.TurnID {
			sameTurn = captured
		}
	}
	if sameTurn.EventID != "" {
		return sameTurn, true
	}
	return latest, latest.EventID != ""
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

func semanticEventSummary(captured event.Event) string {
	description := DescribeEvent(captured)
	if captured.Kind != event.KindToolCompleted {
		return description
	}
	var payload event.ToolCompletedPayload
	if json.Unmarshal(captured.Payload, &payload) != nil {
		return description
	}
	response := strings.TrimSpace(string(payload.Response))
	if response == "" || response == "null" {
		return description
	}
	return description + "\nStructured response: " + response
}

func truncateBytes(value string, maximum int) (string, bool) {
	if maximum <= 0 {
		return "", value != ""
	}
	if len(value) <= maximum {
		return value, false
	}
	const suffix = "…[TRUNCATED]"
	cut := maximum - len(suffix)
	if cut < 0 {
		cut = 0
	}
	value = value[:cut]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + suffix, true
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
