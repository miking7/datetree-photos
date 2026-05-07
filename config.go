package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	RecentSources        []string `json:"recentSources"`
	RecentDestinations   []string `json:"recentDestinations"`
	LastMode             string   `json:"lastMode"`
	PathTemplate         string   `json:"pathTemplate"`
	AlignMtimeToExif     bool     `json:"alignMtimeToExif"`
	SoftMatchDestination bool     `json:"softMatchDestination"`
}

// DefaultPathTemplate matches the v1 hardcoded layout in scanner.destRel
// ("Year/YYYYMMDD"). Kept exported so the settings handler and follow-up
// mover-wiring task share one source of truth.
const DefaultPathTemplate = "2006/20060102"

const (
	stateDirEnv  = "DATETREE_STATE_DIR"
	stateFile    = "state.json"
	maxRecent    = 6
	defaultPerm  = 0o644
	defaultDperm = 0o755
)

// ConfigPath honors DATETREE_STATE_DIR (used by tests) before falling
// back to the OS-appropriate user config directory. os.UserConfigDir
// returns ~/Library/Application Support on macOS (unchanged behavior)
// and $XDG_CONFIG_HOME or ~/.config on Linux.
func ConfigPath() string {
	if dir := os.Getenv(stateDirEnv); dir != "" {
		return filepath.Join(dir, stateFile)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return stateFile
	}
	return filepath.Join(cfg, "datetree", stateFile)
}

// freshConfig is the Config used when no state.json exists, plus the source
// of missing-field defaults inside LoadConfig. The two booleans default to
// true: photographers want EXIF as the authoritative capture date, and want
// imports to merge into existing descriptive date folders rather than
// fragmenting them.
func freshConfig() Config {
	return Config{
		PathTemplate:         DefaultPathTemplate,
		AlignMtimeToExif:     true,
		SoftMatchDestination: true,
	}
}

func LoadConfig() (Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return freshConfig(), nil
		}
		return Config{}, err
	}
	// *bool lets us tell "field absent" (older state.json) from "field set to
	// false" (user unchecked the box). Absent inherits the new on-by-default
	// values; an explicit false is preserved.
	type rawConfig struct {
		RecentSources        []string `json:"recentSources"`
		RecentDestinations   []string `json:"recentDestinations"`
		LastMode             string   `json:"lastMode"`
		PathTemplate         string   `json:"pathTemplate"`
		AlignMtimeToExif     *bool    `json:"alignMtimeToExif"`
		SoftMatchDestination *bool    `json:"softMatchDestination"`
	}
	var r rawConfig
	if err := json.Unmarshal(data, &r); err != nil {
		return Config{}, err
	}
	c := freshConfig()
	c.RecentSources = r.RecentSources
	c.RecentDestinations = r.RecentDestinations
	c.LastMode = r.LastMode
	if r.PathTemplate != "" {
		c.PathTemplate = r.PathTemplate
	}
	if r.AlignMtimeToExif != nil {
		c.AlignMtimeToExif = *r.AlignMtimeToExif
	}
	if r.SoftMatchDestination != nil {
		c.SoftMatchDestination = *r.SoftMatchDestination
	}
	return c, nil
}

// Save writes state.json atomically: temp file + fsync + rename, so a
// power-loss mid-write can't leave a half-written file.
func (c *Config) Save() error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), defaultDperm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, defaultPerm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// RecordRun moves matches to the front and truncates each list to maxRecent.
// Empty inputs are ignored; an invalid mode leaves LastMode unchanged.
func (c *Config) RecordRun(source, dest, mode string) {
	if source != "" {
		c.RecentSources = mru(c.RecentSources, source, maxRecent)
	}
	if dest != "" {
		c.RecentDestinations = mru(c.RecentDestinations, dest, maxRecent)
	}
	if mode == "move" || mode == "copy" {
		c.LastMode = mode
	}
}

func mru(list []string, val string, max int) []string {
	out := make([]string, 0, max)
	out = append(out, val)
	for _, v := range list {
		if v == val {
			continue
		}
		out = append(out, v)
		if len(out) == max {
			break
		}
	}
	return out
}

// pathTemplateDateTokens are the Go reference-time fragments validatePathTemplate
// requires at least one of, so an accidental literal like "photos/2024" is
// rejected up front. Substring search is intentionally permissive — full Go
// layout parsing would reject things like "20060102" that are valid composites
// of multiple tokens.
var pathTemplateDateTokens = []string{
	"2006", "06",
	"01", "1", "Jan", "January",
	"02", "2",
}

// validatePathTemplate enforces the rules a path template must satisfy before
// it is fed to time.Format. Single source of truth: the settings POST and the
// scan handler both call this so a malformed template is rejected the same way
// no matter how it arrives.
func validatePathTemplate(template string) error {
	if template == "" {
		return errors.New("path template must be non-empty")
	}
	if !templateContainsDateToken(template) {
		return errors.New("path template must include at least one date token")
	}
	if strings.HasPrefix(template, "/") {
		return errors.New("path template must be relative")
	}
	for _, seg := range strings.Split(template, "/") {
		if seg == ".." {
			return errors.New("path template must not contain ..")
		}
	}
	if strings.ContainsRune(template, 0) {
		return errors.New("path template contains illegal characters")
	}
	return nil
}

func templateContainsDateToken(tpl string) bool {
	for _, tok := range pathTemplateDateTokens {
		if strings.Contains(tpl, tok) {
			return true
		}
	}
	return false
}
