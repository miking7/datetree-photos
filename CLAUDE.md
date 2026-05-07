# DateTree Photos

*your photos in the right place* — <https://github.com/miking7/datetree-photos>

Replacement for the `~/bin/import_photos` bash script. Single static Go binary that opens a local web UI to scan a source folder, preview planned moves with capture-date metadata, and execute. v1 = import. v2 = dedup against the destination library.

The compiled binary is named `datetree`. The display name "DateTree Photos" appears in the UI; "datetree" is what the user types and what lives in `~/Library/Application Support/datetree/`.

## Authoritative source

`datetree-spec.md` (repo root) is the source of truth. Section §2 lists 20 locked decisions — don't propose alternatives without flagging them first.

## Build / run / test

- `make` / `make build` — `templ generate` then `go build -o datetree .`
- `make run` — build then run
- `make dev` — `templ generate --watch` plus `go run . --no-open`
- `make clean` — remove binary and generated `*_templ.go` files
- `go test ./...` — run tests (uses real fixtures in `testdata/`)
- `go vet ./...` — static checks

## Stack

- Go, stdlib `net/http` (no framework)
- [templ](https://templ.guide) for type-safe HTML templates
- [htmx](https://htmx.org) for fragment swaps and SSE
- [Pico.css](https://picocss.com) for styling — no Tailwind class-noise
- Image metadata: `github.com/evanoberholster/imagemeta` (JPEG/HEIC/CR2/CR3/NEF/ARW/DNG) and `github.com/bep/imagemeta` (PNG/WebP/AVIF/PEF, including XMP); reader is picked by content-sniffing the first 16 bytes, not by extension
- Video metadata: `github.com/abema/go-mp4` (pure Go, MP4/MOV atoms)
- AVCHD `.mts` and other long-tail formats fall back to file mtime — no pure-Go reader exists
- macOS and Linux for v1. Browser launch via `open` (macOS) / `xdg-open` (Linux). State and runs/ live under `os.UserConfigDir()/datetree/` — `~/Library/Application Support/datetree/` on macOS, `$XDG_CONFIG_HOME/datetree/` or `~/.config/datetree/` on Linux.

## Conventions

- No emojis in code or docs.
- Sparse comments — only WHY, never WHAT (per spec).
- Follow the locked decisions in spec §2; flag before deviating.
- Single user, single active scan/run. No DB, no auth, no session cookies.
- Static assets and generated templ Go compile into the binary via `go:embed`.

## Layout pointers

- `testdata/` — real fixture files (no mocks; the metadata libraries *are* the unit under test, see spec §14).
- `~/Library/Application Support/datetree/state.json` — recent paths, last mode, and the three Settings-page toggles (path template, align-mtime, soft-match).
- `~/Library/Application Support/datetree/runs/<RFC3339>.csv` — per-run manifest.

## Don't touch

- `~/bin/import_photos` — the existing bash script being replaced. Both coexist during the transition; leave the script alone until v1 is shippable.
