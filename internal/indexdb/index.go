// Package indexdb implements why-diff's disposable SQLite query projection.
// Canonical provenance remains in append-only JSONL and private Git refs.
package indexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prsuyal/why-diff/internal/entity"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/store"
	_ "modernc.org/sqlite"
)

const SchemaVersion = 3

var ErrRebuildRequired = errors.New("why-diff index is missing or incompatible")

type Database struct {
	path string
	db   *sql.DB
}

type Summary struct {
	ID           string
	StartedAt    time.Time
	LastEventAt  time.Time
	EventCount   int
	WarningCount int
	Ended        bool
	Prompt       string
}

type Change struct {
	SessionID         string
	Prompt            string
	Tool              string
	ToolSummary       string
	StartedEventID    string
	CompletedEventID  string
	StartedSequence   uint64
	CompletedSequence uint64
	BeforeTree        string
	AfterTree         string
	Files             []string
	Patch             string
	FilePatches       map[string]string
}

type EntityOccurrence struct {
	SessionID        string
	CompletedEventID string
	Side             string
	Entity           entity.Entity
}

type LineageEdge struct {
	SessionID        string
	CompletedEventID string
	Edge             entity.Edge
}

type EntityCacheEntry struct {
	BlobID   string
	Language string
	Entities []entity.Entity
}

type Projection struct {
	Fingerprint     string
	Sessions        []store.Session
	Changes         []Change
	Entities        []EntityOccurrence
	Edges           []LineageEdge
	EntityCache     []EntityCacheEntry
	EventOffsets    map[string]int
	ReplaceSessions map[string]bool
	ValidSessionIDs []string
}

type SessionState struct {
	EventCount  int
	LastEventID string
}

type Stats struct {
	Path        string
	Fingerprint string
	SizeBytes   int64
	Sessions    int
	Events      int
	Changes     int
	Files       int
	Entities    int
	Edges       int
	CachedBlobs int
}

type History struct {
	Current entity.Entity
	Nodes   []entity.Entity
	Edges   []LineageEdge
}

func Path(dataRoot string) string {
	return filepath.Join(dataRoot, "index.sqlite")
}

func Open(path string) (*Database, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrRebuildRequired
		}
		return nil, fmt.Errorf("stat why-diff index: %w", err)
	}
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	index := &Database{path: path, db: db}
	var version string
	if err := db.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != fmt.Sprint(SchemaVersion) {
		db.Close()
		return nil, ErrRebuildRequired
	}
	return index, nil
}

func (d *Database) Close() error { return d.db.Close() }

func (d *Database) Fingerprint(ctx context.Context) (string, error) {
	var fingerprint string
	if err := d.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'source_fingerprint'").Scan(&fingerprint); err != nil {
		return "", fmt.Errorf("read index fingerprint: %w", err)
	}
	return fingerprint, nil
}

// Rebuild writes a complete projection to a temporary database and publishes
// it with an atomic rename, so readers never observe a half-built index.
func Rebuild(ctx context.Context, path string, projection Projection) error {
	if projection.Fingerprint == "" {
		return errors.New("index source fingerprint is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "index-rebuild-*.sqlite")
	if err != nil {
		return fmt.Errorf("create temporary index: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary index: %w", err)
	}
	defer os.Remove(temporaryPath)
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("set index permissions: %w", err)
	}

	db, err := openSQLite(temporaryPath)
	if err != nil {
		return err
	}
	if err := build(ctx, db, projection); err != nil {
		db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close rebuilt index: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish rebuilt index: %w", err)
	}
	return nil
}

func openSQLite(path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open why-diff index: %w", err)
	}
	db.SetMaxOpenConns(2)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to why-diff index: %w", err)
	}
	return db, nil
}

