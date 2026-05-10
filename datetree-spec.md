# DateTree Photos — Spec & Plan (v1)

*your photos in the right place* — <https://github.com/miking7/datetree-photos>

The compiled binary is named `datetree`. The display name "DateTree Photos" appears in the UI; references below to the binary use `datetree`.

## 1. Context

Replacement for the existing macOS bash script `import_photos` (in `~/bin/`), which today drives the third-party `organize` Python CLI to move files from a camera/SD card into `/Volumes/Photos/{year}/{YYYYMMDD}/`. The current setup has friction:

- AppleScript dialog UX is dated and clumsy.
- Depends on `organize` (and optionally `exiftool`) being installed.
- Sorts by file mtime only — EXIF capture date is ignored.
- No preview UI; only a text dry-run dump.
- Dedup is a separate manual workflow (Dupeguru app run before importing).

**Goal:** a single static Go binary that opens a local web UI to scan a source folder, preview planned moves with capture-date metadata, and execute. v1 = import. v2 = dedup against the destination library.

The existing bash script is at `bin/import_photos` and shouldn't be touched until v1 is working — both will coexist for a transition period.

## 2. Locked decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language | Go | Single static binary, pure-Go metadata libs cover ~90% of formats including CR3 and HEIC, no CGO. |
| Distribution | Single static binary via `go build` | User runs `./datetree` and it self-launches the browser. |
| Web stack | stdlib `net/http` + `templ` + htmx + Pico.css | templ gives type-safe templates that pair well with htmx fragment endpoints. Pico.css gives a modern look from semantic HTML alone — no Tailwind class-noise. |
| Bind address | `127.0.0.1` only | Localhost single-user. No auth needed. |
| Image metadata | `github.com/evanoberholster/imagemeta` for JPEG/HEIC/CR2/CR3/NEF/ARW/DNG; `github.com/bep/imagemeta` for PNG/WebP/AVIF/PEF | Both pure Go, MIT. evanoberholster is the only pure-Go lib with explicit CR3 support, but does not handle PNG/WebP/AVIF/PEF; bep covers those without giving up CR3 because dispatch is per-extension. |
| Video metadata | `github.com/abema/go-mp4` | Pure Go, MIT. Reads `mvhd` and QuickTime `keys`/`ilst` atoms (`com.apple.quicktime.creationdate`). |
| MTS/AVCHD and other long-tail formats | Fall back to file mtime | No Go library reads AVCHD MDPM date; mtime is correct for fresh-off-the-card files anyway. Same tier covers BMP/GIF/JXL, less-common RAW (ORF/RW2/RAF/SRW/X3F/3FR/FFF/RWL), secondary video containers (AVI/MKV/3GP/3G2/WebM/WMV/FLV) and DJI 360 proxies. |
| Date priority | `EXIF DateTimeOriginal` → `EXIF DateTime/ModifyDate` → `xmp:CreateDate` → PNG `tEXt` "Creation Time" → PNG `tIME` chunk → QuickTime atoms → file mtime | EXIF original survives copying; XMP and PNG-native chunks are the next-best signals when EXIF is absent; mtime is last-resort fallback. |
| Destination structure | Configurable Go time-format path template, default `2006/20060102` (= `{Year}/{YYYYMMDD}`) | Matches current `organize` config out of the box; configurable via the Settings page so the layout, mtime alignment, and soft-matched dest folders can be opted into without per-feature UI churn. |
| Settings page (v1) | Three persisted toggles: destination path template, align-mtime-to-EXIF, soft-match destination folders. Path template uses Go time-format layouts (reference time `Mon Jan 2 15:04:05 MST 2006`); preset dropdown auto-fills the text box on selection and flips to "Custom" the moment the user edits it. Save returns the user to home via `HX-Redirect`; validation failures keep the user on the page with the entered values intact. | Each toggle backs a follow-up behaviour the project owner asked for; one form is cheaper than three feature-specific UIs. Go layout strings are unfamiliar to non-Go users, so the dropdown teaches by example and the input label flags the format explicitly. Further knobs (concurrency, ext list, presets) remain deferred. |
| Filtering | Whitelist by extension, in two tiers. **Metadata-read** (passed to a metadata library): JPG/JPEG, HEIC/HEIF, CR2/CR3, NEF, ARW, DNG, PNG, WebP, AVIF, PEF, MP4, MOV, M4V, LRF, LRV. **Mtime-only** (extension is whitelisted but no metadata reader exists): MTS, M2TS, BMP, GIF, JXL, ORF, RW2, RAF, SRW, X3F, 3FR, FFF, RWL, AVI, MKV, 3GP, 3G2, WebM, WMV, FLV, 360. Anything else is dropped. | Avoids moving `.DS_Store`, `.MODD`, `.AAE`, etc. while broadening the long-tail coverage seen on real SD cards and external drives. |
| File operation | Move (default), with **Copy** UI toggle | Same-FS uses `os.Rename`; cross-FS falls back to copy + verify-size + delete. |
| Conflict policy | Skip on collision (count in summary). When the soft-match toggle is on, "collision" expands to mean: source filename already exists in the rendered date folder OR in any descriptive sibling whose name starts with the rendered date (see §7). | Matches current `organize` behavior; the cross-candidate broadening is defensive — a duplicate already filed under a descriptive folder shouldn't get re-imported into a fresh sibling. |
| Soft-match destination | Hybrid policy applied at scan time when the toggle is on (default). **Conflict detection** spans the exact-named folder and every date-prefixed descriptive sibling. **Routing** prefers the exact-named folder when it exists; otherwise auto-routes into a single descriptive sibling (the original "merge into descriptive folder" benefit) only when there's exactly one candidate; with two or more candidates falls back to creating the exact-named folder. Toggle off: unchanged v1 behaviour. | Auto-routing into the only candidate is unambiguous and removes the "split across two folders" friction. With multiple candidates the importer can't guess intent, so it stays out of the way and lets the user merge manually. Cross-candidate conflict detection is the defence that catches duplicates regardless of which folder ends up being chosen. |
| Recursion | Recurse source folder fully by default | Picks up Sony's `Private/M4ROOT/CLIP` automatically. |
| Mid-run errors | Log per-file, continue, summarize at end | Don't abort whole batch for one bad file. |
| Concurrency | Worker pool (`runtime.NumCPU()`), htmx-SSE progress | Live `processed/total` updates in UI. |
| Sidecars (XMP, JPG-pair, MODD) | Ignored in v1, treated as independent files | RAW+JPG pairs land in same date folder via date logic anyway. |
| Settings persistence | `os.UserConfigDir()/datetree/state.json` (macOS: `~/Library/Application Support/datetree/state.json`; Linux: `$XDG_CONFIG_HOME/datetree/state.json` or `~/.config/datetree/state.json`) | Last-used + 6 most-recent source paths and dest paths, plus the three Settings-page toggles (see §8). |
| Run manifest | CSV per run in `os.UserConfigDir()/datetree/runs/<timestamp>.csv` (colocated with state.json) | Source, dest, date, date-source, status, sha256. The sha256 column is dedup scaffolding for v2. |

