package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const runsDirName = "runs"

// ManifestColumns is the exact column order written to every run CSV. v2's
// dedup index will read this back, so any change is a breaking format change.
var ManifestColumns = []string{
	"source",
	"destination",
	"capture_date",
	"date_source",
	"status",
	"sha256",
	"bytes",
	"duration_ms",
	"error",
}

// runsDir mirrors ConfigPath's env-override pattern so tests can redirect both
// state.json and runs/ via a single DATETREE_STATE_DIR setting. Production
// fallback is os.UserConfigDir/datetree/runs (colocated with state.json).
func runsDir() string {
	if dir := os.Getenv(stateDirEnv); dir != "" {
		return filepath.Join(dir, runsDirName)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return runsDirName
	}
	return filepath.Join(cfg, "datetree", runsDirName)
}

// WriteManifest persists a per-run CSV and returns the absolute path written.
// Failures only on the file write itself; per-row formatting always succeeds.
func WriteManifest(runStart time.Time, completed []CompletedMove) (string, error) {
	dir := runsDir()
	if err := os.MkdirAll(dir, defaultDperm); err != nil {
		return "", err
	}
	name := runStart.UTC().Format(time.RFC3339) + ".csv"
	path := filepath.Join(dir, name)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, defaultPerm)
	if err != nil {
		return "", err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(ManifestColumns); err != nil {
		return "", err
	}
	for _, cm := range completed {
		if err := w.Write(manifestRow(cm)); err != nil {
			return "", err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

func manifestRow(cm CompletedMove) []string {
	captureDate := ""
	if !cm.CaptureDate.IsZero() {
		captureDate = cm.CaptureDate.Format(time.RFC3339)
	}
	dest := cm.DestAbs
	if cm.Status == StatusFailed && cm.SHA256 == "" {
		// On failure the file may have never landed on disk. Empty the dest
		// column so consumers don't think a partial path was created.
		// Exception: a failure that happened *after* a successful copy (e.g.
		// source-remove failed) still has SHA256 populated, so keep dest.
		if cm.ErrMsg != "" && !verifySize(cm.DestAbs, cm.Size) {
			dest = ""
		}
	}
	return []string{
		cm.Source,
		dest,
		captureDate,
		cm.DateSource.String(),
		cm.Status.String(),
		cm.SHA256,
		strconv.FormatInt(cm.Size, 10),
		strconv.FormatInt(cm.Duration.Milliseconds(), 10),
		cm.ErrMsg,
	}
}
