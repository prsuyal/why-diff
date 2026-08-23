# WhyDiff

Git records code states. WhyDiff records evidence about how an AI coding
session moved between those states, so later explanations can cite observable
events instead of inventing a rationale from the final diff.

WhyDiff is currently under active development. The first implemented slice is
the local capture kernel:

```text
Codex hook JSON
  -> Codex adapter
  -> provider-neutral event
  -> pre-storage redaction
  -> append-only active-session JSONL
```

The kernel deliberately stores observations, not causal claims. For example,
a failed test, an edit, and a later passing test are three observations. The
claim that the edit fixed the test will belong to a later attribution layer and
must cite those observations as evidence.

## Development

Requirements:

- Go 1.27 or newer
- Git

Run the checks:

```sh
go test -race ./...
go vet ./...
```

Build the CLI:

```sh
go build ./cmd/whydiff
```

Initialize a Git repository after `whydiff` is available on `PATH`:

```sh
whydiff init
```

Initialization creates a minimal `.whydiff.toml` project marker and safely
merges WhyDiff handlers into `.codex/hooks.json`. Existing handlers and unknown
JSON fields are preserved, and rerunning the command does not add duplicates.
Codex requires project hooks to be reviewed and trusted through `/hooks` before
they run.

The hook-facing command is intentionally internal for now:

```sh
whydiff internal ingest codex
```

It reads one [Codex lifecycle hook](https://developers.openai.com/codex/hooks)
JSON object from standard input. Capture failures are fail-open by default so
they do not stop the coding session; `--strict` is available for tests and
diagnostics.

## Current boundaries

This slice does not yet install the binary, checkpoint Git state, finalize
sessions into Git objects, build a SQLite projection, attribute changes, parse
code entities, or generate semantic explanations. Those capabilities will be
added as separate layers over the stable event log.
