package components

import "sync"

// bannerMu guards bannerTag against concurrent writes from the launch-time
// update-check goroutine and reads from per-request templ rendering.
var (
	bannerMu  sync.RWMutex
	bannerTag string
)

// CurrentBanner returns the version string the layout should advertise as
// available, or "" when no banner should be shown. Read at render time.
func CurrentBanner() string {
	bannerMu.RLock()
	defer bannerMu.RUnlock()
	return bannerTag
}

// SetBanner is called by the main package after a check completes, after a
// dismiss, and after a manual recheck. The empty string clears the banner.
func SetBanner(tag string) {
	bannerMu.Lock()
	bannerTag = tag
	bannerMu.Unlock()
}
