# Working on why-diff

Start with [docs/product-direction.md](docs/product-direction.md) for the evidence model and the distinction between shipped behavior and proposed work. The public CLI contract is in [README.md](README.md). The website's interaction and demo truth rules are in [website/DESIGN.md](website/DESIGN.md).

## Repository map

- `cmd/why-diff` and `internal/cli`: user commands and output.
- `cmd/why-diff-hook`, `internal/capture`, `internal/checkpoint`, `internal/ingest`: event capture and Git snapshots.
- `internal/query`, `internal/indexdb`, `internal/provenance`: evidence lookup and attribution.
- `website/fixtures/record.sh` and `why-output.txt`: reproducible local fixture for the public demo.
- `website/src/components/demo.tsx`: the animated walkthrough of that fixture.

## Change loop

1. Trace the actual capture and query path before changing output or attribution wording.
2. Keep observations, inferences, and any future annotations separate. Preserve event IDs and Git tree IDs.
3. If CLI output or evidence meaning changes, refresh the demo fixture and check the website's copy against it.
4. Run the narrow relevant Go tests, then `go test ./...` for CLI changes. For website changes, run `pnpm lint` and `pnpm build` in `website/` and inspect desktop, mobile, keyboard, and reduced motion in a browser when available.
5. Record a durable product decision in `docs/product-direction.md` or `website/DESIGN.md` when it changes the evidence contract or public story. Do not put speculative behavior in release copy.

Normal capture and queries are local. Only the optional `explain` path uses a model request. The current website walkthrough uses scripted Codex hook events in a local test repository; it is not a live Codex recording.
