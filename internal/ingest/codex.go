// Package ingest wires provider adapters to redaction and durable storage.
package ingest

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/prsuyal/why-diff/internal/capture/claude"
	"github.com/prsuyal/why-diff/internal/capture/codex"
	"github.com/prsuyal/why-diff/internal/checkpoint"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/provenance"
	"github.com/prsuyal/why-diff/internal/redact"
	"github.com/prsuyal/why-diff/internal/repository"
	"github.com/prsuyal/why-diff/internal/store"
)

const defaultLockTimeout = 500 * time.Millisecond

type Options struct {
	StoreRoot   string
	ObservedAt  time.Time
	LockTimeout time.Duration
}

type CodexOptions = Options
type ClaudeOptions = Options

type adapter interface {
	Normalize([]byte, time.Time) (event.Event, error)
}

func Codex(ctx context.Context, raw []byte, options CodexOptions) (event.Event, error) {
	return capture(ctx, raw, options, codex.Adapter{})
}

func Claude(ctx context.Context, raw []byte, options ClaudeOptions) (event.Event, error) {
	return capture(ctx, raw, options, claude.Adapter{})
}

func capture(ctx context.Context, raw []byte, options Options, normalizer adapter) (event.Event, error) {
	observedAt := options.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	normalized, err := normalizer.Normalize(raw, observedAt)
	if err != nil {
		return event.Event{}, fmt.Errorf("normalize hook event: %w", err)
	}

	storeRoot := options.StoreRoot
	var location repository.Location
	haveLocation := false
	if storeRoot == "" {
		location, err = repository.Locate(ctx, normalized.Context.WorkingDirectory)
		if err != nil {
			return event.Event{}, err
		}
		haveLocation = true
		normalized.Context.RepositoryID = location.RepositoryID
		normalized.Context.WorktreeID = location.WorktreeID
		storeRoot = repository.DataRoot(location)
	} else {
		normalized.Context.RepositoryID = repository.LocalID("repo", filepath.Clean(storeRoot))
		normalized.Context.WorktreeID = repository.LocalID("worktree", normalized.Context.WorkingDirectory)
	}

	if shouldCheckpoint(normalized.Kind) {
		if !haveLocation {
			location, err = repository.Locate(ctx, normalized.Context.WorkingDirectory)
			haveLocation = err == nil
		}
		if haveLocation {
			captured, captureErr := checkpoint.Capture(ctx, location)
			if captureErr != nil {
				normalized.AddWarning("checkpoint_failed", captureErr.Error())
			} else {
				normalized.Checkpoint = &captured.State
				for _, warning := range captured.Warnings {
					normalized.AddWarning("checkpoint_partial", warning)
				}
			}
		} else {
			normalized.AddWarning("checkpoint_failed", err.Error())
		}
	}

	if err := redact.Default().Apply(&normalized); err != nil {
		return event.Event{}, err
	}
	if err := normalized.ValidateUnsequenced(); err != nil {
		return event.Event{}, fmt.Errorf("validate redacted event: %w", err)
	}

	lockTimeout := options.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = defaultLockTimeout
	}
	appendContext, cancel := context.WithTimeout(ctx, lockTimeout)

	sessionStore := store.New(storeRoot)
	stored, err := sessionStore.Append(appendContext, normalized)
	cancel()
	if err != nil {
		return event.Event{}, fmt.Errorf("append hook event: %w", err)
	}
	if stored.Kind == event.KindSessionEnded && haveLocation {
		sessions, readErr := sessionStore.Sessions(ctx)
		if readErr != nil {
			return stored, fmt.Errorf("read completed session for finalization: %w", readErr)
		}
		for _, session := range sessions {
			if session.ID != stored.Context.SessionID {
				continue
			}
			if _, finalizeErr := provenance.Finalize(ctx, location, session); finalizeErr != nil {
				return stored, fmt.Errorf("finalize completed session: %w", finalizeErr)
			}
			break
		}
	}
	return stored, nil
}

func shouldCheckpoint(kind event.Kind) bool {
	switch kind {
	case event.KindSessionStarted, event.KindSessionEnded, event.KindToolStarted, event.KindToolCompleted:
		return true
	default:
		return false
	}
}
