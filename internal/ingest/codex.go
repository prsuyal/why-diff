// Package ingest wires provider adapters to redaction and durable storage.
package ingest

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/prsuyal/why-diff/internal/capture/codex"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/redact"
	"github.com/prsuyal/why-diff/internal/repository"
	"github.com/prsuyal/why-diff/internal/store"
)

const defaultLockTimeout = 500 * time.Millisecond

type CodexOptions struct {
	StoreRoot   string
	ObservedAt  time.Time
	LockTimeout time.Duration
}

func Codex(ctx context.Context, raw []byte, options CodexOptions) (event.Event, error) {
	observedAt := options.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	normalized, err := (codex.Adapter{}).Normalize(raw, observedAt)
	if err != nil {
		return event.Event{}, fmt.Errorf("normalize hook event: %w", err)
	}

	storeRoot := options.StoreRoot
	if storeRoot == "" {
		location, err := repository.Locate(ctx, normalized.Context.WorkingDirectory)
		if err != nil {
			return event.Event{}, err
		}
		normalized.Context.RepositoryID = location.RepositoryID
		normalized.Context.WorktreeID = location.WorktreeID
		storeRoot = repository.DataRoot(location)
	} else {
		normalized.Context.RepositoryID = repository.LocalID("repo", filepath.Clean(storeRoot))
		normalized.Context.WorktreeID = repository.LocalID("worktree", normalized.Context.WorkingDirectory)
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
	defer cancel()

	stored, err := store.New(storeRoot).Append(appendContext, normalized)
	if err != nil {
		return event.Event{}, fmt.Errorf("append hook event: %w", err)
	}
	return stored, nil
}
