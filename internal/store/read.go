package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/prsuyal/why-diff/internal/event"
)

type Session struct {
	ID     string
	Events []event.Event
}

func (s Session) StartedAt() time.Time {
	if len(s.Events) == 0 {
		return time.Time{}
	}
	return s.Events[0].ObservedAt
}

func (s *Store) Sessions(ctx context.Context) ([]Session, error) {
	activeRoot := filepath.Join(s.root, "active")
	entries, err := os.ReadDir(activeRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read active sessions: %w", err)
	}

	sessions := make([]Session, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(activeRoot, entry.Name())
		session, err := readSession(ctx, directory)
		if err != nil {
			return nil, fmt.Errorf("read active session %s: %w", entry.Name(), err)
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

func readSession(ctx context.Context, directory string) (Session, error) {
	fileLock := flock.New(filepath.Join(directory, "capture.lock"))
	locked, err := fileLock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return Session{}, fmt.Errorf("lock session for reading: %w", err)
	}
	if !locked {
		return Session{}, context.DeadlineExceeded
	}
	defer func() { _ = fileLock.Unlock() }()

	file, err := os.Open(filepath.Join(directory, "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, nil
	}
	if err != nil {
		return Session{}, fmt.Errorf("open session event log: %w", err)
	}
	defer file.Close()

	return DecodeSession(file)
}

// DecodeSession validates and decodes a canonical JSONL event stream. It is
// shared by the live store and the Git-backed provenance reader.
func DecodeSession(reader io.Reader) (Session, error) {
	var session Session
	var previousSequence uint64
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxEventLineBytes)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		var stored event.Event
		if err := json.Unmarshal(scanner.Bytes(), &stored); err != nil {
			return Session{}, fmt.Errorf("decode session event: %w", err)
		}
		if err := stored.ValidateStored(); err != nil {
			return Session{}, fmt.Errorf("validate session event: %w", err)
		}
		if stored.Sequence <= previousSequence {
			return Session{}, fmt.Errorf("session event sequence is not increasing: %d after %d", stored.Sequence, previousSequence)
		}
		previousSequence = stored.Sequence
		if session.ID == "" {
			session.ID = stored.Context.SessionID
		} else if stored.Context.SessionID != session.ID {
			return Session{}, fmt.Errorf("session log mixes ids %q and %q", session.ID, stored.Context.SessionID)
		}
		session.Events = append(session.Events, stored)
	}
	if err := scanner.Err(); err != nil {
		return Session{}, fmt.Errorf("scan session event log: %w", err)
	}
	return session, nil
}
