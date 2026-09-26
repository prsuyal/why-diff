import type { Metadata } from "next";
import Link from "next/link";
import { Command } from "@/components/command";

export const metadata: Metadata = {
  title: "Docs — why-diff",
  description: "Install why-diff, capture a Codex session, and trace a changed file or line.",
};

const quick = "curl -fsSL https://raw.githubusercontent.com/prsuyal/why-diff/main/scripts/install.sh | sh";

export default function Docs() {
  return (
    <main className="docs-page site-shell">
      <div className="docs-head"><span className="eyebrow">WHY-DIFF / DOCS</span><h1>Get started.</h1><p>Install why-diff, capture a Codex session, and ask about a change.</p></div>
      <div className="docs-layout">
        <nav className="docs-toc" aria-label="On this page"><span>ON THIS PAGE</span><a href="#install">Install</a><a href="#activate">Turn on capture</a><a href="#use">Use it</a><a href="#details">Good to know</a><a href="#reference">Reference</a></nav>
        <div className="docs-content">
          <section id="install"><span className="doc-number">01</span><h2>Install</h2><p>Homebrew is the simplest route on macOS or Linux.</p><Command command="brew install prsuyal/tap/why-diff" /><p>Or use the release installer. It verifies the archive checksum and places both commands in <code>~/.local/bin</code>.</p><Command command={quick} /><p>On Windows, or if you prefer to install by hand, get an archive from <a href="https://github.com/prsuyal/why-diff/releases/latest" target="_blank" rel="noreferrer">GitHub Releases ↗</a>. Put both <code>why-diff</code> and <code>why-diff-hook</code> on your PATH.</p></section>
          <section id="activate"><span className="doc-number">02</span><h2>Turn on capture</h2><p>To capture Codex sessions in every Git repository you use:</p><Command command="why-diff init --global" /><p>To capture only in the current Git repository:</p><Command command="why-diff init" /><p>Review and trust the generated Codex hooks, then start a fresh Codex session. Run <code>why-diff doctor</code> inside a repository to check the setup. A warning about having no sessions yet is normal.</p><Command command="why-diff doctor" /></section>
          <section id="use"><span className="doc-number">03</span><h2>Ask about a change</h2><p>After Codex edits code, list your recorded sessions and choose a file or line from the diff.</p><Command command="why-diff sessions" /><Command command="why-diff why path/to/file.go:42" /><p><code>why</code> shows the request, the tool call during which the edit appeared, its patch, and any matching test result. It cannot tell you why the agent chose a particular value. Use <code>why-diff show latest</code> for the session timeline.</p></section>
          <section id="details"><span className="doc-number">04</span><h2>Good to know</h2><div className="docs-facts"><div><h3>It stays local.</h3><p>Capture and ordinary queries do not contact a model provider. An explicit <code>explain</code> request does.</p></div><div><h3>It shows its limits.</h3><p>A change between tool checkpoints is strong timing evidence. It cannot prove that one tool call was the only cause.</p></div><div><h3>You can turn it off.</h3><p>Run <code>why-diff disable</code> for a repository or <code>why-diff disable --global</code> for user-level hooks. Captured evidence stays in your local Git repository.</p></div></div><p>Requires Git 2.42 or newer. Codex CLI 0.156.1 is the version verified end to end for automatic capture.</p></section>
          <section id="reference"><span className="doc-number">05</span><h2>More to explore</h2><p><code>why-diff diff</code> shows observed changes, <code>lineage</code> follows a code entity, and <code>claims</code> finds fail-change-pass evidence.</p><p>For the complete command reference and technical details, read the <a href="https://github.com/prsuyal/why-diff#command-reference" target="_blank" rel="noreferrer">project README ↗</a>.</p><Link className="docs-back" href="/">← Back to why-diff</Link></section>
        </div>
      </div>
    </main>
  );
}
