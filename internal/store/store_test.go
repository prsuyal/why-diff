package store_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/store"
)

func TestAppendSerializesConcurrentWriters(t *testing.T) {
	t.Parallel()

	const writers = 24
	root := t.TempDir()
	s := store.New(root)
	sequences := make(chan uint64, writers)
	errorsFound := make(chan error, writers)

	var wait sync.WaitGroup
	for i := 0; i < writers; i++ {
		wait.Add(1)
		go func(offset int) {
			defer wait.Done()
			e := newEvent(t, "shared-session", time.Now().Add(time.Duration(offset)*time.Nanosecond))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stored, err := s.Append(ctx, e)
			if err != nil {
				errorsFound <- err
				return
			}
			sequences <- stored.Sequence
		}(i)
	}
	wait.Wait()
	close(sequences)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("Append() error = %v", err)
	}
	if t.Failed() {
		return
	}

	var got []int
	for sequence := range sequences {
		got = append(got, int(sequence))
	}
	sort.Ints(got)
	if len(got) != writers {
		t.Fatalf("stored %d events, want %d", len(got), writers)
	}
	for i, sequence := range got {
		if sequence != i+1 {
			t.Fatalf("sorted sequences = %v, want contiguous 1..%d", got, writers)
		}
	}

	logPath := onlyEventLog(t, root)
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		var stored event.Event
		if err := json.Unmarshal(scanner.Bytes(), &stored); err != nil {
			t.Fatalf("line %d is not a complete event: %v", lineCount, err)
		}
		if err := stored.ValidateStored(); err != nil {
			t.Fatalf("line %d is not valid: %v", lineCount, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan event log: %v", err)
	}
	if lineCount != writers {
		t.Fatalf("event log has %d lines, want %d", lineCount, writers)
	}
}

func TestAppendRefusesIncompleteLogTail(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := store.New(root)
	first, err := s.Append(context.Background(), newEvent(t, "session", time.Now()))
	if err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	if first.Sequence != 1 {
		t.Fatalf("first sequence = %d, want 1", first.Sequence)
	}

	logPath := onlyEventLog(t, root)
	file, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	if _, err := file.WriteString(`{"partial":`); err != nil {
		file.Close()
		t.Fatalf("write incomplete record: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close event log: %v", err)
	}

	_, err = s.Append(context.Background(), newEvent(t, "session", time.Now()))
	if !errors.Is(err, store.ErrCorruptLogTail) {
		t.Fatalf("second Append() error = %v, want ErrCorruptLogTail", err)
	}
}

func newEvent(t *testing.T, sessionID string, observedAt time.Time) event.Event {
	t.Helper()
	e, err := event.New(
		event.KindPromptSubmitted,
		observedAt,
		event.Source{Provider: "codex", AdapterVersion: "test"},
		event.Context{SessionID: sessionID},
		event.PromptSubmittedPayload{Text: "test"},
		[]byte(`{"hook_event_name":"UserPromptSubmit"}`),
	)
	if err != nil {
		t.Fatalf("event.New() error = %v", err)
	}
	return e
}

func onlyEventLog(t *testing.T, root string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "active", "*", "events.jsonl"))
	if err != nil {
		t.Fatalf("glob event logs: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("found event logs %v, want exactly one", matches)
	}
	return matches[0]
}
