package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/selfupdate"
	"github.com/miking7/datetree-photos/components"
)

const (
	releasesAPIURL    = "https://api.github.com/repos/miking7/datetree-photos/releases/latest"
	releaseAssetsBase = "https://github.com/miking7/datetree-photos/releases/download"
	devVersion        = "dev"
	httpTimeout       = 10 * time.Second
	// Read-side cap for the binary archive download. Build asset is ~12 MB; a
	// 100 MB ceiling gives generous headroom while keeping a runaway response
	// from filling RAM.
	maxArchiveBytes = 100 * 1024 * 1024
)

// Updater holds in-memory update-check state. The fields are read by handlers
// rendering the Settings page and the launch-time goroutine.
type Updater struct {
	mu          sync.Mutex
	latestTag   string
	lastChecked time.Time
	checking    bool
	httpClient  *http.Client
}

var updater = &Updater{httpClient: &http.Client{Timeout: httpTimeout}}

func (u *Updater) Snapshot() (latestTag string, lastChecked time.Time, checking bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.latestTag, u.lastChecked, u.checking
}

func (u *Updater) setChecking(v bool) {
	u.mu.Lock()
	u.checking = v
	u.mu.Unlock()
}

func (u *Updater) recordResult(latest string) {
	u.mu.Lock()
	if latest != "" {
		u.latestTag = latest
	}
	u.lastChecked = time.Now()
	u.checking = false
	u.mu.Unlock()
}

