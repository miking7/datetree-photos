package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/miking7/datetree-photos/components"
)

func handleHome(w http.ResponseWriter, r *http.Request) {
	// Default ServeMux maps unknown paths to "/", so reject anything else here.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	renderHome(w, r)
}

// renderHome pre-fills the form from state.json so the next import defaults
// to the most-recent source/dest and offers prior paths via the recents list.
func renderHome(w http.ResponseWriter, r *http.Request) {
	cfg, _ := LoadConfig()
	m := components.HomeModel{
		RecentSources:      cfg.RecentSources,
		RecentDestinations: cfg.RecentDestinations,
	}
	if len(cfg.RecentSources) > 0 {
		m.DefaultSource = cfg.RecentSources[0]
	}
	if len(cfg.RecentDestinations) > 0 {
		m.DefaultDest = cfg.RecentDestinations[0]
	}
	_ = components.Home(m).Render(r.Context(), w)
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		renderScanError(w, r, "could not parse form: "+err.Error())
		return
	}

	source := strings.TrimSpace(r.FormValue("source"))
	dest := strings.TrimSpace(r.FormValue("destination"))

	if source == "" || dest == "" {
		renderScanError(w, r, "source and destination are both required.")
		return
	}
	source = filepath.Clean(source)
	dest = filepath.Clean(dest)

	info, err := os.Stat(source)
	if err != nil {
		renderScanError(w, r, fmt.Sprintf("source path %q is not accessible: %s", source, err))
		return
	}
	if !info.IsDir() {
		renderScanError(w, r, fmt.Sprintf("source path %q is not a directory.", source))
		return
	}

	// Validate the path template up front so a malformed value doesn't produce
	// PlannedMoves with stale paths the user never asked for. Fail-fast: no
	// silent fallback to DefaultPathTemplate, since that hides the corruption.
	cfg, _ := LoadConfig()
	if err := validatePathTemplate(cfg.PathTemplate); err != nil {
		renderScanTemplateError(w, r, err.Error())
		return
	}

	// Persist on scan-start so re-scans before an Execute still pick up the
	// path in the recents list. Mode is chosen at execute time, so pass empty
	// here — RecordRun leaves LastMode unchanged on empty input. cfg was
	// loaded above for path-template validation; reusing it here also
	// snapshots the SoftMatchDestination toggle for this scan, so a setting
	// flip mid-run can't change behaviour partway through.
	cfg.RecordRun(source, dest, "")
	if serr := cfg.Save(); serr != nil {
		log.Printf("config save: %v", serr)
	}

	// Detached context — the scan must outlive this request, since the form
	// POST returns the progress fragment and disconnects.
	scanCtx, cancel := context.WithCancel(context.Background())
	reporter := NewReporter()
	rs := &RunningScan{
		Reporter:  reporter,
		Cancel:    cancel,
		Source:    source,
		Dest:      dest,
		Template:  cfg.PathTemplate,
		SoftMatch: cfg.SoftMatchDestination,
	}
	if err := current.StartScan(rs); err != nil {
		cancel()
		renderScanError(w, r, err.Error())
		return
	}

	go runScan(scanCtx, rs)

	model := components.ProgressModel{
		Source: source,
		Dest:   dest,
		Total:  -1,
	}
	if err := components.Progress(model).Render(r.Context(), w); err != nil {
		log.Printf("render error: %v", err)
	}
}

// runScan executes the scan in the background, persisting the result and the
// final preview HTML for the SSE handler to deliver as the `complete` event.
func runScan(ctx context.Context, rs *RunningScan) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("scan panic: %v", rec)
		}
		// Closing the reporter wakes any SSE subscribers that are still
		// connected so they can finish writing and tear down.
		rs.Reporter.Close()
	}()

	moves, err := Scan(ctx, rs.Source, rs.Dest, rs.Template, rs.SoftMatch, rs.Reporter)
	if err != nil {
		// On cancel or error, surface back to the home form. The SSE handler
		// notices the closed reporter and just shuts the stream.
		log.Printf("scan ended: %v", err)
		current.FinishScan(nil)
		return
	}

	// Persist the result and release the running-scan slot. The deferred
	// reporter Close wakes the SSE handler so it can deliver the `complete`
	// event from the freshly-saved snapshot.
	current.FinishScan(moves)
}

func handleScanEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	rs := current.RunningScan()
	if rs == nil {
		// Race: the scan may have finished before the EventSource connected.
		// If a snapshot is ready, deliver the preview as a single complete
		// event so the body swap still happens. Otherwise 404 — there is
		// genuinely nothing to stream.
		if moves, src, dst, _ := current.Snapshot(); moves != nil {
			writeSSEHeaders(w, flusher)
			writeScanComplete(r.Context(), w, flusher, moves, src, dst)
			return
		}
		http.Error(w, "no active scan", http.StatusNotFound)
		return
	}

	writeSSEHeaders(w, flusher)

	ch, unsub := rs.Reporter.Subscribe()
	defer unsub()

	source, dest := rs.Source, rs.Dest

	for {
		select {
		case <-r.Context().Done():
			return
		case p, ok := <-ch:
			if !ok {
				// Reporter closed — scan finished or was cancelled. If we have
				// a successful result, send the preview as the final event.
				if moves, src, dst, _ := current.Snapshot(); src == source && dst == dest && moves != nil {
					writeScanComplete(r.Context(), w, flusher, moves, src, dst)
				}
				return
			}
			model := components.ProgressModel{
				Source:    source,
				Dest:      dest,
				Processed: p.Processed,
				Total:     p.Total,
				Current:   p.Current,
			}
			body := renderComponent(r.Context(), components.ProgressBody(model))
			if err := writeSSEEvent(w, "progress", body); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEHeaders sets the SSE response headers, sends 200, and flushes.
func writeSSEHeaders(w http.ResponseWriter, flusher http.Flusher) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
}

// writeScanComplete renders the preview as the final `complete` SSE event.
// Headers must already be written and flushed before calling.
func writeScanComplete(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, moves []PlannedMove, source, dest string) {
	body := renderComponent(ctx, components.Preview(buildPreviewModel(moves, source, dest)))
	if err := writeSSEEvent(w, "complete", body); err == nil {
		flusher.Flush()
	}
}

func handleScanCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if rs := current.RunningScan(); rs != nil {
		rs.Cancel()
	}
	renderHome(w, r)
}

func renderComponent(ctx context.Context, c templ.Component) []byte {
	var buf bytes.Buffer
	if err := c.Render(ctx, &buf); err != nil {
		log.Printf("render error: %v", err)
	}
	return buf.Bytes()
}

// writeSSEEvent emits one event in the SSE wire format. Multi-line payloads
// must repeat `data: ` for each line per the SSE spec, terminated by a blank
// line. CRs in the payload would confuse the parser, so they are stripped.
func writeSSEEvent(w http.ResponseWriter, event string, payload []byte) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "event: %s\n", event)
	payload = bytes.ReplaceAll(payload, []byte("\r"), nil)
	if len(payload) == 0 {
		buf.WriteString("data: \n")
	} else {
		for _, line := range bytes.Split(payload, []byte("\n")) {
			buf.WriteString("data: ")
			buf.Write(line)
			buf.WriteByte('\n')
		}
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// filterIncluded returns the subset of moves whose row indices appear in raw,
// preserving original order. Out-of-range or non-numeric values are dropped so
// a stale form post can't index past the current plan. A nil/empty raw means
// no rows checked, which the caller surfaces as a "no rows selected" error.
func filterIncluded(moves []PlannedMove, raw []string) []PlannedMove {
	if len(raw) == 0 {
		return nil
	}
	keep := make(map[int]struct{}, len(raw))
	for _, s := range raw {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n >= len(moves) {
			continue
		}
		keep[n] = struct{}{}
	}
	out := make([]PlannedMove, 0, len(keep))
	for i, m := range moves {
		if _, ok := keep[i]; ok {
			out = append(out, m)
		}
	}
	return out
}

func buildPreviewModel(moves []PlannedMove, source, dest string) components.PreviewModel {
	m := components.PreviewModel{
		Source: source,
		Dest:   dest,
		Total:  len(moves),
	}
	m.Rows = make([]components.PreviewRow, 0, len(moves))
	for _, mv := range moves {
		if mv.Conflict {
			m.Conflicts++
		}
		if mv.DateSource == DateSourceMtime {
			m.MtimeFallbacks++
		}
		if mv.Error != "" {
			m.Errors++
		}
		// Status precedence: Error beats Conflict beats mtime fallback. Used by
		// preview.templ to render the per-row pill and pick the row tint.
		status := "OK"
		switch {
		case mv.Error != "":
			status = "Error"
		case mv.Conflict:
			status = "Conflict"
		case mv.DateSource == DateSourceMtime:
			status = "mtime"
		}
		m.Rows = append(m.Rows, components.PreviewRow{
			Source:      filepath.Base(mv.Source),
			SourceFull:  mv.Source,
			CaptureDate: mv.CaptureDate.Format("2006-01-02 15:04"),
			DateSource:  mv.DateSource.String(),
			DestRel:     mv.DestRel,
			DestAbs:     mv.DestAbs,
			Conflict:    mv.Conflict,
			HasError:    mv.Error != "",
			Status:      status,
		})
	}
	return m
}

func renderScanError(w http.ResponseWriter, r *http.Request, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.ScanError(msg).Render(r.Context(), w)
}

func renderScanTemplateError(w http.ResponseWriter, r *http.Request, reason string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.ScanTemplateError(reason).Render(r.Context(), w)
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		renderScanError(w, r, "could not parse form: "+err.Error())
		return
	}
	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode != "move" && mode != "copy" {
		renderScanError(w, r, "execute requires a mode parameter (move or copy).")
		return
	}
	moves, source, dest, _ := current.Snapshot()
	if len(moves) == 0 {
		renderScanError(w, r, "no scan plan available — run a scan first.")
		return
	}
	// The preview form posts one include=<row-index> value per checked row.
	// Skipped rows never reach Execute, the manifest, or the Done counts.
	moves = filterIncluded(moves, r.Form["include"])
	if len(moves) == 0 {
		renderScanError(w, r, "no rows selected — check at least one row to execute.")
		return
	}

	execCtx, cancel := context.WithCancel(context.Background())
	reporter := NewReporter()
	re := &RunningExecute{
		Reporter: reporter,
		Cancel:   cancel,
		Source:   source,
		Dest:     dest,
		Mode:     mode,
		Moves:    moves,
		Started:  time.Now(),
	}
	if err := current.StartExecute(re); err != nil {
		cancel()
		renderScanError(w, r, err.Error())
		return
	}

	go runExecute(execCtx, re)

	model := components.ProgressModel{
		Source:     source,
		Dest:       dest,
		Mode:       mode,
		Total:      len(moves),
		HeadingTpl: "Executing %s...",
		SSEPath:    "/execute/events",
		CancelPath: "/execute/cancel",
	}
	if err := components.Progress(model).Render(r.Context(), w); err != nil {
		log.Printf("render error: %v", err)
	}
}

