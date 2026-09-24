# why-diff

**Git tells you what changed. why-diff keeps the receipts for how it changed.**

Ask a coding agent to fix one bug and it may inspect twenty files, run a code
generator that rewrites an unrelated config file, change two functions, undo
one approach, and replace it with another. By the end, Git can show the final
diff—but not which prompt led to which tool call, when the unexpected file was
rewritten, which experiment was later undone, or what happened between a
failing test and a passing one.

why-diff records that missing context while the work happens. It is a local CLI
for Codex that connects prompts, tool calls, command results,
and intermediate repository states to the code changes left behind.

```console
$ why-diff why internal/auth/session.go:42
Target:  internal/auth/session.go:42
Session: 01K...
Prompt:  Fix the authentication timeout bug
Tool:    apply_patch — update session timeout handling

Evidence:
- Tool started:   01K...
- Tool completed: 01K...
- Before tree:    a8c...
- After tree:     f31...

Inference: the target changed between checkpoints immediately before and
after this tool call. This is strong temporal evidence, not proof of exclusive
causation.

Validation:
- `go test ./...` failed before the change
- The same command passed afterward
```

The answer is backed by captured event IDs and Git object IDs. It is not a
story generated from the final diff after the work is already over.

## 1. Install why-diff

why-diff is currently in development and has not been published to Homebrew
yet. For now, download the source and build the CLI.

Quick install, Homebrew, and prebuilt downloads are being prepared for v0.1;
see the [install guide](docs/install.md) for the release plan and commands.

Requirements:

- Go 1.27 or newer
- Git 2.42 or newer (for NUL-delimited batch object reads)
- Codex CLI 0.156.1 or newer for CLI capture (the version verified end to end)
- a C compiler for Tree-sitter (`xcode-select --install` on macOS if needed)

Download the repository:

```sh
git clone https://github.com/prsuyal/why-diff.git
cd why-diff
```

Build the binary into your local executable directory:

```sh
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/why-diff" ./cmd/why-diff
go build -o "$HOME/.local/bin/why-diff-hook" ./cmd/why-diff-hook
```

Make the binary available in the current terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Add that `export` line to `~/.zshrc`, `~/.bashrc`, or the startup file for your
shell if `$HOME/.local/bin` is not already on `PATH`.

Verify the installation:

```sh
command -v why-diff
command -v why-diff-hook
why-diff --help
```

You interact with `why-diff`. Generated agent hooks invoke the smaller
`why-diff-hook` recorder, so both executables must be on `PATH` before capture
can work.

## 2. Initialize a repository

For capture in every Git repository you use, configure user-level hooks once:

```sh
why-diff init --global
```

Global setup stores evidence inside each repository's own `.git/why-diff`
directory. It does not create a project marker, and hooks skip sessions outside
Git repositories. Review and trust the user hooks in your agent before starting
a fresh session.

For capture in one repository, change into that Git repository and run:

```sh
why-diff init
```

Use `why-diff disable --global` to remove user-level hooks; plain
`why-diff disable` removes repository hooks. If both are configured, the global
hook records the event once.

`why-diff init` creates a small `.why-diff.toml` project marker and adds
repository-level Codex hooks. Existing hooks and settings are
preserved, and running the command again is safe.

why-diff writes hooks to `.codex/hooks.json`. Trust the project in Codex, then
review and trust its hooks with `/hooks` before starting a fresh session.

Verify the setup before opening the agent:

```sh
why-diff doctor
```

`doctor` checks the repository, Git support, project marker, configured hooks,
both binaries on `PATH`, and captured provenance. A new repository will show a warning
that it has no sessions yet. That is expected.

## 3. Use your coding agent normally

Start a fresh Codex session in the initialized repository and
work normally. why-diff runs quietly through the hooks; there is no separate
recorder to keep open.

why-diff can observe prompts, tool starts and completions, command results,
permission requests, subagents, compaction, and session boundaries. Important
tool events also receive Git checkpoints, so changes made indirectly by shell
commands or generators are visible even when the tool response never names the
files it rewrote.

