package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type FileKind int

const (
	KindOther FileKind = iota
	KindPhoto
	KindVideo
)

func (k FileKind) String() string {
	switch k {
	case KindPhoto:
		return "photo"
	case KindVideo:
		return "video"
	default:
		return "other"
	}
}

type DateSource int

const (
	DateSourceMtime DateSource = iota
	DateSourceExifOriginal
	DateSourceExifDateTime
	DateSourceQuickTime
	DateSourceXMP
	DateSourcePNGCreationText
	DateSourcePNGTime
)

func (d DateSource) String() string {
	switch d {
	case DateSourceExifOriginal:
		return "exif_original"
	case DateSourceExifDateTime:
		return "exif_datetime"
	case DateSourceQuickTime:
		return "quicktime"
	case DateSourceXMP:
		return "xmp_create"
	case DateSourcePNGCreationText:
		return "png_creation_text"
	case DateSourcePNGTime:
		return "png_time"
	default:
		return "mtime"
	}
}

type PlannedMove struct {
	Source      string
	Size        int64
	Kind        FileKind
	CaptureDate time.Time
	DateSource  DateSource
	DestRel     string
	DestAbs     string
	Conflict    bool
	Skipped     bool
	Error       string
}

// Whitelist by extension. Anything not in these maps is dropped during walk.
// photoExts and videoExts are read with metadata libraries; mtimeOnlyExts skip
// straight to mtime because no pure-Go reader exists for them.
var (
	photoExts = map[string]struct{}{
		".jpg": {}, ".jpeg": {}, ".heic": {}, ".heif": {},
		".cr2": {}, ".cr3": {}, ".nef": {}, ".arw": {}, ".dng": {},
		".png": {}, ".webp": {}, ".avif": {}, ".pef": {},
	}
	videoExts = map[string]struct{}{
		".mp4": {}, ".mov": {}, ".m4v": {},
		// DJI/GoPro proxy clips are MP4-internally; let go-mp4 try first.
		".lrf": {}, ".lrv": {},
	}
	mtimeOnlyExts = map[string]struct{}{
		".mts": {}, ".m2ts": {},
		".bmp": {}, ".gif": {}, ".jxl": {},
		".orf": {}, ".rw2": {}, ".raf": {}, ".srw": {},
		".x3f": {}, ".3fr": {}, ".fff": {}, ".rwl": {},
		".avi": {}, ".mkv": {}, ".3gp": {}, ".3g2": {},
		".webm": {}, ".wmv": {}, ".flv": {},
		".360": {},
	}
)

func classify(path string) (FileKind, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := photoExts[ext]; ok {
		return KindPhoto, true
	}
	if _, ok := videoExts[ext]; ok {
		return KindVideo, true
	}
	if _, ok := mtimeOnlyExts[ext]; ok {
		return KindOther, true
	}
	return KindOther, false
}

// Scan walks sourceRoot in two passes: first counting matching files (so the
// UI can show a determinate progress bar), then processing them through the
// worker pool. template is the Go time-format layout for the destination
// directory; the caller must have already passed it through
// validatePathTemplate. softMatch enables the hybrid soft-match policy
// described in spec §7: cross-candidate conflict detection plus auto-routing
// into a single descriptive sibling folder when there's exactly one
// candidate. A nil reporter is allowed for tests / callers that don't care.
func Scan(ctx context.Context, sourceRoot, destRoot, template string, softMatch bool, reporter *Reporter) ([]PlannedMove, error) {
	if reporter != nil {
		reporter.Publish(Progress{Processed: 0, Total: -1})
	}

	total, err := countFiles(ctx, sourceRoot)
	if err != nil {
		return nil, fmt.Errorf("count %s: %w", sourceRoot, err)
	}
	if reporter != nil {
		reporter.Publish(Progress{Processed: 0, Total: total})
	}

	paths := make(chan string)
	results := make(chan PlannedMove)

	var processed atomic.Int64
	workers := runtime.NumCPU()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range paths {
				if ctx.Err() != nil {
					return
				}
				m := planMove(p, destRoot)
				results <- m
				if reporter != nil {
					n := int(processed.Add(1))
					reporter.Publish(Progress{
						Processed: n,
						Total:     total,
						Current:   filepath.Base(p),
					})
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	walkErrCh := make(chan error, 1)
	go func() {
		defer close(paths)
		err := filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				return nil
			}
			if _, ok := classify(path); !ok {
				return nil
			}
			select {
			case paths <- path:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		walkErrCh <- err
	}()

	moves := make([]PlannedMove, 0, total)
	for m := range results {
		moves = append(moves, m)
	}

	if err := <-walkErrCh; err != nil {
		return moves, fmt.Errorf("walk %s: %w", sourceRoot, err)
	}
	if err := ctx.Err(); err != nil {
		return moves, err
	}

	sort.Slice(moves, func(i, j int) bool { return moves[i].Source < moves[j].Source })
	for i := range moves {
		rel := destRel(moves[i].CaptureDate, moves[i].Source, template)
		abs := filepath.Join(destRoot, rel)
		if softMatch {
			leafDir := filepath.Dir(abs)
			parentDir := filepath.Dir(leafDir)
			leafName := filepath.Base(leafDir)
			filename := filepath.Base(abs)
			chosen, conflict, _ := resolveSoftMatch(parentDir, leafName, filename)
			if chosen != leafName {
				abs = filepath.Join(parentDir, chosen, filename)
				if r, rerr := filepath.Rel(destRoot, abs); rerr == nil {
					rel = r
				}
			}
			moves[i].Conflict = conflict
		} else if _, err := os.Stat(abs); err == nil {
			moves[i].Conflict = true
		}
		moves[i].DestRel = rel
		moves[i].DestAbs = abs
	}

	return moves, nil
}

func countFiles(ctx context.Context, root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		if _, ok := classify(path); ok {
			count++
		}
		return nil
	})
	return count, err
}

func planMove(path, destRoot string) PlannedMove {
	kind, _ := classify(path)
	pm := PlannedMove{Source: path, Kind: kind}

	if info, err := os.Stat(path); err == nil {
		pm.Size = info.Size()
	}

	t, src, err := extractMetadata(path, kind)
	pm.CaptureDate = t
	pm.DateSource = src
	if err != nil {
		pm.Error = err.Error()
	}

	// Dest paths get filled in after sort so output is deterministic.
	_ = destRoot
	return pm
}

// destRel renders the relative destination path. template is a Go time-format
// string (e.g. "2006/20060102") that the caller has already validated.
func destRel(capture time.Time, source, template string) string {
	return filepath.Join(capture.Format(template), filepath.Base(source))
}
