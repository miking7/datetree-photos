package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// copyFixture copies a checked-in testdata file under a different name in a
// temp dir, so dispatch tests can verify content-sniffing wins over the
// extension. Returns the new path.
func copyFixture(t *testing.T, src, dstName string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	dst := filepath.Join(t.TempDir(), dstName)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
	return dst
}

// PNG fixtures are built byte-for-byte from the spec rather than checked-in
// binaries — the bytes are still real PNG files that flow through the real
// bep/imagemeta and PNG-chunk-walker code paths.
//
// Layout: signature, IHDR, optional eXIf / tEXt / tIME, minimal IDAT, IEND.

var pngSig = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

func writePNGChunk(buf *bytes.Buffer, chunkType string, data []byte) {
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(data)))
	copy(hdr[4:8], chunkType)
	buf.Write(hdr)
	buf.Write(data)

	crc := crc32.NewIEEE()
	crc.Write(hdr[4:8])
	crc.Write(data)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc.Sum32())
	buf.Write(crcBytes)
}

func ihdrChunk() []byte {
	d := make([]byte, 13)
	binary.BigEndian.PutUint32(d[0:4], 1)  // width
	binary.BigEndian.PutUint32(d[4:8], 1)  // height
	d[8] = 8                               // bit depth
	d[9] = 0                               // color type: grayscale
	d[10] = 0                              // compression
	d[11] = 0                              // filter
	d[12] = 0                              // interlace
	return d
}

// minimal valid IDAT for a 1x1 grayscale image — zlib-wrapped deflate of a
// single filter+pixel byte. Pre-computed so tests don't pull in compress/zlib.
var minimalIDAT = []byte{0x78, 0x9c, 0x62, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0x03, 0x00, 0x00, 0x02, 0x00, 0x01}

func tIMEChunk(t time.Time) []byte {
	d := make([]byte, 7)
	binary.BigEndian.PutUint16(d[0:2], uint16(t.Year()))
	d[2] = byte(t.Month())
	d[3] = byte(t.Day())
	d[4] = byte(t.Hour())
	d[5] = byte(t.Minute())
	d[6] = byte(t.Second())
	return d
}

func tEXtChunk(key, value string) []byte {
	d := make([]byte, 0, len(key)+1+len(value))
	d = append(d, []byte(key)...)
	d = append(d, 0)
	d = append(d, []byte(value)...)
	return d
}

// eXIfChunk wraps a minimal TIFF/EXIF blob. tag is the EXIF tag id — 0x0132
// (ModifyDate, IFD0) for the exif_datetime path, or 0x9003 (DateTimeOriginal,
// ExifIFD) for the exif_original path. The single-entry IFD layout is enough
// to exercise bep/imagemeta's tag dispatch either way.
func eXIfChunk(tag uint16, dateStr string) []byte {
	const tiffHeaderLen = 8
	val := append([]byte(dateStr), 0)

	tiff := make([]byte, tiffHeaderLen)
	tiff[0], tiff[1] = 'I', 'I' // little-endian
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8) // IFD0 offset

	ifd := make([]byte, 0, 2+12+4)
	ifd = binary.LittleEndian.AppendUint16(ifd, 1) // entry count

	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry[0:2], tag)
	binary.LittleEndian.PutUint16(entry[2:4], 2) // ASCII
	binary.LittleEndian.PutUint32(entry[4:8], uint32(len(val)))
	binary.LittleEndian.PutUint32(entry[8:12], uint32(tiffHeaderLen+2+12+4))
	ifd = append(ifd, entry...)
	ifd = binary.LittleEndian.AppendUint32(ifd, 0) // no next IFD

	out := append([]byte{}, tiff...)
	out = append(out, ifd...)
	out = append(out, val...)
	return out
}

func buildPNG(extras func(buf *bytes.Buffer)) []byte {
	var buf bytes.Buffer
	buf.Write(pngSig)
	writePNGChunk(&buf, "IHDR", ihdrChunk())
	if extras != nil {
		extras(&buf)
	}
	writePNGChunk(&buf, "IDAT", minimalIDAT)
	writePNGChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writeFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestExtractMetadata_PNG_eXIfModifyDate(t *testing.T) {
	data := buildPNG(func(buf *bytes.Buffer) {
		writePNGChunk(buf, "eXIf", eXIfChunk(0x0132, "2024:03:14 10:23:01"))
	})
	path := writeFixture(t, "exif_modify.png", data)

	got, src, err := extractMetadata(path, KindPhoto)
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if src != DateSourceExifDateTime {
		t.Errorf("src = %v, want exif_datetime", src)
	}
	want := time.Date(2024, 3, 14, 10, 23, 1, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("date = %v, want %v", got, want)
	}
}

func TestExtractMetadata_PNG_tIMEOnly(t *testing.T) {
	when := time.Date(2023, 7, 4, 12, 30, 0, 0, time.UTC)
	data := buildPNG(func(buf *bytes.Buffer) {
		writePNGChunk(buf, "tIME", tIMEChunk(when))
	})
	path := writeFixture(t, "time.png", data)

	got, src, err := extractMetadata(path, KindPhoto)
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if src != DateSourcePNGTime {
		t.Errorf("src = %v, want png_time", src)
	}
	if !got.Equal(when) {
		t.Errorf("date = %v, want %v", got, when)
	}
}

func TestExtractMetadata_PNG_CreationText(t *testing.T) {
	data := buildPNG(func(buf *bytes.Buffer) {
		writePNGChunk(buf, "tEXt", tEXtChunk("Creation Time", "Mon, 02 Jan 2006 15:04:05 +0000"))
	})
	path := writeFixture(t, "creation.png", data)

	got, src, err := extractMetadata(path, KindPhoto)
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if src != DateSourcePNGCreationText {
		t.Errorf("src = %v, want png_creation_text", src)
	}
	want := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("date = %v, want %v", got, want)
	}
}

