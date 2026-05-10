package components

import (
	"encoding/json"
	"fmt"
)

// presetsJSON serializes the preset list for the inline preview script. The
// templ template embeds it inside a <script type="application/json"> so the
// browser parses it once, on load.
func presetsJSON(presets []SettingsPreset) string {
	b, err := json.Marshal(presets)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// LastCheckedDisplay returns "never" before any check has run, otherwise the
// local-time formatted timestamp. Kept human-readable rather than relative to
// avoid second-by-second template churn.
func (m SettingsModel) LastCheckedDisplay() string {
	if m.LastChecked.IsZero() {
		return "never"
	}
	return m.LastChecked.Local().Format("2006-01-02 15:04:05")
}

// ReleaseNotesURL returns the GitHub Release page URL for the candidate tag.
// Empty when no candidate is known.
func (m SettingsModel) ReleaseNotesURL() string {
	if m.LatestTag == "" {
		return ""
	}
	return fmt.Sprintf("https://github.com/miking7/datetree-photos/releases/tag/%s", m.LatestTag)
}
