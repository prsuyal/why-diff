# WhyDiff

**Git tells you what changed. WhyDiff keeps the receipts for how it changed.**

Ask a coding agent to fix one bug and it may inspect twenty files, run a code
generator that rewrites an unrelated config file, change two functions, undo
one approach, and replace it with another. By the end, Git can show the final
diff—but not which prompt led to which tool call, when the unexpected file was
rewritten, which experiment was later undone, or what happened between a
failing test and a passing one.

WhyDiff records that missing context while the work happens. It is a local CLI
for Codex and Claude Code that connects prompts, tool calls, command results,
and intermediate repository states to the code changes left behind.

```console
$ whydiff why internal/auth/session.go:42
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

## 1. Install WhyDiff

WhyDiff is currently in development and has not been published to Homebrew
yet. For now, download the source and build the CLI.

Requirements:

- Go 1.27 or newer
- Git

Download the repository:

```sh
git clone https://github.com/prsuyal/why-diff.git
cd why-diff
```

Build the binary into your local executable directory:

```sh
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/whydiff" ./cmd/whydiff
```

Make the binary available in the current terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Add that `export` line to `~/.zshrc`, `~/.bashrc`, or the startup file for your
shell if `$HOME/.local/bin` is not already on `PATH`.

Verify the installation:

```sh
command -v whydiff
whydiff --help
```

The generated agent hooks invoke `whydiff` by name, so the binary must be on
`PATH` before capture can work.

## 2. Initialize a repository

Change into the Git repository where you use your coding agent, then choose the
integration you want:

```sh
# Codex
whydiff init --provider codex

# Claude Code
whydiff init --provider claude

