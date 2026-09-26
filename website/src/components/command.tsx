"use client";

import { useState } from "react";

export function Command({ command, compact = false }: { command: string; compact?: boolean }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }

  return (
    <div className={`command ${compact ? "command-compact" : ""}`}>
      <code><span className="command-prompt">$</span> {command}</code>
      <button type="button" onClick={copy} aria-label={copied ? "Copied command" : `Copy ${command}`}>
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
