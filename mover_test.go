package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFile creates a file at path with the given content and parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestPerformMoveSameFSRename(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "a.bin")
	dst := filepath.Join(tmp, "dst", "2024", "20240314", "a.bin")
	body := "hello rename"
	writeFile(t, src, body)

	plan := PlannedMove{Source: src, DestAbs: dst, Size: int64(len(body))}
	cm, err := PerformMove(plan, ModeMove)
	if err != nil {
		t.Fatalf("PerformMove: %v", err)
	}
	if cm.Status != StatusMoved {
		t.Errorf("Status: got %s, want moved", cm.Status)
	}
	if cm.SHA256 != sha256Hex(body) {
		t.Errorf("SHA256: got %q, want %q", cm.SHA256, sha256Hex(body))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("expected source removed, stat err = %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != body {
		t.Errorf("dest body: got %q, want %q", got, body)
	}
}

func TestPerformMoveCopyMode(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "b.bin")
	dst := filepath.Join(tmp, "dst", "b.bin")
	body := "hello copy"
	writeFile(t, src, body)

	plan := PlannedMove{Source: src, DestAbs: dst, Size: int64(len(body))}
	cm, err := PerformMove(plan, ModeCopy)
	if err != nil {
		t.Fatalf("PerformMove: %v", err)
	}
	if cm.Status != StatusCopied {
		t.Errorf("Status: got %s, want copied", cm.Status)
	}
	if cm.SHA256 != sha256Hex(body) {
		t.Errorf("SHA256: got %q, want %q", cm.SHA256, sha256Hex(body))
	}
	// Source must still exist in copy mode.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("expected source kept, got err %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != body {
		t.Errorf("dest body mismatch")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if info.Size() != plan.Size {
		t.Errorf("dest size: got %d, want %d", info.Size(), plan.Size)
	}
}

func TestPerformMoveConflictSkipped(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "c.bin")
	dst := filepath.Join(tmp, "dst", "c.bin")
	writeFile(t, src, "src body")
	writeFile(t, dst, "pre-existing dest")

	plan := PlannedMove{Source: src, DestAbs: dst, Size: 8, Conflict: true}
	cm, err := PerformMove(plan, ModeMove)
	if err != nil {
		t.Fatalf("PerformMove: %v", err)
	}
	if cm.Status != StatusSkipped {
		t.Errorf("Status: got %s, want skipped", cm.Status)
	}
	// Source untouched.
	if got, err := os.ReadFile(src); err != nil || string(got) != "src body" {
		t.Errorf("source mutated: %q (err=%v)", got, err)
	}
	// Dest untouched.
	if got, err := os.ReadFile(dst); err != nil || string(got) != "pre-existing dest" {
		t.Errorf("dest mutated: %q (err=%v)", got, err)
	}
}

func TestPerformMoveSourceMissing(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "missing.bin")
	dst := filepath.Join(tmp, "dst", "missing.bin")

	plan := PlannedMove{Source: src, DestAbs: dst, Size: 1}
	cm, err := PerformMove(plan, ModeMove)
	if err == nil {
		t.Errorf("expected error for missing source")
	}
	if cm.Status != StatusFailed {
		t.Errorf("Status: got %s, want failed", cm.Status)
	}
	if cm.ErrMsg == "" {
		t.Errorf("ErrMsg should be populated")
	}
}

func TestPerformMoveSizeMismatch(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "d.bin")
	dst := filepath.Join(tmp, "dst", "d.bin")
	body := "ten bytes."
	writeFile(t, src, body)

	// Force the copy path with ModeCopy. Pretend the file is bigger so the
	// post-copy size check fails.
	plan := PlannedMove{Source: src, DestAbs: dst, Size: int64(len(body) + 100)}
	cm, err := PerformMove(plan, ModeCopy)
	if err == nil {
		t.Errorf("expected error for size mismatch")
	}
	if cm.Status != StatusFailed {
		t.Errorf("Status: got %s, want failed", cm.Status)
	}
	// Partial dest should be cleaned up so it can be retried.
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("expected partial dest removed, err = %v", err)
	}
	// Source must still exist (move was never finalized).
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source should still exist, err = %v", err)
	}
}

