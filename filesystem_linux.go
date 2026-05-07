//go:build linux

package main

import (
	"fmt"
	"syscall"
)

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
