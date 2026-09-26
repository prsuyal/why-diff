import { readFileSync } from "node:fs";
import { join } from "node:path";
import Image from "next/image";
import Link from "next/link";
import { Command } from "@/components/command";
import { Demo } from "@/components/demo";

const brew = "brew install prsuyal/tap/why-diff";
const quick = "curl -fsSL https://raw.githubusercontent.com/prsuyal/why-diff/main/scripts/install.sh | sh";
const agents = [
  { name: "Codex", icon: "/agents/openai.svg" },
  { name: "Claude Code", icon: "/agents/claude.svg" },
  { name: "Cursor", icon: "/agents/cursor.svg" },
  { name: "Gemini CLI", icon: "/agents/gemini.svg" },
  { name: "Copilot CLI", icon: "/agents/copilot.svg" },
];
const commonCommands = [
  { command: "init [--global]", use: "Add Codex hooks for this repository or all Git repositories." },
  { command: "sessions", use: "List captured sessions." },
  { command: "show [session]", use: "Read a session's prompts, tool calls, and results." },
  { command: "diff [session]", use: "See file changes captured during a session." },
  { command: "why <file[:line]>", use: "See the request, tool call, and patch for a changed file or line." },
  { command: "doctor", use: "Check hook setup and report capture problems." },
];

export default function Home() {
  const demoOutput = readFileSync(join(process.cwd(), "fixtures/why-output.txt"), "utf8");
  return (
    <main>
      <section className="hero site-shell">
        <div className="hero-content">
          <h1>Why did that line change?</h1>
          <p className="hero-description">When Codex changes a file or line you didn&apos;t expect, why-diff shows the request it was working on, the tool call during which the edit appeared, and the patch.</p>
          <div className="hero-command"><span className="hero-command-label">Install with Homebrew</span><Command command={brew} /></div>
          <div className="hero-actions"><Link href="/docs">Docs</Link><a href="#install">Other install options</a></div>
          <div className="hero-agents" aria-label="Coding agents with why-diff hooks">
            <div className="hero-agents-heading"><span>Coding agents</span><Link href="/docs#agents">Integration details</Link></div>
            <div className="agent-marquee">
              <div className="agent-track">
                {[0, 1, 2, 3].map((copy) => (
                  <ul className="agent-list" aria-hidden={copy !== 0} key={copy}>
                    {agents.map(({ name, icon }) => <li key={name}><Image src={icon} alt="" width={25} height={25} unoptimized /><span>{name}</span></li>)}
                  </ul>
                ))}
              </div>
            </div>
            <p>Codex is verified end to end; the other integrations in v0.2.0 are experimental.</p>
          </div>
        </div>
      </section>

      <section className="demo-section site-shell" id="how">
        <h2 className="section-intro">Reviewing an agent&apos;s changes</h2>
        <Demo output={demoOutput} />
      </section>

      <section className="features-section site-shell" id="flow">
        <div className="feature-heading"><h2>How why-diff records and finds changes</h2></div>
        <div className="flow-grid">
          <article className="flow-panel flow-panel-wide">
            <div className="flow-panel-copy"><h3>why-diff records the Codex session</h3><p>Codex sends your prompts, tool calls, and results through hooks, and why-diff stores them in this repository.</p></div>
            <div className="flow-graph" role="img" aria-label="Codex hook events flow into why-diff and local Git evidence">
              <div className="flow-node"><small>AGENT</small><strong>Codex</strong></div>
              <div className="flow-connector" />
              <div className="flow-node"><small>HOOKS</small><strong>why-diff</strong></div>
              <div className="flow-connector" />
              <div className="flow-node"><small>LOCAL GIT</small><strong>Repository</strong></div>
            </div>
          </article>
          <article className="flow-panel">
            <h3>Compare Git snapshots</h3>
            <p>Snapshots around a tool call can show edits made by a script, even if the agent later undoes them.</p>
            <div className="checkpoint-visual" role="img" aria-label="The policy value changed during the generator call">
              <div className="checkpoint-card"><span>BEFORE</span><code>audit_event: enabled</code></div>
              <div className="checkpoint-card"><span>TOOL CALL</span><code>render-session-policy.sh</code></div>
              <div className="checkpoint-card"><span>AFTER</span><code>audit_event: disabled</code></div>
            </div>
          </article>
          <article className="flow-panel">
            <h3>Look up a changed file or line</h3>
            <div className="evidence-table">
              <div><span>REQUEST</span><strong>Revoke existing sessions</strong></div>
              <div><span>TOOL</span><strong>render-session-policy.sh</strong></div>
              <div><span>PATCH</span><strong>audit_event: enabled → disabled</strong></div>
              <div><span>TESTS</span><strong>failed → passed</strong></div>
            </div>
          </article>
        </div>
      </section>

      <section className="command-reference site-shell" id="commands">
        <div className="command-reference-heading"><h2>Commands</h2><Link href="/docs#commands">Full command reference</Link></div>
        <div className="command-reference-list">
          {commonCommands.map(({ command, use }, index) => <div className="command-reference-row" key={command}><span>{String(index + 1).padStart(2, "0")}</span><code>why-diff {command}</code><p>{use}</p></div>)}
        </div>
      </section>

      <section className="install-section" id="install">
        <div className="site-shell">
          <div className="install-heading"><h2>Install why-diff</h2></div>
          <div className="install-grid">
            <article className="install-card install-featured"><div className="install-card-top"><span>macOS · Linux</span></div><h3>Homebrew</h3><Command command={brew} compact /><p>To update later, run <code>brew upgrade prsuyal/tap/why-diff</code>.</p></article>
            <article className="install-card"><div className="install-card-top"><span>macOS · Linux</span></div><h3>Install script</h3><Command command={quick} compact /><p>Downloads the release archive and verifies its SHA-256 checksum.</p></article>
            <article className="install-card"><div className="install-card-top"><span>macOS · Linux · Windows</span></div><h3>Prebuilt binaries</h3><a className="release-link" href="https://github.com/prsuyal/why-diff/releases/latest" target="_blank" rel="noreferrer">GitHub Releases <span aria-hidden="true">↗</span></a><p>Extract both commands and add them to your PATH.</p></article>
          </div>
          <div className="activate-box">
            <div><span className="eyebrow">After installing</span><h3>Enable the Codex hooks</h3></div>
            <div className="activate-commands"><div><span>All Git repositories</span><Command command="why-diff init --global" compact /></div><div><span>This repository</span><Command command="why-diff init" compact /></div><p>Review and trust the hooks in Codex, then start a new session. <Link href="/docs">Setup instructions</Link></p></div>
          </div>
        </div>
      </section>
    </main>
  );
}