func TestExecuteMixedConflicts(t *testing.T) {
	tmp := t.TempDir()
	srcA := filepath.Join(tmp, "src", "a.bin")
	dstA := filepath.Join(tmp, "dst", "a.bin")
	writeFile(t, srcA, "AAA")

	srcB := filepath.Join(tmp, "src", "b.bin")
	dstB := filepath.Join(tmp, "dst", "b.bin")
	writeFile(t, srcB, "BBB")
	writeFile(t, dstB, "preexisting")

	moves := []PlannedMove{
		{Source: srcA, DestAbs: dstA, Size: 3},
		{Source: srcB, DestAbs: dstB, Size: 3, Conflict: true},
	}
	completed := Execute(context.Background(), moves, ModeMove, nil)
	if len(completed) != 2 {
		t.Fatalf("got %d results, want 2", len(completed))
	}
	if completed[0].Status != StatusMoved {
		t.Errorf("[0] status: got %s, want moved", completed[0].Status)
	}
	if completed[1].Status != StatusSkipped {
		t.Errorf("[1] status: got %s, want skipped", completed[1].Status)
	}
}

func TestExecuteCancelStopsFeedingJobs(t *testing.T) {
	tmp := t.TempDir()
	moves := make([]PlannedMove, 5)
	for i := range moves {
		src := filepath.Join(tmp, "src", string(rune('a'+i))+".bin")
		dst := filepath.Join(tmp, "dst", string(rune('a'+i))+".bin")
		writeFile(t, src, "x")
		moves[i] = PlannedMove{Source: src, DestAbs: dst, Size: 1}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the first job is dispatched

	completed := Execute(ctx, moves, ModeMove, nil)
	if len(completed) != len(moves) {
		t.Fatalf("got %d results, want %d", len(completed), len(moves))
	}
	// With ctx already cancelled, none of the feeds will succeed and every
	// entry should land as Failed/cancelled.
	for i, cm := range completed {
		if cm.Status != StatusFailed {
			t.Errorf("[%d] status: got %s, want failed (cancelled)", i, cm.Status)
		}
		if cm.ErrMsg != "cancelled" {
			t.Errorf("[%d] ErrMsg: got %q, want %q", i, cm.ErrMsg, "cancelled")
		}
	}
	// No source files removed because no work happened.
	for _, m := range moves {
		if _, err := os.Stat(m.Source); err != nil {
			t.Errorf("source %s should still exist: %v", m.Source, err)
		}
	}
}

// TestExecuteSubsetSkipsUnselectedSources mirrors what the /execute handler
// does when the user has unchecked rows: the filtered slice is what reaches
// Execute, and any source not in that slice must be untouched on disk.
func TestExecuteSubsetSkipsUnselectedSources(t *testing.T) {
	tmp := t.TempDir()
	all := make([]PlannedMove, 5)
	for i := range all {
		name := string(rune('a'+i)) + ".bin"
		src := filepath.Join(tmp, "src", name)
		dst := filepath.Join(tmp, "dst", name)
		writeFile(t, src, name)
		all[i] = PlannedMove{Source: src, DestAbs: dst, Size: int64(len(name))}
	}

	subset := []PlannedMove{all[0], all[2], all[4]}
	completed := Execute(context.Background(), subset, ModeMove, nil)
	if len(completed) != len(subset) {
		t.Fatalf("got %d results, want %d", len(completed), len(subset))
	}
	for i, cm := range completed {
		if cm.Status != StatusMoved {
			t.Errorf("[%d] status: got %s, want moved", i, cm.Status)
		}
	}
	// Even-indexed sources should be gone, odd-indexed sources untouched.
	for i, m := range all {
		_, err := os.Stat(m.Source)
		if i%2 == 0 {
			if !os.IsNotExist(err) {
				t.Errorf("[%d] selected source %s should be removed; stat err=%v", i, m.Source, err)
			}
		} else {
			if err != nil {
				t.Errorf("[%d] unselected source %s should still exist; err=%v", i, m.Source, err)
			}
			if _, derr := os.Stat(m.DestAbs); !os.IsNotExist(derr) {
				t.Errorf("[%d] unselected dest %s should not exist; stat err=%v", i, m.DestAbs, derr)
			}
		}
	}
}

// knownMtime is the source-file timestamp asserted by the mtime regression
// tests. Picked so any drift caused by the code under test resetting mtime
// to "now" produces an obvious diff.
var knownMtime = time.Date(2020, 1, 15, 12, 0, 0, 0, time.UTC)

func TestPerformMoveCrossFSPreservesMtime(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "a.bin")
	dst := filepath.Join(tmp, "dst", "a.bin")
	body := "cross-fs body"
	writeFile(t, src, body)
	if err := os.Chtimes(src, knownMtime, knownMtime); err != nil {
		t.Fatalf("seed src mtime: %v", err)
	}

	// Force the cross-FS branch even though src and dst share a tmpfs.
	prev := sameFilesystemFn
	sameFilesystemFn = func(string, string) (bool, error) { return false, nil }
	t.Cleanup(func() { sameFilesystemFn = prev })

	plan := PlannedMove{Source: src, DestAbs: dst, Size: int64(len(body))}
	cm, err := PerformMove(plan, ModeMove)
	if err != nil {
		t.Fatalf("PerformMove: %v", err)
	}
	if cm.Status != StatusMoved {
		t.Fatalf("Status: got %s, want moved", cm.Status)
	}
	if cm.ErrMsg != "" {
		t.Errorf("unexpected ErrMsg: %q", cm.ErrMsg)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if drift := info.ModTime().Sub(knownMtime); drift < -time.Second || drift > time.Second {
		t.Errorf("dest mtime: got %s, want %s (drift %s)", info.ModTime(), knownMtime, drift)
	}
}

func TestPerformMoveSameFSRenamePreservesMtime(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "a.bin")
	dst := filepath.Join(tmp, "dst", "a.bin")
	body := "same-fs body"
	writeFile(t, src, body)
	if err := os.Chtimes(src, knownMtime, knownMtime); err != nil {
		t.Fatalf("seed src mtime: %v", err)
	}

	plan := PlannedMove{Source: src, DestAbs: dst, Size: int64(len(body))}
	cm, err := PerformMove(plan, ModeMove)
	if err != nil {
		t.Fatalf("PerformMove: %v", err)
	}
	if cm.Status != StatusMoved {
		t.Fatalf("Status: got %s, want moved", cm.Status)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if drift := info.ModTime().Sub(knownMtime); drift < -time.Second || drift > time.Second {
		t.Errorf("dest mtime: got %s, want %s (drift %s)", info.ModTime(), knownMtime, drift)
	}
}

// TestDestRelPresets walks each preset offered in the Settings UI and
// confirms time.Format-driven path construction renders the expected
// directory shape. Mirrors components.pathTemplatePresets so a drift
// between the UI list and the mover's renderer surfaces here.
func TestDestRelPresets(t *testing.T) {
	d := time.Date(2024, 7, 4, 9, 30, 0, 0, time.UTC)
	src := "/src/IMG_0001.JPG"
	cases := []struct {
		name     string
		template string
		wantDir  string
	}{
		{"Year/YYYYMMDD", "2006/20060102", filepath.Join("2024", "20240704")},
		{"Year/Month/Day", "2006/01/02", filepath.Join("2024", "07", "04")},
		{"Year-Month/Day", "2006-01/02", filepath.Join("2024-07", "04")},
		{"Year/YYYYMMDD-HHMM", "2006/20060102-1504", filepath.Join("2024", "20240704-0930")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := destRel(d, src, tc.template)
			want := filepath.Join(tc.wantDir, "IMG_0001.JPG")
			if got != want {
				t.Errorf("destRel(_, _, %q) = %q, want %q", tc.template, got, want)
			}
		})
	}
}

