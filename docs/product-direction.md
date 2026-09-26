# why-diff product direction

## The job

An agent finishes a task and leaves a surprising line in the working tree. The developer—or another agent—needs to answer four questions quickly:

1. What changed, including edits later undone?
2. Which recorded prompt and tool call bracketed this change?
3. What tests and other observations surround it?
4. What remains uncertain, and what should I inspect or run next?

why-diff is the local evidence layer for that investigation. It does not read the agent's mind, prove that one edit fixed a test, or control the agent's next action. The user makes that decision with better evidence.

## One system, four layers

| Layer | Current role | Contract |
| --- | --- | --- |
| Capture | Codex hooks record prompts, tool events, results, and Git checkpoints; v0.2.0 includes four experimental agent adapters | Missing or failed capture must be visible; never silently fill gaps |
| Store | Local event stream, Git objects, and indexes inside the repository | Keep original event IDs and tree IDs available for verification |
| Query | `sessions`, `show`, `diff`, `why`, `lineage`, `claims`, `compare`, `doctor` | Lead with the relevant answer, then show the events and patch behind it |
| Interpret | Human judgment or optional bounded `explain` call | Separate observed facts, temporal inference, and unknown intent |

The website and docs are projections of these same layers. An animated view may omit details for clarity, but its facts must match a reproducible CLI fixture and its full output must remain reachable.

## The agent in the driver's seat

The efficient loop is **check → narrow → inspect → verify → act**:

1. Run `why-diff doctor` when capture looks suspect. A missing hook or checkpoint changes how much to trust every later answer.
2. Find the run with `sessions`, then scan `show` or `diff` for the changed file and the surrounding tool calls.
3. Ask `why file:line` for a particular surprise. Read the request and patch first, then inspect the tool interval and before/after tree IDs if needed. A line can have more than one plausible surrounding event.
4. Compare the claim with test events and the current working tree. A failed-then-passed test is a sequence, not proof that each changed line was necessary.
5. Decide whether to keep, revert, retest, or ask the coding agent a narrower question. why-diff supplies evidence for that choice; it does not choose for the user.

Every query should help the next query become narrower. The `why` answer now leads with the tool interval, request, and patch, then puts verification IDs last. It establishes where an edit appeared, not why the agent chose a value. Optional model interpretation receives a bounded packet, with cited event IDs; normal capture and queries stay local and do not spend model tokens.

## What is shipped and what is proposed

The current CLI provides the commands above and human-readable output. The following is a product plan, not a description of released behavior.

| Priority | Proposed change | Why it matters | Done when |
| --- | --- | --- | --- |
| 1 | Structured `--json` output for health, sessions, diff, why, and claims | Agents can parse results without brittle terminal scraping | A schema has stable field names, evidence IDs, uncertainty fields, and fixture tests |
| 2 | Explicit coverage in each answer | An agent must know whether the relevant interval was actually observed | Queries report missing hooks/checkpoints and distinguish no match from incomplete capture |
| 3 | Scoped, cheap queries by session, file, line, and event interval | Large repositories need an answer without dumping an entire run | Common investigations return bounded output with a path to expand exact evidence |
| 4 | Local, append-only investigation notes linked to event IDs | Useful conclusions and corrections should survive into the next attempt | Notes are inspectable, reversible, and never presented as captured facts |
| 5 | Compare an earlier attempt with a later one using the same evidence model | An agent can learn which hypothesis or edit changed between attempts | The comparison cites both runs and marks unobserved intervals |

Do not add autonomous recommendations until coverage and uncertainty are reliable. A confident next-step suggestion built on a missed hook is worse than a short, honest answer.

## Rules for every surface

- Keep **observation**, **inference**, and **user/agent annotation** visibly distinct.
- Preserve the exact event and Git IDs behind any summary; display short IDs only when expansion reveals the full value.
- Make the most common investigation cheap: local indexing, bounded output, and no model call by default.
- Use one reproducible fixture across CLI examples, docs, and website demo. If the CLI changes, refresh the fixture before changing the story.
- Test the failure path as carefully as the success path: no hooks, partial events, dirty tree, renamed file, undone edit, and failed test with no later pass.
- Never advertise a live integration or outcome that has not been verified.

The website interaction and visual rules live in [website/DESIGN.md](../website/DESIGN.md).

## Agent integrations

The capture boundary is the agent host's hook events, not its model API. A new adapter must map prompts, tool start/end events, and working directories into the existing event and checkpoint format. Only Codex has been verified end to end. The v0.2.0 release includes the other adapters as experimental; do not call them live-verified.

The source tree now has hook adapters and `init --agent` setup for [Claude Code](https://code.claude.com/docs/en/hooks), [Cursor](https://prod.cursor.com/docs/hooks), [Gemini CLI](https://geminicli.com/docs/hooks/reference/), and [GitHub Copilot CLI](https://docs.github.com/en/copilot/reference/hooks-reference). Tests send payloads shaped like each host's documented hook events through the recorder, edit a real Git worktree, and query the changed line. These are contract and local user-flow tests, not live sessions of the four hosts. Codex remains the only live-verified integration in release copy. See [agent-adapters.md](agent-adapters.md) for setup and the remaining live checks.

Cursor supplies `tool_use_id`. Gemini and Copilot's documented tool hooks do not supply a call ID. The adapter derives one from the session, tool name, and input, marks that fact in capture warnings, and declines to attribute overlapping identical calls. A change observed during another concurrent call can still be ambiguous; do not describe the derived key as a provider ID.