// CheckLatest queries the GitHub Releases API for the latest tag and reports
// whether it is newer than currentVersion. A "dev" build is treated as not
// newer than anything: we never want a banner on a local dev rebuild.
func (u *Updater) CheckLatest(ctx context.Context, currentVersion string) (latest string, isNewer bool, err error) {
	if currentVersion == devVersion {
		return "", false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPIURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("github releases API: %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false, err
	}
	if body.TagName == "" {
		return "", false, errors.New("github releases API returned empty tag_name")
	}
	return body.TagName, compareSemver(body.TagName, currentVersion) > 0, nil
}

// RunCheckIfEnabled is the launch-time goroutine entry. Bails out cleanly when
// auto-checks are disabled, when the build is dev, or when the request fails;
// any successful result populates the in-memory cache and recomputes the
// cross-page banner.
func (u *Updater) RunCheckIfEnabled(ctx context.Context, cfg Config) {
	if !cfg.UpdateChecksEnabled {
		return
	}
	if version == devVersion {
		return
	}
	u.setChecking(true)
	latest, _, err := u.CheckLatest(ctx, version)
	if err != nil {
		// Best-effort: a failed launch check should never noisily block the UI.
		// The user can still trigger a manual check from Settings.
		u.setChecking(false)
		return
	}
	u.recordResult(latest)
	recomputeBanner(cfg)
}

// recomputeBanner derives the visible banner tag from the current updater
// snapshot and the supplied config, then publishes it through components.
// Called after launch-time check, manual recheck, and dismiss.
func recomputeBanner(cfg Config) {
	latest, _, _ := updater.Snapshot()
	if latest == "" || version == devVersion {
		components.SetBanner("")
		return
	}
	if compareSemver(latest, version) <= 0 {
		components.SetBanner("")
		return
	}
	if latest == cfg.DismissedUpdateVersion {
		components.SetBanner("")
		return
	}
	components.SetBanner(latest)
}

// compareSemver returns -1, 0, or 1 for a vs b. Strips a leading "v", splits on
// ".", numeric-compares each segment. Non-numeric or pre-release suffixes
// (e.g. "v0.1.0-rc1") collapse to 0 — we don't ship pre-release channels in v1
// and would rather treat such tags as unordered against vanilla semver than
// invent ordering rules. Missing segments compare as 0.
func compareSemver(a, b string) int {
	as := splitSemver(a)
	bs := splitSemver(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func splitSemver(s string) []int {
	s = strings.TrimPrefix(s, "v")
	// Cut at the first non-numeric/non-dot rune so "1.2.3-rc1" splits as 1.2.3.
	for i, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			s = s[:i]
			break
		}
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// archiveName mirrors the .goreleaser.yaml name_template. Kept as a function
// so update_test.go can pin it independently of runtime.GOOS / runtime.GOARCH.
func archiveName(goos, goarch string) string {
	return fmt.Sprintf("datetree_%s_%s.tar.gz", goos, goarch)
}

// ApplyUpdate downloads the OS+arch archive for targetTag, verifies its SHA256
// against the release's checksums.txt, untars the binary in-memory, and hands
// the bytes to selfupdate.Apply. On any failure, calls selfupdate.RollbackError
// to restore the previous binary if Apply got far enough to displace it.
//
// Progress is reported through reporter as four phases via Progress.Current:
// "downloading", "verifying", "extracting", "applying". During download,
// Processed/Total carry byte counts so the SSE consumer can render a bar.
func ApplyUpdate(ctx context.Context, targetTag string, reporter *Reporter) error {
	asset := archiveName(runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("%s/%s", releaseAssetsBase, targetTag)

	reporter.Publish(Progress{Current: "downloading", Total: -1})
	archive, err := downloadWithProgress(ctx, fmt.Sprintf("%s/%s", base, asset), reporter)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	reporter.Publish(Progress{Current: "verifying"})
	checksums, err := downloadBytes(ctx, fmt.Sprintf("%s/%s", base, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	wantHex, err := lookupChecksum(checksums, asset)
	if err != nil {
		return err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != wantHex {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}

	reporter.Publish(Progress{Current: "extracting"})
	binary, err := extractBinary(archive, "datetree")
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	reporter.Publish(Progress{Current: "applying"})
	applyErr := selfupdate.Apply(bytes.NewReader(binary), selfupdate.Options{})
	if applyErr != nil {
		// RollbackError returns nil if the previous binary was successfully
		// restored (or never displaced). Surface the rollback failure if any.
		if rerr := selfupdate.RollbackError(applyErr); rerr != nil {
			return fmt.Errorf("apply failed (rollback also failed: %v): %w", rerr, applyErr)
		}
		return fmt.Errorf("apply: %w", applyErr)
	}
	return nil
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := updater.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes))
}

// downloadWithProgress streams the archive into a buffer and reports byte
// counts so the SSE consumer can render a progress bar.
func downloadWithProgress(ctx context.Context, url string, reporter *Reporter) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := updater.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	total := int(resp.ContentLength)
	buf := bytes.Buffer{}
	chunk := make([]byte, 32*1024)
	read := 0
	for {
		n, rerr := resp.Body.Read(chunk)
		if n > 0 {
			read += n
			if read > maxArchiveBytes {
				return nil, fmt.Errorf("archive exceeds %d byte cap", maxArchiveBytes)
			}
			buf.Write(chunk[:n])
			reporter.Publish(Progress{Current: "downloading", Processed: read, Total: total})
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}
	return buf.Bytes(), nil
}

// lookupChecksum parses a goreleaser-style checksums.txt where each line is
// "<sha256-hex>  <filename>". Returns the hex digest matching asset.
func lookupChecksum(checksums []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s", asset)
}

// extractBinary scans a gzipped tarball for the entry with the given basename
// and returns its bytes. The goreleaser archive layout puts the binary at the
// archive root, so a basename match is sufficient and avoids brittle path
// assumptions.
func extractBinary(archive []byte, basename string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Match either a top-level basename or a nested path that ends in /<basename>.
		// Defensive against future goreleaser config tweaks.
		if hdr.Name == basename || strings.HasSuffix(hdr.Name, "/"+basename) {
			return io.ReadAll(io.LimitReader(tr, maxArchiveBytes))
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", basename)
}
