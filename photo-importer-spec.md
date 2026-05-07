# photo-importer — Spec & Plan (v1)

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
| Distribution | Single static binary via `go build` | User runs `./photo-importer` and it self-launches the browser. |
| Web stack | stdlib `net/http` + `templ` + htmx + Pico.css | templ gives type-safe templates that pair well with htmx fragment endpoints. Pico.css gives a modern look from semantic HTML alone — no Tailwind class-noise. |
| Bind address | `127.0.0.1` only | Localhost single-user. No auth needed. |
| Image metadata | `github.com/evanoberholster/imagemeta` | Pure Go, MIT, actively maintained, only pure-Go lib with explicit CR3 support. Handles JPEG/HEIC/CR2/NEF/ARW/DNG/CR3. |
| Video metadata | `github.com/abema/go-mp4` | Pure Go, MIT. Reads `mvhd` and QuickTime `keys`/`ilst` atoms (`com.apple.quicktime.creationdate`). |
| MTS/AVCHD | Fall back to file mtime | No Go library reads AVCHD MDPM date; mtime is correct for fresh-off-the-card files anyway. |
| Date priority | `EXIF DateTimeOriginal` → `EXIF DateTime` → QuickTime atoms → file mtime | EXIF original survives copying; mtime is last-resort fallback. |
| Destination structure | Hardcoded `{Year}/{YYYYMMDD}/` (v1) | Matches current `organize` config. Settings page deferred. |
| Filtering | Whitelist by extension | Avoids moving `.DS_Store`, `.MODD`, etc. |
| File operation | Move (default), with **Copy** UI toggle | Same-FS uses `os.Rename`; cross-FS falls back to copy + verify-size + delete. |
| Conflict policy | Skip on collision (count in summary) | Matches current `organize` behavior. |
| Recursion | Recurse source folder fully by default | Picks up Sony's `Private/M4ROOT/CLIP` automatically. |
| Mid-run errors | Log per-file, continue, summarize at end | Don't abort whole batch for one bad file. |
| Concurrency | Worker pool (`runtime.NumCPU()`), htmx-SSE progress | Live `processed/total` updates in UI. |
| Sidecars (XMP, JPG-pair, MODD) | Ignored in v1, treated as independent files | RAW+JPG pairs land in same date folder via date logic anyway. |
| Settings persistence | `~/Library/Application Support/photo-importer/state.json` | Last-used + 6 most-recent source paths and dest paths. |
| Run manifest | CSV per run in `~/Library/Application Support/photo-importer/runs/<timestamp>.csv` | Source, dest, date, date-source, status, sha256. The sha256 column is dedup scaffolding for v2. |

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
│   └── exec.Command("open", url) [unless --no-open]        │
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
    DateSource   DateSource // ExifOriginal | ExifDateTime | QuickTime | Mtime
    DestRel      string    // "2024/20240314/IMG_1234.CR3"
    DestAbs      string    // joined with chosen destination root
    Conflict     bool      // dest already exists
    Skipped      bool      // user toggled off
    Error        string    // metadata read errors
}

type CompletedMove struct {
    PlannedMove
    Status   MoveStatus // Moved | Copied | Skipped | Failed
    SHA256   string     // computed during copy/verify (used for v2 dedup)
    Duration time.Duration
}
```

The scanner produces `[]PlannedMove`; the mover consumes the same slice and produces `[]CompletedMove`.

## 5. UI flow

Five screens (all server-rendered fragments via htmx `hx-swap`):

1. **Home** — `GET /`
   - Two text inputs: source path, destination root (auto-complete from recent list, default to last-used).
   - Mode toggle: **Move** (default) / **Copy**.
   - "Scan" button → `POST /scan`.

2. **Scanning** — htmx swaps in a progress fragment. SSE endpoint `GET /scan/events` pushes `{processed, total}` updates. Cancel button calls `POST /scan/cancel`.

3. **Preview** — table of planned moves:
   | ✓ | Source | Capture Date | Source | Destination | Conflict |
   - Checkbox column lets user uncheck individual files. State posted via htmx on click.
   - Summary bar: "1,234 files, 23 conflicts, 12 mtime fallbacks, 3 errors."
   - "Execute" button → `POST /execute`.
   - "Re-scan" button → back to home with same paths preserved.

4. **Executing** — same SSE pattern as scanning. Live count + currently-active file.

5. **Done** — summary: moved/copied count, skipped count, errors list, link to manifest CSV.

## 6. Metadata pipeline

```
File  ──► extension check ──► reader chosen by kind
  │                              │
  │                              ├── Photo: imagemeta.Decode(file)
  │                              │     └─ try DateTimeOriginal → DateTime → mtime
  │                              │
  │                              ├── Video MP4/MOV: go-mp4 walk
  │                              │     └─ try com.apple.quicktime.creationdate
  │                              │        → mvhd.creation_time → mtime
  │                              │
  │                              └── MTS / Other: mtime
  │
  └── PlannedMove with CaptureDate + DateSource label
