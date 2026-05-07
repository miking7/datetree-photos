package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func redirectConfig(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv(stateDirEnv, tmp)
	return tmp
}

func TestConfigLoadMissingFile(t *testing.T) {
	redirectConfig(t)
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig on missing file: unexpected error %v", err)
	}
	if len(c.RecentSources) != 0 || len(c.RecentDestinations) != 0 || c.LastMode != "" {
		t.Errorf("expected zero-value Config, got %+v", c)
	}
}

func TestConfigSaveLoadRoundTrip(t *testing.T) {
	redirectConfig(t)
	saved := Config{
		RecentSources:      []string{"/Volumes/SDCARD/DCIM", "/tmp/cards/A"},
		RecentDestinations: []string{"/Volumes/Photos", "/tmp/scratch"},
		LastMode:           "copy",
	}
	if err := saved.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Loader fills an empty saved PathTemplate with the default so callers
	// can rely on it being usable. Compare against that expectation.
	want := saved
	want.PathTemplate = DefaultPathTemplate
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestConfigRecordRunFresh(t *testing.T) {
	redirectConfig(t)
	var c Config
	c.RecordRun("/src", "/dst", "move")

	if got, want := c.RecentSources, []string{"/src"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RecentSources: got %v, want %v", got, want)
	}
	if got, want := c.RecentDestinations, []string{"/dst"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RecentDestinations: got %v, want %v", got, want)
	}
	if c.LastMode != "move" {
		t.Errorf("LastMode: got %q, want %q", c.LastMode, "move")
	}
}

func TestConfigRecordRunDedupMovesToFront(t *testing.T) {
	redirectConfig(t)
	c := Config{
		RecentSources:      []string{"/a", "/b", "/c"},
		RecentDestinations: []string{"/x", "/y", "/z"},
		LastMode:           "move",
	}
	c.RecordRun("/b", "/z", "copy")

	if got, want := c.RecentSources, []string{"/b", "/a", "/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RecentSources: got %v, want %v", got, want)
	}
	if got, want := c.RecentDestinations, []string{"/z", "/x", "/y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RecentDestinations: got %v, want %v", got, want)
	}
	if c.LastMode != "copy" {
		t.Errorf("LastMode: got %q, want %q", c.LastMode, "copy")
	}
}

func TestConfigRecordRunTruncatesToSix(t *testing.T) {
	redirectConfig(t)
	var c Config
	for _, s := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		c.RecordRun(s, s+"-dst", "move")
	}

	wantSrc := []string{"g", "f", "e", "d", "c", "b"}
	if !reflect.DeepEqual(c.RecentSources, wantSrc) {
		t.Errorf("RecentSources: got %v, want %v", c.RecentSources, wantSrc)
	}
	wantDst := []string{"g-dst", "f-dst", "e-dst", "d-dst", "c-dst", "b-dst"}
	if !reflect.DeepEqual(c.RecentDestinations, wantDst) {
		t.Errorf("RecentDestinations: got %v, want %v", c.RecentDestinations, wantDst)
	}
}

func TestConfigRecordRunMode(t *testing.T) {
	redirectConfig(t)
	cases := []struct {
		name  string
		prior string
		input string
		want  string
	}{
		{"set move on empty", "", "move", "move"},
		{"set copy on empty", "", "copy", "copy"},
		{"empty leaves prior", "move", "", "move"},
		{"invalid leaves prior", "copy", "rename", "copy"},
		{"invalid leaves empty", "", "garbage", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{LastMode: tc.prior}
			c.RecordRun("/s", "/d", tc.input)
			if c.LastMode != tc.want {
				t.Errorf("LastMode: got %q, want %q", c.LastMode, tc.want)
			}
		})
	}
}

func TestConfigRecordRunIgnoresEmptyPaths(t *testing.T) {
	redirectConfig(t)
	c := Config{
		RecentSources:      []string{"/a"},
		RecentDestinations: []string{"/x"},
	}
	c.RecordRun("", "", "move")

	if got, want := c.RecentSources, []string{"/a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RecentSources: got %v, want %v", got, want)
	}
	if got, want := c.RecentDestinations, []string{"/x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RecentDestinations: got %v, want %v", got, want)
	}
}

