package main

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setLastDone is a test-only helper that installs a Done snapshot directly,
// bypassing the StartExecute/FinishExecute lifecycle. Lives on *Session in a
// _test.go file so it isn't part of the production API surface.
func (s *Session) setLastDone(snap *DoneSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastDone = snap
}

// resetSession reinitializes the package-global session so tests don't leak
// state into one another.
func resetSession() {
	current = &Session{}
}

func TestScanEvents_DeliversCompleteFromSnapshot(t *testing.T) {
	resetSession()
	moves := []PlannedMove{{
		Source:     "/src/IMG_1234.jpg",
		DestRel:    "2025/20250115/IMG_1234.jpg",
		DestAbs:    "/dst/2025/20250115/IMG_1234.jpg",
		DateSource: DateSourceExifOriginal,
	}}
	current.Set(moves, "/src", "/dst", "")

	req := httptest.NewRequest(http.MethodGet, "/scan/events", nil)
	rec := httptest.NewRecorder()
	handleScanEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: complete") {
		t.Errorf("missing `event: complete` in body:\n%s", body)
	}
	if !strings.Contains(body, "IMG_1234.jpg") {
		t.Errorf("missing basename in body:\n%s", body)
	}
}

func TestScanEvents_404WhenNothingToShow(t *testing.T) {
	resetSession()
	req := httptest.NewRequest(http.MethodGet, "/scan/events", nil)
	rec := httptest.NewRecorder()
	handleScanEvents(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestExecuteEvents_DeliversCompleteFromLastDone(t *testing.T) {
	resetSession()
	started := time.Now().Add(-time.Second)
	snap := &DoneSnapshot{
		Source:       "/src",
		Dest:         "/dst",
		Mode:         "move",
		Started:      started,
		Finished:     time.Now(),
		ManifestPath: "/runs/2026-05-08T10-00-00Z.csv",
		ManifestURL:  "/runs/2026-05-08T10-00-00Z.csv",
		Completed: []CompletedMove{{
			PlannedMove: PlannedMove{Source: "/src/IMG_1234.jpg"},
			Status:      StatusMoved,
		}},
	}
	current.setLastDone(snap)

	req := httptest.NewRequest(http.MethodGet, "/execute/events", nil)
	rec := httptest.NewRecorder()
	handleExecuteEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: complete") {
		t.Errorf("missing `event: complete` in body:\n%s", body)
	}
	// DoneModel.Counts() emits "%d moved, %d copied, %d skipped, %d failed".
	if !strings.Contains(body, "moved") {
		t.Errorf("missing Done-screen counts (`moved`) in body:\n%s", body)
	}
}

func TestExecuteEvents_404WhenNothingToShow(t *testing.T) {
	resetSession()
	req := httptest.NewRequest(http.MethodGet, "/execute/events", nil)
	rec := httptest.NewRecorder()
	handleExecuteEvents(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFilterIncluded(t *testing.T) {
	moves := []PlannedMove{
		{Source: "/src/a"},
		{Source: "/src/b"},
		{Source: "/src/c"},
		{Source: "/src/d"},
	}
	tests := []struct {
		name string
		raw  []string
		want []string // expected sources, in order
	}{
		{"empty raw -> nil", nil, nil},
		{"all four", []string{"0", "1", "2", "3"}, []string{"/src/a", "/src/b", "/src/c", "/src/d"}},
		{"subset preserves input order", []string{"2", "0"}, []string{"/src/a", "/src/c"}},
		{"out-of-range and garbage are dropped", []string{"1", "9", "-1", "x", "3"}, []string{"/src/b", "/src/d"}},
		{"duplicates are deduped", []string{"1", "1", "2"}, []string{"/src/b", "/src/c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterIncluded(moves, tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i].Source != tc.want[i] {
					t.Errorf("[%d] source: got %q, want %q", i, got[i].Source, tc.want[i])
				}
			}
		})
	}
}

// TestHandleExecute_SubsetMovesOnlyCheckedRows drives the /execute handler
// end-to-end with a real source tree and a subset POST. Verifies the file
// system, the Done snapshot, and the manifest CSV all reflect just the rows
// the user checked.
func TestHandleExecute_SubsetMovesOnlyCheckedRows(t *testing.T) {
	resetSession()

	stateDir := t.TempDir()
	t.Setenv(stateDirEnv, stateDir)

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")

	type fixture struct {
		name string
		body string
	}
	fixtures := []fixture{
		{"a.bin", "AAAA"},
		{"b.bin", "BBBB"},
		{"c.bin", "CCCC"},
	}
	moves := make([]PlannedMove, len(fixtures))
	for i, f := range fixtures {
		src := filepath.Join(srcDir, f.name)
		dst := filepath.Join(dstDir, "2024", "20240101", f.name)
		writeFile(t, src, f.body)
		moves[i] = PlannedMove{
			Source:     src,
			DestRel:    filepath.Join("2024", "20240101", f.name),
			DestAbs:    dst,
			Size:       int64(len(f.body)),
			DateSource: DateSourceMtime,
		}
	}
	current.Set(moves, srcDir, dstDir, "move")

	form := url.Values{}
	form.Set("mode", "move")
	// User unchecks index 1; rows 0 and 2 stay checked.
	form.Add("include", "0")
	form.Add("include", "2")
	req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleExecute(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	// runExecute is goroutine-scheduled; poll LastDone until it lands or
	// timeout. Two tiny renames complete in milliseconds, but go scheduler
	// jitter on CI dictates a generous bound.
	var snap *DoneSnapshot
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := current.LastDone(); s != nil {
			snap = s
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap == nil {
		t.Fatalf("LastDone never populated after handleExecute")
	}

	if got, want := len(snap.Completed), 2; got != want {
		t.Fatalf("Done.Completed: got %d entries, want %d", got, want)
	}
	if snap.Completed[0].Source != moves[0].Source {
		t.Errorf("Done[0].Source = %q, want %q", snap.Completed[0].Source, moves[0].Source)
	}
	if snap.Completed[1].Source != moves[2].Source {
		t.Errorf("Done[1].Source = %q, want %q", snap.Completed[1].Source, moves[2].Source)
	}

	// Filesystem: rows 0 and 2 moved, row 1 source untouched.
	if _, err := os.Stat(moves[0].Source); !os.IsNotExist(err) {
		t.Errorf("checked row 0 source still exists after move: err=%v", err)
	}
	if _, err := os.Stat(moves[1].Source); err != nil {
		t.Errorf("unchecked row 1 source removed: err=%v", err)
	}
	if _, err := os.Stat(moves[2].Source); !os.IsNotExist(err) {
		t.Errorf("checked row 2 source still exists after move: err=%v", err)
	}
	if _, err := os.Stat(moves[1].DestAbs); !os.IsNotExist(err) {
		t.Errorf("unchecked row 1 dest exists: err=%v", err)
	}

	// Manifest: header + 2 rows, and the unchecked source must not appear.
	if snap.ManifestPath == "" {
		t.Fatalf("ManifestPath empty")
	}
	f, err := os.Open(snap.ManifestPath)
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("csv read: %v", err)
	}
	if got, want := len(rows), 1+2; got != want {
		t.Errorf("manifest rows: got %d, want %d (header+2)", got, want)
	}
	for _, row := range rows[1:] {
		if row[0] == moves[1].Source {
			t.Errorf("manifest contains unchecked source %q", row[0])
		}
	}
}

func TestHandleSettings_GETRendersFormWithCurrentValues(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(stateDirEnv, stateDir)

	cfg := Config{
		PathTemplate:         "2006-01/02",
		AlignMtimeToExif:     true,
		SoftMatchDestination: false,
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	handleSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="pathTemplate"`) {
		t.Errorf("missing pathTemplate input:\n%s", body)
	}
	if !strings.Contains(body, `value="2006-01/02"`) {
		t.Errorf("pathTemplate input not pre-populated:\n%s", body)
	}
	// The Align checkbox should render as checked.
	if !strings.Contains(body, `name="alignMtimeToExif" checked`) {
		t.Errorf("align checkbox should be checked:\n%s", body)
	}
	// The Soft-match checkbox should NOT carry the checked attribute.
	if strings.Contains(body, `name="softMatchDestination" checked`) {
		t.Errorf("soft-match checkbox should be unchecked:\n%s", body)
	}
	// Settings link still rendered in the shared header.
	if !strings.Contains(body, `href="/settings"`) {
		t.Errorf("missing /settings nav link:\n%s", body)
	}
}

func TestHandleSettings_GETDefaultsWhenNoConfig(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	handleSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// Default template should pre-populate the input.
	if !strings.Contains(body, `value="`+DefaultPathTemplate+`"`) {
		t.Errorf("default template not rendered:\n%s", body)
	}
}

func TestHandleSettings_POSTPersistsAndShowsSuccess(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(stateDirEnv, stateDir)

	form := url.Values{}
	form.Set("pathTemplate", "2006/01/02")
	form.Set("alignMtimeToExif", "on")
	// softMatchDestination intentionally omitted -> false
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	// Success: handler signals htmx to navigate home instead of rendering a
	// banner. Body is empty; the redirect is on the response header.
	if got := rec.Header().Get("HX-Redirect"); got != "/" {
		t.Errorf("HX-Redirect: got %q, want %q", got, "/")
	}

	// Persisted state.json reflects the new values.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.PathTemplate != "2006/01/02" {
		t.Errorf("PathTemplate: got %q, want %q", cfg.PathTemplate, "2006/01/02")
	}
	if !cfg.AlignMtimeToExif {
		t.Errorf("AlignMtimeToExif: want true, got false")
	}
	if cfg.SoftMatchDestination {
		t.Errorf("SoftMatchDestination: want false, got true")
	}
}

func TestHandleSettings_POSTRejectsTemplateWithoutDateToken(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(stateDirEnv, stateDir)

	// Save a known-good baseline first so we can prove POST didn't overwrite.
	prior := Config{
		PathTemplate:     DefaultPathTemplate,
		AlignMtimeToExif: false,
	}
	if err := prior.Save(); err != nil {
		t.Fatalf("Save baseline: %v", err)
	}

	form := url.Values{}
	form.Set("pathTemplate", "static/folder")
	form.Set("alignMtimeToExif", "on")
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Could not save") {
		t.Errorf("missing validation banner:\n%s", body)
	}
	// Persisted state must still reflect baseline; rejected POST cannot mutate.
	cfg, _ := LoadConfig()
	if cfg.PathTemplate != DefaultPathTemplate {
		t.Errorf("PathTemplate clobbered by rejected POST: got %q", cfg.PathTemplate)
	}
	if cfg.AlignMtimeToExif {
		t.Errorf("AlignMtimeToExif clobbered by rejected POST")
	}
}

func TestHandleSettings_POSTRejectsEmptyTemplate(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())

	form := url.Values{}
	form.Set("pathTemplate", "   ")
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleSettings(rec, req)

	if !strings.Contains(rec.Body.String(), "non-empty") {
		t.Errorf("missing 'non-empty' message:\n%s", rec.Body.String())
	}
}

// TestHandleSettings_DoesNotAffectScanOrExecute guards the merge surface: the
// new handler is reachable but POSTing it must not touch the recents/last-mode
// fields used by the home form.
func TestHandleSettings_DoesNotAffectRecents(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())

	prior := Config{
		RecentSources:      []string{"/Volumes/SDCARD/DCIM"},
		RecentDestinations: []string{"/Volumes/Photos"},
		LastMode:           "move",
	}
	if err := prior.Save(); err != nil {
		t.Fatalf("Save baseline: %v", err)
	}

	form := url.Values{}
	form.Set("pathTemplate", "2006/20060102")
	form.Set("softMatchDestination", "on")
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	cfg, _ := LoadConfig()
	if len(cfg.RecentSources) != 1 || cfg.RecentSources[0] != "/Volumes/SDCARD/DCIM" {
		t.Errorf("RecentSources mutated: %v", cfg.RecentSources)
	}
	if cfg.LastMode != "move" {
		t.Errorf("LastMode mutated: %q", cfg.LastMode)
	}
}

// TestHandleScan_RejectsInvalidTemplate verifies the fail-fast posture: a
// malformed PathTemplate in state.json must short-circuit before any file walk
// or PlannedMove is produced. The user sees the template error fragment and
// the session never enters scanning state.
func TestHandleScan_RejectsInvalidTemplate(t *testing.T) {
	resetSession()
	t.Setenv(stateDirEnv, t.TempDir())

	// Persist a deliberately-bad template that the validator will reject.
	cfg := Config{PathTemplate: "static/folder"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}

	form := url.Values{}
	form.Set("source", srcDir)
	form.Set("destination", dstDir)
	req := httptest.NewRequest(http.MethodPost, "/scan", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Path template invalid") {
		t.Errorf("missing 'Path template invalid' header:\n%s", body)
	}
	if !strings.Contains(body, `href="/settings"`) {
		t.Errorf("missing /settings link:\n%s", body)
	}
	if current.RunningScan() != nil {
		t.Errorf("scan slot should not be claimed when validation fails")
	}
	if moves, _, _, _ := current.Snapshot(); moves != nil {
		t.Errorf("PlannedMoves should not be produced; got %d entries", len(moves))
	}
}

// TestHandleScan_CustomTemplateProducesMatchingDestRel runs the scan handler
// end-to-end with a non-default PathTemplate and asserts the resulting
// PlannedMoves' DestRel reflects the custom layout, not the v1 default.
func TestHandleScan_CustomTemplateProducesMatchingDestRel(t *testing.T) {
	resetSession()
	t.Setenv(stateDirEnv, t.TempDir())

	cfg := Config{PathTemplate: "2006-01/02"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	// Drop a single mtime-fallback fixture into the source. Pin its mtime so
	// the resulting DestRel is deterministic.
	srcFile := filepath.Join(srcDir, "clip.mts")
	writeFile(t, srcFile, "fixture")
	mtime := time.Date(2024, 7, 4, 9, 30, 0, 0, time.UTC)
	if err := os.Chtimes(srcFile, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	form := url.Values{}
	form.Set("source", srcDir)
	form.Set("destination", dstDir)
	req := httptest.NewRequest(http.MethodPost, "/scan", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	// runScan is goroutine-scheduled; poll Snapshot until it lands.
	var moves []PlannedMove
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m, _, _, _ := current.Snapshot(); m != nil {
			moves = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(moves) != 1 {
		t.Fatalf("got %d moves, want 1", len(moves))
	}
	wantDir := filepath.Join("2024-07", "04")
	if got := filepath.Dir(moves[0].DestRel); got != wantDir {
		t.Errorf("DestRel dir: got %q, want %q (custom template did not take effect)", got, wantDir)
	}
}

func TestHandleExecute_NoneCheckedRendersError(t *testing.T) {
	resetSession()
	current.Set([]PlannedMove{{Source: "/src/a"}}, "/src", "/dst", "move")

	form := url.Values{}
	form.Set("mode", "move")
	req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleExecute(rec, req)

	if rec.Code != http.StatusOK {
		// renderScanError is a 200 with the error article body, not a 4xx.
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no rows selected") {
		t.Errorf("missing 'no rows selected' in body:\n%s", rec.Body.String())
	}
	if current.RunningExecute() != nil {
		t.Errorf("execute slot should not be claimed when nothing was selected")
	}
}

// withFakeRevealExec swaps revealExec for the duration of t and restores it
// on cleanup. The capture closure records (path, isFile) so each test can
// assert the call shape without spawning a real Finder/xdg-open.
func withFakeRevealExec(t *testing.T) *struct {
	called bool
	path   string
	isFile bool
} {
	t.Helper()
	rec := &struct {
		called bool
		path   string
		isFile bool
	}{}
	prior := revealExec
	revealExec = func(path string, isFile bool) error {
		rec.called = true
		rec.path = path
		rec.isFile = isFile
		return nil
	}
	t.Cleanup(func() { revealExec = prior })
	return rec
}

func postReveal(path, kind string) *httptest.ResponseRecorder {
	form := url.Values{}
	form.Set("path", path)
	form.Set("kind", kind)
	req := httptest.NewRequest(http.MethodPost, "/reveal", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleReveal(rec, req)
	return rec
}

func TestHandleReveal_HappyPathSourceFile(t *testing.T) {
	resetSession()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	srcFile := filepath.Join(srcDir, "IMG.jpg")
	writeFile(t, srcFile, "x")
	current.Set([]PlannedMove{{Source: srcFile}}, srcDir, dstDir, "")

	exec := withFakeRevealExec(t)
	rec := postReveal(srcFile, "source")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	if !exec.called {
		t.Fatalf("revealExec was not called")
	}
	if exec.path != srcFile || !exec.isFile {
		t.Errorf("call shape: path=%q isFile=%v, want path=%q isFile=true", exec.path, exec.isFile, srcFile)
	}
}

func TestHandleReveal_DestFileRevealsAsFile(t *testing.T) {
	resetSession()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	dayDir := filepath.Join(dstDir, "2024", "20240101")
	destFile := filepath.Join(dayDir, "IMG.jpg")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, destFile, "x")
	current.Set([]PlannedMove{{DestAbs: destFile}}, srcDir, dstDir, "")

	exec := withFakeRevealExec(t)
	rec := postReveal(destFile, "dest")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	if !exec.called || exec.path != destFile || !exec.isFile {
		t.Errorf("call shape: path=%q isFile=%v, want path=%q isFile=true", exec.path, exec.isFile, destFile)
	}
}

// Typical OK-row case: the dest file hasn't been written yet but its parent
// directory exists. The handler falls back to revealing the parent folder.
func TestHandleReveal_DestMissingFallsBackToParent(t *testing.T) {
	resetSession()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	dayDir := filepath.Join(dstDir, "2024", "20240101")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	missing := filepath.Join(dayDir, "IMG.jpg")
	current.Set([]PlannedMove{{DestAbs: missing}}, srcDir, dstDir, "")

	exec := withFakeRevealExec(t)
	rec := postReveal(missing, "dest")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	if !exec.called || exec.path != dayDir || exec.isFile {
		t.Errorf("call shape: path=%q isFile=%v, want path=%q isFile=false", exec.path, exec.isFile, dayDir)
	}
}

func TestHandleReveal_HappyPathDestDir(t *testing.T) {
	resetSession()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	dayDir := filepath.Join(dstDir, "2024", "20240101")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	current.Set([]PlannedMove{{DestAbs: filepath.Join(dayDir, "IMG.jpg")}}, srcDir, dstDir, "")

	exec := withFakeRevealExec(t)
	rec := postReveal(dayDir, "dest")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	if !exec.called || exec.path != dayDir || exec.isFile {
		t.Errorf("call shape: path=%q isFile=%v, want path=%q isFile=false", exec.path, exec.isFile, dayDir)
	}
}

func TestHandleReveal_RejectsTraversal(t *testing.T) {
	resetSession()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	current.Set([]PlannedMove{{Source: filepath.Join(srcDir, "IMG.jpg")}}, srcDir, dstDir, "")

	exec := withFakeRevealExec(t)
	// Concatenate with a literal '..' segment so the un-Cleaned form survives
	// to the handler. filepath.Join would collapse it before transmission.
	bad := srcDir + string(filepath.Separator) + ".." + string(filepath.Separator) + "secret"
	rec := postReveal(bad, "source")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
	}
	if exec.called {
		t.Errorf("revealExec must not be invoked on traversal reject")
	}
}

func TestHandleReveal_RejectsOutsideRoots(t *testing.T) {
	resetSession()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A real, existing path outside both roots — different temp dir.
	other := t.TempDir()
	otherFile := filepath.Join(other, "leak.txt")
	writeFile(t, otherFile, "x")
	current.Set([]PlannedMove{{Source: filepath.Join(srcDir, "IMG.jpg")}}, srcDir, dstDir, "")

	exec := withFakeRevealExec(t)
	rec := postReveal(otherFile, "source")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
	}
	if exec.called {
		t.Errorf("revealExec must not be invoked for out-of-roots path")
	}
}

func TestHandleReveal_RejectsWhenNoLiveScan(t *testing.T) {
	resetSession()
	exec := withFakeRevealExec(t)
	rec := postReveal("/tmp/whatever", "source")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
	}
	if exec.called {
		t.Errorf("revealExec must not be invoked without an active scan")
	}
}
