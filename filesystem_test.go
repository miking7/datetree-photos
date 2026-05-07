package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameFilesystem(t *testing.T) {
	t.Run("same path", func(t *testing.T) {
		tmp := t.TempDir()
		ok, err := sameFilesystem(tmp, tmp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatalf("expected same path to report same filesystem")
		}
	})

	t.Run("two subdirs in same tempdir", func(t *testing.T) {
		tmp := t.TempDir()
		a := filepath.Join(tmp, "a")
		b := filepath.Join(tmp, "b")
		for _, p := range []string{a, b} {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", p, err)
			}
		}
		ok, err := sameFilesystem(a, b)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatalf("expected sibling tempdirs to report same filesystem")
		}
	})

	t.Run("missing path returns error", func(t *testing.T) {
		tmp := t.TempDir()
		missing := filepath.Join(tmp, "does-not-exist")
		if _, err := sameFilesystem(tmp, missing); err == nil {
			t.Fatalf("expected error for missing path, got nil")
		}
		if _, err := sameFilesystem(missing, tmp); err == nil {
			t.Fatalf("expected error when first path is missing, got nil")
		}
	})
}
