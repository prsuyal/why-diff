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
  { command: "init [--global]", use: "Turn on capture for this repository or every repository." },
  { command: "sessions", use: "List recorded agent sessions." },
  { command: "show [session]", use: "Read the prompts, tools, and results in order." },
  { command: "diff [session]", use: "See file changes observed around tool calls." },
  { command: "why <file[:line]>", use: "Find the tool interval and patch for a change." },
  { command: "doctor", use: "Check hooks, Git support, and capture warnings." },
];

export default function Home() {
  const demoOutput = readFileSync(join(process.cwd(), "fixtures/why-output.txt"), "utf8");
  return (
    <main>
      <section className="hero site-shell">
        <div className="hero-content">
          <h1>Why did that line change?</h1>
          <p className="hero-description">When Codex leaves a change you didn&apos;t expect, run why-diff on the line. See the request it was handling, the command running when the edit appeared, and the patch around it.</p>
          <div className="hero-command"><span className="hero-command-label">Install with Homebrew</span><Command command={brew} /></div>
          <div className="hero-actions"><Link href="/docs">Docs</Link><a href="#install">Other install options</a></div>
          <div className="hero-agents" aria-label="Coding agents with why-diff hooks">
            <div className="hero-agents-heading"><span>Connect your coding agent.</span><Link href="/docs#agents">Agent setup</Link></div>
            <div className="agent-marquee">
              <div className="agent-track">
                {[0, 1, 2, 3].map((copy) => (
                  <ul className="agent-list" aria-hidden={copy !== 0} key={copy}>
                    {agents.map(({ name, icon }) => <li key={name}><Image src={icon} alt="" width={25} height={25} unoptimized /><span>{name}</span></li>)}
                  </ul>
                ))}
              </div>
            </div>
            <p>Codex is in the current release. The other hooks are available in source builds.</p>
          </div>
        </div>
      </section>

      <section className="demo-section site-shell" id="how">
        <h2 className="section-intro">Reviewing an agent&apos;s changes</h2>
        <Demo output={demoOutput} />
      </section>

      <section className="features-section site-shell" id="flow">
        <div className="feature-heading"><span className="eyebrow">How it fits</span><h2>One setup. A record of what happened.</h2><p>why-diff runs alongside Codex and stores its evidence in your Git repository.</p></div>
        <div className="flow-grid">
          <article className="flow-panel flow-panel-wide">
            <div className="flow-panel-copy"><span className="flow-index">01 / SETUP</span><h3>Add capture once.</h3><p><code>why-diff init --global</code> adds the hooks. They run as Codex works; you keep using your editor and terminal.</p></div>
            <div className="flow-graph" role="img" aria-label="Codex hook events flow into why-diff and local Git evidence">
              <div className="flow-node"><small>AGENT</small><strong>Codex</strong></div>
              <div className="flow-connector"><span>hook events</span></div>
              <div className="flow-node flow-node-main"><small>HOOKS</small><strong>why-diff</strong></div>
              <div className="flow-connector"><span>Git trees</span></div>
              <div className="flow-node"><small>LOCAL GIT</small><strong>Evidence</strong></div>
            </div>
          </article>
          <article className="flow-panel">
            <span className="flow-index">02 / CAPTURE</span><h3>See the edit between tool calls.</h3>
            <p>Snapshots bracket each call, including commands that quietly rewrite files.</p>
            <div className="checkpoint-visual" role="img" aria-label="The policy value changed during the generator call">
              <div className="checkpoint-card"><span>BEFORE</span><code>audit_event: enabled</code></div>
              <div className="checkpoint-card checkpoint-tool"><span>TOOL CALL</span><code>render-session-policy.sh</code></div>
              <div className="checkpoint-card"><span>AFTER</span><code>audit_event: disabled</code></div>
            </div>
          </article>
          <article className="flow-panel">
            <span className="flow-index">03 / QUERY</span><h3>Ask about the line.</h3>
            <p>The answer ties the changed line to the recorded request, tool interval, patch, and nearby tests.</p>
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
        <div className="command-reference-heading"><div><span className="eyebrow">THE CLI</span><h2>Command reference.</h2></div><Link href="/docs#commands">Every command in the docs</Link></div>
        <div className="command-reference-list">
          {commonCommands.map(({ command, use }, index) => <div className="command-reference-row" key={command}><span>{String(index + 1).padStart(2, "0")}</span><code>why-diff {command}</code><p>{use}</p></div>)}
        </div>
      </section>

      <section className="install-section" id="install">
        <div className="site-shell">
          <div className="install-heading"><span className="eyebrow">Get started</span><h2>Install why-diff.</h2><p>Then enable capture for one repository or all of them.</p></div>
          <div className="install-grid">
            <article className="install-card install-featured"><div className="install-card-top"><span>Homebrew</span><span>macOS · Linux</span></div><h3>Use Homebrew</h3><Command command={brew} compact /><p>Upgrade later with Brew.</p></article>
            <article className="install-card"><div className="install-card-top"><span>Install script</span><span>macOS · Linux</span></div><h3>Run the installer</h3><Command command={quick} compact /><p>Downloads the release and checks its SHA-256 checksum.</p></article>
            <article className="install-card"><div className="install-card-top"><span>Release archives</span><span>macOS · Linux · Windows</span></div><h3>Download an archive</h3><a className="release-link" href="https://github.com/prsuyal/why-diff/releases/latest" target="_blank" rel="noreferrer">GitHub Releases <span aria-hidden="true">↗</span></a><p>Put both why-diff and why-diff-hook on your PATH.</p></article>
          </div>
          <div className="activate-box">
            <div><span className="eyebrow">After installing</span><h3>Turn on capture.</h3><p>why-diff records sessions while you work in Codex.</p></div>
            <div className="activate-commands"><div><span>Every Git repository</span><Command command="why-diff init --global" compact /></div><div><span>Only this repository</span><Command command="why-diff init" compact /></div><p>Review and trust the hooks in Codex before your next session. <Link href="/docs">Setup details</Link></p></div>
          </div>
        </div>
      </section>
    </main>
  );
}