## 3. Architecture overview

Single process with three logical layers:

1. **HTTP layer** (`handlers.go`) — routes, htmx fragment endpoints, SSE.
2. **Domain** (`scanner.go`, `mover.go`, `metadata.go`) — pure functions that do the work.
3. **State** (`session.go`, `config.go`) — in-memory scan results; on-disk settings.

Single user, single active scan/run. No DB, no auth, no sessions cookie.

```
┌───────────────────────────────────────────────────────────┐
│  main.go                                                  │
│   ├── config.Load() → Config                              │
│   ├── http.NewServeMux + routes                           │
│   ├── listen on 127.0.0.1:<auto-port>                     │
│   └── openBrowser(url) [open / xdg-open; unless --no-open] │
└───────────────────────────────────────────────────────────┘
                         │
        ┌────────────────┼────────────────┐
        ▼                ▼                ▼
   GET  /           POST /scan       POST /execute
   home page        (worker pool +   (move/copy +
   form view        SSE progress)    SSE progress)
        │                │                │
        └────────────────┴────────────────┘
                         │
                         ▼
              session.Current (in-memory)
              ├── ScanResult { Files []PlannedMove }
              └── RunResult  { Moves []CompletedMove }
```

## 4. Domain model

```go
type PlannedMove struct {
    Source       string    // absolute path
    Size         int64
    Kind         FileKind  // Photo | Video | Other
    CaptureDate  time.Time
    DateSource   DateSource // ExifOriginal | ExifDateTime | XMP | PNGCreationText | PNGTime | QuickTime | Mtime
    DestRel      string    // "2024/20240314/IMG_1234.CR3"
    DestAbs      string    // joined with chosen destination root
    Conflict     bool      // dest already exists (soft-match expands the check; see §7)
    Skipped      bool      // reserved; row inclusion is currently driven by the preview form's include= values
    Error        string    // metadata read errors
}

type CompletedMove struct {
    PlannedMove
    Status   MoveStatus // Moved | Copied | Skipped | Failed
    SHA256   string     // computed during copy/verify (used for v2 dedup)
    Duration time.Duration
    ErrMsg   string     // populated on Failed; surfaces in the manifest's error column
}
```

