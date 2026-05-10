//go:build linux

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
)

// RevealInFileManager opens the system file manager via xdg-open. There is no
// portable way to ask Linux file managers to highlight a single file (GNOME
// and KDE expose this through different DBus interfaces), so for files we
// fall back to opening the parent directory. v1 deliberately doesn't try to
// special-case desktop environments — see datetree-spec.md §13.
func RevealInFileManager(path string, isFile bool) error {
	target := path
	if isFile {
		target = filepath.Dir(path)
	}
	return exec.Command("xdg-open", target).Start()
}

func dev(path string) (uint64, error) {
	fi, err := statDev(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("sameFilesystem: unexpected FileInfo.Sys() type for %q", path)
	}
	return st.Dev, nil
}