// TestDestRelLiteralPassthrough confirms non-token literals in the template
// (e.g., "archive/") are emitted unchanged by time.Format — the renderer
// does no extra parsing on top.
func TestDestRelLiteralPassthrough(t *testing.T) {
	d := time.Date(2024, 7, 4, 0, 0, 0, 0, time.UTC)
	got := destRel(d, "/src/IMG.JPG", "archive/2006/20060102")
	want := filepath.Join("archive", "2024", "20240704", "IMG.JPG")
	if got != want {
		t.Errorf("destRel literal passthrough: got %q, want %q", got, want)
	}
}

// softMatchFixture builds a parent dir under t.TempDir() with the named
// subdirectories, optionally writing the given filename into each of the
// "withFile" subdirectories. Returns the parent path. The leaf name is fixed
// at "20260509" across the table cases so any case-insensitive matches still
// resolve against the same logical date.
func softMatchFixture(t *testing.T, dirs []string, withFile []string, filename string) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "2026")
	for _, d := range dirs {
		full := filepath.Join(parent, d)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
	}
	for _, d := range withFile {
		full := filepath.Join(parent, d, filename)
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return parent
}

func TestResolveSoftMatch(t *testing.T) {
	const leaf = "20260509"
	const file = "IMG.JPG"

	tests := []struct {
		name         string
		dirs         []string
		withFile     []string
		wantChosen   string
		wantConflict bool
	}{
		{
			name:         "no candidates, no exact",
			dirs:         []string{"other-folder", "20251231 nye"},
			wantChosen:   "20260509",
			wantConflict: false,
		},
		{
			name:         "exact only, no file",
			dirs:         []string{"20260509"},
			wantChosen:   "20260509",
			wantConflict: false,
		},
		{
			name:         "exact only, file present",
			dirs:         []string{"20260509"},
			withFile:     []string{"20260509"},
			wantChosen:   "20260509",
			wantConflict: true,
		},
		{
			name:         "single soft only, no file",
			dirs:         []string{"20260509 test"},
			wantChosen:   "20260509 test",
			wantConflict: false,
		},
		{
			name:         "single soft only, file present",
			dirs:         []string{"20260509 test"},
			withFile:     []string{"20260509 test"},
			wantChosen:   "20260509 test",
			wantConflict: true,
		},
		{
			name:         "exact wins over single soft",
			dirs:         []string{"20260509", "20260509 wedding"},
			wantChosen:   "20260509",
			wantConflict: false,
		},
		{
			name:         "exact + soft, file in soft (cross-folder catch)",
			dirs:         []string{"20260509", "20260509 wedding"},
			withFile:     []string{"20260509 wedding"},
			wantChosen:   "20260509",
			wantConflict: true,
		},
		{
			name:         "two soft candidates, ambiguous fall back to exact-named",
			dirs:         []string{"20260509 reception", "20260509 wedding"},
			wantChosen:   "20260509",
			wantConflict: false,
		},
		{
			name:         "two soft candidates, file in one of them",
			dirs:         []string{"20260509 reception", "20260509 wedding"},
			withFile:     []string{"20260509 reception"},
			wantChosen:   "20260509",
			wantConflict: true,
		},
		{
			name:         "anchor rejection: 202605091 is next-day prefix",
			dirs:         []string{"202605091"},
			wantChosen:   "20260509",
			wantConflict: false,
		},
		{
			name:         "case-insensitive soft preserves on-disk casing",
			dirs:         []string{"20260509 WEDDING"},
			wantChosen:   "20260509 WEDDING",
			wantConflict: false,
		},
		{
			name:         "non-matching siblings are ignored",
			dirs:         []string{"random", "20251231 nye", "2025"},
			wantChosen:   "20260509",
			wantConflict: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parent := softMatchFixture(t, tc.dirs, tc.withFile, file)
			chosen, conflict, err := resolveSoftMatch(parent, leaf, file)
			if err != nil {
				t.Fatalf("resolveSoftMatch err: %v", err)
			}
			if chosen != tc.wantChosen {
				t.Errorf("chosen: got %q, want %q", chosen, tc.wantChosen)
			}
			if conflict != tc.wantConflict {
				t.Errorf("conflict: got %v, want %v", conflict, tc.wantConflict)
			}
		})
	}
}

