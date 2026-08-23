# WhyDiff

Git records code states. WhyDiff records evidence about how an AI coding
session moved between those states, so later explanations can cite observable
events instead of inventing a rationale from the final diff.

WhyDiff is an early, local-first CLI. The current core works with Codex hooks
and deliberately distinguishes observations from causal claims. A prompt, a
failed test, an edit, and a passing test are observations; “that edit fixed the
test” is an inference that must cite those observations.

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

For local development, put the binary somewhere on `PATH` before enabling the
generated hooks. Homebrew packaging is planned after the CLI surface settles.

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

The core currently supports one provider (Codex), file/hunk attribution, and
private local refs. It does not yet include SQLite acceleration, Tree-sitter
entity lineage, test-result claims, LLM semantic enrichment, human annotations,
shared provenance refs, tamper-evident chaining, or Homebrew distribution.
