# Experimental agent hooks

The v0.2.0 release includes these adapters. [Install why-diff](install.md) and
make sure both `why-diff` and `why-diff-hook` are on your `PATH`.

Pick an agent and a scope. Repeat `--agent` to configure more than one.

```sh
why-diff init --agent claude          # this repository
why-diff init --agent cursor --global # all local Git repositories
why-diff init --agent gemini
why-diff init --agent copilot
why-diff doctor --agent cursor
```

| Agent | Project hooks | User hooks | Events captured |
| --- | --- | --- | --- |
| Claude Code | `.claude/settings.json` | `~/.claude/settings.json` | Prompts, tool start/end/failure, session and other lifecycle events |
| Cursor | `.cursor/hooks.json` | `~/.cursor/hooks.json` | Prompts, tool start/end/failure, session boundaries |
| Gemini CLI | `.gemini/settings.json` | `~/.gemini/settings.json` | Prompts, tool start/end, session boundaries |
| GitHub Copilot CLI | `.github/copilot/settings.local.json` | `~/.copilot/hooks/why-diff.json` | Prompts, tool start/end/failure, session boundaries |

`why-diff disable` removes the generated project hooks; `why-diff disable --global` removes user hooks. Captured evidence stays in Git. Review each agent's hook file and restart the agent after setup. Run `why-diff sessions` and `why-diff why path/to/file:line` after an edit. Copilot uses its local settings file here because its `.github/hooks/*.json` files also run in cloud agents, where your local `why-diff-hook` binary is unavailable.

The four adapters have passed tests that feed documented hook payloads through `why-diff-hook`, change a real Git file, and query the result. They have **not** been exercised in live Claude Code, Cursor, Gemini CLI, or Copilot sessions. A live check must confirm hook invocation, permission behavior, tool results, and the `why` answer before removing the experimental label. Cursor's `preToolUse` hook returns `ask` so why-diff does not intentionally approve tools; this especially needs a live permission check.

Gemini and Copilot do not document a tool call ID in their hook payloads. why-diff derives a matching key from session, tool name, and input and records a warning. If identical calls overlap, it omits their attribution rather than picking one. Use the surrounding event timeline when a result is ambiguous.