## 4. Inspect the captured session

After the agent has done some work, list the sessions why-diff saw:

```console
$ why-diff sessions
SESSION       STARTED                    EVENTS  WARNINGS  STATUS  FIRST PROMPT
01K...        2026-08-25T10:14:02-04:00  38      0         ended   Fix the authentication timeout bug
```

Then inspect the latest session:

```sh
why-diff show latest
```

This prints the observed timeline: prompts, tool calls, permission requests,
test commands, subagents, and session lifecycle events. Events with repository
snapshots are marked with `[checkpoint]`.

## Command reference

### Setup and health

| Command                                        | What it does                                                                                                                                                |
| ---------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `why-diff init [--global]`                   | Adds repository or user-level Codex hooks.                                                                                                                |
| `why-diff doctor`                               | Checks the Git repository, project marker, hooks, executable on `PATH`, stored sessions, and capture warnings. Exits non-zero when the setup is not usable. |
| `why-diff disable [--global]`                   | Removes repository or user-level why-diff hooks. It preserves unrelated agent settings and all previously captured provenance.                                  |

### Reading a session

| Command                                           | What it does                                                                                                                    |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `why-diff sessions`                                | Lists captured sessions newest first, including event count, warning count, status, and first prompt.                           |
| `why-diff show [session]`                          | Prints the normalized event timeline. Defaults to `latest`.                                                                     |
| `why-diff diff [session]`                          | Shows file changes observed between checkpoints around tool calls. Defaults to `latest`.                                        |
| `why-diff why <file[:line]>`                       | Finds the captured tool call that changed a file or post-change line and prints its prompt, evidence IDs, Git trees, and patch. |
| `why-diff why <file[:line]> --session <session>`   | Runs the same attribution query within one selected session.                                                                    |
| `why-diff lineage <file:line>`                     | Traces a containing function, method, class, or type across captured edits, renames, and moves.                                 |
| `why-diff claims [session]`                        | Shows deterministic fail-change-pass claims. Defaults to `latest`.                                                              |
| `why-diff compare <session-a> <session-b>`         | Compares the prompts, changed files, and validation commands from two attempts.                                                 |
| `why-diff compare <session-a> <session-b> --patch` | Includes the exact checkpoint patches in the comparison.                                                                        |

### Optional model interpretation

| Command                                   | What it does                                                                                           |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `why-diff explain <file[:line]> --dry-run` | Prints the exact evidence packet without making an API request.                                        |
| `why-diff explain <file[:line]>`           | Sends that bounded packet to the configured OpenAI model and prints a citation-checked interpretation. |
| `why-diff compare <a> <b> --dry-run`       | Prints the comparison evidence packet without making an API request.                                   |
| `why-diff compare <a> <b> --explain`       | Requests a model interpretation of the comparison.                                                     |

Both `explain` and `compare --explain` accept `--model <name>` and
`--timeout <duration>`. `explain` also accepts `--session <session>`.

### Maintenance

| Command                                            | What it does                                                                                                         |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `why-diff finalize [session]`                       | Manually archives an active session under a private Git ref. Session-end hooks normally do this automatically.       |
| `why-diff index status`                             | Shows the sessions, events, changes, entities, and lineage edges in the SQLite query index.                          |
| `why-diff index rebuild`                            | Recreates the disposable SQLite index from canonical JSONL and Git provenance.                                      |
| `why-diff completion <bash\|fish\|powershell\|zsh>` | Generates a shell-completion script. Run the command with `--help` for the installation instructions for that shell. |
| `why-diff <command> --help`                         | Shows the authoritative syntax and flags for any command.                                                            |

Where a command accepts `[session]`, you can use the full session ID, an
unambiguous prefix, or `latest`. Omitting it selects `latest`.

## What why-diff catches that a final diff misses

An agent does not need to use an explicit editing tool to change a file. It can
run `generate.js`, a formatter, a migration, or a package-manager command that
quietly rewrites other files. The command output might only say “done.” why-diff
compares the repository checkpoints around that tool call, so it can still
report that `config.json` or a lockfile changed at that moment.