# Both
whydiff init --provider all
```

Running `whydiff init` without `--provider` is the same as selecting Codex.

`whydiff init` creates a small `.whydiff.toml` project marker and adds
repository-level hooks for the selected agent. Existing hooks and settings are
preserved, and running the command again is safe.

WhyDiff writes Codex hooks to `.codex/hooks.json` and Claude Code hooks to
`.claude/settings.json`. Review and trust those project hooks before starting
the agent.

Verify the setup before opening the agent:

```sh
whydiff doctor
```

`doctor` checks the repository, project marker, configured hooks, `whydiff`
binary on `PATH`, and captured provenance. A new repository will show a warning
that it has no sessions yet. That is expected.

## 3. Use your coding agent normally

Start a fresh Codex or Claude Code session in the initialized repository and
work normally. WhyDiff runs quietly through the hooks; there is no separate
recorder to keep open.

WhyDiff can observe prompts, tool starts and completions, command results,
permission requests, subagents, compaction, and session boundaries. Important
tool events also receive Git checkpoints, so changes made indirectly by shell
commands or generators are visible even when the tool response never names the
files it rewrote.

## 4. Inspect the captured session

After the agent has done some work, list the sessions WhyDiff saw:

```console
$ whydiff sessions
SESSION       STARTED                    EVENTS  WARNINGS  STATUS  FIRST PROMPT
01K...        2026-08-25T10:14:02-04:00  38      0         ended   Fix the authentication timeout bug
```

Then inspect the latest session:

```sh
whydiff show latest
```

This prints the observed timeline: prompts, tool calls, permission requests,
test commands, subagents, and session lifecycle events. Events with repository
snapshots are marked with `[checkpoint]`.

## Command reference

### Setup and health

| Command                                        | What it does                                                                                                                                                |
| ---------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `whydiff init [--provider codex\|claude\|all]` | Adds WhyDiff's repository-level agent hooks. Codex is the default.                                                                                          |
| `whydiff doctor`                               | Checks the Git repository, project marker, hooks, executable on `PATH`, stored sessions, and capture warnings. Exits non-zero when the setup is not usable. |
| `whydiff disable`                              | Removes WhyDiff's marker and hook handlers. It preserves unrelated agent settings and all previously captured provenance.                                   |

### Reading a session

| Command                                           | What it does                                                                                                                    |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `whydiff sessions`                                | Lists captured sessions newest first, including event count, warning count, status, and first prompt.                           |
| `whydiff show [session]`                          | Prints the normalized event timeline. Defaults to `latest`.                                                                     |
| `whydiff diff [session]`                          | Shows file changes observed between checkpoints around tool calls. Defaults to `latest`.                                        |
| `whydiff why <file[:line]>`                       | Finds the captured tool call that changed a file or post-change line and prints its prompt, evidence IDs, Git trees, and patch. |
| `whydiff why <file[:line]> --session <session>`   | Runs the same attribution query within one selected session.                                                                    |
| `whydiff claims [session]`                        | Shows deterministic fail-change-pass claims. Defaults to `latest`.                                                              |
| `whydiff compare <session-a> <session-b>`         | Compares the prompts, changed files, and validation commands from two attempts.                                                 |
| `whydiff compare <session-a> <session-b> --patch` | Includes the exact checkpoint patches in the comparison.                                                                        |

### Optional model interpretation

| Command                                   | What it does                                                                                           |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `whydiff explain <file[:line]> --dry-run` | Prints the exact evidence packet without making an API request.                                        |
| `whydiff explain <file[:line]>`           | Sends that bounded packet to the configured OpenAI model and prints a citation-checked interpretation. |
| `whydiff compare <a> <b> --dry-run`       | Prints the comparison evidence packet without making an API request.                                   |
| `whydiff compare <a> <b> --explain`       | Requests a model interpretation of the comparison.                                                     |

Both `explain` and `compare --explain` accept `--model <name>` and
`--timeout <duration>`. `explain` also accepts `--session <session>`.

### Maintenance

| Command                                            | What it does                                                                                                         |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `whydiff finalize [session]`                       | Manually archives an active session under a private Git ref. Session-end hooks normally do this automatically.       |
| `whydiff completion <bash\|fish\|powershell\|zsh>` | Generates a shell-completion script. Run the command with `--help` for the installation instructions for that shell. |
| `whydiff <command> --help`                         | Shows the authoritative syntax and flags for any command.                                                            |

Where a command accepts `[session]`, you can use the full session ID, an
unambiguous prefix, or `latest`. Omitting it selects `latest`.

## What WhyDiff catches that a final diff misses

An agent does not need to use an explicit editing tool to change a file. It can
run `generate.js`, a formatter, a migration, or a package-manager command that
quietly rewrites other files. The command output might only say “done.” WhyDiff
compares the repository checkpoints around that tool call, so it can still
report that `config.json` or a lockfile changed at that moment.

Experiments also disappear from normal Git history. An agent might add a cache,
test it, remove it, and implement a different solution—all before the next
commit. The final diff contains only the surviving solution. WhyDiff's timeline
retains the observed intermediate mutations and their relationship to the
surrounding prompts, tools, and tests.

WhyDiff also takes a baseline when the agent session begins. If you already
changed a timeout from 5 to 10 before starting the agent and the agent later
changes it from 10 to 30, WhyDiff attributes the session's 10-to-30 change. It
does not pretend the agent made your earlier edit.

## Understanding what WhyDiff tells you

WhyDiff is deliberately careful about the difference between something it saw
and something it inferred.

Suppose an agent runs `go test ./...` and it fails, changes two files, then runs
the same command and it passes. WhyDiff can state these as observations:

- the first test command failed;
- two files changed between captured checkpoints;
- the later test command passed.

WhyDiff can then infer that the changes are _consistent with resolving the
failure_. It cannot honestly claim that every changed line was necessary, or
that no outside action contributed to the result.

That boundary appears directly in command output. When evidence is incomplete,
WhyDiff reports a warning or says it cannot attribute the target instead of
filling the gap with a confident-sounding explanation.

### Why a line changed

```sh
whydiff why internal/auth/session.go:42
```

`why` finds a captured tool call whose before-and-after checkpoints contain a
change to the requested file or post-change line. It shows the relevant prompt,
tool, event IDs, Git tree IDs, and patch.

Use `--session` when you want to search a particular attempt:

```sh
whydiff why internal/auth/session.go:42 --session 01K...
```

### Whether a change resolved a test failure

```sh
whydiff claims latest
```

When WhyDiff observes a test command fail, sees repository changes, and later
observes the same command pass, it produces a reproducible
`test_fail_change_pass/v1` claim. The claim cites the failed command, change
events, passing command, and affected files.

WhyDiff uses structured tool results for pass/fail evidence. It does not guess
from arbitrary terminal output containing words such as “passed” or “failed.”

### How two attempts differed

```sh
whydiff compare 01K... 01J...
```

`compare` shows the prompts, changed files, validation commands, and evidence
shared by or unique to two sessions. Add `--patch` to include the exact diffs.
It reports what differed without inventing a reason for the divergence.

## Optional AI explanations

WhyDiff's normal capture and query commands do not require an LLM or an API
key. They work from local evidence.

If you want a higher-level interpretation, `explain` can send a bounded
evidence packet to the OpenAI Responses API:

```sh
export OPENAI_API_KEY=...
whydiff explain internal/auth/session.go:42
```

The packet contains the relevant redacted event summaries, prompt, checkpoint
patch, and deterministic claims—not the complete agent transcript. Preview the
exact JSON without making a network request:

```sh
whydiff explain internal/auth/session.go:42 --dry-run
```

Model output is labeled as an interpretation. Every generated claim must cite
an evidence ID in the packet, and WhyDiff rejects responses that invent
citations. The request uses `store: false`, and WhyDiff does not persist the
model response.

Optional configuration:

```sh
export WHYDIFF_OPENAI_MODEL=gpt-5.4-mini
export OPENAI_BASE_URL=https://api.openai.com/v1
```

Running `explain` is an explicit network and cost boundary. Hook capture never
calls a model automatically.

## How it works

An agent already knows when it receives a prompt, starts a tool, finishes a
command, or ends a session. Codex and Claude Code expose those moments through
hooks. WhyDiff listens to the hooks and turns their different JSON formats into
one shared event format.

```text
Codex or Claude Code
        │
        │ project hook JSON
        ▼
 provider adapter
        │
        │ normalized, redacted event
        ▼
 append-only session log ────── Git checkpoint
        │                            │
        └──────────────┬─────────────┘
                       ▼
           timeline, diff, claims, why
