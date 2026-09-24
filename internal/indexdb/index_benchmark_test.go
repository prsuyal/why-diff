package indexdb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/indexdb"
	"github.com/prsuyal/why-diff/internal/store"
)

// BenchmarkSQLiteScale makes the 1k-100k event behavior and on-disk cost
// reproducible without requiring a large real provenance archive.
func BenchmarkSQLiteScale(b *testing.B) {
	for _, eventCount := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("events_%d", eventCount), func(b *testing.B) {
			events := make([]event.Event, eventCount)
			observedAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
			payload := json.RawMessage(`{"text":"benchmark prompt"}`)
			for index := range events {
				events[index] = event.Event{
					SchemaVersion: event.SchemaVersion,
					EventID:       fmt.Sprintf("event-%09d", index),
					Kind:          event.KindPromptSubmitted,
					ObservedAt:    observedAt.Add(time.Duration(index) * time.Millisecond),
					Sequence:      uint64(index + 1),
					Source:        event.Source{Provider: "benchmark", AdapterVersion: "1"},
					Context:       event.Context{SessionID: "scale-session", TurnID: "turn"},
					Payload:       payload,
				}
			}
			path := filepath.Join(b.TempDir(), "index.sqlite")
			projection := indexdb.Projection{
				Fingerprint: "scale", Sessions: []store.Session{{ID: "scale-session", Events: events}},
			}
			if err := indexdb.Rebuild(context.Background(), path, projection); err != nil {
				b.Fatal(err)
			}
			database, err := indexdb.Open(path)
			if err != nil {
				b.Fatal(err)
			}
			defer database.Close()
			stats, err := database.Stats(context.Background())
			if err != nil {
				b.Fatal(err)
			}
			bytesPerEvent := float64(stats.SizeBytes) / float64(eventCount)

			b.Run("summary", func(b *testing.B) {
				for b.Loop() {
					if _, err := database.Summaries(context.Background()); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(bytesPerEvent, "index_B/event")
			})
			b.Run("load_session", func(b *testing.B) {
				for b.Loop() {
					session, err := database.Session(context.Background(), "scale-session")
					if err != nil || len(session.Events) != eventCount {
						b.Fatalf("Session() events = %d, error = %v", len(session.Events), err)
					}
				}
				b.ReportMetric(bytesPerEvent, "index_B/event")
			})
			b.Run("stream_session", func(b *testing.B) {
				for b.Loop() {
					_, count, err := database.StreamSession(context.Background(), "scale-session", func(string, int, event.Event) error { return nil })
					if err != nil || count != eventCount {
						b.Fatalf("StreamSession() events = %d, error = %v", count, err)
					}
				}
				b.ReportMetric(bytesPerEvent, "index_B/event")
			})
		})
	}
}