```

Worker pool: `N = runtime.NumCPU()` goroutines pull from a `chan string` of paths, push results to a `chan PlannedMove`. Aggregator collects into the `ScanResult`. Errors per-file are captured in the struct, not raised.

**Library entry points (verify when implementing):**
- `imagemeta.Decode(io.ReadSeeker)` returns metadata struct including `DateTimeOriginal()` and `DateTime()`.
- For CR3 specifically: `imagemeta.DecodeCR3(io.ReadSeeker)` (or auto-detect via `Decode`).
- For MP4/MOV: walk boxes via `mp4.ReadBoxStructure`, find `moov.mvhd` and `moov.meta.keys`+`moov.meta.ilst`.

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

## 8. Settings & state

`~/Library/Application Support/photo-importer/state.json`:
```json
{
  "recentSources": ["/Volumes/SDCARD/DCIM", "..."],   // max 6, MRU order
  "recentDestinations": ["/Volumes/Photos", "..."],   // max 6, MRU order
  "lastMode": "move"
}
```

Loaded on startup; rewritten after each successful run. No defaults baked in beyond an empty state.

## 9. Run manifest

After each run, write CSV to `~/Library/Application Support/photo-importer/runs/<RFC3339-timestamp>.csv`:

```csv
source,destination,capture_date,date_source,status,sha256,bytes,duration_ms,error
/Volumes/SDCARD/DCIM/IMG_1234.CR3,/Volumes/Photos/2024/20240314/IMG_1234.CR3,2024-03-14T10:23:01,exif_original,moved,e3b0c44…,28415126,142,
```

The `sha256` column is computed during copy operations (free) and stored even on Move-with-rename (computed on the destination after rename, slightly more work but enables v2 dedup).

## 10. Project layout

```
photo-importer/
├── go.mod
├── go.sum
├── Makefile                  # `make` → templ generate && go build
├── README.md
├── main.go                   # flag parsing, HTTP setup, browser launch
├── handlers.go               # route handlers, SSE
├── scanner.go                # walk + worker pool
├── metadata.go               # imagemeta + go-mp4 + mtime fallback
├── mover.go                  # rename / copy+verify+delete
├── session.go                # in-memory current scan/run
├── config.go                 # state.json read/write
├── manifest.go               # CSV writer
├── filesystem.go             # sameFilesystem, sanitize paths
├── components/               # templ files
│   ├── layout.templ
│   ├── home.templ
│   ├── progress.templ
│   ├── preview.templ
│   └── done.templ
└── static/
    ├── htmx.min.js           # vendored (or CDN)
    ├── pico.min.css          # vendored
    └── app.css               # ~50 lines of custom tweaks
```

All static assets and generated templ Go files compile into the binary via `go:embed`. No runtime file dependencies.

## 11. Build & run

`Makefile`:
```makefile
.PHONY: build run dev clean

build:
	templ generate
	go build -o photo-importer .

run: build
	./photo-importer

dev:
	templ generate --watch &
	go run . --no-open

clean:
	rm -f photo-importer
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
- Network access (only `127.0.0.1`)
- Settings UI (destination pattern, ext list, concurrency knob — all hardcoded in v1)
- Sidecar pairing (XMP/JPG-pair/MODD)
- Resumable runs after crash
- Windows / Linux support (macOS-only paths and `open` shellout — would need build tags later)
- Direct integration with Apple Photos library
- Dedup against destination

## 14. Testing approach

- Unit test the metadata pipeline against a small fixture set (one file per format) committed in `testdata/`. Use real files, not mocks — metadata libs are the whole point.
- Unit test `sameFilesystem` and the cross-FS copy path with `t.TempDir()`.
- Integration test: scan a fixture folder, assert PlannedMove output structurally; execute, assert files moved correctly; verify CSV manifest.
- No need for end-to-end browser tests in v1 — htmx fragments are simple enough that handler-level tests are sufficient.

## 15. Verification (how to test end-to-end manually)

1. `make build`
2. `./photo-importer`
3. Browser opens to `http://127.0.0.1:97xx`.
4. Point source at a test folder containing samples of each format (JPEG, HEIC, CR3, MP4, MTS).
5. Set destination to a scratch folder (not `/Volumes/Photos`).
6. Click Scan → verify preview shows correct dates and date-source labels (EXIF for CR3, QuickTime for MP4, mtime for MTS).
7. Click Execute (Move mode) → verify files land in `<scratch>/YYYY/YYYYMMDD/`.
8. Re-run on same source → verify Conflict column flags everything as already-present and skipped.
9. Open the manifest CSV in Numbers — verify all columns populated, sha256 present.
