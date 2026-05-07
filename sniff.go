package main

import (
	"bytes"
	"errors"
	"io"
)

type sniffedFormat int

const (
	formatUnknown sniffedFormat = iota
	formatPNG
	formatJPEG
	formatHEIC
	formatWebP
	formatAVIF
	formatTIFF
	formatCR3
	formatMP4
)

// sniffFormat returns the format identified by up to the first 16 bytes of r.
// Short reads / EOF return formatUnknown with a nil error: "couldn't tell"
// is the same shape as "didn't recognise it" as far as the caller cares.
func sniffFormat(r io.Reader) (sniffedFormat, error) {
	var buf [16]byte
	n, err := io.ReadFull(r, buf[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return formatUnknown, err
	}
	b := buf[:n]

	if len(b) >= 8 && bytes.HasPrefix(b, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return formatPNG, nil
	}
	if len(b) >= 3 && bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}) {
		return formatJPEG, nil
	}
	if len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")) {
		return formatWebP, nil
	}
	if len(b) >= 4 && (bytes.HasPrefix(b, []byte{0x49, 0x49, 0x2A, 0x00}) ||
		bytes.HasPrefix(b, []byte{0x4D, 0x4D, 0x00, 0x2A})) {
		return formatTIFF, nil
	}
	if len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")) {
		return brandToFormat(b[8:12]), nil
	}
	return formatUnknown, nil
}

// brandToFormat maps an ISO BMFF major_brand to the metadata-pipeline format.
// Unknown brands fall through to formatMP4 — abema/go-mp4 accepts the
// QuickTime/MP4 family broadly, and a brand we don't enumerate here will
// still get a try; if it fails the caller falls back to mtime.
func brandToFormat(brand []byte) sniffedFormat {
	switch string(brand) {
	case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
		return formatHEIC
	case "avif", "avis":
		return formatAVIF
	case "crx ":
		return formatCR3
	}
	return formatMP4
}
