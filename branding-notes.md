# Branding Notes — naming, tone, and icon direction

Working document to pick a public name for this project before publishing it on GitHub. The user makes the final call; this is research + recommendation, not a decision.

## 1. What we're naming

A single static Go binary (macOS and Linux for v1) that opens a local web UI on `127.0.0.1` to:

1. Scan a source folder (typically an SD card or camera mount).
2. Read EXIF / XMP / QuickTime / mtime capture dates.
3. Preview a table of planned moves into a date-sorted destination tree (`{Year}/{YYYYMMDD}/`).
4. Execute the move (or copy), with conflict detection, soft-match destination folders, and a CSV manifest.

Replaces a creaky bash script that drives the Python `organize` tool. v1 = import. v2 = dedup.

Tone the name should land in:

- Photographer-friendly, not enterprise / DAM.
- Local + private (not cloud, not a service, no auth).
- Single-purpose: ingest from card to library. Not a viewer, not an editor, not a catalog.
- Mechanical / decisive — this is a workflow tool that *moves files*, not a contemplative app.

Anti-tone: avoid AI-buzzword names, avoid "smart"/"cloud"/"sync" framing, avoid anything that implies a server-side product.

## 2. Landscape — what we're competing or coexisting with

| Tool | Type | Tone of name |
|---|---|---|
| ExifTool | CLI library | Functional, technical |
| Photo Mechanic | Commercial ingest app | Professional, mechanical |
| Lightroom Classic | DAM + editor (Adobe) | Light/optics imagery |
| Aperture | DAM (Apple, defunct) | Optics imagery |
| digiKam | DAM (KDE) | Hybrid, slightly nerdy |
| PhotoPrism | Self-hosted DAM | Optics + crystal imagery |
| Immich | Self-hosted DAM | Invented word |
| Mylio | Cross-device DAM | Brand-y, "My Life Organized" |
| Rapid Photo Downloader | Linux ingest | Literal/utility |
| Phockup | Python ingest CLI | Playful (phocus + backup) |
| Elodie | Python ingest CLI | Human name |
| sashacmc/photo-importer | CLI (Python) | Literal, **slug taken on GitHub** |
| themightymanuel/Photo-Importer | Windows app | Literal, **slug taken on GitHub** |
| immich-go | Immich CLI (Go) | Derivative |
| osxphotos | Apple Photos library tool | Literal |

Observations:

- The literal slug `photo-importer` is **already used by at least two unrelated GitHub repos**. Even if we shipped under the local module path, the public repo should use a different name.
- Ingest-side tools tend toward two camps: literal/utility (`phockup`, `photo-ingest`, `rapid-photo-downloader`) or playful human names (`elodie`). Most are Python; a Go entrant has space to feel different.
- DAM-side tools have already claimed the prettiest natural words (Lightroom, Aperture, Halide, Lumen, Mylio, Frame.io). Avoid trying to compete in that vocabulary — we're not a DAM.
- Mechanical/cargo metaphors are *underused* in this space and fit the workflow well: SD card in, conflict-checked moves out.

## 3. Candidate list (16 names across four flavours)

### Literal / utility
1. **OffCard** — descriptive: "get the photos off the card."
2. **Cardpop** — playful, short, evokes ejecting the card.
3. **Sift** — exactly what it does. Generic.
4. **Sluice** — flow + filter (mining metaphor). Distinctive.

### Mechanical / decisive
5. **Latch** — the click of seating a card; locks files into the library.
6. **Clasp** — sibling of Latch; slightly softer.
7. **Stow** — put cargo where it belongs; fits the date-sorted tree.
8. **Berth** — a place to dock. Cargo metaphor.

### Photographic / period
9. **Plate** — photographic plate. Short. Probably too generic.
10. **Cassette** — film cassette / cartridge.
11. **Daguerre** — inventor of photography. Distinctive, hard to spell.
12. **Tessera** — small tile / piece in a mosaic. "Each file in its slot."

### Logistics / shipping
13. **Manifest** — already used internally for the run CSV. Cargo manifest doubles as a literal feature.
14. **Bellhop** — carries luggage to the right room. Friendly.
15. **Ferry** — moves things across. Common word.

### Date-anchored
16. **Dateline** — a line dividing dates. Newsroom-y; fits a time-sorted ingest.

## 4. Top 7 with full treatment

### A. Sluice (recommended)
- **Tagline:** *Sluice your photos off the card and into a date-sorted library.*
- **Why it fits:** Sluicing is the gold-mining act of running gravel through a channel that catches what matters and lets the rest go — exactly the preview-and-execute workflow. Photographers will hear it as motion + selection; non-photographers will hear it as "channeled flow." Single syllable, easy to type, easy to brand.
- **Icon direction:** A simple side-on channel: angled trough, three or four small rectangles (representing files / photos) flowing down it; the lower edge resolves into a tidy stacked-folder shape. Could work as a single bold glyph: a chevron-like trough with a notch. Limit to one accent colour over a neutral background — Pico.css default palette already gives a tasteful base.
- **Risks:** Word is unfamiliar to people who haven't watched gold-prospecting videos; some users will mispronounce ("slice"). No major GitHub or trademark collisions in the photo space; slug `sluice` may be taken on GitHub by unrelated projects so prefer `sluice-photos` or scope to a personal/org account.
- **GitHub slug suggestions:** `sluice`, `sluice-photos`, `sluice-import`.