func TestResolveSoftMatchParentMissing(t *testing.T) {
	// ENOENT on parent must not be an error; caller falls back to exact-named.
	parent := filepath.Join(t.TempDir(), "does-not-exist", "2026")
	chosen, conflict, err := resolveSoftMatch(parent, "20260509", "IMG.JPG")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if chosen != "20260509" {
		t.Errorf("chosen: got %q, want %q", chosen, "20260509")
	}
	if conflict {
		t.Errorf("conflict should be false on missing parent")
	}
}

// TestResolveSoftMatchNonDateLeaf guards the digitPrefix guard. A literal,
// non-date leaf has no digit prefix, so the helper short-circuits to
// exact-only conflict-check semantics.
func TestResolveSoftMatchNonDateLeaf(t *testing.T) {
	parent := softMatchFixture(t, []string{"literal", "literal extra"}, []string{"literal"}, "IMG.JPG")
	chosen, conflict, err := resolveSoftMatch(parent, "literal", "IMG.JPG")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if chosen != "literal" {
		t.Errorf("chosen: got %q, want %q", chosen, "literal")
	}
	if !conflict {
		t.Errorf("conflict should be true (file present in literal/)")
	}
}

func TestExecuteReturnsResultsInInputOrder(t *testing.T) {
	tmp := t.TempDir()
	moves := make([]PlannedMove, 8)
	for i := range moves {
		name := string(rune('a' + i))
		src := filepath.Join(tmp, "src", name+".bin")
		dst := filepath.Join(tmp, "dst", name+".bin")
		writeFile(t, src, name)
		moves[i] = PlannedMove{Source: src, DestAbs: dst, Size: 1}
	}
	completed := Execute(context.Background(), moves, ModeCopy, nil)
	if len(completed) != len(moves) {
		t.Fatalf("got %d results, want %d", len(completed), len(moves))
	}
	for i := range completed {
		if completed[i].Source != moves[i].Source {
			t.Errorf("[%d] order broken: got %s, want %s", i, completed[i].Source, moves[i].Source)
		}
	}
}
