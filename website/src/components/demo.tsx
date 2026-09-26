"use client";

import { useEffect, useState } from "react";
import { Files, GitBranch, Search, TerminalSquare } from "lucide-react";

const command = "why-diff why deploy/session-policy.yaml:3";
const steps = [
  {
    number: "01",
    title: "A security setting moved too.",
    body: "The session revocation test passes after the fix. The generated policy also disables password reset audit events and lengthens refresh tokens.",
  },
  {
    number: "02",
    title: "Ask about the line.",
    body: "Run why-diff on the audit setting. You can query any file or line in the captured diff.",
  },
  {
    number: "03",
    title: "Read what was recorded.",
    body: "The answer points to the policy generator and shows its patch. The test passed, but that does not explain or justify the audit change.",
  },
] as const;

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
    <div className="investigation">
      <div className="investigation-copy">
        {steps.map((item, index) => (
          <article className={`investigation-step ${step === index ? "is-current" : ""}`} data-investigation-step={index} id={`investigation-step-${index}`} key={item.number}>
            <div className="investigation-step-inner">
              <span className="investigation-count">{item.number} / 03</span>
              <h3>{item.title}</h3>
              <p>{item.body}</p>
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
                <div className="editor-line"><span>1</span><code>password_reset:</code></div>
                <div className="editor-line is-removed"><span>2</span><code>-  revoke_existing_sessions: false</code></div>
                <div className="editor-line is-added"><span>2</span><code>+  revoke_existing_sessions: true</code></div>
                <div className="editor-line is-removed"><span>3</span><code>-  audit_event: enabled</code></div>
                <div className="editor-line is-added is-focus"><span>3</span><code>+  audit_event: disabled</code></div>
                <div className="editor-line"><span>4</span><code>refresh_tokens:</code></div>
                <div className="editor-line is-removed"><span>5</span><code>-  ttl: 30m</code></div>
                <div className="editor-line is-added"><span>5</span><code>+  ttl: 30d</code></div>
                <div className="editor-line"><span>6</span><code>  rotate_on_use: true</code></div>
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
      <p className="investigation-source">Editor mockup. CLI output recorded in a local test repository using scripted Codex hook events.</p>
    </div>
  );
}
