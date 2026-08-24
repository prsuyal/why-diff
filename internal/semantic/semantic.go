// Package semantic generates explicitly labeled model claims from bounded
// WhyDiff evidence packets.
package semantic

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const EvidenceSchemaVersion = 2

type Operation string

const (
	OperationExplainChange   Operation = "explain_change"
	OperationCompareSessions Operation = "compare_sessions"
)

type EvidencePacket struct {
	SchemaVersion int            `json:"schema_version"`
	Operation     Operation      `json:"operation"`
	SessionIDs    []string       `json:"session_ids"`
	Target        string         `json:"target"`
	Truncated     bool           `json:"truncated"`
	Evidence      []EvidenceItem `json:"evidence"`
}

type EvidenceItem struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

type Claim struct {
	Statement     string   `json:"statement"`
	Confidence    string   `json:"confidence"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Qualification string   `json:"qualification"`
}

type Explanation struct {
	Provider   string   `json:"provider"`
	Model      string   `json:"model"`
	ResponseID string   `json:"response_id"`
	Summary    string   `json:"summary"`
	Claims     []Claim  `json:"claims"`
	Unknowns   []string `json:"unknowns"`
}

type Generator interface {
	Explain(context.Context, EvidencePacket) (Explanation, error)
}

func ValidatePacket(packet EvidencePacket) error {
	if packet.SchemaVersion != EvidenceSchemaVersion {
		return fmt.Errorf("unsupported semantic evidence schema %d", packet.SchemaVersion)
	}
	switch packet.Operation {
	case OperationExplainChange, OperationCompareSessions:
	default:
		return fmt.Errorf("unsupported semantic operation %q", packet.Operation)
	}
	if len(packet.SessionIDs) == 0 || packet.Target == "" {
		return errors.New("semantic evidence requires sessions and a target")
	}
	sessions := make(map[string]struct{}, len(packet.SessionIDs))
	for _, sessionID := range packet.SessionIDs {
		if strings.TrimSpace(sessionID) == "" {
			return errors.New("semantic evidence contains an empty session id")
		}
		if _, exists := sessions[sessionID]; exists {
			return fmt.Errorf("duplicate semantic session id %q", sessionID)
		}
		sessions[sessionID] = struct{}{}
	}
	if packet.Operation == OperationExplainChange && len(packet.SessionIDs) != 1 {
		return errors.New("change explanation requires exactly one session")
	}
	if packet.Operation == OperationCompareSessions && len(packet.SessionIDs) != 2 {
		return errors.New("session comparison requires exactly two sessions")
	}
	if len(packet.Evidence) == 0 {
		return errors.New("semantic evidence is empty")
	}
	seen := map[string]struct{}{}
	for _, item := range packet.Evidence {
		if item.ID == "" || item.Kind == "" || strings.TrimSpace(item.Summary) == "" {
			return errors.New("semantic evidence items require id, kind, and summary")
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("duplicate semantic evidence id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
	}
	return nil
}

func validateExplanation(explanation Explanation, packet EvidencePacket) error {
	if strings.TrimSpace(explanation.Summary) == "" {
		return errors.New("semantic response has no summary")
	}
	allowed := make(map[string]struct{}, len(packet.Evidence))
	for _, item := range packet.Evidence {
		allowed[item.ID] = struct{}{}
	}
	for index, claim := range explanation.Claims {
		if strings.TrimSpace(claim.Statement) == "" {
			return fmt.Errorf("semantic claim %d has no statement", index+1)
		}
		switch claim.Confidence {
		case "low", "medium", "high":
		default:
			return fmt.Errorf("semantic claim %d has invalid confidence %q", index+1, claim.Confidence)
		}
		if len(claim.EvidenceIDs) == 0 {
			return fmt.Errorf("semantic claim %d cites no evidence", index+1)
		}
		for _, id := range claim.EvidenceIDs {
			if _, ok := allowed[id]; !ok {
				return fmt.Errorf("semantic claim %d cites unknown evidence %q", index+1, id)
			}
		}
	}
	return nil
}
