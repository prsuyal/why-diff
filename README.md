# WhyDiff

Git records code states. WhyDiff records evidence about how an AI coding
session moved between those states, so later explanations can cite observable
events instead of inventing a rationale from the final diff.

WhyDiff is an early, local-first CLI. The current core works with Codex hooks
and deliberately distinguishes observations from causal claims. A prompt, a
failed test, an edit, and a passing test are observations; “that edit fixed the
test” is an inference that must cite those observations.

## Install

WhyDiff is currently in development. Build and run the CLI from this
repository:

```sh
go build -o ./whydiff ./cmd/whydiff
./whydiff --help
```

## How it works

```text
Codex lifecycle hook JSON on stdin
  -> Codex adapter
  -> provider-neutral event
  -> deterministic redaction
  -> append-only per-session JSONL
  -> non-invasive Git checkpoint before/after tool calls
  -> private Git archive when the session ends
  -> deterministic timeline, diff, and why queries
```

Every checkpoint records object IDs for `HEAD`, the user's real index, and the
working tree. WhyDiff creates the index and working-tree snapshots with
temporary Git indexes; it does not stage files, change branches, create a
source-code commit, or modify the developer's working tree.

Finalized sessions live under private `refs/whydiff/sessions/*` refs. Each
provenance commit contains the canonical `events.jsonl`, session metadata, and
references to every checkpoint tree. This keeps intermediate states reachable
from Git even if they never appeared in a normal commit. The writable live log
under `.git/whydiff/active` is treated as a projection: queries fall back to the
private Git archive if that live copy is removed.

## Development

Requirements:

- Go 1.27 or newer
- Git

Run the checks and build the CLI:

```sh
go test -race ./...
go vet ./...
go build -o ./whydiff ./cmd/whydiff
```

Run the reproducible hook-boundary benchmarks:

```sh
go test ./internal/ingest -run '^$' -bench '^BenchmarkCodex' -benchmem -count=5
```

On an Apple M4 Pro with Go 1.27 (three two-second samples), prompt capture
measured 10.0–11.1 ms p50 and 11.7–19.7 ms p95. Checkpointed tool capture
measured 49.0–51.0 ms p50 and 54.9–56.1 ms p95. These local filesystem
results are a baseline, not a cross-machine SLA.

For local development, put the binary somewhere on `PATH` before enabling the
generated hooks.

## Use

In a Git repository:

```sh
whydiff init
```

Initialization creates `.whydiff.toml` and safely merges WhyDiff handlers into
`.codex/hooks.json`. Existing handlers and unknown JSON fields are preserved;
rerunning the command is idempotent. Review and trust the project hooks through
`/hooks` in Codex before relying on capture.

After using Codex, inspect the captured evidence:

```sh
# List sessions, newest first.
whydiff sessions

# Show the normalized event timeline.
whydiff show latest

# Show every file mutation observed around a tool call.
whydiff diff latest

# Show replayable fail-change-pass claims.
whydiff claims latest

# Preview the bounded model evidence; this makes no network request.
whydiff explain internal/auth/auth.go:42 --dry-run

# Generate an explicitly labeled model interpretation.
OPENAI_API_KEY=... whydiff explain internal/auth/auth.go:42

# Compare two captured attempts using deterministic evidence.
whydiff compare 01K... 01J...

# Preview or semantically interpret the bounded comparison evidence.
whydiff compare 01K... 01J... --dry-run
OPENAI_API_KEY=... whydiff compare 01K... 01J... --explain

# Find the most recent captured tool call that changed a file.
whydiff why internal/auth/auth.go

# Narrow the evidence to a post-change line and/or session.
whydiff why internal/auth/auth.go:42 --session 01K...

# Manually archive an active session. SessionEnd does this automatically.
whydiff finalize latest
```

`whydiff why` currently makes a narrow deterministic claim: the target changed
between the checkpoints immediately before and after a particular tool call.
It prints the prompt, tool-call event IDs, tree IDs, and exact patch supporting
that claim. This is strong temporal evidence, not proof that the tool call was
the only cause. A `file:line` query uses the line number in that tool call's
post-change snapshot; cross-edit entity lineage is not implemented yet.

`whydiff compare` contrasts two captured sessions without assigning intent. It
shows their cited prompts, checkpointed change counts, validation outcomes,
and shared versus attempt-specific files and test commands. `--patch` includes
the exact checkpoint diffs. Optional `--explain` sends a bounded packet through
the same citation-validated semantic layer used by `whydiff explain`; the
offline comparison remains authoritative.

When the captured structured response explicitly reports a test command
failing, repository changes follow, and the same command explicitly passes,
`whydiff claims` derives a versioned `test_fail_change_pass/v1` claim. Each
claim has a stable ID and cites the failed command event, intervening change
events, passing command event, and affected files. Unstructured output text is
not treated as a pass/fail fact.

## Optional semantic enrichment

`whydiff explain <file[:line]>` lazily sends a bounded evidence packet to the
[OpenAI Responses API](https://developers.openai.com/api/reference/resources/responses/methods/create).
The packet contains relevant redacted event summaries, the prompt, checkpoint
patch, and deterministic validation claim—not the complete raw transcript. Use
`--dry-run` to inspect the exact JSON before sending anything.

The API response uses strict Structured Outputs. Every model-generated claim
must cite an evidence ID that exists in the packet; WhyDiff rejects the entire
response if the model invents a citation. Output is prominently labeled as a
model interpretation, and the underlying events remain authoritative. WhyDiff
sets `store: false` on the request and does not persist the semantic result yet.

Configuration:

```sh
export OPENAI_API_KEY=...
export WHYDIFF_OPENAI_MODEL=gpt-5.4-mini   # optional
export OPENAI_BASE_URL=https://api.openai.com/v1  # optional

whydiff explain path/to/file.go:42
```

The prompt and patch may contain repository code that deterministic event
redaction did not remove. Running `explain` is therefore an explicit network and
cost boundary; hook capture itself never calls a model.

The hook-facing command is internal:

```sh
whydiff internal ingest codex
```

It reads one Codex hook JSON object from standard input. Capture failures are
fail-open by default so WhyDiff does not stop the coding agent; `--strict` is
available for tests and diagnostics.

## Privacy and failure boundaries

- Known secret-shaped JSON fields and values are redacted before event storage.
- Unknown provider fields are retained after redaction so later adapter versions
  can reinterpret old evidence.
- Git checkpoints include tracked and non-ignored working-tree files. They are
  local, but may contain sensitive repository content just like local Git
  objects; ignored files are excluded.
- A locked or unavailable capture store produces a warning and lets Codex
  continue, which can leave an evidence gap.
- A checkpoint failure preserves the lifecycle event with a warning rather than
  pretending repository state was captured.

## Current boundaries

The core currently supports one capture provider (Codex), file/hunk
attribution, fail-change-pass validation claims, private local refs, and one
optional semantic provider (OpenAI). It does not yet include SQLite
acceleration, Tree-sitter entity lineage, cached/persisted semantic claims,
human annotations, shared provenance refs, or tamper-evident chaining.
