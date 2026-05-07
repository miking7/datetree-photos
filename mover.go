package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type Mode int

const (
	ModeMove Mode = iota
	ModeCopy
)

func ParseMode(s string) Mode {
	if s == "copy" {
		return ModeCopy
	}
	return ModeMove
}

type MoveStatus int

const (
	StatusFailed MoveStatus = iota
	StatusMoved
	StatusCopied
	StatusSkipped
)

func (s MoveStatus) String() string {
	switch s {
	case StatusMoved:
		return "moved"
	case StatusCopied:
		return "copied"
	case StatusSkipped:
		return "skipped"
	default:
		return "failed"
	}
}

type CompletedMove struct {
	PlannedMove
	Status   MoveStatus
	SHA256   string
	Duration time.Duration
	ErrMsg   string
}

const copyBufSize = 1 << 20 // 1 MiB

// Indirected so tests can force the cross-FS copy path on a single tmpfs.
var sameFilesystemFn = sameFilesystem

// resolveSoftMatch implements the hybrid soft-match policy used at scan time.
// It looks at parent for the rendered leafName plus any date-prefixed sibling
// directories, and returns:
//
//   - chosen: the directory under parent that the move targets. The exact-named
//     on-disk folder wins if it exists; otherwise a single soft-match candidate
//     is used; otherwise the rendered leafName itself (caller will mkdir it).
//   - conflict: true when filename already exists in the exact-named folder OR
//     in ANY soft-match candidate, even one routing did not pick. This catches
//     duplicates that have already been imported into a descriptive folder.
//
// A non-existent parent (fresh tree) returns chosen=leafName, conflict=false.
// "Soft-match candidates" are sibling directories whose name begins with the
// digit prefix of leafName and whose next character is non-digit, so
// "20260509" matches "20260509 wedding"/"20260509-trip" but NOT "202605091"
// (which is a different day's prefix).
func resolveSoftMatch(parent, leafName, filename string) (chosen string, conflict bool, err error) {
	chosen = leafName
	prefix := digitPrefix(leafName)
	if prefix == "" {
		// Defensive: a non-date leaf has no digit prefix to match on, so fall
		// back to exact-only behaviour.
		if _, statErr := os.Stat(filepath.Join(parent, leafName, filename)); statErr == nil {
			return leafName, true, nil
		}
		return leafName, false, nil
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return leafName, false, nil
		}
		return leafName, false, err
	}

	var exactName string
	var softMatches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < len(prefix) {
			continue
		}
		if name[:len(prefix)] != prefix {
			continue
		}
		if name == leafName {
			exactName = name
			continue
		}
		// Anchor: char following the digit prefix must be non-digit, or this
		// is a longer pure-digit prefix (= different date) and not a match.
		if len(name) > len(prefix) {
			next := name[len(prefix)]
			if next >= '0' && next <= '9' {
				continue
			}
		}
		softMatches = append(softMatches, name)
	}

	switch {
	case exactName != "":
		chosen = exactName
	case len(softMatches) == 1:
		chosen = softMatches[0]
	}

	if exactName != "" {
		if _, statErr := os.Stat(filepath.Join(parent, exactName, filename)); statErr == nil {
			conflict = true
		}
	}
	if !conflict {
		for _, s := range softMatches {
			if _, statErr := os.Stat(filepath.Join(parent, s, filename)); statErr == nil {
				conflict = true
				break
			}
		}
	}
	return chosen, conflict, nil
}

// digitPrefix returns the longest run of ASCII digits at the start of s.
// Empty string when the leaf isn't date-prefixed (e.g. a literal folder name),
// signalling that soft-match should be skipped.
func digitPrefix(s string) string {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return s[:n]
}