```

Before and after important tool events, WhyDiff asks Git to snapshot three
views of the repository: `HEAD`, the staging area, and the working tree. It
builds those snapshots with a temporary Git index. Your real staging area is
left alone—WhyDiff does not stage files, switch branches, rewrite source
commits, or change the working tree.

Each normalized event is appended to a per-session JSONL log. Appending one
line at a time means capture does not rewrite the history it has already
recorded. It also preserves the redacted provider payload, so a future adapter
can understand fields the current version did not recognize.

When a session ends, WhyDiff archives its evidence in a provenance commit under
`refs/whydiff/sessions/*`. These are private Git refs: ordinary pushes do not
publish them. The commit keeps the event log and checkpoint trees reachable,
including intermediate code states that never appeared in a normal source
commit.

Queries combine the event timeline with differences between checkpoint trees.
That is how `why` can connect a prompt and tool call to the exact patch observed
around it, while still being honest that timing is evidence rather than proof
of causation.

## Built to stay out of your way

WhyDiff runs inside agent hooks, so capture needs to be quick. In local
benchmarks, ordinary events complete in about **10 ms** and events requiring a
Git checkpoint complete in about **50 ms**.

| Codex hook capture             |          p50 |          p95 |
| ------------------------------ | -----------: | -----------: |
| Prompt event                   |   8.0–9.0 ms |  9.9–10.1 ms |
| Tool event with Git checkpoint | 47.5–49.8 ms | 51.0–53.8 ms |

Measurements are the ranges from three two-second samples on an Apple M4 Pro
with Go 1.27 and local filesystem storage. They are a development baseline,
not a cross-machine latency promise.

Reproduce the benchmark:

```sh
go test ./internal/ingest -run '^$' -bench '^BenchmarkCodex' \
  -benchmem -benchtime=2s -count=3
```

## Privacy and control

WhyDiff records development activity, so it is worth knowing exactly where the
boundaries are.

**Does hook capture send anything over the network?**

No. Capture, checkpoints, storage, and deterministic queries are local. Only an
explicit `whydiff explain` or `whydiff compare --explain` request contacts a
model provider.

**Where is the evidence stored?**

Live session data is stored under `.git/whydiff`. Completed sessions are also
archived under private `refs/whydiff/sessions/*` refs.

**Can checkpoints contain source code?**

Yes. Checkpoints include tracked and non-ignored working-tree files, much like
local Git objects. Ignored files are excluded. Treat `.git/whydiff` and WhyDiff
refs with the same care as the repository itself.

**What gets redacted?**

Known secret-shaped JSON fields and values are redacted before events are
stored. Redaction is a safety layer, not a promise that arbitrary source code
contains no sensitive information.

**What happens if capture fails?**

WhyDiff warns and lets the agent continue. This fail-open behavior keeps a
locked store or broken checkpoint from stopping the coding session, but it can
leave an evidence gap. Warnings remain attached to the affected session.

**How do I turn it off?**

```sh
whydiff disable
```

This removes WhyDiff's project marker and its Codex and Claude Code handlers
while preserving unrelated agent settings. Previously captured provenance is
retained.

## Troubleshooting

### No sessions appear

Run:

```sh
whydiff doctor
```

Confirm that the correct provider is configured, `whydiff` is on `PATH`, and
the generated project hooks have been trusted. Then start a fresh agent
session; an already-running session may not have loaded the new hooks.

### `doctor` says the development binary is different

The binary you are running is not the same binary the hooks will find on
`PATH`. Rebuild it:

```sh
go build -o "$HOME/.local/bin/whydiff" ./cmd/whydiff
```

### `why` cannot attribute a file or line

The selected session may not contain checkpoints around the change, the target
may have changed outside a captured tool call, or a capture warning may have
left a gap. Use `whydiff show <session>` to inspect the timeline and warnings,
then `whydiff diff <session>` to see the changes WhyDiff can support.

### A session ended without being archived

Session-end hooks normally archive automatically. You can finalize an active
session manually:

```sh
whydiff finalize latest
```

### Capture stopped during a log write

On the next append, WhyDiff preserves an incomplete JSONL suffix in a private
`corrupt-tail-*.bin` file, resumes after the last complete event, and records a
`log_tail_recovered` warning. It never silently rewrites a complete event to
repair the log.

## Development

Run the full checks:

```sh
go test -race ./...
go vet ./...
go build -o ./whydiff ./cmd/whydiff
```

WhyDiff is licensed under the [MIT License](LICENSE).
