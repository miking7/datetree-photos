//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

// RevealInFileManager opens the system file manager. For a file we use
// `open -R` so Finder selects/highlights the file inside its parent folder;
// for a directory we open the directory itself. The spawned process is
// detached (Start, not Run) so a slow Finder launch never blocks the request.
func RevealInFileManager(path string, isFile bool) error {
	if isFile {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command("open", path).Start()
}

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
