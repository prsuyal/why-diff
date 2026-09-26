"use client";

import { useEffect, useState } from "react";
import { Files, GitBranch, Search, TerminalSquare } from "lucide-react";

const command = "why-diff why deploy/session-policy.yaml:3";
const steps = [
  { number: "01", title: "The diff" },
  { number: "02", title: "The command" },
  { number: "03", title: "The result" },
] as const;
const diffLines = [
  { number: "1", text: "password_reset:", className: "" },
  { number: "2", text: "-  revoke_existing_sessions: false", className: "is-removed" },
  { number: "2", text: "+  revoke_existing_sessions: true", className: "is-added" },
  { number: "3", text: "-  audit_event: enabled", className: "is-removed" },
  { number: "3", text: "+  audit_event: disabled", className: "is-added is-focus" },
  { number: "4", text: "refresh_tokens:", className: "" },
  { number: "5", text: "-  ttl: 30m", className: "is-removed" },
  { number: "5", text: "+  ttl: 30d", className: "is-added" },
  { number: "6", text: "  rotate_on_use: true", className: "" },
];

function outputStyle(line: string) {
  if (line.startsWith("+  ")) return "is-added";
  if (line.startsWith("-  ")) return "is-removed";
  if (/^(Request|Patch|Tests|Session|Evidence IDs):/.test(line) || line.startsWith("deploy/session-policy.yaml:3 changed")) return "is-heading";
  return "";
}

export function Demo({ output }: { output: string }) {
  const [step, setStep] = useState(0);

  useEffect(() => {
    const observer = new IntersectionObserver((entries) => {
      const current = entries.filter((entry) => entry.isIntersecting).sort((a, b) => b.intersectionRatio - a.intersectionRatio)[0];
      if (current) setStep(Number((current.target as HTMLElement).dataset.investigationStep));
    }, { rootMargin: "-32% 0px -38% 0px" });
    document.querySelectorAll<HTMLElement>("[data-investigation-step]").forEach((chapter) => observer.observe(chapter));
    return () => observer.disconnect();
  }, []);

  return (
    <>
    <div className="investigation">
      <div className="investigation-copy">
        {steps.map((item, index) => (
          <article className={`investigation-step ${step === index ? "is-current" : ""}`} data-investigation-step={index} id={`investigation-step-${index}`} key={item.number}>
            <div className="investigation-step-inner">
              <span className="investigation-count">{item.number} / 03</span>
              <h3>{item.title}</h3>
            </div>
          </article>
        ))}
      </div>

      <div className="investigation-visual" data-step={step} aria-label={`Editor and terminal demo: ${steps[step].title}`}>
        <div className="editor-shell">
          <div className="editor-titlebar"><span className="editor-titlemark" aria-hidden="true">⌘</span><span>demo</span><span className="editor-titlebar-right">session-policy.yaml — demo</span></div>
          <div className="editor-main">
            <div className="editor-activity" aria-hidden="true"><Files size={17} /><Search size={17} /><GitBranch size={17} className="is-active" /><TerminalSquare size={17} /></div>
            <aside className="source-control" aria-label="Source control example">
              <div className="source-control-title">SOURCE CONTROL <span>···</span></div>
              <div className="source-control-group">CHANGES <span>3</span></div>
              <div className="source-file"><span>M</span> session.go</div>
              <div className="source-file"><span>U</span> session_test.go</div>
              <div className="source-file is-selected"><span>M</span> session-policy.yaml</div>
            </aside>
            <div className="editor-pane">
              <div className="editor-tabs"><span className="editor-json-icon">◇</span> session-policy.yaml <span className="editor-tab-close">×</span></div>
              <div className="editor-breadcrumb">demo <span>›</span> deploy <span>›</span> session-policy.yaml</div>
              <div className="editor-code">
                {diffLines.map(({ number, text, className }, index) => <div className={`editor-line ${className}`} key={index}><span>{number}</span><code>{text}</code></div>)}
              </div>
            </div>
          </div>

          <div className="integrated-terminal">
            <div className="integrated-terminal-header"><span><TerminalSquare size={14} /> TERMINAL</span><span className="terminal-shell-label">zsh</span></div>
            <div className="integrated-terminal-screen" aria-label={step === 2 ? `Output from ${command}` : "Integrated terminal example"}>
              {step > 0 && <div className="terminal-command"><span className="terminal-location">~/demo</span><span className="terminal-prompt">%</span><span className="terminal-typed">{command}</span></div>}
              {step === 2 && <pre className="terminal-output">{output.split("\n").map((line, index) => <span className={`terminal-line ${outputStyle(line)}`} key={index}>{line || "\u00a0"}</span>)}</pre>}
            </div>
          </div>
          <div className="editor-statusbar"><span><GitBranch size={12} /> Git</span><span>session-policy.yaml · Ln 3</span></div>
        </div>
      </div>
      <p className="investigation-source">Editor mockup. Terminal output comes from a local test run using scripted Codex hooks.</p>
    </div>
    <div className="mobile-investigation">
      {steps.map((item, index) => (
        <article className={`mobile-investigation-step ${step === index ? "is-current" : ""}`} data-investigation-step={index} key={item.number}>
          <span className="investigation-count">{item.number} / 03</span>
          <h3>{item.title}</h3>
          {index === 0 && <div className="mobile-diff"><div className="mobile-panel-title">deploy/session-policy.yaml</div><div className="mobile-diff-lines">{diffLines.map(({ number, text, className }, lineIndex) => <div className={`mobile-diff-line ${className}`} key={lineIndex}><span>{number}</span><code>{text}</code></div>)}</div></div>}
          {index === 1 && <div className="mobile-terminal"><div className="mobile-panel-title">TERMINAL <span>zsh</span></div><div className="mobile-terminal-body"><span className="terminal-location">~/demo %</span><code>{command}</code></div></div>}
          {index === 2 && <div className="mobile-terminal"><div className="mobile-panel-title">TERMINAL <span>zsh</span></div><div className="mobile-terminal-body"><div className="mobile-terminal-command"><span className="terminal-location">~/demo %</span><code>{command}</code></div><pre className="mobile-terminal-output">{output.split("\n").map((line, lineIndex) => <span className={`terminal-line ${outputStyle(line)}`} key={lineIndex}>{line || "\u00a0"}</span>)}</pre></div></div>}
        </article>
      ))}
      <p className="investigation-source">Editor mockup. Terminal output comes from a local test run using scripted Codex hooks.</p>
    </div>
    </>
  );
}
