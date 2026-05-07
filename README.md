# DateTree Photos

![DateTree Photos - Your photos. In the right place.](static/datetree-logo.png)

*your photos in the right place*

Local web UI for importing camera SD cards into a date-organized library. Single static Go binary; macOS and Linux.

Repository: <https://github.com/miking7/datetree-photos>

## Status

v1 milestones:

- [x] Project scaffold + Claude Code config
- [x] HTTP server, embedded static assets, templ home page
- [x] Scan handler: walk source, read EXIF/QuickTime/mtime dates, render preview
- [x] Async scan with SSE progress + Cancel button
- [x] Execute / mover (move-or-rename, cross-FS copy-and-verify)
- [x] Run manifest CSV
- [x] Settings persistence (recent paths MRU, last mode)
- [x] Done screen with run summary and manifest link
- [x] Per-row checkbox state plumbing in preview (with range-select + bulk tick/untick)
- [x] Settings page with three persisted toggles (path template, align-mtime, soft-match)
- [x] Path template and soft-match wired through scan; align-mtime consumption pending

v2 will add dedup against destination library — see spec §12.

## Why this exists

Replaces the older `~/bin/import_photos` bash/AppleScript pipeline. Reads real
EXIF and QuickTime capture dates instead of leaning on file mtime, so files
land under the date the photo was actually taken. Pure-Go single binary — no
Python, no virtualenv, no `exiftool` dependency.

## Install

One-line install (macOS, Linux):

```
curl -fsSL https://raw.githubusercontent.com/miking7/datetree-photos/master/install.sh | sh
```

This downloads the latest release archive, verifies its SHA256 against the
release's `checksums.txt`, and installs `datetree` to `~/.local/bin/`. If
`~/.local/bin` is not on your `$PATH`, the script prints a one-line hint to
add it to your shell rc.

Updates are not yet automatic — re-run the same command to upgrade.
Self-update is on the roadmap.

**macOS Gatekeeper.** Binaries fetched via `curl` are typically free of the
quarantine xattr, so the "developer cannot be verified" dialog should not
appear. If it does, clear the attribute manually:

```
xattr -d com.apple.quarantine ~/.local/bin/datetree
```

## Quick start

```
datetree
```

The binary picks a free port in `9700-9799` and opens the browser at
`http://127.0.0.1:97xx/`. Flags: `--no-open` to suppress the browser launch,
`--port N` to pin a specific port, `--version` to print the version and
exit. Visit `/settings` to configure path template and import behavior.

## Build from source

```
make build
./datetree
```

## Usage flow

1. Type or paste source path (your SD-card mount point — e.g. `/Volumes/SDCARD/DCIM` on macOS, `/media/<user>/SDCARD/DCIM` on Linux).
2. Type destination root (your photo library root — e.g. `/Volumes/Photos` on macOS, `/home/<user>/Photos` on Linux).
3. Click Scan — review the preview table (capture date, date source, conflict);
   uncheck any rows to skip.
4. Click Execute (Move) or Execute (Copy) — files land in `<dest>/YYYY/YYYYMMDD/`.

The Settings page exposes three persisted toggles: **destination path
template** (Go time-format layout, e.g. `2006/20060102`), **align mtime to
EXIF** (preserves source mtime on cross-FS copies; full EXIF-alignment
consumption is still pending), and **soft-match existing date folders by
prefix**. Soft-match opts into a hybrid policy: the importer flags duplicates
across the exact date folder and every descriptive sibling whose name starts
with the rendered date (`20260509 wedding`, `20260509-trip`), and auto-merges
into a single such descriptive folder when there's exactly one candidate.
With multiple candidates it stays out of the way and creates the exact-named
folder. Align-mtime and soft-match default to on.

## Tech stack

