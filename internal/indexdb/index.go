// Package indexdb implements WhyDiff's disposable SQLite query projection.
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
	"sort"
	"strings"
	"time"

	"github.com/prsuyal/why-diff/internal/entity"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/store"
	_ "modernc.org/sqlite"
)

const SchemaVersion = 2

var ErrRebuildRequired = errors.New("WhyDiff index is missing or incompatible")

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

type Projection struct {
	Fingerprint string
	Sessions    []store.Session
	Changes     []Change
	Entities    []EntityOccurrence
	Edges       []LineageEdge
}

type Stats struct {
	Path        string
	Fingerprint string
	Sessions    int
	Events      int
	Changes     int
	Files       int
	Entities    int
	Edges       int
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
		return nil, fmt.Errorf("stat WhyDiff index: %w", err)
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
		return nil, fmt.Errorf("open WhyDiff index: %w", err)
	}
	// A change query may fetch its file rows while the outer result set is
	// still open, so allow a small bounded pool of read connections.
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to WhyDiff index: %w", err)
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
		if err := insertSession(ctx, tx, session); err != nil {
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit index rebuild: %w", err)
	}
	return nil
}

func insertSession(ctx context.Context, tx *sql.Tx, session store.Session) error {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions
		(session_id, started_at, last_event_at, event_count, warning_count, ended, first_prompt)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, session.ID,
		session.Events[0].ObservedAt.Format(time.RFC3339Nano),
		session.Events[len(session.Events)-1].ObservedAt.Format(time.RFC3339Nano),
		len(session.Events), warnings, ended, prompt); err != nil {
		return fmt.Errorf("index session %s: %w", session.ID, err)
	}
	for _, captured := range session.Events {
		raw, err := json.Marshal(captured)
		if err != nil {
			return fmt.Errorf("encode event %s for index: %w", captured.EventID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO changes
		(session_id, started_event_id, completed_event_id, started_sequence, completed_sequence,
		 before_tree, after_tree, tool, tool_summary, prompt, patch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, change.SessionID, change.StartedEventID,
		change.CompletedEventID, change.StartedSequence, change.CompletedSequence, change.BeforeTree,
		change.AfterTree, change.Tool, change.ToolSummary, change.Prompt, change.Patch); err != nil {
		return fmt.Errorf("index change %s: %w", change.CompletedEventID, err)
	}
	for _, path := range change.Files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO change_files
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
		 content_hash, structure_hash, structure)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, value.VersionID, value.TreeID, value.Path,
		value.Language, value.Kind, value.Name, value.QualifiedName, value.StartLine, value.EndLine,
		value.ContentHash, value.StructureHash, value.Structure); err != nil {
		return fmt.Errorf("index entity %s: %w", value.QualifiedName, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entity_occurrences
		(version_id, session_id, completed_event_id, side) VALUES (?, ?, ?, ?)`,
		value.VersionID, occurrence.SessionID, occurrence.CompletedEventID, occurrence.Side); err != nil {
		return fmt.Errorf("index entity occurrence %s: %w", value.QualifiedName, err)
	}
	return nil
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

func (d *Database) Changes(ctx context.Context, selector string) ([]Change, error) {
	id, err := d.resolveSessionID(ctx, selector)
	if err != nil {
		return nil, err
	}
	return d.queryChanges(ctx, `WHERE c.session_id = ?`, "c.patch", id)
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
	return d.queryChanges(ctx, `JOIN change_files f ON
		f.session_id = c.session_id AND f.started_event_id = c.started_event_id AND
		f.completed_event_id = c.completed_event_id `+where, "f.patch", arguments...)
}

func (d *Database) queryChanges(ctx context.Context, clause, patchExpression string, arguments ...any) ([]Change, error) {
	query := `SELECT c.session_id, c.prompt, c.tool, c.tool_summary, c.started_event_id,
		c.completed_event_id, c.started_sequence, c.completed_sequence, c.before_tree,
		c.after_tree, ` + patchExpression + ` FROM changes c ` + clause + `
		ORDER BY (SELECT started_at FROM sessions s WHERE s.session_id = c.session_id) DESC,
		c.completed_sequence`
	rows, err := d.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query indexed changes: %w", err)
	}
	defer rows.Close()
	var changes []Change
	for rows.Next() {
		var change Change
		if err := rows.Scan(&change.SessionID, &change.Prompt, &change.Tool, &change.ToolSummary,
			&change.StartedEventID, &change.CompletedEventID, &change.StartedSequence,
			&change.CompletedSequence, &change.BeforeTree, &change.AfterTree, &change.Patch); err != nil {
			return nil, fmt.Errorf("scan indexed change: %w", err)
		}
		files, err := d.filesForChange(ctx, change)
		if err != nil {
			return nil, err
		}
		change.Files = files
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

func (d *Database) filesForChange(ctx context.Context, change Change) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT path FROM change_files
		WHERE session_id = ? AND started_event_id = ? AND completed_event_id = ? ORDER BY path`,
		change.SessionID, change.StartedEventID, change.CompletedEventID)
	if err != nil {
		return nil, fmt.Errorf("query indexed changed files: %w", err)
	}
	defer rows.Close()
	var files []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	return files, rows.Err()
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

func (d *Database) History(ctx context.Context, versionID string) (History, error) {
	current, err := scanEntity(d.db.QueryRowContext(ctx, entitySelect+" WHERE version_id = ?", versionID))
	if err != nil {
		return History{}, fmt.Errorf("query lineage entity: %w", err)
	}
	edges, err := d.allEdges(ctx)
	if err != nil {
		return History{}, err
	}
	connected := map[string]bool{versionID: true}
	changed := true
	for changed {
		changed = false
		for _, edge := range edges {
			if connected[edge.Edge.FromVersionID] || connected[edge.Edge.ToVersionID] {
				if !connected[edge.Edge.FromVersionID] || !connected[edge.Edge.ToVersionID] {
					changed = true
				}
				connected[edge.Edge.FromVersionID] = true
				connected[edge.Edge.ToVersionID] = true
			}
		}
	}
	history := History{Current: current}
	for _, edge := range edges {
		if connected[edge.Edge.FromVersionID] && connected[edge.Edge.ToVersionID] {
			history.Edges = append(history.Edges, edge)
		}
	}
	for id := range connected {
		value, err := scanEntity(d.db.QueryRowContext(ctx, entitySelect+" WHERE version_id = ?", id))
		if err != nil {
			return History{}, fmt.Errorf("query connected lineage entity: %w", err)
		}
		history.Nodes = append(history.Nodes, value)
	}
	sort.Slice(history.Nodes, func(i, j int) bool {
		if history.Nodes[i].TreeID == history.Nodes[j].TreeID {
			return history.Nodes[i].Path < history.Nodes[j].Path
		}
		return history.Nodes[i].TreeID < history.Nodes[j].TreeID
	})
	return history, nil
}

func (d *Database) allEdges(ctx context.Context) ([]LineageEdge, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT session_id, completed_event_id, from_version_id,
		to_version_id, relation, method, confidence, evidence FROM lineage_edges`)
	if err != nil {
		return nil, fmt.Errorf("query lineage edges: %w", err)
	}
	defer rows.Close()
	var edges []LineageEdge
	for rows.Next() {
		var value LineageEdge
		if err := rows.Scan(&value.SessionID, &value.CompletedEventID, &value.Edge.FromVersionID,
			&value.Edge.ToVersionID, &value.Edge.Relation, &value.Edge.Method,
			&value.Edge.Confidence, &value.Edge.Evidence); err != nil {
			return nil, fmt.Errorf("scan lineage edge: %w", err)
		}
		edges = append(edges, value)
	}
	return edges, rows.Err()
}

func (d *Database) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{Path: d.path}
	var err error
	if stats.Fingerprint, err = d.Fingerprint(ctx); err != nil {
		return Stats{}, err
	}
	for table, destination := range map[string]*int{
		"sessions": &stats.Sessions, "events": &stats.Events, "changes": &stats.Changes,
		"change_files": &stats.Files, "entities": &stats.Entities, "lineage_edges": &stats.Edges,
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
		return "", errors.New("no captured WhyDiff sessions")
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
	qualified_name, start_line, end_line, content_hash, structure_hash, structure FROM entities`

func scanEntity(row rowScanner) (entity.Entity, error) {
	var value entity.Entity
	err := row.Scan(&value.VersionID, &value.TreeID, &value.Path, &value.Language, &value.Kind,
		&value.Name, &value.QualifiedName, &value.StartLine, &value.EndLine, &value.ContentHash,
		&value.StructureHash, &value.Structure)
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
  patch TEXT NOT NULL,
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
  structure_hash TEXT NOT NULL,
  structure TEXT NOT NULL
);
CREATE INDEX entities_location ON entities(tree_id, path, start_line, end_line);
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
