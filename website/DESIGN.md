# Website design and demo plan

## What the visitor should understand

Within one minute, a developer should be able to say: “Codex changed a line I didn't expect. why-diff can show the request, the tool interval when it appeared, the patch, and nearby test results.” They should also know that temporal evidence is not proof of the agent's intent.

The page follows the user's actual workflow: notice an unexpected file in the editor diff → run `why-diff why file:line` in the integrated terminal → read the recorded result → install.

## Visual language

- Near-black surfaces, white text, gray hierarchy, and thin borders follow the contrast of a dark shadcn/ui theme. Restrained green and red identify additions and removals; the rest of the page stays neutral.
- The editor is a generic VS Code/Cursor-style mockup of a normal developer workflow. It is not a why-diff graphical app. The integrated terminal shows the real CLI command and the complete recorded output from the local fixture.
- Scrolling moves from diff to command to result. The editor is a visual aid; it has no fake clickable product controls.
- Let the diff, command, and recorded output explain the walkthrough. Use one plain section heading and short step labels; do not repeat the same scenario in paragraphs beside the demo.
- Write headings as descriptions of the actual action or data. Each supporting sentence must add a fact the visual does not already show. Avoid clipped slogan fragments and repeated instructions.
- Keep the product UI still long enough to read. Motion supports a change of focus; it does not continuously obscure the answer.
- Keep the flow and checkpoint diagrams static; arrows and before/after labels explain their meaning. The editor walkthrough changes with scroll, and the agent logos move as a carousel. Do not add moving lights that imply data is flowing when nothing is happening.
- Desktop uses a sticky editor while the three chapters change focus. At 900px wide, on short screens, and with reduced motion, show three stacked panels: the diff, the command, and the complete CLI output. These panels keep the story readable without a pinned editor or nested terminal scrolling.
- The hero holds the question, install command, and agent logo carousel in one section. The capture flow, checkpoint timeline, and evidence table visualize facts in the demo trace. They are diagrams, not a second why-diff UI. The home page ends with install and capture setup.
- The home page shows the six commands used in a typical investigation. The docs carry the full public CLI reference. Both follow the README and command help when syntax changes.

The presentation reference is [General Translation](https://generaltranslation.com/en-US): its landing page puts working product surfaces and concrete outputs into the story. The exact animation implementation there is unverified. The editor layout follows the familiar Source Control, editor, and [integrated terminal workflow documented by VS Code](https://code.visualstudio.com/docs/terminal/basics). Native CSS moves the logo strip, while IntersectionObserver changes the scroll scene. [GSAP ScrollTrigger](https://gsap.com/docs/v3/Plugins/ScrollTrigger/) supports continuous scrubbing and pinning, which this interaction does not need.

## Source and truth

`fixtures/record.sh` creates a local repository and sends scripted Codex hook events through the real capture binary. Run `sh website/fixtures/record.sh > website/fixtures/why-output.txt` from the repository root to refresh the captured CLI output. The integrated terminal renders that file directly. The editor diff and copy must agree with it.

Do not call the walkthrough a live Codex recording. Do not imply that a passed test proves the policy edit was required. The logo strip names hook adapters present in source after CI passes their contract tests. The docs say the published release predates the four new adapters and that live host checks remain. Do not describe the strip as live-verified compatibility. Keep the terminal output identical to the generated fixture, including its evidence IDs. Explain the limits in docs instead of adding repeated disclaimers to each command result.

## Implementation and review

- Next.js static export, locally hosted Geist fonts, Tailwind, and CSS diagrams and motion.
- Native sticky positioning drives the desktop scene. IntersectionObserver marks the current chapter on either layout, but never hides the mobile content. GSAP would be justified only if a future interaction needs continuous scroll scrubbing or pin control that these native tools cannot provide.
- The logo carousel repeats two complete sets per animation span, wider than the widest site shell, then repeats that span. This keeps the track filled at the loop boundary. Reduced motion shows one static, wrapped set.
- Keep the hero, walkthrough, install methods, and docs in one visual system. Do not add motion packages or generic UI blocks to create decoration. Agent marks come from static Simple Icons SVGs; use them only to identify the corresponding hook adapters, without implying endorsement.
- Before publishing: verify desktop and mobile scroll scenes in a browser, keyboard scrolling, terminal output overflow, reduced motion, the generated fixture against current CLI output, then run lint and production build.

The product behavior and next CLI improvements are recorded in [docs/product-direction.md](../docs/product-direction.md).