Experiments also disappear from normal Git history. An agent might add a cache,
test it, remove it, and implement a different solution—all before the next
commit. The final diff contains only the surviving solution. why-diff's timeline
retains the observed intermediate mutations and their relationship to the
surrounding prompts, tools, and tests.

why-diff also takes a baseline when the agent session begins. If you already
changed a timeout from 5 to 10 before starting the agent and the agent later
changes it from 10 to 30, why-diff attributes the session's 10-to-30 change. It
does not pretend the agent made your earlier edit.

## Understanding what why-diff tells you

why-diff is deliberately careful about the difference between something it saw
and something it inferred.

Suppose an agent runs `go test ./...` and it fails, changes two files, then runs
the same command and it passes. why-diff can state these as observations:

- the first test command failed;
- two files changed between captured checkpoints;
- the later test command passed.

why-diff can then infer that the changes are _consistent with resolving the
failure_. It cannot honestly claim that every changed line was necessary, or
that no outside action contributed to the result.

That boundary appears directly in command output. When evidence is incomplete,
why-diff reports a warning or says it cannot attribute the target instead of
filling the gap with a confident-sounding explanation.

### Why a line changed

```sh
why-diff why internal/auth/session.go:42
```

`why` finds a captured tool call whose before-and-after checkpoints contain a
change to the requested file or post-change line. It shows the relevant prompt,
tool, event IDs, Git tree IDs, and patch.

Use `--session` when you want to search a particular attempt:

```sh
why-diff why internal/auth/session.go:42 --session 01K...
```

### Where a function went

```sh
why-diff lineage internal/auth/session.go:42
```

why-diff parses the checkpointed file with Tree-sitter and finds the narrowest
function, method, class, interface, enum, or type containing that line. It then
walks the entity versions linked across captured mutations. Exact names and
content produce strong matches; structural similarity can recover a rename
that also changed the body. Every edge prints its matching method and
confidence, and ambiguous candidates are left unlinked.

### Whether a change resolved a test failure

```sh
why-diff claims latest
```

When why-diff observes a test command fail, sees repository changes, and later
observes the same command pass, it produces a reproducible
`test_fail_change_pass/v1` claim. The claim cites the failed command, change
events, passing command, and affected files.

why-diff uses structured tool results for pass/fail evidence. It does not guess
from arbitrary terminal output containing words such as “passed” or “failed.”

### How two attempts differed

```sh
why-diff compare 01K... 01J...
```

`compare` shows the prompts, changed files, validation commands, and evidence
shared by or unique to two sessions. Add `--patch` to include the exact diffs.
It reports what differed without inventing a reason for the divergence.

## Optional AI explanations

why-diff's normal capture and query commands do not require an LLM or an API
key. They work from local evidence.

If you want a higher-level interpretation, `explain` can send a bounded
evidence packet to the OpenAI Responses API:

```sh
export OPENAI_API_KEY=...
why-diff explain internal/auth/session.go:42
```

The packet contains the relevant redacted event summaries, prompt, checkpoint
patch, and deterministic claims—not the complete agent transcript. Preview the
exact JSON without making a network request:

```sh
why-diff explain internal/auth/session.go:42 --dry-run
```

Model output is labeled as an interpretation. Every generated claim must cite
an evidence ID in the packet, and why-diff rejects responses that invent
citations. The request uses `store: false`, and why-diff does not persist the
model response.

Optional configuration:

```sh
export WHY_DIFF_OPENAI_MODEL=gpt-5.4-mini
export OPENAI_BASE_URL=https://api.openai.com/v1
```

Running `explain` is an explicit network and cost boundary. Hook capture never
calls a model automatically.

## How it works

An agent already knows when it receives a prompt, starts a tool, finishes a
command, or ends a session. Codex exposes those moments through
hooks. why-diff listens to the hooks and turns their different JSON formats into
one shared event format.