### B. Latch (recommended alt)
- **Tagline:** *Latch your camera card to your library.*
- **Why it fits:** SD cards click when seated — *latch* is the sound of the card meeting its slot, and the metaphor extends to "latching" each file to its permanent home in the date tree. Crisp, mechanical, decisive. Pairs well with "Scan / Preview / Latch" as the three-button workflow language.
- **Icon direction:** A stylized SD-card silhouette with a small mechanical hook or hinge cutting across one corner; or, more abstractly, two interlocking brackets. Monochrome works.
- **Risks:** "Latch" is a heavily used word — `latchbio` (bioinformatics) is a prominent GitHub org, and the generic noun makes search unfriendly. Trademark search across software classes likely returns hits.
- **GitHub slug suggestions:** `latch-photos`, `photolatch`, `latchcard`.

### C. Tessera (recommended alt)
- **Tagline:** *Each photo finds its tile.*
- **Why it fits:** A tessera is a single tile in a mosaic. The destination tree is literally a mosaic of date folders, and each file is a tile slotting into its day. Distinctive, beautiful, gives latitude for nice typography. Slightly archaic word implies craft and care — appropriate for a photographer audience.
- **Icon direction:** A 3×3 grid of small squares with one square highlighted/lit (the "current" file finding its place); or a single tilted square dropping into a grid slot.
- **Risks:** Unfamiliar word — many users will need the tagline to make sense of it. Pronunciation ambiguity (TESS-er-uh). Some prior-art software named Tessera exists in unrelated domains (medical imaging, FPGA tools); the photo space appears clear.
- **GitHub slug suggestions:** `tessera`, `tessera-photos`, `tessera-import`.

### D. OffCard
- **Tagline:** *Get your photos off the card and into the right folder.*
- **Why it fits:** Ruthlessly literal. Names the user's actual job. Two syllables. Tells you what it does before you open the README.
- **Icon direction:** SD-card silhouette with an arrow leaving the right edge. Could also do a film-canister silhouette for a more analog feel.
- **Risks:** Very generic; unlikely to be memorable as a brand. "Off-card" is also a poker term. Slug almost certainly free.
- **GitHub slug suggestions:** `offcard`, `off-card`.

### E. Cassette
- **Tagline:** *From cassette to catalog.*
- **Why it fits:** Photographic period reference (film cassette) that also reads as "cartridge" → fits SD card just fine. Warm, analog tone for a digital tool.
- **Icon direction:** Stylized film cassette in profile, with the dark leader emerging into stacked folder ridges. Mid-century palette.
- **Risks:** Common English word — search and trademark are noisy. Several unrelated `cassette` repos exist. Slightly nostalgic feel may date the tool.
- **GitHub slug suggestions:** `cassette-photos`, `photocassette`.

### F. Stow
- **Tagline:** *Stow today's shoot.*
- **Why it fits:** Stow = put cargo where it belongs. Maps directly to "render destination path → move file there." Terse, friendly verb.
- **Icon direction:** Simple boxed-folder glyph; or, a small rectangle (file) sliding into a labelled slot.
- **Risks:** **GNU Stow** is a well-known dotfiles symlink manager — strong namespace collision in CLI-tool space; users will conflate the two. Probably a dealbreaker.
- **GitHub slug suggestions:** would need disambiguation — `stow-photos`.

### G. Manifest
- **Tagline:** *Every shoot, on the manifest.*
- **Why it fits:** The project already writes a CSV "run manifest" per import — the name is *literally* the artifact. Cargo / shipping connotation matches the move-files-into-the-right-bin metaphor.
- **Icon direction:** Clipboard with a checkmark; or, a stack of slips/cards. Could lean into a "shipping manifest" feel.
- **Risks:** "Manifest" is heavily overloaded in software (web app manifests, Kubernetes manifests, Android manifests). Search and discoverability suffer. Strong meaning collision.
- **GitHub slug suggestions:** `photo-manifest`, `shoot-manifest`.

## 5. Recommendations (ordered)

1. **Sluice** — best metaphor coverage (filter + flow + route), distinctive in the photo-tools space, strong icon potential, room for tagline play. Pronunciation ambiguity is the only real friction.
2. **Latch** — best tone fit (mechanical, decisive, photographer-shaped), but namespace is crowded.
3. **Tessera** — best aesthetic ceiling for a quietly crafted tool; needs the tagline to do work.

Skip recommendations: **Stow** (GNU Stow collision is too strong); **Manifest** (term-overload is too high). Worth keeping in reserve: **OffCard** if the user prefers ruthlessly literal naming over evocative branding.

## 6. Drop-in copy for whichever name wins

### README hero line
> **Sluice** — preview and move photos off your camera card into a date-sorted library, from a single Go binary on macOS or Linux.

### GitHub *About* (one sentence, ≤ 350 chars)
> A single-binary local web UI for previewing and moving photos and videos off an SD card into a date-sorted library, using EXIF / XMP / QuickTime capture dates with mtime fallback. Local, no cloud, no auth.

### Short pitch (for HN / social)
> Tired of `import_photos | organize` not knowing what EXIF is, I built a small Go binary that opens a local web UI, scans your SD card, shows you a preview table of planned date-sorted moves, and lets you uncheck rows before clicking Execute. No server, no auth, no Docker. macOS and Linux.

## 7. Open questions for the user

- Pronunciation tolerance: is "sluice" too unfamiliar for the audience you're publishing to?
- Do you want the public repo slug to match the binary name (`sluice` ↔ `sluice`) or is a hyphenated variant fine (`sluice-photos`, repo named for clarity, binary kept short)?
- Any names on the brainstorm list that you want expanded into a full treatment that I skipped?

The Go module path and import paths in this repo are unaffected by the public name choice — `go.mod` can stay as-is, or be renamed in a single follow-up commit when the GitHub URL is confirmed.