Go (stdlib `net/http`, no framework), [templ](https://templ.guide) for
type-safe HTML, [htmx](https://htmx.org) for fragment swaps and SSE,
[Tailwind CSS v4](https://tailwindcss.com) for styling (compiled at build
time via the standalone CLI; no Node toolchain), [Inter](https://rsms.me/inter/)
self-hosted as woff2. Image metadata splits across two
pure-Go libs: `github.com/evanoberholster/imagemeta` for JPEG/HEIC/CR2/CR3/
NEF/ARW/DNG (the only pure-Go lib with explicit CR3 support), and
`github.com/bep/imagemeta` for PNG/WebP/AVIF/PEF including XMP. PNG `tEXt`
"Creation Time" and `tIME` chunks are read directly when EXIF/XMP are absent.
Video metadata via `github.com/abema/go-mp4` (MP4/MOV/M4V plus DJI/GoPro
`.lrf`/`.lrv` proxies). AVCHD `.mts`/`.m2ts`, BMP/GIF/JXL, less-common RAW
(ORF/RW2/RAF/SRW/X3F/3FR/FFF/RWL), secondary video containers (AVI/MKV/3GP/
3G2/WebM/WMV/FLV) and DJI 360 proxies are whitelisted but fall back to file
mtime — no pure-Go reader exists for them.

## Development

```
make dev          # tailwindcss --watch + templ generate --watch + go run . --no-open
go test ./...     # run tests
go vet ./...      # static checks
```

Prereqs: Go 1.22+, the `templ` CLI (`go install github.com/a-h/templ/cmd/templ@latest`),
`make`, and the standalone Tailwind CLI binary at `tools/tailwindcss`.

The Tailwind binary is fetched automatically by `make` on first build; the
`make tools/tailwindcss` target detects the host OS/arch and downloads the
matching release (darwin-arm64, darwin-x64, linux-x64, linux-arm64). The
binary is gitignored — every developer (or CI runner) downloads their own
copy.

To download manually, see the
[Tailwind releases page](https://github.com/tailwindlabs/tailwindcss/releases)
and place the binary at `tools/tailwindcss`.

## Project layout

Go source files live at the repo root: `main.go` (HTTP setup), `handlers.go`
(routes and SSE), `scanner.go` + `metadata.go` + `sniff.go` (walk +
content-sniff dispatch + capture-date extraction), `progress.go` (pub-sub for
SSE consumers), `mover.go` (rename / copy-and-verify, sha256, worker pool),
`manifest.go` (per-run CSV writer), `session.go` (in-memory state),
`config.go` (settings persistence), `filesystem.go` plus
`filesystem_darwin.go` / `filesystem_linux.go` (`sameFilesystem` with
per-OS `dev()` lookup). Templ components (home, preview, progress, done, settings) are
in `components/`. Vendored static assets (htmx core + SSE extension, the
brand-lockup logo, Inter woff2 weights, and the Tailwind-compiled `app.css`)
live in `static/` and are embedded into the binary via `go:embed`. The
Tailwind input lives at `static/app.src.css`; the compiled `static/app.css`
is gitignored and rebuilt by `make`. Real
fixture files for tests are in `testdata/`. See `datetree-spec.md` §10
for the detailed breakdown.

## Scope and limitations

- macOS and Linux. Browser auto-launch uses `open` (macOS) or `xdg-open`
  (Linux); on headless setups the printed URL is the fallback. State and
  run manifests live under `os.UserConfigDir()/datetree/`
  (`~/Library/Application Support/datetree/` on macOS,
  `$XDG_CONFIG_HOME/datetree/` or `~/.config/datetree/` on Linux).
- Single user, localhost only — no auth, no remote access.
- v1 ignores sidecars (XMP, RAW+JPG pairing, MODD); see spec §13 for the
  full out-of-scope list.
- No resumable runs after a crash.

## Roadmap

- **v1** — import (scan + execute, with manifest).
- **v2** — dedup against destination library (skip already-imported files
  via SHA index).

## Spec

For locked architectural decisions, domain model, and full design rationale,
see [datetree-spec.md](datetree-spec.md). The spec is the source
of truth.