The scanner produces `[]PlannedMove`; the mover consumes the same slice and produces `[]CompletedMove`.

## 5. UI flow

Five screens (all server-rendered fragments via htmx `hx-swap`):

1. **Home** — `GET /`
   - Two text inputs: source path, destination root (combobox-style: focus reveals a recents dropdown; default value is last-used).
   - "Scan" button → `POST /scan`. Mode (Move/Copy) is chosen on the preview screen, not here.

2. **Scanning** — htmx swaps in a progress fragment. SSE endpoint `GET /scan/events` pushes `{processed, total, current}` updates. Cancel button calls `POST /scan/cancel`. The final `complete` event swaps in the preview body.

3. **Preview** — table of planned moves:
   | ✓ | Source | Capture Date | Date Source | Destination | Conflict |
   - Checkbox column lets the user uncheck individual files; conflict rows are pre-disabled. Click a row to select it; shift-click extends the range, cmd/ctrl-click toggles. The selection drives bulk **Tick selected** / **Untick selected** buttons that flip the checkboxes for the highlighted rows in one step.
   - Selections post as `include=<row-index>` form values when an Execute button fires; rows the user unchecks never reach Execute, the manifest, or the Done counts.
   - Summary bar: "1,234 files, 23 conflicts, 12 mtime fallbacks, 3 errors."
   - **Execute (Move)** / **Execute (Copy)** buttons → `POST /execute` with the chosen mode.
   - **Back** link → home (the just-scanned source/dest sit at the top of the recents list, so they pre-fill on return).

4. **Executing** — same SSE pattern as scanning. Live count + currently-active file.

5. **Done** — summary: moved/copied count, skipped count, errors list, link to manifest CSV.

## 6. Metadata pipeline

**Clarification (post-v1):** dispatch within the photo tier is by **content sniff**, not extension. Extension still gates the kind (Photo / Video / Other) at `scanner.classify()` so non-image files drop out of the scan; once a file is in the photo or video tier, the first ~16 bytes pick the reader. iCloud Optimize Storage, Photos.app exports, and AirDrop "Most Compatible" silently re-encode iPhone screenshots to JPEG while leaving the `.PNG` filename intact — extension-based dispatch sent those to `bep` declaring `ImageFormat: PNG`, which fails on JPEG bytes and silently fell through to mtime. Sniff is authoritative.

| Sniffed magic | Library |
|---|---|
| PNG signature | `bep/imagemeta` (PNG) |
| JPEG `FFD8FF` | `evanoberholster/imagemeta` |
| `RIFF...WEBP` | `bep/imagemeta` (WebP) |
| TIFF `II*\0` / `MM\0*` | `evanoberholster/imagemeta` (CR2/NEF/ARW/DNG/TIFF), `bep/imagemeta` if extension is `.pef` |
| ISO BMFF `ftyp` brand `heic`/`heix`/`hevc`/`hevx`/`mif1`/`msf1` | `evanoberholster/imagemeta` |
| ISO BMFF `ftyp` brand `avif`/`avis` | `bep/imagemeta` (AVIF) |
| ISO BMFF `ftyp` brand `crx ` | `evanoberholster/imagemeta` (CR3) |
| ISO BMFF other `ftyp` brands | `abema/go-mp4` (MP4/MOV/M4V/LRF/LRV) |
| anything else | mtime |

