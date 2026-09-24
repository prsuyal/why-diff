package indexdb_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/entity"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/indexdb"
	"github.com/prsuyal/why-diff/internal/store"
)

func TestRebuildPublishesQueryableDisposableProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	observedAt := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	captured, err := event.New(event.KindPromptSubmitted, observedAt,
		event.Source{Provider: "codex", AdapterVersion: "1"},
		event.Context{SessionID: "session-1", TurnID: "turn-1"},
		event.PromptSubmittedPayload{Text: "rename timeout"}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	captured.Sequence = 1

	before := entity.Entity{
		VersionID: "entity-before", TreeID: "tree-before", Path: "auth.go", Language: "go",
		Kind: "function", Name: "Timeout", QualifiedName: "Timeout", StartLine: 3, EndLine: 5,
		ContentHash: "content-before", StructureHash: "structure", Structure: "function identifier block",
	}
	after := before
	after.VersionID = "entity-after"
	after.TreeID = "tree-after"
	after.Name = "SessionTimeout"
	after.QualifiedName = "SessionTimeout"
	after.ContentHash = "content-after"
	edge := entity.Edge{
		FromVersionID: before.VersionID, ToVersionID: after.VersionID,
		Relation: "renamed", Method: "exact_structure", Confidence: 0.9,
		Evidence: "Timeout auth.go:3-5 -> auth.go:3-5",
	}
	projection := indexdb.Projection{
		Fingerprint: "fingerprint-1",
		Sessions:    []store.Session{{ID: "session-1", Events: []event.Event{captured}}},
		Changes: []indexdb.Change{{
			SessionID: "session-1", Prompt: "rename timeout", Tool: "apply_patch",
			StartedEventID: "start", CompletedEventID: "complete", StartedSequence: 2,
			CompletedSequence: 3, BeforeTree: "tree-before", AfterTree: "tree-after",
			Files: []string{"auth.go"}, Patch: "full patch",
			FilePatches: map[string]string{"auth.go": "file patch"},
		}},
		Entities: []indexdb.EntityOccurrence{
			{SessionID: "session-1", CompletedEventID: "complete", Side: "before", Entity: before},
			{SessionID: "session-1", CompletedEventID: "complete", Side: "after", Entity: after},
		},
		Edges: []indexdb.LineageEdge{{SessionID: "session-1", CompletedEventID: "complete", Edge: edge}},
		EntityCache: []indexdb.EntityCacheEntry{{
			BlobID: "blob-after", Language: "go", Entities: []entity.Entity{after},
		}},
	}

	path := filepath.Join(t.TempDir(), "nested", "index.sqlite")
	if err := indexdb.Rebuild(ctx, path, projection); err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("index permissions = %o, want 600", got)
	}

	database, err := indexdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if fingerprint, err := database.Fingerprint(ctx); err != nil || fingerprint != "fingerprint-1" {
		t.Fatalf("fingerprint = %q, error = %v", fingerprint, err)
	}
	summaries, err := database.Summaries(ctx)
	if err != nil || len(summaries) != 1 || summaries[0].Prompt != "rename timeout" {
		t.Fatalf("summaries = %+v, error = %v", summaries, err)
	}
	session, err := database.Session(ctx, "session")
	if err != nil || len(session.Events) != 1 || session.Events[0].EventID != captured.EventID {
		t.Fatalf("session = %+v, error = %v", session, err)
	}
	if string(session.Events[0].SourcePayload) != "null" {
		t.Fatalf("indexed event retained redundant source payload: %s", session.Events[0].SourcePayload)
	}
	changes, err := database.Changes(ctx, "latest")
	if err != nil || len(changes) != 1 || changes[0].Patch != "file patch" {
		t.Fatalf("changes = %+v, error = %v", changes, err)
	}
	candidates, err := database.CandidateChanges(ctx, "auth.go", "session-1")
	if err != nil || len(candidates) != 1 || candidates[0].Patch != "file patch" {
		t.Fatalf("candidates = %+v, error = %v", candidates, err)
	}
	current, found, err := database.EntityAt(ctx, "tree-after", "auth.go", 4)
	if err != nil || !found || current.QualifiedName != "SessionTimeout" {
		t.Fatalf("EntityAt() = %+v, %t, %v", current, found, err)
	}
	history, err := database.History(ctx, current.VersionID)
	if err != nil || len(history.Nodes) != 2 || len(history.Edges) != 1 {
		t.Fatalf("history = %+v, error = %v", history, err)
	}
	cache, found, err := database.CachedEntities(ctx, "blob-after", "go")
	if err != nil || !found || len(cache) != 1 || cache[0].QualifiedName != "SessionTimeout" {
		t.Fatalf("cached entities = %+v, found = %t, error = %v", cache, found, err)
	}
	stats, err := database.Stats(ctx)
	if err != nil || stats.Sessions != 1 || stats.Events != 1 || stats.Changes != 1 || stats.Entities != 2 || stats.Edges != 1 || stats.CachedBlobs != 1 {
		t.Fatalf("stats = %+v, error = %v", stats, err)
	}
}

func TestOpenMissingIndexRequiresRebuild(t *testing.T) {
	t.Parallel()
	if _, err := indexdb.Open(filepath.Join(t.TempDir(), "missing.sqlite")); err != indexdb.ErrRebuildRequired {
		t.Fatalf("Open() error = %v, want ErrRebuildRequired", err)
	}
}