```text
Codex
        │
        │ project hook JSON
        ▼
 why-diff-hook (small recorder)
        │
        ▼
 provider adapter
        │
        │ normalized, redacted event
        ▼
 append-only session log ────── Git checkpoint
        │                            │
        └──────────────┬─────────────┘
                       ▼
              canonical evidence
                       │
              rebuildable SQLite
                │             │
                ▼             ▼
       indexed queries   Tree-sitter entities
                └───────┬─────┘
                        ▼
          timeline, why, claims, lineage
```

Before and after important tool events, why-diff asks Git to snapshot three
views of the repository: `HEAD`, the staging area, and the working tree. It
builds those snapshots with a temporary Git index. Your real staging area is
left alone—why-diff does not stage files, switch branches, rewrite source
commits, or change the working tree.

Each normalized event is appended to a per-session JSONL log. Appending one
line at a time means capture does not rewrite the history it has already
recorded. It also preserves the redacted provider payload, so a future adapter
can understand fields the current version did not recognize.

When a session ends, why-diff archives its evidence in a provenance commit under
`refs/why-diff/sessions/*`. These are private Git refs: ordinary pushes do not
publish them. The commit keeps the event log and checkpoint trees reachable,
including intermediate code states that never appeared in a normal source
commit.

Queries combine the event timeline with differences between checkpoint trees.
That is how `why` can connect a prompt and tool call to the exact patch observed
around it, while still being honest that timing is evidence rather than proof
of causation.

The first query builds `.git/why-diff/index.sqlite`. It indexes session
metadata, normalized events, checkpoint changes, changed paths, code entities,
and lineage edges. Later queries use indexed paths and event IDs instead of
rescanning every JSONL record and diffing every checkpoint pair. why-diff checks
a lightweight fingerprint of the live logs and private refs before using the
index, then incrementally refreshes it when canonical evidence changes.

For ordinary append-only sessions, why-diff updates only the new events and
newly completed changes in one SQLite transaction. Cold reconstruction batches
Git object reads through one `git cat-file` process, and Tree-sitter results are
cached by Git blob ID and language so identical source is not parsed twice.
Lineage walks use recursive SQLite queries rather than loading the repository's
entire entity graph into memory.

The database is deliberately disposable. Deleting `index.sqlite` loses no
evidence: `why-diff index rebuild` or the next query reconstructs it from the
append-only logs and Git objects. This keeps the authoritative history easy to
audit while giving interactive commands a relational query layer.

## Built to stay out of your way

why-diff runs inside agent hooks, so capture needs to be quick. Hooks use a
dedicated **3.2 MiB** recorder instead of loading the **19.2 MiB** query CLI and
its SQLite, Tree-sitter, Cobra, and model-provider code.

| End-to-end Codex hook process  |          p50 |          p95 |
| ------------------------------ | -----------: | -----------: |
| Prompt event                   | 22.0–22.9 ms | 23.2–25.1 ms |
| Tool event with Git checkpoint | 54.2–54.9 ms | 55.7–59.1 ms |

These measurements include OS process startup, CLI parsing, redaction, locking,
repository lookup, and durable event-log writes. They are ranges from two one-second samples on
an Apple M4 Pro with Go 1.27, local filesystem storage, and the September 2026
release-candidate build, not a cross-machine latency promise.

Reproduce the benchmark:

```sh
go test ./internal/cli -run '^$' -bench '^BenchmarkHookProcess$' \
  -benchmem -benchtime=1s -count=3
```

SQLite also changes the cost of repeated attribution queries. In a local test
session with 21 events and 10 checkpointed edits, the same `why` lookup took
**6.45–7.38 ms** with a current index, compared with **178–202 ms** after deleting
the index and forcing a complete reconstruction from canonical evidence. The
median samples were about **30×** apart for this controlled workload.

| Attribution query          |       time/op | allocations/op |
| -------------------------- | ------------: | -------------: |
| Current SQLite index       |  6.45–7.38 ms |    1,913–1,914 |
| Forced canonical rebuild   |    178–202 ms |    6,274–6,298 |

