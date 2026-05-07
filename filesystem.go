package main

import (
	"os"
)

// sameFilesystem reports whether two paths sit on the same filesystem (same Dev).
// Both paths must exist; if either Stat fails, returns the error.
// For destination paths that don't yet exist, callers should pass filepath.Dir(dest) instead.
func sameFilesystem(a, b string) (bool, error) {
	devA, err := dev(a)
	if err != nil {
		return false, err
	}
	devB, err := dev(b)
	if err != nil {
		return false, err
	}
	return devA == devB, nil
}

func statDev(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
