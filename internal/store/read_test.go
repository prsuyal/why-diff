package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/store"
)

func TestSessionsReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := store.New(root)
	older := newEvent(t, "older", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := newEvent(t, "newer", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if _, err := s.Append(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(context.Background(), newer); err != nil {
		t.Fatal(err)
	}

	sessions, err := s.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(sessions) != 2 || sessions[0].ID != "newer" || sessions[1].ID != "older" {
		t.Fatalf("sessions = %+v", sessions)
	}
}
