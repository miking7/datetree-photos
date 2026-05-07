package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestScanSoftMatchHybrid drives the scan loop end-to-end against a fixture
// tree that mirrors the user's reported failure: the exact-named "20260409"
// folder is absent under <dst>/2026/, but a descriptive "20260409 test"
// sibling exists. The hybrid policy must (a) route both source files into
// "20260409 test" so the preview reflects the soft-matched destination, and
// (b) flag the row whose filename already exists in "20260409 test" as a
// Conflict.
func TestScanSoftMatchHybrid(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	descriptive := filepath.Join(dst, "2026", "20260409 test")
	if err := os.MkdirAll(descriptive, 0o755); err != nil {
		t.Fatalf("mkdir descriptive: %v", err)
	}

	// Two source files with mtime → 2026-04-09 so destRel renders 20260409.
	// Use .bmp so classify() routes to the mtime-only tier — no metadata
	// libraries involved, the test stays hermetic.
	captureDate := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	srcA := filepath.Join(src, "IMG_8005.bmp")
	srcB := filepath.Join(src, "IMG_8024.bmp")
	for _, p := range []string{srcA, srcB} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir src: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		if err := os.Chtimes(p, captureDate, captureDate); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}

	// Pre-seed a duplicate of IMG_8005.bmp in the descriptive folder so that
	// the cross-candidate conflict check has something to find.
	if err := os.WriteFile(filepath.Join(descriptive, "IMG_8005.bmp"), []byte("y"), 0o644); err != nil {
		t.Fatalf("seed conflict: %v", err)
	}

	moves, err := Scan(context.Background(), src, dst, DefaultPathTemplate, true, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(moves) != 2 {
		t.Fatalf("got %d moves, want 2: %+v", len(moves), moves)
	}

	wantLeaf := filepath.Join(dst, "2026", "20260409 test")
	wantA := filepath.Join(wantLeaf, "IMG_8005.bmp")
	wantB := filepath.Join(wantLeaf, "IMG_8024.bmp")

	// Moves are sorted by source path; IMG_8005 < IMG_8024 so [0]=A, [1]=B.
	if moves[0].DestAbs != wantA {
		t.Errorf("[0] DestAbs: got %q, want %q", moves[0].DestAbs, wantA)
	}
	if !moves[0].Conflict {
		t.Errorf("[0] Conflict: got false, want true (file already in soft-matched folder)")
	}
	if moves[1].DestAbs != wantB {
		t.Errorf("[1] DestAbs: got %q, want %q", moves[1].DestAbs, wantB)
	}
	if moves[1].Conflict {
		t.Errorf("[1] Conflict: got true, want false")
	}

	// DestRel must be relative and reflect the soft-matched leaf, otherwise
	// the preview will show the wrong path even though DestAbs is right.
	wantRelA := filepath.Join("2026", "20260409 test", "IMG_8005.bmp")
	if moves[0].DestRel != wantRelA {
		t.Errorf("[0] DestRel: got %q, want %q", moves[0].DestRel, wantRelA)
	}
}

// TestScanSoftMatchOff is the regression guard for "toggle off changes nothing":
// even when a candidate folder exists, the exact-named path must be returned
// and the conflict check must run against that exact path only.
func TestScanSoftMatchOff(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(filepath.Join(dst, "2026", "20260409 test"), 0o755); err != nil {
		t.Fatalf("mkdir descriptive: %v", err)
	}
	// Pre-seed file inside the descriptive folder. With softMatch=false this
	// must NOT be flagged as conflict, since the exact-named path is what's
	// checked.
	if err := os.WriteFile(filepath.Join(dst, "2026", "20260409 test", "IMG.bmp"), []byte("y"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	captureDate := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	srcFile := filepath.Join(src, "IMG.bmp")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.Chtimes(srcFile, captureDate, captureDate); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	moves, err := Scan(context.Background(), src, dst, DefaultPathTemplate, false, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(moves) != 1 {
		t.Fatalf("got %d moves, want 1", len(moves))
	}
	wantDest := filepath.Join(dst, "2026", "20260409", "IMG.bmp")
	if moves[0].DestAbs != wantDest {
		t.Errorf("DestAbs: got %q, want %q", moves[0].DestAbs, wantDest)
	}
	if moves[0].Conflict {
		t.Errorf("Conflict: got true, want false (toggle-off must not see across to soft-match folder)")
	}
}