// PerformMove handles one file per spec §7. The returned error is non-nil for
// failures that the caller should log but not abort the batch on; the same
// information is also reflected in the returned CompletedMove (Status=Failed,
// ErrMsg populated) so callers can simply record and continue. The destination
// path was already finalised at scan time (including any soft-match
// resolution), so this function operates on whatever DestAbs the plan carries.
func PerformMove(p PlannedMove, mode Mode) (CompletedMove, error) {
	start := time.Now()
	out := CompletedMove{PlannedMove: p}

	if p.Conflict {
		out.Status = StatusSkipped
		out.Duration = time.Since(start)
		return out, nil
	}

	destDir := filepath.Dir(p.DestAbs)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		out.Status = StatusFailed
		out.ErrMsg = fmt.Sprintf("mkdir %s: %v", destDir, err)
		out.Duration = time.Since(start)
		return out, fmt.Errorf("perform move %s: mkdir %s: %w", p.Source, destDir, err)
	}

	if mode == ModeMove {
		// sameFilesystem needs the dir, since the dest file does not yet exist.
		sameFS, err := sameFilesystemFn(p.Source, destDir)
		if err == nil && sameFS {
			if err := os.Rename(p.Source, p.DestAbs); err != nil {
				out.Status = StatusFailed
				out.ErrMsg = fmt.Sprintf("rename %s -> %s: %v", p.Source, p.DestAbs, err)
				out.Duration = time.Since(start)
				return out, fmt.Errorf("perform move %s: rename: %w", p.Source, err)
			}
			// Post-rename hash: the file is at rest on the destination, so we
			// can read it once for the v2-dedup sha256 column.
			sha, herr := hashFile(p.DestAbs)
			out.Status = StatusMoved
			out.SHA256 = sha
			if herr != nil {
				out.ErrMsg = fmt.Sprintf("hash dest %s: %v", p.DestAbs, herr)
			}
			out.Duration = time.Since(start)
			return out, nil
		}
		// Cross-filesystem move: fall through to the copy+verify+delete path.
	}

	// Capture before copy so the source mtime survives a Move-mode source delete.
	srcInfo, statErr := os.Stat(p.Source)
	if statErr != nil {
		out.Status = StatusFailed
		out.ErrMsg = fmt.Sprintf("stat %s: %v", p.Source, statErr)
		out.Duration = time.Since(start)
		return out, fmt.Errorf("perform move %s: stat: %w", p.Source, statErr)
	}

	sha, n, err := copyAndHash(p.Source, p.DestAbs)
	if err != nil {
		_ = os.Remove(p.DestAbs)
		out.Status = StatusFailed
		out.ErrMsg = fmt.Sprintf("copy %s -> %s: %v", p.Source, p.DestAbs, err)
		out.Duration = time.Since(start)
		return out, fmt.Errorf("perform move %s: copy: %w", p.Source, err)
	}
	if n != p.Size || !verifySize(p.DestAbs, p.Size) {
		_ = os.Remove(p.DestAbs)
		out.Status = StatusFailed
		out.ErrMsg = fmt.Sprintf("size mismatch %s: copied %d, expected %d", p.DestAbs, n, p.Size)
		out.Duration = time.Since(start)
		return out, fmt.Errorf("perform move %s: size mismatch", p.Source)
	}

	if mode == ModeMove {
		if err := os.Remove(p.Source); err != nil {
			out.Status = StatusFailed
			out.ErrMsg = fmt.Sprintf("remove source %s: %v", p.Source, err)
			out.SHA256 = sha
			out.Duration = time.Since(start)
			return out, fmt.Errorf("perform move %s: remove source: %w", p.Source, err)
		}
		out.Status = StatusMoved
	} else {
		out.Status = StatusCopied
	}
	// Preserve source mtime as a secondary capture-date record (spec §6).
	// atime mirrors mtime: cross-platform atime semantics are unreliable, and
	// this matches what most archive/backup tools do. A Chtimes failure is
	// non-fatal — the copy itself succeeded.
	mtime := srcInfo.ModTime()
	if err := os.Chtimes(p.DestAbs, mtime, mtime); err != nil {
		out.ErrMsg = fmt.Sprintf("chtimes %s: %v", p.DestAbs, err)
	}
	out.SHA256 = sha
	out.Duration = time.Since(start)
	return out, nil
}

// copyAndHash streams source into dest and a sha256 simultaneously via
// io.MultiWriter, so the data is read once. fsyncs the dest before close to
// force the bytes to disk before any subsequent source delete.
func copyAndHash(src, dst string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}

	h := sha256.New()
	buf := make([]byte, copyBufSize)
	n, copyErr := io.CopyBuffer(io.MultiWriter(out, h), in, buf)
	if copyErr != nil {
		out.Close()
		return "", n, copyErr
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return "", n, err
	}
	if err := out.Close(); err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifySize(path string, want int64) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() == want
}

// Execute drives PerformMove across moves with a NumCPU worker pool. Returns
// the slice in input order. ctx cancellation stops feeding new jobs; in-flight
// items run to completion (a single file is small enough that the cleanest
// "abort" is to let it finish atomically). Items that never started are
// recorded as Failed with ErrMsg "cancelled".
func Execute(ctx context.Context, moves []PlannedMove, mode Mode, reporter *Reporter) []CompletedMove {
	completed := make([]CompletedMove, len(moves))
	if reporter != nil {
		reporter.Publish(Progress{Processed: 0, Total: len(moves)})
	}
	if len(moves) == 0 {
		return completed
	}

	type job struct {
		index int
		plan  PlannedMove
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var processed atomic.Int64

	workers := runtime.NumCPU()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				cm, _ := PerformMove(j.plan, mode)
				completed[j.index] = cm
				n := int(processed.Add(1))
				if reporter != nil {
					reporter.Publish(Progress{
						Processed: n,
						Total:     len(moves),
						Current:   filepath.Base(j.plan.Source),
					})
				}
			}
		}()
	}

	cancelledFrom := len(moves)
feed:
	for i, p := range moves {
		select {
		case <-ctx.Done():
			cancelledFrom = i
			break feed
		case jobs <- job{index: i, plan: p}:
		}
	}
	close(jobs)
	wg.Wait()

	for k := cancelledFrom; k < len(moves); k++ {
		completed[k] = CompletedMove{
			PlannedMove: moves[k],
			Status:      StatusFailed,
			ErrMsg:      "cancelled",
		}
		if reporter != nil {
			reporter.Publish(Progress{
				Processed: int(processed.Add(1)),
				Total:     len(moves),
				Current:   filepath.Base(moves[k].Source),
			})
		}
	}

	return completed
}
