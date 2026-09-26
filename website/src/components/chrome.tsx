import Link from "next/link";

function Mark() {
  return (
    <svg className="brand-mark" viewBox="0 0 32 32" fill="none" aria-hidden="true">
      <path d="M9 4v24M9 11h8c5 0 7 3 7 8v9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="9" cy="4" r="2.7" fill="currentColor" />
      <circle cx="24" cy="28" r="2.7" fill="currentColor" />
    </svg>
  );
}

export function SiteHeader() {
  return (
    <header className="site-header">
      <div className="site-shell header-inner">
        <Link className="brand" href="/" aria-label="why-diff home"><Mark /><span>why-diff</span></Link>
        <nav className="site-nav" aria-label="Main navigation">
          <Link href="/#how">How it works</Link>
          <Link href="/#commands">Commands</Link>
          <Link href="/docs">Docs</Link>
          <a href="https://github.com/prsuyal/why-diff" target="_blank" rel="noreferrer">GitHub <span aria-hidden="true">↗</span></a>
        </nav>
        <Link className="header-cta" href="/#install">Install</Link>
      </div>
    </header>
  );
}

export function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="site-shell footer-inner">
        <div className="footer-identity">
          <Link className="brand" href="/"><Mark /><span>why-diff</span></Link>
          <span>Free and open source · MIT</span>
        </div>
        <nav className="footer-links" aria-label="Footer navigation"><Link href="/docs">Docs</Link><a href="https://github.com/prsuyal/why-diff" target="_blank" rel="noreferrer">GitHub ↗</a></nav>
      </div>
    </footer>
  );
}
