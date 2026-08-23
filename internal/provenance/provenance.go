// Package provenance finalizes captured sessions into private Git refs.
package provenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/repository"
	"github.com/prsuyal/why-diff/internal/store"
)

const schemaVersion = 1

type Archive struct {
	Ref    string
	Commit string
	Tree   string
}

type metadata struct {
	SchemaVersion int       `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	RepositoryID  string    `json:"repository_id,omitempty"`
	WorktreeID    string    `json:"worktree_id,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	EventCount    int       `json:"event_count"`
}

func Finalize(ctx context.Context, location repository.Location, session store.Session) (Archive, error) {
	if session.ID == "" || len(session.Events) == 0 {
		return Archive{}, errors.New("cannot finalize an empty session")
	}
	events := append([]event.Event(nil), session.Events...)
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })

	eventsBlob, err := writeEventsBlob(ctx, location, events)
	if err != nil {
		return Archive{}, err
	}
	metadataBlob, err := writeMetadataBlob(ctx, location, session.ID, events)
	if err != nil {
		return Archive{}, err
	}
	checkpointsTree, err := writeCheckpointsTree(ctx, location, events)
	if err != nil {
		return Archive{}, err
	}

	rootEntries := []treeEntry{
		{Mode: "100644", Type: "blob", Object: eventsBlob, Name: "events.jsonl"},
		{Mode: "100644", Type: "blob", Object: metadataBlob, Name: "metadata.json"},
	}
	if checkpointsTree != "" {
		rootEntries = append(rootEntries, treeEntry{Mode: "040000", Type: "tree", Object: checkpointsTree, Name: "checkpoints"})
	}
	rootTree, err := writeTree(ctx, location, rootEntries)
	if err != nil {
		return Archive{}, err
	}

	ref := sessionRef(session.ID)
	previous, _ := gitOutput(ctx, location, nil, nil, "rev-parse", "--verify", ref+"^{commit}")
	if previous != "" {
		previousTree, err := gitOutput(ctx, location, nil, nil, "show", "-s", "--format=%T", previous)
		if err == nil && previousTree == rootTree {
			return Archive{Ref: ref, Commit: previous, Tree: rootTree}, nil
		}
	}

	endedAt := events[len(events)-1].ObservedAt.UTC()
	arguments := []string{"commit-tree", rootTree}
	if previous != "" {
		arguments = append(arguments, "-p", previous)
	}
	environment := append(os.Environ(),
		"GIT_AUTHOR_NAME=WhyDiff",
		"GIT_AUTHOR_EMAIL=whydiff@localhost",
		"GIT_COMMITTER_NAME=WhyDiff",
		"GIT_COMMITTER_EMAIL=whydiff@localhost",
		"GIT_AUTHOR_DATE="+endedAt.Format(time.RFC3339),
		"GIT_COMMITTER_DATE="+endedAt.Format(time.RFC3339),
	)
	commit, err := gitOutput(ctx, location, environment, []byte("WhyDiff session "+session.ID+"\n"), arguments...)
	if err != nil {
		return Archive{}, err
	}
	updateArguments := []string{"update-ref", ref, commit}
	if previous != "" {
		updateArguments = append(updateArguments, previous)
	}
	if _, err := gitOutput(ctx, location, nil, nil, updateArguments...); err != nil {
		return Archive{}, err
	}
	return Archive{Ref: ref, Commit: commit, Tree: rootTree}, nil
}

