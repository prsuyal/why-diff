# why-diff website

Static Next.js site for the [why-diff CLI](https://github.com/prsuyal/why-diff).

```sh
cd website
pnpm install
pnpm dev
```

`pnpm build` creates a static site in `out/`. Set a hosting project's root
directory to `website/` and publish that directory. The CLI release process is
separate from the site build.

The interaction, source fixture, and review checks are in [DESIGN.md](DESIGN.md).