func build(ctx context.Context, db *sql.DB, projection Projection) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin index rebuild: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create index schema: %w", err)
	}
	for key, value := range map[string]string{
		"schema_version":     fmt.Sprint(SchemaVersion),
		"source_fingerprint": projection.Fingerprint,
		"rebuilt_at":         time.Now().UTC().Format(time.RFC3339Nano),
	} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta(key, value) VALUES (?, ?)", key, value); err != nil {
			return fmt.Errorf("write index metadata: %w", err)
		}
	}
	for _, session := range projection.Sessions {
		if err := insertSession(ctx, tx, session, 0, false); err != nil {
			return err
		}
	}
	for _, change := range projection.Changes {
		if err := insertChange(ctx, tx, change); err != nil {
			return err
		}
	}
	for _, occurrence := range projection.Entities {
		if err := insertEntity(ctx, tx, occurrence); err != nil {
			return err
		}
	}
	for _, edge := range projection.Edges {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO lineage_edges
			(session_id, completed_event_id, from_version_id, to_version_id, relation, method, confidence, evidence)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, edge.SessionID, edge.CompletedEventID,
			edge.Edge.FromVersionID, edge.Edge.ToVersionID, edge.Edge.Relation,
			edge.Edge.Method, edge.Edge.Confidence, edge.Edge.Evidence); err != nil {
			return fmt.Errorf("index lineage edge: %w", err)
		}
	}
	if err := insertEntityCache(ctx, tx, projection.EntityCache); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit index rebuild: %w", err)
	}
	return nil
}

// Update applies an append-aware projection refresh transactionally. Sessions
// whose canonical history was rewritten are replaced; append-only sessions
// insert only events and derived records after EventOffsets.
func (d *Database) Update(ctx context.Context, projection Projection) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incremental index update: %w", err)
	}
	defer tx.Rollback()

	valid := make(map[string]bool, len(projection.ValidSessionIDs))
	for _, id := range projection.ValidSessionIDs {
		valid[id] = true
	}
	rows, err := tx.QueryContext(ctx, "SELECT session_id FROM sessions")
	if err != nil {
		return fmt.Errorf("list indexed sessions for refresh: %w", err)
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !valid[id] {
			stale = append(stale, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE session_id = ?", id); err != nil {
			return fmt.Errorf("remove stale indexed session %s: %w", id, err)
		}
	}
	for _, session := range projection.Sessions {
		if projection.ReplaceSessions[session.ID] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE session_id = ?", session.ID); err != nil {
				return fmt.Errorf("replace indexed session %s: %w", session.ID, err)
			}
		}
		if err := insertSession(ctx, tx, session, projection.EventOffsets[session.ID], true); err != nil {
			return err
		}
	}
	for _, change := range projection.Changes {
		if err := insertChange(ctx, tx, change); err != nil {
			return err
		}
	}
	for _, occurrence := range projection.Entities {
		if err := insertEntity(ctx, tx, occurrence); err != nil {
			return err
		}
	}
	for _, edge := range projection.Edges {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO lineage_edges
			(session_id, completed_event_id, from_version_id, to_version_id, relation, method, confidence, evidence)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, edge.SessionID, edge.CompletedEventID,
			edge.Edge.FromVersionID, edge.Edge.ToVersionID, edge.Edge.Relation,
			edge.Edge.Method, edge.Edge.Confidence, edge.Edge.Evidence); err != nil {
			return fmt.Errorf("index lineage edge: %w", err)
		}
	}
	if err := insertEntityCache(ctx, tx, projection.EntityCache); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entities WHERE version_id NOT IN
		(SELECT version_id FROM entity_occurrences)`); err != nil {
		return fmt.Errorf("prune unreferenced entities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES ('source_fingerprint', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, projection.Fingerprint); err != nil {
		return fmt.Errorf("update index fingerprint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incremental index update: %w", err)
	}
	return nil
}

func insertEntityCache(ctx context.Context, tx *sql.Tx, entries []EntityCacheEntry) error {
	for _, entry := range entries {
		encoded, err := json.Marshal(entry.Entities)
		if err != nil {
			return fmt.Errorf("encode cached entities for blob %s: %w", entry.BlobID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entity_cache
			(blob_id, language, entities_json) VALUES (?, ?, ?)`, entry.BlobID, entry.Language, encoded); err != nil {
			return fmt.Errorf("cache entities for blob %s: %w", entry.BlobID, err)
		}
	}
	return nil
}

