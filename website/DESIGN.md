# Website design and demo plan

## What the visitor should understand

Within one minute, a developer should be able to say: “Codex changed a line I didn't expect. why-diff can show the request, the tool interval when it appeared, the patch, and nearby test results.” They should also know that temporal evidence is not proof of the agent's intent.

The page follows the user's actual workflow: notice an unexpected file in the editor diff → run `why-diff why file:line` in the integrated terminal → read the recorded result → install.

## Visual language

- Near-black surfaces, white text, gray hierarchy, and thin borders follow the contrast of a dark shadcn/ui theme. Restrained green and red identify additions and removals; the rest of the page stays neutral.
- The editor is a generic VS Code/Cursor-style mockup of a normal developer workflow. It is not a why-diff graphical app. The integrated terminal shows the real CLI command and the complete recorded output from the local fixture.
- Scrolling moves from diff to command to result. The editor is a visual aid; it has no fake clickable product controls.
- Keep the product UI still long enough to read. Motion supports a change of focus; it does not continuously obscure the answer.
- Mobile keeps the same scroll story with a smaller sticky editor. Reduced-motion users get the same content without the scene transitions.
- The hero uses the full initial viewport for the question and install command. The capture flow, checkpoint timeline, and evidence table visualize facts in the demo trace. They are diagrams, not a second why-diff UI. The closing section stays still.

The presentation reference is [General Translation](https://generaltranslation.com/en-US): its landing page puts working product surfaces and concrete outputs into the story. The exact animation implementation there is unverified. The editor layout follows the familiar Source Control, editor, and [integrated terminal workflow documented by VS Code](https://code.visualstudio.com/docs/terminal/basics). Native CSS handles the continuously moving closing visual, while IntersectionObserver changes the scroll scene. [GSAP ScrollTrigger](https://gsap.com/docs/v3/Plugins/ScrollTrigger/) supports continuous scrubbing and pinning, which this interaction does not need.

## Source and truth

`fixtures/record.sh` creates a local repository and sends scripted Codex hook events through the real capture binary. Run `sh website/fixtures/record.sh > website/fixtures/why-output.txt` from the repository root to refresh the captured CLI output. The integrated terminal renders that file directly. The editor diff and copy must agree with it.

Do not call the walkthrough a live Codex recording. Do not imply that a passed test proves the policy edit was required. The product supports Codex in public copy; Claude, Cursor, Gemini CLI, and Copilot hook adapters have local contract tests but still need live host checks before a compatibility logo strip appears. Keep the terminal output identical to the generated fixture, including its evidence IDs. Explain the limits in docs instead of adding repeated disclaimers to each command result.

## Implementation and review

- Next.js static export, locally hosted Geist fonts, Tailwind, and CSS diagrams and motion.
- Native sticky positioning and IntersectionObserver drive three scene changes. GSAP would be justified only if a future interaction needs continuous scroll scrubbing or pin control that these native tools cannot provide.
- Keep the hero, walkthrough, install methods, and docs in one visual system. Do not add motion packages or generic UI blocks to create decoration. Add agent logos only after each displayed host has a verified live setup path.
- Before publishing: verify desktop and mobile scroll scenes in a browser, keyboard scrolling, terminal output overflow, reduced motion, the generated fixture against current CLI output, then run lint and production build.

The product behavior and next CLI improvements are recorded in [docs/product-direction.md](../docs/product-direction.md).
