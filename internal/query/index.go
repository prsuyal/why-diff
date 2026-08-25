package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prsuyal/why-diff/internal/entity"
	"github.com/prsuyal/why-diff/internal/indexdb"
	"github.com/prsuyal/why-diff/internal/repository"
)

func (s *Service) openIndex(ctx context.Context, force bool) (*indexdb.Database, error) {
	fingerprint, err := s.sourceFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	path := indexdb.Path(repository.DataRoot(s.location))
	if !force {
		index, openErr := indexdb.Open(path)
		if openErr == nil {
			indexedFingerprint, fingerprintErr := index.Fingerprint(ctx)
			if fingerprintErr == nil && indexedFingerprint == fingerprint {
				return index, nil
			}
			_ = index.Close()
		} else if !errors.Is(openErr, indexdb.ErrRebuildRequired) {
			// Corruption or an incompatible database is recoverable because the
			// projection contains no canonical data. Rebuild below.
		}
	}
	projection, err := s.buildProjection(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	if err := indexdb.Rebuild(ctx, path, projection); err != nil {
		return nil, err
	}
	return indexdb.Open(path)
}

func (s *Service) buildProjection(ctx context.Context, fingerprint string) (indexdb.Projection, error) {
	sessions, err := s.allSessions(ctx)
	if err != nil {
		return indexdb.Projection{}, fmt.Errorf("read canonical provenance for index rebuild: %w", err)
	}
	projection := indexdb.Projection{Fingerprint: fingerprint, Sessions: sessions}
	for _, session := range sessions {
		changes, err := s.deriveChangesForSession(ctx, session)
		if err != nil {
			return indexdb.Projection{}, fmt.Errorf("derive changes for index rebuild: %w", err)
		}
		for _, change := range changes {
			indexedChange := toIndexedChange(change)
			indexedChange.FilePatches = make(map[string]string, len(change.Files))
			for _, path := range change.Files {
				patch, err := s.diffPath(ctx, change.BeforeTree, change.AfterTree, path)
				if err != nil {
					return indexdb.Projection{}, fmt.Errorf("derive per-file patch for index rebuild: %w", err)
				}
				indexedChange.FilePatches[path] = patch
			}
			projection.Changes = append(projection.Changes, indexedChange)
			before, after, err := s.entitiesForChange(ctx, change)
			if err != nil {
				return indexdb.Projection{}, err
			}
			for _, value := range before {
				projection.Entities = append(projection.Entities, indexdb.EntityOccurrence{
					SessionID: session.ID, CompletedEventID: change.CompletedEventID,
					Side: "before", Entity: value,
				})
			}
			for _, value := range after {
				projection.Entities = append(projection.Entities, indexdb.EntityOccurrence{
					SessionID: session.ID, CompletedEventID: change.CompletedEventID,
					Side: "after", Entity: value,
				})
			}
			for _, edge := range entity.Relate(before, after) {
				projection.Edges = append(projection.Edges, indexdb.LineageEdge{
					SessionID: session.ID, CompletedEventID: change.CompletedEventID, Edge: edge,
				})
			}
		}
	}
	return projection, nil
}

func (s *Service) entitiesForChange(ctx context.Context, change ToolChange) ([]entity.Entity, []entity.Entity, error) {
	var before, after []entity.Entity
	for _, path := range change.Files {
		beforeSource, exists, err := s.readTreeFile(ctx, change.BeforeTree, path)
		if err != nil {
			return nil, nil, err
		}
		if exists {
			values, err := entity.Extract(path, change.BeforeTree, beforeSource)
			if err != nil {
				return nil, nil, fmt.Errorf("extract entities from %s before %s: %w", path, change.CompletedEventID, err)
			}
			before = append(before, values...)
		}
		afterSource, exists, err := s.readTreeFile(ctx, change.AfterTree, path)
		if err != nil {
			return nil, nil, err
		}
		if exists {
			values, err := entity.Extract(path, change.AfterTree, afterSource)
			if err != nil {
				return nil, nil, fmt.Errorf("extract entities from %s after %s: %w", path, change.CompletedEventID, err)
			}
			after = append(after, values...)
		}
	}
	return before, after, nil
}

func (s *Service) readTreeFile(ctx context.Context, treeID, path string) ([]byte, bool, error) {
	command := exec.CommandContext(ctx, "git", "-C", s.location.WorktreeRoot, "show", treeID+":"+path)
	output, err := command.Output()
	if err == nil {
		return output, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("read %s from checkpoint %s: %w", path, treeID, err)
}

func (s *Service) sourceFingerprint(ctx context.Context) (string, error) {
	hash := sha256.New()
	dataRoot := repository.DataRoot(s.location)
	activeRoot := filepath.Join(dataRoot, "active")
	var records []string
	err := filepath.WalkDir(activeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "events.jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(activeRoot, path)
		records = append(records, fmt.Sprintf("%s\x00%d\x00%d", filepath.ToSlash(relative), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("fingerprint active provenance: %w", err)
	}
	sort.Strings(records)
	for _, record := range records {
		_, _ = hash.Write([]byte(record))
		_, _ = hash.Write([]byte{'\n'})
	}
	command := exec.CommandContext(ctx, "git", "-C", s.location.WorktreeRoot,
		"for-each-ref", "--format=%(refname)%00%(objectname)", "refs/whydiff/sessions/")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fingerprint archived provenance: %w: %s", err, strings.TrimSpace(string(output)))
	}
	_, _ = hash.Write(output)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func toIndexedChange(change ToolChange) indexdb.Change {
	return indexdb.Change{
		SessionID: change.SessionID, Prompt: change.Prompt, Tool: change.Tool,
		ToolSummary: change.ToolSummary, StartedEventID: change.StartedEventID,
		CompletedEventID: change.CompletedEventID, StartedSequence: change.StartedSequence,
		CompletedSequence: change.CompletedSequence, BeforeTree: change.BeforeTree,
		AfterTree: change.AfterTree, Files: append([]string(nil), change.Files...), Patch: change.Patch,
	}
}

func fromIndexedChange(change indexdb.Change) ToolChange {
	return ToolChange{
		SessionID: change.SessionID, Prompt: change.Prompt, Tool: change.Tool,
		ToolSummary: change.ToolSummary, StartedEventID: change.StartedEventID,
		CompletedEventID: change.CompletedEventID, StartedSequence: change.StartedSequence,
		CompletedSequence: change.CompletedSequence, BeforeTree: change.BeforeTree,
		AfterTree: change.AfterTree, Files: append([]string(nil), change.Files...), Patch: change.Patch,
	}
}

// Index rebuilds or inspects the disposable SQLite projection.
func (s *Service) Index(ctx context.Context, force bool) (indexdb.Stats, error) {
	index, err := s.openIndex(ctx, force)
	if err != nil {
		return indexdb.Stats{}, err
	}
	defer index.Close()
	return index.Stats(ctx)
}