func insertSession(ctx context.Context, tx *sql.Tx, session store.Session, eventOffset int, upsert bool) error {
	if len(session.Events) == 0 {
		return nil
	}
	warnings, ended, prompt := 0, false, ""
	for _, captured := range session.Events {
		warnings += len(captured.Capture.Warnings)
		if captured.Kind == event.KindSessionEnded {
			ended = true
		}
		if prompt == "" && captured.Kind == event.KindPromptSubmitted {
			var payload event.PromptSubmittedPayload
			if json.Unmarshal(captured.Payload, &payload) == nil {
				prompt = payload.Text
			}
		}
	}
	statement := `INSERT INTO sessions
		(session_id, started_at, last_event_at, event_count, warning_count, ended, first_prompt)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	if upsert {
		statement += ` ON CONFLICT(session_id) DO UPDATE SET
			started_at=excluded.started_at, last_event_at=excluded.last_event_at,
			event_count=excluded.event_count, warning_count=excluded.warning_count,
			ended=excluded.ended, first_prompt=excluded.first_prompt`
	}
	if _, err := tx.ExecContext(ctx, statement, session.ID,
		session.Events[0].ObservedAt.Format(time.RFC3339Nano),
		session.Events[len(session.Events)-1].ObservedAt.Format(time.RFC3339Nano),
		len(session.Events), warnings, ended, prompt); err != nil {
		return fmt.Errorf("index session %s: %w", session.ID, err)
	}
	if eventOffset < 0 || eventOffset > len(session.Events) {
		eventOffset = 0
	}
	for _, captured := range session.Events[eventOffset:] {
		// SourcePayload is canonical in JSONL/Git but unused by indexed query
		// paths. Omitting it avoids duplicating the largest raw field.
		projected := captured
		projected.SourcePayload = nil
		raw, err := json.Marshal(projected)
		if err != nil {
			return fmt.Errorf("encode event %s for index: %w", captured.EventID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO events
			(event_id, session_id, sequence, kind, observed_at, provider, turn_id, tool_call_id, event_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, captured.EventID, session.ID, captured.Sequence,
			captured.Kind, captured.ObservedAt.Format(time.RFC3339Nano), captured.Source.Provider,
			captured.Context.TurnID, captured.Context.ToolCallID, raw); err != nil {
			return fmt.Errorf("index event %s: %w", captured.EventID, err)
		}
	}
	return nil
}

func insertChange(ctx context.Context, tx *sql.Tx, change Change) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO changes
		(session_id, started_event_id, completed_event_id, started_sequence, completed_sequence,
		 before_tree, after_tree, tool, tool_summary, prompt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, change.SessionID, change.StartedEventID,
		change.CompletedEventID, change.StartedSequence, change.CompletedSequence, change.BeforeTree,
		change.AfterTree, change.Tool, change.ToolSummary, change.Prompt); err != nil {
		return fmt.Errorf("index change %s: %w", change.CompletedEventID, err)
	}
	for _, path := range change.Files {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO change_files
			(session_id, started_event_id, completed_event_id, path, patch) VALUES (?, ?, ?, ?, ?)`,
			change.SessionID, change.StartedEventID, change.CompletedEventID, path, change.FilePatches[path]); err != nil {
			return fmt.Errorf("index changed file %s: %w", path, err)
		}
	}
	return nil
}

