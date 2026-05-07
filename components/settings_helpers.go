package components

import "encoding/json"

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
