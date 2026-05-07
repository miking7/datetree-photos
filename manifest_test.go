package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWriteManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(stateDirEnv, dir)

	runStart := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)
	captureA := time.Date(2024, 3, 14, 10, 23, 1, 0, time.UTC)
	completed := []CompletedMove{
		{
			PlannedMove: PlannedMove{
				Source:      "/src/IMG_1234.CR3",
				DestAbs:     "/dst/2024/20240314/IMG_1234.CR3",
				Size:        28415126,
				CaptureDate: captureA,
				DateSource:  DateSourceExifOriginal,
			},
			Status:   StatusMoved,
			SHA256:   "deadbeef",
			Duration: 142 * time.Millisecond,
		},
		{
			PlannedMove: PlannedMove{
				Source:      "/src/clash.jpg",
				DestAbs:     "/dst/2024/20240314/clash.jpg",
				Size:        4096,
				CaptureDate: captureA,
				DateSource:  DateSourceExifOriginal,
				Conflict:    true,
			},
			Status:   StatusSkipped,
			Duration: 1 * time.Millisecond,
		},
		{
			PlannedMove: PlannedMove{
				Source:     "/src/broken.jpg",
				DestAbs:    "/dst/2024/20240101/broken.jpg",
				Size:       0,
				DateSource: DateSourceMtime,
			},
			Status:   StatusFailed,
			Duration: 5 * time.Millisecond,
			ErrMsg:   "copy: short read",
		},
	}

	path, err := WriteManifest(runStart, completed)
	if err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("manifest path %q not under %q", path, dir)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("csv read: %v", err)
	}
	if len(rows) != 1+len(completed) {
		t.Fatalf("rows: got %d, want %d", len(rows), 1+len(completed))
	}
	if !reflect.DeepEqual(rows[0], ManifestColumns) {
		t.Errorf("header: got %v, want %v", rows[0], ManifestColumns)
	}
	// Row 1: moved
	if got, want := rows[1][0], "/src/IMG_1234.CR3"; got != want {
		t.Errorf("row1 source: %q != %q", got, want)
	}
	if got, want := rows[1][2], "2024-03-14T10:23:01Z"; got != want {
		t.Errorf("row1 capture_date: %q != %q", got, want)
	}
	if got, want := rows[1][4], "moved"; got != want {
		t.Errorf("row1 status: %q != %q", got, want)
	}
	if got, want := rows[1][5], "deadbeef"; got != want {
		t.Errorf("row1 sha256: %q != %q", got, want)
	}
	if got, want := rows[1][6], "28415126"; got != want {
		t.Errorf("row1 bytes: %q != %q", got, want)
	}
	if got, want := rows[1][7], "142"; got != want {
		t.Errorf("row1 duration_ms: %q != %q", got, want)
	}
	// Row 2: skipped — destination column populated even though no work
	// happened, sha empty.
	if got, want := rows[2][1], "/dst/2024/20240314/clash.jpg"; got != want {
		t.Errorf("row2 dest: %q != %q", got, want)
	}
	if got, want := rows[2][4], "skipped"; got != want {
		t.Errorf("row2 status: %q != %q", got, want)
	}
	if rows[2][5] != "" {
		t.Errorf("row2 sha256: want empty, got %q", rows[2][5])
	}
	// Row 3: failed — dest empty (file never landed), error populated.
	if rows[3][1] != "" {
		t.Errorf("row3 dest: want empty for never-landed failure, got %q", rows[3][1])
	}
	if got, want := rows[3][4], "failed"; got != want {
		t.Errorf("row3 status: %q != %q", got, want)
	}
	if got, want := rows[3][8], "copy: short read"; got != want {
		t.Errorf("row3 error: %q != %q", got, want)
	}
}

func TestWriteManifestStateDirRedirect(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(stateDirEnv, dir)
	path, err := WriteManifest(time.Now(), nil)
	if err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	wantDir := filepath.Join(dir, runsDirName)
	if filepath.Dir(path) != wantDir {
		t.Errorf("dir: got %q, want %q", filepath.Dir(path), wantDir)
	}
}

func TestWriteManifestFilenameUsesRunStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(stateDirEnv, dir)
	runStart := time.Date(2026, 5, 8, 14, 30, 15, 0, time.UTC)
	path, err := WriteManifest(runStart, nil)
	if err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	wantName := runStart.UTC().Format(time.RFC3339) + ".csv"
	if got := filepath.Base(path); got != wantName {
		t.Errorf("filename: got %q, want %q", got, wantName)
	}
}