// Sessions reads the latest canonical event stream from every WhyDiff session
// ref. These refs are authoritative after finalization; live JSONL files are a
// writable projection that may be deleted and rebuilt.
func Sessions(ctx context.Context, location repository.Location) ([]store.Session, error) {
	output, err := gitOutput(ctx, location, nil, nil,
		"for-each-ref", "--format=%(refname)", "refs/whydiff/sessions/")
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, nil
	}

	var sessions []store.Session
	for _, ref := range strings.Split(output, "\n") {
		encoded, err := gitBytes(ctx, location, "show", ref+":events.jsonl")
		if err != nil {
			return nil, fmt.Errorf("read canonical events from %s: %w", ref, err)
		}
		session, err := store.DecodeSession(bytes.NewReader(encoded))
		if err != nil {
			return nil, fmt.Errorf("decode canonical events from %s: %w", ref, err)
		}
		if len(session.Events) > 0 {
			sessions = append(sessions, session)
		}
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

func writeEventsBlob(ctx context.Context, location repository.Location, events []event.Event) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	for _, captured := range events {
		if err := encoder.Encode(captured); err != nil {
			return "", fmt.Errorf("encode canonical event log: %w", err)
		}
	}
	return gitOutput(ctx, location, nil, buffer.Bytes(), "hash-object", "-w", "--stdin")
}

func writeMetadataBlob(ctx context.Context, location repository.Location, sessionID string, events []event.Event) (string, error) {
	data := metadata{
		SchemaVersion: schemaVersion,
		SessionID:     sessionID,
		RepositoryID:  events[0].Context.RepositoryID,
		WorktreeID:    events[0].Context.WorktreeID,
		StartedAt:     events[0].ObservedAt,
		EndedAt:       events[len(events)-1].ObservedAt,
		EventCount:    len(events),
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode session metadata: %w", err)
	}
	encoded = append(encoded, '\n')
	return gitOutput(ctx, location, nil, encoded, "hash-object", "-w", "--stdin")
}

func writeCheckpointsTree(ctx context.Context, location repository.Location, events []event.Event) (string, error) {
	var entries []treeEntry
	for _, captured := range events {
		if captured.Checkpoint == nil {
			continue
		}
		checkpointEntries := []treeEntry{
			{Mode: "040000", Type: "tree", Object: captured.Checkpoint.WorktreeTree, Name: "worktree"},
		}
		if captured.Checkpoint.IndexTree != "" {
			checkpointEntries = append(checkpointEntries, treeEntry{Mode: "040000", Type: "tree", Object: captured.Checkpoint.IndexTree, Name: "index"})
		}
		checkpointTree, err := writeTree(ctx, location, checkpointEntries)
		if err != nil {
			return "", err
		}
		entries = append(entries, treeEntry{Mode: "040000", Type: "tree", Object: checkpointTree, Name: captured.EventID})
	}
	if len(entries) == 0 {
		return "", nil
	}
	return writeTree(ctx, location, entries)
}

type treeEntry struct {
	Mode   string
	Type   string
	Object string
	Name   string
}

func writeTree(ctx context.Context, location repository.Location, entries []treeEntry) (string, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	var input strings.Builder
	for _, entry := range entries {
		if strings.ContainsAny(entry.Name, "\x00\n\t/") {
			return "", fmt.Errorf("invalid provenance tree entry name %q", entry.Name)
		}
		fmt.Fprintf(&input, "%s %s %s\t%s\n", entry.Mode, entry.Type, entry.Object, entry.Name)
	}
	return gitOutput(ctx, location, nil, []byte(input.String()), "mktree")
}

func sessionRef(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return "refs/whydiff/sessions/" + hex.EncodeToString(digest[:])
}

func gitOutput(ctx context.Context, location repository.Location, environment []string, input []byte, arguments ...string) (string, error) {
	output, err := gitBytesWithEnvironment(ctx, location, environment, input, arguments...)
	return strings.TrimSpace(string(output)), err
}

func gitBytes(ctx context.Context, location repository.Location, arguments ...string) ([]byte, error) {
	return gitBytesWithEnvironment(ctx, location, nil, nil, arguments...)
}

func gitBytesWithEnvironment(ctx context.Context, location repository.Location, environment []string, input []byte, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", location.WorktreeRoot}, arguments...)...)
	if environment != nil {
		command.Env = environment
	}
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
