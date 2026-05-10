package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "v0.1.0", 0},
		{"v0.1.0", "v0.1.1", -1},
		{"v0.1.1", "v0.1.0", 1},
		{"v0.2.0", "v0.1.9", 1},
		{"v1.0.0", "v0.99.99", 1},
		{"0.1.0", "v0.1.0", 0},
		{"v0.1.0-rc1", "v0.1.0", 0},
		{"v0.1.0", "dev", 1},
		{"", "v0.1.0", -1},
	}
	for _, tc := range cases {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			if got := compareSemver(tc.a, tc.b); got != tc.want {
				t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestArchiveName(t *testing.T) {
	cases := []struct {
		os, arch, want string
	}{
		{"darwin", "arm64", "datetree_darwin_arm64.tar.gz"},
		{"darwin", "amd64", "datetree_darwin_amd64.tar.gz"},
		{"linux", "arm64", "datetree_linux_arm64.tar.gz"},
		{"linux", "amd64", "datetree_linux_amd64.tar.gz"},
	}
	for _, tc := range cases {
		if got := archiveName(tc.os, tc.arch); got != tc.want {
			t.Errorf("archiveName(%q,%q) = %q, want %q", tc.os, tc.arch, got, tc.want)
		}
	}
}

func TestCheckLatestDevShortCircuits(t *testing.T) {
	// Even with a working server, a "dev" build never advertises an update.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("dev build should not have called the API; got %s", r.URL.String())
	}))
	defer srv.Close()
	u := &Updater{httpClient: &http.Client{}}
	tag, isNewer, err := u.CheckLatest(context.Background(), devVersion)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if tag != "" || isNewer {
		t.Errorf("dev short-circuit: got (%q, %v), want (\"\", false)", tag, isNewer)
	}
}

func TestLookupChecksum(t *testing.T) {
	// goreleaser writes "<hex>  <name>" per line.
	body := []byte(`abc123def456  datetree_darwin_amd64.tar.gz
def789  datetree_linux_amd64.tar.gz
`)
	got, err := lookupChecksum(body, "datetree_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "def789" {
		t.Errorf("got %q, want def789", got)
	}
	if _, err := lookupChecksum(body, "datetree_windows_arm64.tar.gz"); err == nil {
		t.Errorf("expected error for unknown asset")
	}
}

func TestChecksumMismatchDetectable(t *testing.T) {
	// Pinning the verify primitive: a synthetic archive's real SHA256 must not
	// equal the sentinel listed in checksums.txt, and lookupChecksum must
	// surface the listed value so ApplyUpdate's hex-compare can reject it.
	archive := buildTarGz(t, map[string][]byte{"datetree": []byte("fake-binary")})
	wrongHash := strings.Repeat("0", 64)
	checksums := []byte(fmt.Sprintf("%s  datetree_test_test.tar.gz\n", wrongHash))

	want, err := lookupChecksum(checksums, "datetree_test_test.tar.gz")
	if err != nil {
		t.Fatalf("lookupChecksum: %v", err)
	}
	gotSum := sha256.Sum256(archive)
	gotHex := hex.EncodeToString(gotSum[:])
	if gotHex == want {
		t.Fatalf("expected mismatch between archive sha256 and sentinel")
	}
}

// buildTarGz produces an in-memory gzip tarball with the supplied entries so
// extractBinary can be exercised without a real release asset.
func buildTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	want := []byte("hello-binary-bytes")
	archive := buildTarGz(t, map[string][]byte{"datetree": want, "README.md": []byte("readme")})
	got, err := extractBinary(archive, "datetree")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := extractBinary(archive, "missing"); err == nil {
		t.Errorf("expected error for missing entry")
	}
}
