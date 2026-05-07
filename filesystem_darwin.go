//go:build darwin

package main

import (
	"fmt"
	"syscall"
)

// dev returns the device id for path. uint64(uint32(...)) avoids sign-extension
// of darwin's int32 Stat_t.Dev when widened to the cross-platform uint64.
func dev(path string) (uint64, error) {
	fi, err := statDev(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("sameFilesystem: unexpected FileInfo.Sys() type for %q", path)
	}
	return uint64(uint32(st.Dev)), nil
}
