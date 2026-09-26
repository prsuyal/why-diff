import type { Metadata } from "next";
import Link from "next/link";
import { Command } from "@/components/command";

export const metadata: Metadata = {
  title: "Docs — why-diff",
  description: "Install why-diff, capture a Codex session, and trace a changed file or line.",
};

const quick = "curl -fsSL https://raw.githubusercontent.com/prsuyal/why-diff/main/scripts/install.sh | sh";
const commandGroups = [
  { name: "Setup and health", entries: [
    { command: "why-diff init [--global] [--agent NAME]", detail: "Add hooks to this repository or your user settings. Defaults to Codex; repeat --agent for more than one host." },
    { command: "why-diff doctor [--agent NAME]", detail: "Check Git, hooks, installed binaries, sessions, and capture warnings." },
    { command: "why-diff disable [--global]", detail: "Remove why-diff hooks. Captured evidence stays in Git." },
  ] },
  { name: "Investigate", entries: [
    { command: "why-diff sessions", detail: "List captured sessions, newest first, with event and warning counts." },
    { command: "why-diff show [session]", detail: "Read the event timeline, including prompts, tool calls, and results." },
    { command: "why-diff diff [session]", detail: "See changes observed between tool checkpoints." },
    { command: "why-diff why <file[:line]>", detail: "Find the tool interval that contains the change, with its request, patch, and evidence IDs. Add --session to narrow the search." },
    { command: "why-diff lineage <file:line>", detail: "Follow a function, method, class, or type across captured edits and moves." },
    { command: "why-diff claims [session]", detail: "Find fail-change-pass test sequences in the recorded evidence." },
    { command: "why-diff compare <session-a> <session-b>", detail: "Compare prompts, changed files, and validation commands. Add --patch for checkpoint patches." },
  ] },
  { name: "Optional model interpretation", entries: [
    { command: "why-diff explain <file[:line]> --dry-run", detail: "Show the exact evidence packet without making a model request." },
    { command: "why-diff explain <file[:line]>", detail: "Request a citation-checked interpretation from the configured OpenAI model." },
    { command: "why-diff compare <a> <b> --dry-run", detail: "Show the comparison packet without making a model request." },
    { command: "why-diff compare <a> <b> --explain", detail: "Request a model interpretation of two attempts." },
  ] },
  { name: "Maintenance", entries: [
    { command: "why-diff finalize [session]", detail: "Archive an active session under a private Git ref." },
    { command: "why-diff index status", detail: "Show the contents of the rebuildable SQLite query index." },
    { command: "why-diff index rebuild", detail: "Rebuild that index from Git objects and canonical event logs." },
    { command: "why-diff completion <shell>", detail: "Generate completion for bash, fish, PowerShell, or zsh." },
  ] },
];

export default function Docs() {
  return (
    <main className="docs-page site-shell">
      <div className="docs-head"><span className="eyebrow">WHY-DIFF / DOCS</span><h1>Get started.</h1><p>Install why-diff, capture a Codex session, and ask about a change.</p></div>
      <div className="docs-layout">
        <nav className="docs-toc" aria-label="On this page"><span>ON THIS PAGE</span><a href="#install">Install</a><a href="#activate">Turn on capture</a><a href="#agents">Agent adapters</a><a href="#use">Use it</a><a href="#details">Good to know</a><a href="#commands">Commands</a></nav>
        <div className="docs-content">
          <section id="install"><span className="doc-number">01</span><h2>Install</h2><p>Homebrew is the simplest route on macOS or Linux.</p><Command command="brew install prsuyal/tap/why-diff" /><p>Or use the release installer. It verifies the archive checksum and places both commands in <code>~/.local/bin</code>.</p><Command command={quick} /><p>On Windows, or if you prefer to install by hand, get an archive from <a href="https://github.com/prsuyal/why-diff/releases/latest" target="_blank" rel="noreferrer">GitHub Releases ↗</a>. Put both <code>why-diff</code> and <code>why-diff-hook</code> on your PATH.</p></section>
          <section id="activate"><span className="doc-number">02</span><h2>Turn on capture</h2><p>To capture Codex sessions in every Git repository you use:</p><Command command="why-diff init --global" /><p>To capture only in the current Git repository:</p><Command command="why-diff init" /><p>Review and trust the generated Codex hooks, then start a fresh Codex session. Run <code>why-diff doctor</code> inside a repository to check the setup. A warning about having no sessions yet is normal.</p><Command command="why-diff doctor" /></section>
          <section id="agents"><span className="doc-number">03</span><h2>Experimental agent hooks</h2><p>v0.2.0 includes hooks for Claude Code, Cursor, Gemini CLI, and GitHub Copilot CLI. They pass local hook and changed-line tests but have not been checked in live sessions with those agents.</p><p>After installing, choose an agent:</p><Command command="why-diff init --agent cursor --global" /><p>Use <code>claude</code>, <code>gemini</code>, or <code>copilot</code> in place of <code>cursor</code>. The <a href="https://github.com/prsuyal/why-diff/blob/main/docs/agent-adapters.md" target="_blank" rel="noreferrer">adapter setup guide ↗</a> covers each host and its known limits.</p></section>
          <section id="use"><span className="doc-number">04</span><h2>Ask about a change</h2><p>After an agent edits code, list your recorded sessions and choose a file or line from the diff.</p><Command command="why-diff sessions" /><Command command="why-diff why path/to/file.go:42" /><p><code>why</code> shows the request, the tool call during which the edit appeared, its patch, and any matching test result. It cannot tell you why the agent chose a particular value. Use <code>why-diff show latest</code> for the session timeline.</p></section>
          <section id="details"><span className="doc-number">05</span><h2>Good to know</h2><div className="docs-facts"><div><h3>It stays local.</h3><p>Capture and ordinary queries do not contact a model provider. An explicit <code>explain</code> request does.</p></div><div><h3>It shows its limits.</h3><p>A change between tool checkpoints is strong timing evidence. It cannot prove that one tool call was the only cause.</p></div><div><h3>You can turn it off.</h3><p>Run <code>why-diff disable</code> for a repository or <code>why-diff disable --global</code> for user-level hooks. Captured evidence stays in your local Git repository.</p></div></div><p>Requires Git 2.42 or newer. Codex CLI 0.156.1 is the version verified end to end for automatic capture.</p></section>
          <section id="commands"><span className="doc-number">06</span><h2>Command reference</h2><p>Where a command accepts <code>[session]</code>, use a full session ID, an unambiguous prefix, or <code>latest</code>. Omitting it selects the latest session.</p>{commandGroups.map(({ name, entries }) => <div className="docs-command-group" key={name}><h3>{name}</h3><div className="docs-command-table">{entries.map(({ command, detail }) => <div key={command}><code>{command}</code><p>{detail}</p></div>)}</div></div>)}<p><code>explain</code> and <code>compare --explain</code> accept <code>--model</code> and <code>--timeout</code>. Run <code>why-diff &lt;command&gt; --help</code> for every flag.</p><Link className="docs-back" href="/">← Back to why-diff</Link></section>
        </div>
      </div>
    </main>
  );
}
