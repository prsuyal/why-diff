// Package reason derives replayable claims from captured observations.
package reason

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/prsuyal/why-diff/internal/event"
)

type Outcome string

const (
	OutcomeUnknown Outcome = "unknown"
	OutcomePassed  Outcome = "passed"
	OutcomeFailed  Outcome = "failed"
)

type Category string

const (
	CategoryOther Category = "other"
	CategoryTest  Category = "test"
)

// CommandObservation is an observed command result, not a causal claim.
type CommandObservation struct {
	EventID           string
	Sequence          uint64
	TurnID            string
	ToolCallID        string
	Command           string
	NormalizedCommand string
	Category          Category
	Outcome           Outcome
	OutcomeBasis      string
}

// ChangeEvidence describes a repository mutation bounded by tool checkpoints.
type ChangeEvidence struct {
	StartedSequence   uint64
	CompletedSequence uint64
	StartedEventID    string
	CompletedEventID  string
	Files             []string
}

// ValidationClaim is a deterministic inference from a fail-change-pass
// sequence. It intentionally does not claim exclusive causation.
type ValidationClaim struct {
	ClaimID        string
	RuleID         string
	Command        string
	FailedEventID  string
	PassedEventID  string
	ChangeEventIDs []string
	Files          []string
	FailedBasis    string
	PassedBasis    string
}

const ValidationRuleID = "whydiff.test_fail_change_pass/v1"

var testCommandPattern = regexp.MustCompile(`(?i)(?:^|(?:&&|\|\||;|\|)\s*)(?:env\s+(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*|(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*)?(?:go\s+test|cargo\s+test|pytest|python(?:3)?\s+-m\s+pytest|npm(?:\s+run)?\s+test|pnpm(?:\s+run)?\s+test|yarn(?:\s+run)?\s+test|bun\s+test|npx\s+(?:jest|vitest)|(?:jest|vitest|rspec)|mvn(?:w)?\s+test|\./gradlew\s+test|gradle\s+test|dotnet\s+test|swift\s+test|mix\s+test)(?:\s|$)`)

func Commands(events []event.Event) []CommandObservation {
	observations := make([]CommandObservation, 0)
	for _, captured := range events {
		if captured.Kind != event.KindToolCompleted {
			continue
		}
		var payload event.ToolCompletedPayload
		if json.Unmarshal(captured.Payload, &payload) != nil {
			continue
		}
		command := commandFromInput(payload.Input)
		if command == "" {
			continue
		}
		outcome, basis := responseOutcome(payload.Response)
		category := CategoryOther
		if testCommandPattern.MatchString(command) {
			category = CategoryTest
		}
		observations = append(observations, CommandObservation{
			EventID:           captured.EventID,
			Sequence:          captured.Sequence,
			TurnID:            captured.Context.TurnID,
			ToolCallID:        captured.Context.ToolCallID,
			Command:           command,
			NormalizedCommand: strings.Join(strings.Fields(command), " "),
			Category:          category,
			Outcome:           outcome,
			OutcomeBasis:      basis,
		})
	}
	return observations
}

func ValidationClaims(events []event.Event, changes []ChangeEvidence) []ValidationClaim {
	failed := make(map[string]CommandObservation)
	var claims []ValidationClaim
	for _, observation := range Commands(events) {
		if observation.Category != CategoryTest || observation.Outcome == OutcomeUnknown {
			continue
		}
		switch observation.Outcome {
		case OutcomeFailed:
			failed[observation.NormalizedCommand] = observation
		case OutcomePassed:
			failure, ok := failed[observation.NormalizedCommand]
			if !ok {
				continue
			}
			claim := ValidationClaim{
				RuleID:        ValidationRuleID,
				Command:       observation.Command,
				FailedEventID: failure.EventID,
				PassedEventID: observation.EventID,
				FailedBasis:   failure.OutcomeBasis,
				PassedBasis:   observation.OutcomeBasis,
			}
			files := map[string]struct{}{}
			for _, change := range changes {
				if change.StartedSequence <= failure.Sequence || change.CompletedSequence >= observation.Sequence {
					continue
				}
				claim.ChangeEventIDs = append(claim.ChangeEventIDs, change.StartedEventID, change.CompletedEventID)
				for _, file := range change.Files {
					files[file] = struct{}{}
				}
			}
			if len(claim.ChangeEventIDs) == 0 {
				continue
			}
			for file := range files {
				claim.Files = append(claim.Files, file)
			}
			sort.Strings(claim.Files)
			claim.ClaimID = claimID(claim)
			claims = append(claims, claim)
			delete(failed, observation.NormalizedCommand)
		}
	}
	return claims
}

func claimID(claim ValidationClaim) string {
	hash := sha256.New()
	parts := append([]string{claim.RuleID, claim.FailedEventID, claim.PassedEventID}, claim.ChangeEventIDs...)
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return "clm_" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func commandFromInput(input json.RawMessage) string {
	var value struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value.Command)
}

func responseOutcome(response json.RawMessage) (Outcome, string) {
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil {
		return OutcomeUnknown, ""
	}
	for _, key := range []string{"exit_code", "exitCode"} {
		if raw, ok := value[key]; ok {
			if code, ok := integer(raw); ok {
				if code == 0 {
					return OutcomePassed, "tool_response." + key + "=0"
				}
				return OutcomeFailed, "tool_response." + key + "=" + strconv.FormatInt(code, 10)
			}
		}
	}
	if success, ok := value["success"].(bool); ok {
		if success {
			return OutcomePassed, "tool_response.success=true"
		}
		return OutcomeFailed, "tool_response.success=false"
	}
	if status, ok := value["status"].(string); ok {
		switch strings.ToLower(status) {
		case "success", "succeeded", "passed", "ok":
			return OutcomePassed, "tool_response.status=" + status
		case "failure", "failed", "error":
			return OutcomeFailed, "tool_response.status=" + status
		}
	}
	return OutcomeUnknown, ""
}

func integer(value any) (int64, bool) {
	switch value := value.(type) {
	case json.Number:
		parsed, err := value.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