Reproduce the query benchmark:

```sh
go test ./internal/query -run '^$' \
  -bench '^BenchmarkWhySQLiteProjection$' -benchmem -benchtime=1s -count=3
```

This measures a deliberately cold rebuild against a current local projection;
it is not a claim that every repository or query will see the same ratio.
Normal appended events update the existing database transactionally instead of
triggering this cold reconstruction.

Synthetic scale tests keep indexed session summaries around **11 µs** from
1,000 through 100,000 events. At 100,000 events, the projection uses about
**562 bytes per event**, and streaming the complete timeline took about
**163 ms** without retaining all events in memory.

```sh
go test ./internal/indexdb -run '^$' -bench '^BenchmarkSQLiteScale$' \
  -benchmem -benchtime=200ms
```

## Privacy and control

why-diff records development activity, so it is worth knowing exactly where the
boundaries are.

**Does hook capture send anything over the network?**

No. Capture, checkpoints, storage, and deterministic queries are local. Only an
explicit `why-diff explain` or `why-diff compare --explain` request contacts a
model provider.

**Where is the evidence stored?**

Live session data is stored under `.git/why-diff`. Completed sessions are also
archived under private `refs/why-diff/sessions/*` refs.

Global setup captures activity in every Git repository used with a configured
agent. Choose repository setup if you want to limit capture to selected projects.

**Can checkpoints contain source code?**

Yes. Checkpoints include tracked and non-ignored working-tree files, much like
local Git objects. Ignored files are excluded. Treat `.git/why-diff` and why-diff
refs with the same care as the repository itself.

**What gets redacted?**

Known secret-shaped JSON fields and values are redacted before events are
stored. Redaction is a safety layer, not a promise that arbitrary source code
contains no sensitive information.

**What happens if capture fails?**

why-diff warns and lets the agent continue. This fail-open behavior keeps a
locked store or broken checkpoint from stopping the coding session, but it can
leave an evidence gap. Warnings remain attached to the affected session.

**How do I turn it off?**

```sh
why-diff disable
```

This removes why-diff's project marker and its Codex handlers
while preserving unrelated agent settings. Previously captured provenance is
retained.

## Troubleshooting

### No sessions appear

Run:

```sh
why-diff doctor
```

Confirm that the correct provider is configured, both `why-diff` and
`why-diff-hook` are on `PATH`, and the generated project hooks have been trusted.
For repository setup, trust the project in Codex and review the hooks with
`/hooks`; Codex skips project hooks in untrusted projects. Then start a fresh
session; an already-running session may not have loaded the new hooks. Use
Codex CLI 0.156.1 or newer for the verified CLI path.

### `doctor` says the development binary is different

The binary you are running is not the same binary the hooks will find on
`PATH`. Rebuild it:

```sh
go build -o "$HOME/.local/bin/why-diff" ./cmd/why-diff
go build -o "$HOME/.local/bin/why-diff-hook" ./cmd/why-diff-hook
```

### `why` cannot attribute a file or line

The selected session may not contain checkpoints around the change, the target
may have changed outside a captured tool call, or a capture warning may have
left a gap. Use `why-diff show <session>` to inspect the timeline and warnings,
then `why-diff diff <session>` to see the changes why-diff can support.

### A session ended without being archived

Session-end hooks normally archive automatically. You can finalize an active
session manually:

```sh
why-diff finalize latest
```

### The SQLite index is missing or damaged

The index is only a query cache. Rebuild it from the canonical event logs and
Git objects:

```sh
why-diff index rebuild
```

### Capture stopped during a log write

On the next append, why-diff preserves an incomplete JSONL suffix in a private
`corrupt-tail-*.bin` file, resumes after the last complete event, and records a
`log_tail_recovered` warning. It never silently rewrites a complete event to
repair the log.

## Development

Run the full checks:

```sh
go test -race ./...
go vet ./...
go build -o ./why-diff ./cmd/why-diff
```

why-diff is licensed under the [MIT License](LICENSE).
