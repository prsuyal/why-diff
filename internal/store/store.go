// Package store persists active-session events as append-only JSON Lines.
package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/prsuyal/why-diff/internal/event"
)

const maxEventLineBytes = 32 * 1024 * 1024

type Store struct {
	root string
}

func New(root string) *Store {
	return &Store{root: root}
}

// Append assigns the next ingestion sequence and durably appends one event.
// Calls from different hook processes are serialized with an advisory lock.
func (s *Store) Append(ctx context.Context, unsequenced event.Event) (event.Event, error) {
	if err := unsequenced.ValidateUnsequenced(); err != nil {
		return event.Event{}, fmt.Errorf("validate event before append: %w", err)
	}

	sessionDir := s.sessionDir(unsequenced.Context.SessionID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return event.Event{}, fmt.Errorf("create session store: %w", err)
	}

	fileLock := flock.New(filepath.Join(sessionDir, "capture.lock"))
	locked, err := fileLock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return event.Event{}, fmt.Errorf("acquire session store lock: %w", err)
	}
	if !locked {
		return event.Event{}, context.DeadlineExceeded
	}
	defer func() { _ = fileLock.Unlock() }()

	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	quarantine, err := recoverIncompleteTail(sessionDir, eventsPath)
	if err != nil {
		return event.Event{}, err
	}
	if quarantine != "" {
		unsequenced.AddWarning(
			"log_tail_recovered",
			"an incomplete event-log suffix was preserved in "+quarantine+" before capture resumed",
		)
	}

	current, err := s.readCurrentSequence(sessionDir, eventsPath)
	if err != nil {
		return event.Event{}, err
	}
	if current == math.MaxUint64 {
		return event.Event{}, errors.New("event sequence exhausted")
	}

	stored := unsequenced
	stored.Sequence = current + 1
	if err := stored.ValidateStored(); err != nil {
		return event.Event{}, fmt.Errorf("validate stored event: %w", err)
	}
	line, err := json.Marshal(stored)
	if err != nil {
		return event.Event{}, fmt.Errorf("encode event for append: %w", err)
	}

	// Reserving first means a crash can leave an observable gap, but a later
	// writer can never reuse the same sequence number.
	if err := writeSequence(filepath.Join(sessionDir, "sequence"), stored.Sequence); err != nil {
		return event.Event{}, err
	}
	if err := appendLine(eventsPath, line); err != nil {
		return event.Event{}, err
	}
	return stored, nil
}

func (s *Store) sessionDir(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return filepath.Join(s.root, "active", hex.EncodeToString(digest[:]))
}

func (s *Store) readCurrentSequence(sessionDir, eventsPath string) (uint64, error) {
	sequencePath := filepath.Join(sessionDir, "sequence")
	raw, err := os.ReadFile(sequencePath)
	if err == nil {
		value, parseErr := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("parse session sequence: %w", parseErr)
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read session sequence: %w", err)
	}
	return scanMaxSequence(eventsPath)
}

func scanMaxSequence(path string) (uint64, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	var maximum uint64
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEventLineBytes)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		var stored event.Event
		if err := json.Unmarshal(scanner.Bytes(), &stored); err != nil {
			return 0, fmt.Errorf("decode existing event log: %w", err)
		}
		if err := stored.ValidateStored(); err != nil {
			return 0, fmt.Errorf("validate existing event log: %w", err)
		}
		if stored.Sequence > maximum {
			maximum = stored.Sequence
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan existing event log: %w", err)
	}
	return maximum, nil
}

func recoverIncompleteTail(sessionDir, path string) (string, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open event log tail: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat event log: %w", err)
	}
	if info.Size() == 0 {
		return "", nil
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return "", fmt.Errorf("read event log tail: %w", err)
	}
	if last[0] == '\n' {
		return "", nil
	}

	completeBytes, err := lastCompleteRecordOffset(file, info.Size())
	if err != nil {
		return "", err
	}
	quarantine, err := os.CreateTemp(sessionDir, "corrupt-tail-*.bin")
	if err != nil {
		return "", fmt.Errorf("create corrupt-tail quarantine: %w", err)
	}
	quarantinePath := quarantine.Name()
	keepQuarantine := false
	defer func() {
		_ = quarantine.Close()
		if !keepQuarantine {
			_ = os.Remove(quarantinePath)
		}
	}()
	if err := quarantine.Chmod(0o600); err != nil {
		return "", fmt.Errorf("set corrupt-tail quarantine permissions: %w", err)
	}
	if _, err := file.Seek(completeBytes, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek incomplete event-log suffix: %w", err)
	}
	if _, err := io.Copy(quarantine, file); err != nil {
		return "", fmt.Errorf("preserve incomplete event-log suffix: %w", err)
	}
	if err := quarantine.Sync(); err != nil {
		return "", fmt.Errorf("sync corrupt-tail quarantine: %w", err)
	}
	if err := quarantine.Close(); err != nil {
		return "", fmt.Errorf("close corrupt-tail quarantine: %w", err)
	}
	if err := file.Truncate(completeBytes); err != nil {
		return "", fmt.Errorf("remove incomplete event-log suffix: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync recovered event log: %w", err)
	}
	keepQuarantine = true
	return filepath.Base(quarantinePath), nil
}

func lastCompleteRecordOffset(file *os.File, size int64) (int64, error) {
	const blockSize = int64(64 * 1024)
	for end := size; end > 0; {
		start := end - blockSize
		if start < 0 {
			start = 0
		}
		buffer := make([]byte, end-start)
		if _, err := file.ReadAt(buffer, start); err != nil {
			return 0, fmt.Errorf("scan event log for last complete record: %w", err)
		}
		if index := bytes.LastIndexByte(buffer, '\n'); index >= 0 {
			return start + int64(index) + 1, nil
		}
		end = start
	}
	return 0, nil
}

func writeSequence(path string, sequence uint64) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, "sequence-*")
	if err != nil {
		return fmt.Errorf("create sequence update: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set sequence permissions: %w", err)
	}
	if _, err := fmt.Fprintf(temporary, "%d\n", sequence); err != nil {
		temporary.Close()
		return fmt.Errorf("write sequence update: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync sequence update: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close sequence update: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish sequence update: %w", err)
	}
	return nil
}

func appendLine(path string, line []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open event log for append: %w", err)
	}
	defer file.Close()

	record := make([]byte, 0, len(line)+1)
	record = append(record, line...)
	record = append(record, '\n')
	if _, err := file.Write(record); err != nil {
		return fmt.Errorf("append event log: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync event log: %w", err)
	}
	return nil
}