func TestExtractMetadata_PNG_NoMetadataFallsBackToMtime(t *testing.T) {
	data := buildPNG(nil)
	path := writeFixture(t, "bare.png", data)

	mtime := time.Date(2022, 1, 15, 9, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got, src, _ := extractMetadata(path, KindPhoto)
	if src != DateSourceMtime {
		t.Errorf("src = %v, want mtime", src)
	}
	if !got.Equal(mtime) {
		t.Errorf("date = %v, want %v", got, mtime)
	}
}

// Regression: an iPhone screenshot exported through iCloud Optimize Storage /
// AirDrop "Most Compatible" can land as JPEG bytes wrapped in a .PNG filename.
// Dispatch by extension routes to bep-with-PNG and silently falls through to
// mtime; sniff-based dispatch must route the JPEG bytes to evano regardless of
// the lying suffix.
func TestExtractMetadata_JPEGBytesWithPNGExtension(t *testing.T) {
	path := copyFixture(t, "testdata/jpeg-with-exif.jpg", "IMG_8024.PNG")

	mtime := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got, src, err := extractMetadata(path, KindPhoto)
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if src != DateSourceExifOriginal {
		t.Fatalf("src = %v, want exif_original (mtime fallback means dispatch is wrong)", src)
	}
	want := time.Date(2024, 3, 14, 10, 23, 1, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("date = %v, want %v", got, want)
	}
}

// Regression: PNG bytes wearing a .jpg suffix must still flow through the bep
// path that knows how to read PNG eXIf chunks.
func TestExtractMetadata_PNGBytesWithJPGExtension(t *testing.T) {
	data := buildPNG(func(buf *bytes.Buffer) {
		writePNGChunk(buf, "eXIf", eXIfChunk(0x0132, "2024:03:14 10:23:01"))
	})
	path := writeFixture(t, "screenshot-mislabeled.jpg", data)

	got, src, err := extractMetadata(path, KindPhoto)
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if src != DateSourceExifDateTime {
		t.Fatalf("src = %v, want exif_datetime", src)
	}
	want := time.Date(2024, 3, 14, 10, 23, 1, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("date = %v, want %v", got, want)
	}
}

func TestExtractMetadata_MtimeOnly(t *testing.T) {
	mtime := time.Date(2021, 6, 1, 8, 30, 0, 0, time.UTC)
	for _, ext := range []string{".bmp", ".gif", ".avi", ".mkv", ".mts", ".jxl", ".orf"} {
		path := writeFixture(t, "stub"+ext, []byte("not really "+ext))
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", ext, err)
		}
		kind, ok := classify(path)
		if !ok {
			t.Errorf("%s: classify dropped, want kept", ext)
			continue
		}
		got, src, _ := extractMetadata(path, kind)
		if src != DateSourceMtime {
			t.Errorf("%s: src = %v, want mtime", ext, src)
		}
		if !got.Equal(mtime) {
			t.Errorf("%s: date = %v, want %v", ext, got, mtime)
		}
	}
}

func TestClassify_DropsUnknown(t *testing.T) {
	for _, ext := range []string{".xyz", ".DS_Store", ".txt", ".aae"} {
		_, ok := classify("/tmp/sample" + ext)
		if ok {
			t.Errorf("%s: classify kept, want dropped", ext)
		}
	}
}

func TestClassify_NewWhitelistEntries(t *testing.T) {
	cases := []struct {
		ext  string
		want FileKind
	}{
		{".png", KindPhoto},
		{".webp", KindPhoto},
		{".avif", KindPhoto},
		{".pef", KindPhoto},
		{".lrf", KindVideo},
		{".lrv", KindVideo},
		{".bmp", KindOther},
		{".gif", KindOther},
		{".jxl", KindOther},
		{".orf", KindOther},
		{".rw2", KindOther},
		{".raf", KindOther},
		{".srw", KindOther},
		{".x3f", KindOther},
		{".3fr", KindOther},
		{".fff", KindOther},
		{".rwl", KindOther},
		{".avi", KindOther},
		{".mkv", KindOther},
		{".3gp", KindOther},
		{".3g2", KindOther},
		{".webm", KindOther},
		{".wmv", KindOther},
		{".flv", KindOther},
		{".360", KindOther},
	}
	for _, c := range cases {
		kind, ok := classify("/tmp/sample" + c.ext)
		if !ok {
			t.Errorf("%s: classify dropped, want kept", c.ext)
			continue
		}
		if kind != c.want {
			t.Errorf("%s: kind = %v, want %v", c.ext, kind, c.want)
		}
	}
}