func TestConfigSettingsRoundTrip(t *testing.T) {
	redirectConfig(t)
	want := Config{
		PathTemplate:         "2006/01/02",
		AlignMtimeToExif:     true,
		SoftMatchDestination: true,
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.PathTemplate != want.PathTemplate {
		t.Errorf("PathTemplate: got %q, want %q", got.PathTemplate, want.PathTemplate)
	}
	if got.AlignMtimeToExif != want.AlignMtimeToExif {
		t.Errorf("AlignMtimeToExif: got %v, want %v", got.AlignMtimeToExif, want.AlignMtimeToExif)
	}
	if got.SoftMatchDestination != want.SoftMatchDestination {
		t.Errorf("SoftMatchDestination: got %v, want %v", got.SoftMatchDestination, want.SoftMatchDestination)
	}
}

// TestConfigLoadOlderStateFile guards the upgrade path: a state.json written
// before the settings fields existed must load with sensible defaults — empty
// PathTemplate gets DefaultPathTemplate, and the two bool toggles default to
// true (on-by-default for fresh installs and pre-settings state files).
func TestConfigLoadOlderStateFile(t *testing.T) {
	tmp := redirectConfig(t)
	older := []byte(`{
		"recentSources": ["/Volumes/SDCARD/DCIM"],
		"recentDestinations": ["/Volumes/Photos"],
		"lastMode": "move"
	}`)
	if err := os.WriteFile(filepath.Join(tmp, stateFile), older, 0o644); err != nil {
		t.Fatalf("seed older state.json: %v", err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.PathTemplate != DefaultPathTemplate {
		t.Errorf("PathTemplate fallback: got %q, want %q", got.PathTemplate, DefaultPathTemplate)
	}
	if !got.AlignMtimeToExif {
		t.Errorf("AlignMtimeToExif: expected true on missing field, got false")
	}
	if !got.SoftMatchDestination {
		t.Errorf("SoftMatchDestination: expected true on missing field, got false")
	}
	// Ensure the older fields still round-tripped intact.
	if len(got.RecentSources) != 1 || got.RecentSources[0] != "/Volumes/SDCARD/DCIM" {
		t.Errorf("RecentSources: got %v, want [/Volumes/SDCARD/DCIM]", got.RecentSources)
	}
	if got.LastMode != "move" {
		t.Errorf("LastMode: got %q, want move", got.LastMode)
	}
}

// TestDefaultPathTemplateProducesV1Layout pins DefaultPathTemplate to the
// pre-template-wiring v1 layout (Year/YYYYMMDD/basename). Drift here means
// upgraders would land in different folders than they did before.
func TestDefaultPathTemplateProducesV1Layout(t *testing.T) {
	// 2026-05-09 14:23:01 — picked to exercise both year and intra-year padding.
	d := time.Date(2026, 5, 9, 14, 23, 1, 0, time.UTC)
	got := destRel(d, "/src/IMG_0001.JPG", DefaultPathTemplate)
	want := filepath.Join("2026", "20260509", "IMG_0001.JPG")
	if got != want {
		t.Errorf("destRel(_, _, DefaultPathTemplate) = %q, want %q", got, want)
	}
}

func TestValidatePathTemplate(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string // substring expected in error message; "" means valid
	}{
		{"valid default", DefaultPathTemplate, ""},
		{"valid Year-Month/Day", "2006-01/02", ""},
		{"valid with literal", "archive/2006/20060102", ""},
		{"empty", "", "non-empty"},
		{"no date token", "static/folder", "date token"},
		{"absolute path", "/2006/01/02", "relative"},
		{"contains ..", "2006/../01", ".."},
		{"NUL byte", "2006/01\x0002", "illegal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePathTemplate(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("got error %q, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestConfigSaveCreatesParentDirs(t *testing.T) {
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "deep", "nested", "subdir")
	t.Setenv(stateDirEnv, nested)

	c := Config{LastMode: "move"}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nested, stateFile)); err != nil {
		t.Errorf("state.json not created in nested dir: %v", err)
	}
}
