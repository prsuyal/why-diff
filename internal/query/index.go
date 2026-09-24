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
	"github.com/prsuyal/why-diff/internal/gitobject"
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
			if fingerprintErr == nil {
				projection, updateErr := s.buildIncrementalProjection(ctx, fingerprint, index)
				if updateErr == nil {
					updateErr = index.Update(ctx, projection)
				}
				if updateErr == nil {
					return index, nil
				}
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

func (s *Service) buildIncrementalProjection(ctx context.Context, fingerprint string, index *indexdb.Database) (indexdb.Projection, error) {
	sessions, err := s.allSessions(ctx)
	if err != nil {
		return indexdb.Projection{}, fmt.Errorf("read canonical provenance for incremental index update: %w", err)
	}
	states, err := index.SessionStates(ctx)
	if err != nil {
		return indexdb.Projection{}, err
	}
	projection := indexdb.Projection{
		Fingerprint: fingerprint, EventOffsets: make(map[string]int),
		ReplaceSessions: make(map[string]bool),
	}
	for _, session := range sessions {
		projection.ValidSessionIDs = append(projection.ValidSessionIDs, session.ID)
		state, exists := states[session.ID]
		offset, cutoff, replace := 0, uint64(0), !exists
		if exists && state.EventCount > 0 && state.EventCount <= len(session.Events) &&
			session.Events[state.EventCount-1].EventID == state.LastEventID {
			offset = state.EventCount
			cutoff = session.Events[offset-1].Sequence
			replace = false
		} else if exists {
			replace = true
		}
		if !replace && offset == len(session.Events) {
			continue
		}
		projection.Sessions = append(projection.Sessions, session)
		projection.EventOffsets[session.ID] = offset
		projection.ReplaceSessions[session.ID] = replace
		changes, err := s.deriveChangesForSession(ctx, session)
		if err != nil {
			return indexdb.Projection{}, fmt.Errorf("derive appended session changes: %w", err)
		}
		for _, change := range changes {
			if !replace && change.CompletedSequence <= cutoff {
				continue
			}
			if err := s.addChangeToProjection(ctx, &projection, change, index); err != nil {
				return indexdb.Projection{}, err
			}
		}
	}
	return projection, nil
}

func (s *Service) buildProjection(ctx context.Context, fingerprint string) (indexdb.Projection, error) {
	sessions, err := s.allSessions(ctx)
	if err != nil {
		return indexdb.Projection{}, fmt.Errorf("read canonical provenance for index rebuild: %w", err)
	}
	projection := indexdb.Projection{Fingerprint: fingerprint, Sessions: sessions}
	for _, session := range sessions {
		projection.ValidSessionIDs = append(projection.ValidSessionIDs, session.ID)
	}
	for _, session := range sessions {
		changes, err := s.deriveChangesForSession(ctx, session)
		if err != nil {
			return indexdb.Projection{}, fmt.Errorf("derive changes for index rebuild: %w", err)
		}
		for _, change := range changes {
			if err := s.addChangeToProjection(ctx, &projection, change, nil); err != nil {
				return indexdb.Projection{}, err
			}
		}
	}
	return projection, nil
}

func (s *Service) addChangeToProjection(ctx context.Context, projection *indexdb.Projection, change ToolChange, index *indexdb.Database) error {
	indexedChange := toIndexedChange(change)
	indexedChange.FilePatches = make(map[string]string, len(change.Files))
	for _, path := range change.Files {
		patch := ""
		if len(change.Files) == 1 {
			patch = change.Patch
		} else {
			var err error
			patch, err = s.diffPath(ctx, change.BeforeTree, change.AfterTree, path)
			if err != nil {
				return fmt.Errorf("derive per-file patch for index update: %w", err)
			}
		}
		indexedChange.FilePatches[path] = patch
	}
	projection.Changes = append(projection.Changes, indexedChange)
	before, after, cacheEntries, err := s.entitiesForChange(ctx, change, index)
	if err != nil {
		return err
	}
	projection.EntityCache = append(projection.EntityCache, cacheEntries...)
	for _, value := range before {
		projection.Entities = append(projection.Entities, indexdb.EntityOccurrence{
			SessionID: change.SessionID, CompletedEventID: change.CompletedEventID,
			Side: "before", Entity: value,
		})
	}
	for _, value := range after {
		projection.Entities = append(projection.Entities, indexdb.EntityOccurrence{
			SessionID: change.SessionID, CompletedEventID: change.CompletedEventID,
			Side: "after", Entity: value,
		})
	}
	for _, edge := range entity.Relate(before, after) {
		projection.Edges = append(projection.Edges, indexdb.LineageEdge{
			SessionID: change.SessionID, CompletedEventID: change.CompletedEventID, Edge: edge,
		})
	}
	return nil
}

func (s *Service) entitiesForChange(ctx context.Context, change ToolChange, index *indexdb.Database) ([]entity.Entity, []entity.Entity, []indexdb.EntityCacheEntry, error) {
	type request struct {
		path string
		tree string
		side string
	}
	requests := make([]request, 0, len(change.Files)*2)
	specs := make([]string, 0, len(change.Files)*2)
	for _, path := range change.Files {
		requests = append(requests, request{path: path, tree: change.BeforeTree, side: "before"})
		specs = append(specs, change.BeforeTree+":"+path)
		requests = append(requests, request{path: path, tree: change.AfterTree, side: "after"})
		specs = append(specs, change.AfterTree+":"+path)
	}
	objects, err := gitobject.ReadBatch(ctx, s.location.WorktreeRoot, specs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("batch-read checkpoint blobs for %s: %w", change.CompletedEventID, err)
	}
	var before, after []entity.Entity
	var cacheEntries []indexdb.EntityCacheEntry
	for objectIndex, object := range objects {
		if object.Missing {
			continue
		}
		if object.Type != "blob" {
			return nil, nil, nil, fmt.Errorf("checkpoint path %s resolved to %s, want blob", requests[objectIndex].path, object.Type)
		}
		request := requests[objectIndex]
		language, supported := entity.LanguageForPath(request.path)
		if !supported {
			continue
		}
		cacheKey := object.OID + "\x00" + language
		s.entityMu.Lock()
		values, cached := s.entityCache[cacheKey]
		s.entityMu.Unlock()
		if !cached && index != nil {
			values, cached, err = index.CachedEntities(ctx, object.OID, language)
			if err != nil {
				return nil, nil, nil, err
			}
		}
		if !cached {
			values, err = entity.Extract(request.path, request.tree, object.Data)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("extract entities from %s at %s: %w", request.path, request.tree, err)
			}
			values = entity.Templates(values)
			cacheEntries = append(cacheEntries, indexdb.EntityCacheEntry{
				BlobID: object.OID, Language: language, Entities: values,
			})
		}
		s.entityMu.Lock()
		s.entityCache[cacheKey] = values
		s.entityMu.Unlock()
		rebased := entity.Rebase(values, request.path, request.tree)
		if request.side == "before" {
			before = append(before, rebased...)
		} else {
			after = append(after, rebased...)
		}
	}
	return before, after, cacheEntries, nil
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
		"for-each-ref", "--format=%(refname)%00%(objectname)", "refs/why-diff/sessions/")
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