func insertEntity(ctx context.Context, tx *sql.Tx, occurrence EntityOccurrence) error {
	value := occurrence.Entity
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entities
		(version_id, tree_id, path, language, kind, name, qualified_name, start_line, end_line,
		 content_hash, structure_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, value.VersionID, value.TreeID, value.Path,
		value.Language, value.Kind, value.Name, value.QualifiedName, value.StartLine, value.EndLine,
		value.ContentHash, value.StructureHash); err != nil {
		return fmt.Errorf("index entity %s: %w", value.QualifiedName, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entity_occurrences
		(version_id, session_id, completed_event_id, side) VALUES (?, ?, ?, ?)`,
		value.VersionID, occurrence.SessionID, occurrence.CompletedEventID, occurrence.Side); err != nil {
		return fmt.Errorf("index entity occurrence %s: %w", value.QualifiedName, err)
	}
	return nil
}

func (d *Database) SessionStates(ctx context.Context) (map[string]SessionState, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT s.session_id, s.event_count,
		COALESCE((SELECT event_id FROM events e WHERE e.session_id = s.session_id
		ORDER BY sequence DESC LIMIT 1), '') FROM sessions s`)
	if err != nil {
		return nil, fmt.Errorf("query indexed session states: %w", err)
	}
	defer rows.Close()
	states := make(map[string]SessionState)
	for rows.Next() {
		var id string
		var state SessionState
		if err := rows.Scan(&id, &state.EventCount, &state.LastEventID); err != nil {
			return nil, fmt.Errorf("scan indexed session state: %w", err)
		}
		states[id] = state
	}
	return states, rows.Err()
}

func (d *Database) Summaries(ctx context.Context) ([]Summary, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT session_id, started_at, last_event_at,
		event_count, warning_count, ended, first_prompt FROM sessions
		ORDER BY started_at DESC, session_id`)
	if err != nil {
		return nil, fmt.Errorf("query indexed sessions: %w", err)
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		var summary Summary
		var started, last string
		if err := rows.Scan(&summary.ID, &started, &last, &summary.EventCount,
			&summary.WarningCount, &summary.Ended, &summary.Prompt); err != nil {
			return nil, fmt.Errorf("scan indexed session: %w", err)
		}
		summary.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		summary.LastEventAt, _ = time.Parse(time.RFC3339Nano, last)
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func (d *Database) Session(ctx context.Context, selector string) (store.Session, error) {
	id, err := d.resolveSessionID(ctx, selector)
	if err != nil {
		return store.Session{}, err
	}
	rows, err := d.db.QueryContext(ctx, "SELECT event_json FROM events WHERE session_id = ? ORDER BY sequence", id)
	if err != nil {
		return store.Session{}, fmt.Errorf("query indexed events: %w", err)
	}
	defer rows.Close()
	session := store.Session{ID: id}
	for rows.Next() {
		var raw []byte
		var captured event.Event
		if err := rows.Scan(&raw); err != nil {
			return store.Session{}, fmt.Errorf("scan indexed event: %w", err)
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			return store.Session{}, fmt.Errorf("decode indexed event: %w", err)
		}
		session.Events = append(session.Events, captured)
	}
	return session, rows.Err()
}

// StreamSession visits indexed events in sequence order without retaining the
// whole timeline in memory.
func (d *Database) StreamSession(ctx context.Context, selector string, visit func(string, int, event.Event) error) (string, int, error) {
	id, err := d.resolveSessionID(ctx, selector)
	if err != nil {
		return "", 0, err
	}
	var total int
	if err := d.db.QueryRowContext(ctx, "SELECT event_count FROM sessions WHERE session_id = ?", id).Scan(&total); err != nil {
		return "", 0, fmt.Errorf("query streamed session count: %w", err)
	}
	rows, err := d.db.QueryContext(ctx, "SELECT event_json FROM events WHERE session_id = ? ORDER BY sequence", id)
	if err != nil {
		return "", 0, fmt.Errorf("stream indexed events: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		var captured event.Event
		if err := rows.Scan(&raw); err != nil {
			return "", count, fmt.Errorf("scan streamed event: %w", err)
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			return "", count, fmt.Errorf("decode streamed event: %w", err)
		}
		if err := visit(id, total, captured); err != nil {
			return "", count, err
		}
		count++
	}
	return id, count, rows.Err()
}

func (d *Database) Changes(ctx context.Context, selector string) ([]Change, error) {
	id, err := d.resolveSessionID(ctx, selector)
	if err != nil {
		return nil, err
	}
	return d.queryChanges(ctx, `WHERE c.session_id = ?`, id)
}

func (d *Database) CandidateChanges(ctx context.Context, path, selector string) ([]Change, error) {
	where, arguments := "WHERE f.path = ?", []any{path}
	if selector != "" {
		id, err := d.resolveSessionID(ctx, selector)
		if err != nil {
			return nil, err
		}
		where += " AND c.session_id = ?"
		arguments = append(arguments, id)
	}
	return d.queryChanges(ctx, where, arguments...)
}

func (d *Database) queryChanges(ctx context.Context, clause string, arguments ...any) ([]Change, error) {
	query := `SELECT c.session_id, c.prompt, c.tool, c.tool_summary, c.started_event_id,
		c.completed_event_id, c.started_sequence, c.completed_sequence, c.before_tree,
		c.after_tree, f.path, f.patch FROM changes c JOIN change_files f ON
		f.session_id = c.session_id AND f.started_event_id = c.started_event_id AND
		f.completed_event_id = c.completed_event_id ` + clause + `
		ORDER BY (SELECT started_at FROM sessions s WHERE s.session_id = c.session_id) DESC,
		c.completed_sequence, f.path`
	rows, err := d.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query indexed changes: %w", err)
	}
	defer rows.Close()
	var changes []Change
	var patches []string
	for rows.Next() {
		var change Change
		var path, patch string
		if err := rows.Scan(&change.SessionID, &change.Prompt, &change.Tool, &change.ToolSummary,
			&change.StartedEventID, &change.CompletedEventID, &change.StartedSequence,
			&change.CompletedSequence, &change.BeforeTree, &change.AfterTree, &path, &patch); err != nil {
			return nil, fmt.Errorf("scan indexed change: %w", err)
		}
		if len(changes) == 0 || changes[len(changes)-1].SessionID != change.SessionID ||
			changes[len(changes)-1].CompletedEventID != change.CompletedEventID {
			if len(changes) > 0 {
				changes[len(changes)-1].Patch = strings.Join(patches, "\n")
			}
			changes = append(changes, change)
			patches = patches[:0]
		}
		current := &changes[len(changes)-1]
		current.Files = append(current.Files, path)
		if patch != "" {
			patches = append(patches, patch)
		}
	}
	if len(changes) > 0 {
		changes[len(changes)-1].Patch = strings.Join(patches, "\n")
	}
	return changes, rows.Err()
}

func (d *Database) EntityAt(ctx context.Context, treeID, path string, line int) (entity.Entity, bool, error) {
	row := d.db.QueryRowContext(ctx, entitySelect+`
		WHERE tree_id = ? AND path = ? AND start_line <= ? AND end_line >= ?
		ORDER BY (end_line - start_line), start_line DESC LIMIT 1`, treeID, path, line, line)
	value, err := scanEntity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return entity.Entity{}, false, nil
	}
	if err != nil {
		return entity.Entity{}, false, fmt.Errorf("query entity at %s:%d: %w", path, line, err)
	}
	return value, true, nil
}

func (d *Database) CachedEntities(ctx context.Context, blobID, language string) ([]entity.Entity, bool, error) {
	var encoded []byte
	err := d.db.QueryRowContext(ctx, `SELECT entities_json FROM entity_cache
		WHERE blob_id = ? AND language = ?`, blobID, language).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query entity cache for blob %s: %w", blobID, err)
	}
	var values []entity.Entity
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, false, fmt.Errorf("decode entity cache for blob %s: %w", blobID, err)
	}
	return values, true, nil
}

func (d *Database) History(ctx context.Context, versionID string) (History, error) {
	current, err := scanEntity(d.db.QueryRowContext(ctx, entitySelect+" WHERE version_id = ?", versionID))
	if err != nil {
		return History{}, fmt.Errorf("query lineage entity: %w", err)
	}
	nodeRows, err := d.db.QueryContext(ctx, connectedEntityCTE+entitySelect+`
		WHERE version_id IN (SELECT version_id FROM connected)
		ORDER BY tree_id, path, start_line`, versionID)
	if err != nil {
		return History{}, fmt.Errorf("query connected lineage entities: %w", err)
	}
	history := History{Current: current}
	for nodeRows.Next() {
		value, err := scanEntity(nodeRows)
		if err != nil {
			nodeRows.Close()
			return History{}, fmt.Errorf("scan connected lineage entity: %w", err)
		}
		history.Nodes = append(history.Nodes, value)
	}
	if err := nodeRows.Close(); err != nil {
		return History{}, err
	}
	edgeRows, err := d.db.QueryContext(ctx, connectedEntityCTE+`SELECT le.session_id,
		le.completed_event_id, le.from_version_id, le.to_version_id, le.relation,
		le.method, le.confidence, le.evidence FROM lineage_edges le
		WHERE le.from_version_id IN (SELECT version_id FROM connected)
		  AND le.to_version_id IN (SELECT version_id FROM connected)
		ORDER BY le.rowid`, versionID)
	if err != nil {
		return History{}, fmt.Errorf("query connected lineage edges: %w", err)
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var value LineageEdge
		if err := edgeRows.Scan(&value.SessionID, &value.CompletedEventID,
			&value.Edge.FromVersionID, &value.Edge.ToVersionID, &value.Edge.Relation,
			&value.Edge.Method, &value.Edge.Confidence, &value.Edge.Evidence); err != nil {
			return History{}, fmt.Errorf("scan connected lineage edge: %w", err)
		}
		history.Edges = append(history.Edges, value)
	}
	return history, edgeRows.Err()
}

const connectedEntityCTE = `WITH RECURSIVE connected(version_id) AS (
	SELECT ?
	UNION
	SELECT CASE WHEN le.from_version_id = connected.version_id
		THEN le.to_version_id ELSE le.from_version_id END
	FROM lineage_edges le JOIN connected
		ON le.from_version_id = connected.version_id OR le.to_version_id = connected.version_id
) `

func (d *Database) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{Path: d.path}
	if info, err := os.Stat(d.path); err == nil {
		stats.SizeBytes = info.Size()
	}
	var err error
	if stats.Fingerprint, err = d.Fingerprint(ctx); err != nil {
		return Stats{}, err
	}
	for table, destination := range map[string]*int{
		"sessions": &stats.Sessions, "events": &stats.Events, "changes": &stats.Changes,
		"change_files": &stats.Files, "entities": &stats.Entities, "lineage_edges": &stats.Edges,
		"entity_cache": &stats.CachedBlobs,
	} {
		if err := d.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(destination); err != nil {
			return Stats{}, fmt.Errorf("count indexed %s: %w", table, err)
		}
	}
	return stats, nil
}

func (d *Database) resolveSessionID(ctx context.Context, selector string) (string, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT session_id FROM sessions ORDER BY started_at DESC, session_id")
	if err != nil {
		return "", fmt.Errorf("query indexed session ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return "", errors.New("no captured why-diff sessions")
	}
	if selector == "" || selector == "latest" {
		return ids[0], nil
	}
	for _, id := range ids {
		if id == selector {
			return id, nil
		}
	}
	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(id, selector) {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("session prefix %q is ambiguous", selector)
	}
	return "", fmt.Errorf("session %q was not found", selector)
}

type rowScanner interface {
	Scan(...any) error
}

const entitySelect = `SELECT version_id, tree_id, path, language, kind, name,
	qualified_name, start_line, end_line, content_hash, structure_hash FROM entities`

func scanEntity(row rowScanner) (entity.Entity, error) {
	var value entity.Entity
	err := row.Scan(&value.VersionID, &value.TreeID, &value.Path, &value.Language, &value.Kind,
		&value.Name, &value.QualifiedName, &value.StartLine, &value.EndLine, &value.ContentHash,
		&value.StructureHash)
	return value, err
}

const schema = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE sessions (
  session_id TEXT PRIMARY KEY,
  started_at TEXT NOT NULL,
  last_event_at TEXT NOT NULL,
  event_count INTEGER NOT NULL,
  warning_count INTEGER NOT NULL,
  ended INTEGER NOT NULL,
  first_prompt TEXT NOT NULL
);
CREATE TABLE events (
  event_id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  kind TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  provider TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  tool_call_id TEXT NOT NULL,
  event_json BLOB NOT NULL,
  UNIQUE(session_id, sequence)
);
CREATE INDEX events_session_kind ON events(session_id, kind, sequence);
CREATE TABLE changes (
  session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  started_event_id TEXT NOT NULL,
  completed_event_id TEXT NOT NULL,
  started_sequence INTEGER NOT NULL,
  completed_sequence INTEGER NOT NULL,
  before_tree TEXT NOT NULL,
  after_tree TEXT NOT NULL,
  tool TEXT NOT NULL,
  tool_summary TEXT NOT NULL,
  prompt TEXT NOT NULL,
  PRIMARY KEY(session_id, started_event_id, completed_event_id)
);
CREATE TABLE change_files (
  session_id TEXT NOT NULL,
  started_event_id TEXT NOT NULL,
  completed_event_id TEXT NOT NULL,
  path TEXT NOT NULL,
  patch TEXT NOT NULL,
  PRIMARY KEY(session_id, started_event_id, completed_event_id, path),
  FOREIGN KEY(session_id, started_event_id, completed_event_id)
    REFERENCES changes(session_id, started_event_id, completed_event_id) ON DELETE CASCADE
);
CREATE INDEX change_files_path ON change_files(path, session_id, completed_event_id);
CREATE TABLE entities (
  version_id TEXT PRIMARY KEY,
  tree_id TEXT NOT NULL,
  path TEXT NOT NULL,
  language TEXT NOT NULL,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  qualified_name TEXT NOT NULL,
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  content_hash TEXT NOT NULL,
  structure_hash TEXT NOT NULL
);
CREATE INDEX entities_location ON entities(tree_id, path, start_line, end_line);
CREATE TABLE entity_cache (
  blob_id TEXT NOT NULL,
  language TEXT NOT NULL,
  entities_json BLOB NOT NULL,
  PRIMARY KEY(blob_id, language)
);
CREATE TABLE entity_occurrences (
  version_id TEXT NOT NULL REFERENCES entities(version_id) ON DELETE CASCADE,
  session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  completed_event_id TEXT NOT NULL,
  side TEXT NOT NULL CHECK(side IN ('before', 'after')),
  PRIMARY KEY(version_id, session_id, completed_event_id, side)
);
CREATE TABLE lineage_edges (
  session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  completed_event_id TEXT NOT NULL,
  from_version_id TEXT NOT NULL REFERENCES entities(version_id) ON DELETE CASCADE,
  to_version_id TEXT NOT NULL REFERENCES entities(version_id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  method TEXT NOT NULL,
  confidence REAL NOT NULL CHECK(confidence >= 0 AND confidence <= 1),
  evidence TEXT NOT NULL,
  PRIMARY KEY(session_id, completed_event_id, from_version_id, to_version_id)
);
CREATE INDEX lineage_from ON lineage_edges(from_version_id);
CREATE INDEX lineage_to ON lineage_edges(to_version_id);
`