func runExecute(ctx context.Context, re *RunningExecute) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("execute panic: %v", rec)
		}
		re.Reporter.Close()
	}()

	completed := Execute(ctx, re.Moves, ParseMode(re.Mode), re.Reporter)

	// Manifest write is best-effort: even if it fails the bytes have been
	// moved/copied, so the user has work to keep. Log and carry on.
	manifestPath, err := WriteManifest(re.Started, completed)
	if err != nil {
		log.Printf("manifest write: %v", err)
	}
	manifestURL := ""
	if manifestPath != "" {
		manifestURL = "/runs/" + filepath.Base(manifestPath)
	}

	// Persist recents/last-mode. Don't block the run on a save failure.
	if cfg, lerr := LoadConfig(); lerr == nil {
		cfg.RecordRun(re.Source, re.Dest, re.Mode)
		if serr := cfg.Save(); serr != nil {
			log.Printf("config save: %v", serr)
		}
	} else {
		log.Printf("config load: %v", lerr)
	}

	current.FinishExecute(completed, manifestPath, manifestURL)
}

func handleExecuteEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	re := current.RunningExecute()
	if re == nil {
		// Race: the execute may have finished before the EventSource connected.
		// If a Done snapshot is ready, deliver the Done screen as a single
		// complete event. Otherwise 404 — nothing to stream.
		if snap := current.LastDone(); snap != nil {
			writeSSEHeaders(w, flusher)
			writeExecuteComplete(r.Context(), w, flusher, snap)
			return
		}
		http.Error(w, "no active execute", http.StatusNotFound)
		return
	}

	writeSSEHeaders(w, flusher)

	ch, unsub := re.Reporter.Subscribe()
	defer unsub()

	source, dest, mode := re.Source, re.Dest, re.Mode
	started := re.Started
	total := len(re.Moves)

	for {
		select {
		case <-r.Context().Done():
			return
		case p, ok := <-ch:
			if !ok {
				if snap := current.LastDone(); snap != nil && snap.Source == source && snap.Dest == dest && snap.Mode == mode && snap.Started.Equal(started) {
					writeExecuteComplete(r.Context(), w, flusher, snap)
				}
				return
			}
			model := components.ProgressModel{
				Source:     source,
				Dest:       dest,
				Mode:       mode,
				Processed:  p.Processed,
				Total:      total,
				Current:    p.Current,
				HeadingTpl: "Executing %s...",
				SSEPath:    "/execute/events",
				CancelPath: "/execute/cancel",
			}
			body := renderComponent(r.Context(), components.ProgressBody(model))
			if err := writeSSEEvent(w, "progress", body); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeExecuteComplete renders the Done screen as the final `complete` SSE
// event. Headers must already be written and flushed before calling.
func writeExecuteComplete(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, snap *DoneSnapshot) {
	body := renderComponent(ctx, components.Done(buildDoneModel(snap)))
	if err := writeSSEEvent(w, "complete", body); err == nil {
		flusher.Flush()
	}
}

func handleExecuteCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if re := current.RunningExecute(); re != nil {
		re.Cancel()
	}
	renderHome(w, r)
}

func buildDoneModel(snap *DoneSnapshot) components.DoneModel {
	m := components.DoneModel{
		Source:       snap.Source,
		Dest:         snap.Dest,
		Mode:         snap.Mode,
		Total:        len(snap.Completed),
		ManifestPath: snap.ManifestPath,
		ManifestURL:  snap.ManifestURL,
		DurationStr:  snap.Finished.Sub(snap.Started).Truncate(time.Millisecond).String(),
	}
	for _, c := range snap.Completed {
		switch c.Status {
		case StatusMoved:
			m.Moved++
		case StatusCopied:
			m.Copied++
		case StatusSkipped:
			m.Skipped++
		case StatusFailed:
			m.Failed++
			m.Failures = append(m.Failures, components.DoneFailure{
				Source: c.Source,
				ErrMsg: c.ErrMsg,
			})
		}
	}
	return m
}

// pathTemplatePresets is the canonical preset list shown in the dropdown. The
// inline preview script in settings.templ reads the same list as JSON, so this
// is the single source of truth.
var pathTemplatePresets = []components.SettingsPreset{
	{Label: "Year / YYYYMMDD", Layout: "2006/20060102"},
	{Label: "Year / Month / Day", Layout: "2006/01/02"},
	{Label: "Year-Month / Day", Layout: "2006-01/02"},
	{Label: "Year / YYYYMMDD-HHMM", Layout: "2006/20060102-1504"},
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_ = components.Settings(settingsModelFromConfig(loadConfigOrEmpty())).Render(r.Context(), w)
	case http.MethodPost:
		handleSettingsPost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		// Same banner-only swap as the success path so the form stays put.
		_ = components.SettingsBanner(components.SettingsModel{
			Error: "could not parse form: " + err.Error(),
		}).Render(r.Context(), w)
		return
	}

	tpl := strings.TrimSpace(r.FormValue("pathTemplate"))
	if err := validatePathTemplate(tpl); err != nil {
		_ = components.SettingsBanner(components.SettingsModel{
			Error: err.Error() + ".",
		}).Render(r.Context(), w)
		return
	}

	cfg := loadConfigOrEmpty()
	cfg.PathTemplate = tpl
	cfg.AlignMtimeToExif = parseCheckbox(r.FormValue("alignMtimeToExif"))
	cfg.SoftMatchDestination = parseCheckbox(r.FormValue("softMatchDestination"))
	if err := cfg.Save(); err != nil {
		_ = components.SettingsBanner(components.SettingsModel{
			Error: "save failed: " + err.Error(),
		}).Render(r.Context(), w)
		return
	}

	// Success: tell htmx to navigate the browser back to home rather than
	// swapping a banner in place. Errors above still render the banner so the
	// user keeps their entered values and sees the message in context.
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

func settingsModelFromConfig(cfg Config) components.SettingsModel {
	tpl := cfg.PathTemplate
	if tpl == "" {
		tpl = DefaultPathTemplate
	}
	return components.SettingsModel{
		PathTemplate:         tpl,
		AlignMtimeToExif:     cfg.AlignMtimeToExif,
		SoftMatchDestination: cfg.SoftMatchDestination,
		Presets:              pathTemplatePresets,
		PreviewToday:         time.Now().Format(tpl),
	}
}

func loadConfigOrEmpty() Config {
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("config load: %v", err)
		return Config{PathTemplate: DefaultPathTemplate}
	}
	return cfg
}

// parseCheckbox treats HTML form checkbox semantics: present (any of "on",
// "true", "1") = true, absent or any other value = false.
func parseCheckbox(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}

// handleManifest serves a previously-written manifest CSV. Path-traversal is
// rejected at the router boundary by validating the basename has no slashes
// and no parent-dir tokens before joining with runsDir().
func handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/runs/")
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	if !strings.HasSuffix(name, ".csv") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(runsDir(), name)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	http.ServeFile(w, r, full)
}

