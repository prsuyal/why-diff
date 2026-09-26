import type { Metadata } from "next";
import localFont from "next/font/local";
import { SiteFooter, SiteHeader } from "@/components/chrome";
import "./globals.css";

const geistSans = localFont({
  src: "../fonts/geist-sans.woff2",
  variable: "--font-geist-sans",
  display: "swap",
  weight: "100 900",
});

const geistMono = localFont({
  src: "../fonts/geist-mono.woff2",
  variable: "--font-geist-mono",
  display: "swap",
  weight: "100 900",
});

export const metadata: Metadata = {
  title: "why-diff — Where did that change come from?",
  description:
    "See what Codex was doing when it changed a file or line.",
  openGraph: {
    title: "why-diff",
    description: "Where did that change come from?",
    type: "website",
  },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable}`}
    >
      <body><SiteHeader />{children}<SiteFooter /></body>
    </html>
  );
}