```
File  ──► extension check ──► reader chosen by tier + format
  │                              │
  │                              ├── Photo (JPG/HEIC/CR2/CR3/NEF/ARW/DNG):
  │                              │     evanoberholster/imagemeta.Decode(file)
  │                              │     └─ DateTimeOriginal → ModifyDate → mtime
  │                              │
  │                              ├── Photo (PNG/WebP/AVIF/PEF):
  │                              │     bep/imagemeta.Decode(file, EXIF|XMP)
  │                              │     └─ EXIF DateTimeOriginal
  │                              │        → EXIF ModifyDate/DateTime
  │                              │        → xmp:CreateDate
  │                              │        → PNG tEXt "Creation Time" (PNG only,
  │                              │           native walker — bep does not emit)
  │                              │        → PNG tIME chunk (PNG only)
  │                              │        → mtime
  │                              │
  │                              ├── Video MP4/MOV/M4V/LRF/LRV: go-mp4 walk
  │                              │     └─ try com.apple.quicktime.creationdate
  │                              │        → mvhd.creation_time → mtime
  │                              │
  │                              └── Mtime-only tier (MTS, M2TS, BMP, GIF, JXL,
  │                                  long-tail RAW, secondary video, drone
  │                                  proxy): mtime
  │
  └── PlannedMove with CaptureDate + DateSource label, drawn from the
      seven-value enum: exif_original, exif_datetime, xmp_create,
      png_creation_text, png_time, quicktime, mtime.
```

Destination directory paths are rendered via `time.Format(cfg.PathTemplate, captureDate)` after the worker pool finishes. The basename is then joined onto the rendered directory to form `DestRel`. Literal segments in the template (e.g., `archive/` in `archive/2006/20060102`) pass through unchanged.

Worker pool: `N = runtime.NumCPU()` goroutines pull from a `chan string` of paths, push results to a `chan PlannedMove`. Aggregator collects into the `ScanResult`. Errors per-file are captured in the struct, not raised.

**Library entry points (verify when implementing):**
- `evanoberholster/imagemeta.Decode(io.ReadSeeker)` returns a metadata struct exposing `ExifIFD.DateTimeOriginal` and `IFD0.ModifyDate`. CR3 is auto-detected.
- `bep/imagemeta.Decode(Options{R, ImageFormat, Sources, HandleTag})` invokes `HandleTag` per tag with `(Source, Tag, Namespace, Value)` — used here for EXIF DateTimeOriginal/ModifyDate and XMP CreateDate.
- PNG `tEXt` "Creation Time" and `tIME` chunks aren't surfaced by either library, so a small native PNG chunk walker reads them as the final pre-mtime step.
- For MP4/MOV: walk boxes via `mp4.ExtractBoxWithPayload`, find `moov.mvhd` and (future) `moov.meta.keys`+`moov.meta.ilst`.

## 7. File operations

```go
func PerformMove(p PlannedMove, mode Mode) (CompletedMove, error) {
    if p.Conflict { return skipped(p), nil }
    if err := os.MkdirAll(filepath.Dir(p.DestAbs), 0o755); err != nil { … }

    if mode == Move && sameFilesystem(p.Source, p.DestAbs) {
        return os.Rename(...)
    }
    // Cross-FS or copy mode: copy → fsync → size verify → (if Move) delete source
    sha, err := copyAndHash(p.Source, p.DestAbs)
    if err != nil { … }
    if !verifySize(p.DestAbs, p.Size) { rollback; error }
    if mode == Move { os.Remove(p.Source) }
    return CompletedMove{..., SHA256: sha}, nil
}
```

`sameFilesystem` checks the `Dev` field of `unix.Stat_t` for both the source and `filepath.Dir(dest)`.

`copyAndHash` streams through a `sha256.New()` writer alongside the destination — no extra read pass needed.

The cross-FS copy path preserves the source mtime on the destination via `os.Chtimes` after verify (and source-removal in Move mode), as a secondary capture-date record alongside EXIF. Same-FS `os.Rename` already preserves mtime atomically.

Path-template validation runs once at scan-handler entry, against `cfg.PathTemplate` as loaded from `state.json`. A malformed template (empty, no date token, absolute, contains `..`, or contains illegal bytes) short-circuits the scan and renders an error fragment with a /settings link — no `PlannedMove`s are produced and no files move. There is no graceful fallback to `DefaultPathTemplate` at execute time, since silent substitution would land files in directories the user never asked for.

**Soft-match destination folders.** When `softMatchDestination` is true, the scan loop applies a hybrid policy per file before committing `DestAbs`/`Conflict` to the plan, so the preview reflects exactly what Execute will do. The leaf `prefix` is the longest run of digits at the start of the rendered leaf — `20260509` for either `2006/20060102` or `2006/20060102-1504`. Only the leaf is in scope; parent path levels are never rewritten.

