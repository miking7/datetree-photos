# testdata

Fixture files used by the metadata-pipeline tests (spec §14).

The metadata libraries (`github.com/evanoberholster/imagemeta`,
`github.com/bep/imagemeta`, `github.com/abema/go-mp4`) **are** the unit under
test — mocks would defeat the purpose. Two fixture sources coexist here:

- **Real camera/phone files** (`sample.<ext>` checked in) for formats whose
  metadata layout depends on vendor-specific quirks the parsers must accept.
- **Synthesised-but-spec-conforming fixtures**, built byte-for-byte at test
  time (see `metadata_test.go`'s `buildPNG` helper). The bytes are real PNG /
  EXIF / tIME / tEXt content — no parser shortcuts are taken — so they
  exercise the same code paths as a Photoshop export. Used where committing a
  binary fixture would add license drag without test-coverage gain.

## Expected camera-source fixtures

| File | Format | Why this fixture exists |
|---|---|---|
| `sample.jpg` | JPEG | Has EXIF `DateTimeOriginal`. Baseline happy path for the photo reader. |
| `sample.heic` | HEIC (iPhone) | Exercises HEIF/HEIC EXIF extraction. |
| `sample.cr3` | Canon CR3 (RAW) | Exercises `evanoberholster/imagemeta`'s CR3 path — the main reason that lib was chosen. |
| `sample.mp4` | MP4 | Has the `com.apple.quicktime.creationdate` atom. |
| `sample.mov` | QuickTime | Exercises QuickTime atom walking. |
| `sample.mts` | AVCHD | No embedded date — exercises the mtime fallback (spec §6, §2 locked decision). |

## Synthesised fixtures (built in `metadata_test.go`)

| Scenario | What it asserts |
|---|---|
| PNG with `eXIf` chunk (ModifyDate) | bep reads EXIF -> `exif_datetime` |
| PNG with `tIME` chunk only | native PNG walker -> `png_time` |
| PNG with `tEXt` "Creation Time" | native PNG walker -> `png_creation_text` |
| Bare PNG (no metadata) | falls through to `mtime` |
| BMP, GIF, AVI, MKV, JXL, ORF, MTS stubs | mtime-only tier classifies correctly |

## Adding a new camera-source fixture

1. Drop the real file in here with a `sample.<ext>` name.
2. Note its expected capture date in the test that consumes it (don't put it
   in this README — the test is the source of truth).
3. Keep files small — these are committed to git. Trim long videos to a
   second or two if you can without disturbing the metadata atoms.