1. `os.ReadDir(parent)`. ENOENT → no candidates (fresh tree; the exact-named folder is mkdir'd at execute time).
2. Walk entries once. Classify each directory whose name begins with `prefix` (case-insensitive) as either *exact* (name equals the rendered leaf, case-insensitive) or *soft* (name extends the leaf with a non-digit character — `20260509 wedding`, `20260509-trip`, `20260509_archive`, but not `202605091`, which is a different day's prefix).
3. **Routing.** Use the on-disk exact-named folder if it exists (preserving its actual casing). Otherwise, if there is *exactly one* soft candidate, use that. Otherwise (zero candidates, or two or more), use the rendered leaf as-is (the exact-named folder is created on Execute).
4. **Conflict detection.** A row is flagged Conflict when the source filename exists inside the exact-named folder OR inside ANY soft candidate, even ones the routing step did not pick. This catches the "already imported into a descriptive folder" case that the rendered template path alone would miss.

Toggle off: behaviour is unchanged from v1 — the rendered leaf is used verbatim and the conflict check stats only that exact path. `PerformMove` is unaware of the toggle: it operates on whatever `DestAbs` Scan committed, so all routing logic lives in one place.

## 8. Settings & state

`os.UserConfigDir()/datetree/state.json` (macOS: `~/Library/Application Support/datetree/state.json`; Linux: `~/.config/datetree/state.json` or `$XDG_CONFIG_HOME/datetree/state.json`):
```json
{
  "recentSources": ["/Volumes/SDCARD/DCIM", "..."],   // max 6, MRU order
  "recentDestinations": ["/Volumes/Photos", "..."],   // max 6, MRU order
  "lastMode": "move",
  "pathTemplate": "2006/20060102",                    // Go time-format layout
  "alignMtimeToExif": true,                           // Settings toggle
  "softMatchDestination": true,                       // Settings toggle
  "updateChecksEnabled": true,                        // Settings toggle
  "dismissedUpdateVersion": ""                        // tag the user dismissed the banner for
}
```

Loaded on startup; rewritten after each successful run and on every Settings save. Older state.json files predating the settings fields load with defaults: `pathTemplate` becomes `2006/20060102` (preserves v1 behaviour), and the booleans default to true. Recents/last-mode keep their existing semantics. `softMatchDestination` is wired into the scan loop (§7), so the preview reflects the resolved destination and conflict signal. `alignMtimeToExif` consumption lands in a follow-up task.

**Updates.** When `updateChecksEnabled` is true (default), the binary issues one HTTPS GET to `https://api.github.com/repos/miking7/datetree-photos/releases/latest` at startup and compares the returned `tag_name` to the baked-in version. When newer (and not equal to `dismissedUpdateVersion`), a dismissible banner is rendered on every page; clicking Update fetches the matching `datetree_<goos>_<goarch>.tar.gz` and `checksums.txt` from the release, verifies SHA256 against the listed digest, extracts the binary, and hands the bytes to `github.com/minio/selfupdate`. After a successful apply, the running process is unchanged — the user must restart `datetree` to pick up the new version. `dev` builds skip the check entirely. Dismiss writes the tag into `dismissedUpdateVersion`; the banner re-appears for any later release. Toggle off disables only the launch-time check — manual "Check for updates" from the Settings page still works.

`pathTemplate` is wired through to the scanner: `Scan(ctx, source, dest, template, softMatch, reporter)` renders each `DestRel` via `time.Format(template, captureDate)`. The scan handler calls `validatePathTemplate(cfg.PathTemplate)` before walking, so the user's chosen template actually takes effect on the next import — and a corrupt template surfaces as a UI error rather than misrouting files.

## 9. Run manifest

After each run, write CSV to `os.UserConfigDir()/datetree/runs/<RFC3339-timestamp>.csv` (colocated with `state.json`; see §8 for the per-OS path):

```csv
source,destination,capture_date,date_source,status,sha256,bytes,duration_ms,error
/Volumes/SDCARD/DCIM/IMG_1234.CR3,/Volumes/Photos/2024/20240314/IMG_1234.CR3,2024-03-14T10:23:01,exif_original,moved,e3b0c44…,28415126,142,
```

The `sha256` column is computed during copy operations (free) and stored even on Move-with-rename (computed on the destination after rename, slightly more work but enables v2 dedup).

## 10. Project layout

```
datetree-photos/
├── go.mod
├── go.sum
├── Makefile                  # `make` → templ generate && go build
├── README.md
├── main.go                   # flag parsing, HTTP setup, browser launch
├── handlers.go               # route handlers, SSE
├── scanner.go                # walk + worker pool, soft-match resolution
├── metadata.go               # evano + bep imagemeta + go-mp4 + mtime fallback
├── sniff.go                  # content-sniff dispatch (first 16 bytes)
├── mover.go                  # rename / copy+verify+delete, sha256
├── progress.go               # Reporter pub-sub for SSE consumers
├── session.go                # in-memory current scan/run + Done snapshot
├── config.go                 # state.json read/write, path-template validation
├── manifest.go               # per-run CSV writer
├── filesystem.go             # sameFilesystem (cross-platform)
├── filesystem_darwin.go      # dev() via syscall.Stat_t (darwin build tag)
├── filesystem_linux.go       # dev() via syscall.Stat_t (linux build tag)
├── components/               # templ files
│   ├── layout.templ
│   ├── home.templ
│   ├── progress.templ
│   ├── preview.templ
│   ├── done.templ
│   ├── settings.templ
│   └── settings_helpers.go   # tiny helper alongside the templ
└── static/
    ├── htmx.min.js           # vendored
    ├── htmx-ext-sse.js       # vendored htmx SSE extension
    ├── pico.min.css          # vendored
    └── app.css               # custom tweaks (preview table, combobox, settings)
```

All static assets and generated templ Go files compile into the binary via `go:embed`. No runtime file dependencies.

## 11. Build & run

`Makefile`:
```makefile
.PHONY: build run dev clean

build:
	templ generate
	go build -o datetree .

run: build
	./datetree

dev:
	templ generate --watch &
	go run . --no-open

clean:
	rm -f datetree
	find . -name '*_templ.go' -delete
```

CLI flags:
- `--port <n>` — explicit port (default: pick free in 9700–9799)
- `--no-open` — don't auto-open browser

## 12. v2 (dedup) scaffolding — what v1 should leave in place

- `CompletedMove.SHA256` populated for every move.
- Run manifest CSV is the persistence layer for "what's already in the library."
- v2 will: (a) walk destination root, build SHA index from existing files + historical manifests, (b) during scan, hash incoming files and flag matches as "already imported, skip."
- v1 should keep the metadata package's signatures stable enough that adding `Hash() (string, error)` to `PlannedMove` is non-breaking.

## 13. Out of scope (v1)

- Authentication / multi-user
- Network access — outbound HTTPS to GitHub Releases only, for update checks and self-update. User-disablable via the Settings page (default on). Server bind remains 127.0.0.1 only.
- Settings UI — partial. **In scope** for v1: destination path template, align-mtime-to-EXIF toggle, soft-match destination folders toggle, update-check toggle. Path template and soft-match are wired through the scan path; align-mtime consumption is the remaining follow-up. **Out of scope**: ext-list customization, concurrency knob, per-source presets, Apple Photos integration.
- Sidecar pairing (XMP/JPG-pair/MODD)
- Resumable runs after crash
- Windows support — would need a different `syscall.Stat_t` shape and `start` for browser launch. Linux is supported alongside macOS via `os.UserConfigDir()` paths and an `xdg-open` shellout.
- Direct integration with Apple Photos library
- Dedup against destination

## 14. Testing approach

- Unit test the metadata pipeline against a small fixture set (one file per format) committed in `testdata/`. Use real files, not mocks — metadata libs are the whole point.
- Unit test `sameFilesystem` and the cross-FS copy path with `t.TempDir()`.
- Integration test: scan a fixture folder, assert PlannedMove output structurally; execute, assert files moved correctly; verify CSV manifest.
- No need for end-to-end browser tests in v1 — htmx fragments are simple enough that handler-level tests are sufficient.

## 15. Verification (how to test end-to-end manually)

1. `make build`
2. `./datetree`
3. Browser opens to `http://127.0.0.1:97xx`.
4. Point source at a test folder containing samples of each format (JPEG, HEIC, CR3, MP4, MTS).
5. Set destination to a scratch folder (not `/Volumes/Photos`).
6. Click Scan → verify preview shows correct dates and date-source labels (EXIF for CR3, QuickTime for MP4, mtime for MTS).
7. Click Execute (Move mode) → verify files land in `<scratch>/YYYY/YYYYMMDD/`.
8. Re-run on same source → verify Conflict column flags everything as already-present and skipped.
9. Open the manifest CSV in Numbers — verify all columns populated, sha256 present.
