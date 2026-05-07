package main

import (
	"bytes"
	"testing"
)

func TestSniffFormat(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want sniffedFormat
	}{
		{
			"png signature",
			[]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52},
			formatPNG,
		},
		{
			"jpeg JFIF",
			append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x00}, 12)...),
			formatJPEG,
		},
		{
			"jpeg EXIF",
			append([]byte{0xFF, 0xD8, 0xFF, 0xE1}, bytes.Repeat([]byte{0x00}, 12)...),
			formatJPEG,
		},
		{
			"webp",
			append(append([]byte("RIFF"), 0x10, 0x00, 0x00, 0x00), []byte("WEBP")...),
			formatWebP,
		},
		{
			"webp wrong inner brand is unknown",
			append(append([]byte("RIFF"), 0x10, 0x00, 0x00, 0x00), []byte("WAVE")...),
			formatUnknown,
		},
		{
			"tiff little endian",
			append([]byte{0x49, 0x49, 0x2A, 0x00}, bytes.Repeat([]byte{0x00}, 12)...),
			formatTIFF,
		},
		{
			"tiff big endian",
			append([]byte{0x4D, 0x4D, 0x00, 0x2A}, bytes.Repeat([]byte{0x00}, 12)...),
			formatTIFF,
		},
		{
			"heic ftyp",
			ftypBytes("heic"),
			formatHEIC,
		},
		{
			"heix ftyp",
			ftypBytes("heix"),
			formatHEIC,
		},
		{
			"hevc ftyp",
			ftypBytes("hevc"),
			formatHEIC,
		},
		{
			"mif1 ftyp",
			ftypBytes("mif1"),
			formatHEIC,
		},
		{
			"avif ftyp",
			ftypBytes("avif"),
			formatAVIF,
		},
		{
			"avis ftyp",
			ftypBytes("avis"),
			formatAVIF,
		},
		{
			"crx ftyp (trailing space)",
			ftypBytes("crx "),
			formatCR3,
		},
		{
			"mp4 ftyp",
			ftypBytes("mp42"),
			formatMP4,
		},
		{
			"isom ftyp",
			ftypBytes("isom"),
			formatMP4,
		},
		{
			"qt ftyp (trailing spaces)",
			ftypBytes("qt  "),
			formatMP4,
		},
		{
			"unknown ftyp brand still maps to mp4",
			ftypBytes("zzzz"),
			formatMP4,
		},
		{
			"empty",
			nil,
			formatUnknown,
		},
		{
			"one byte",
			[]byte{0x89},
			formatUnknown,
		},
		{
			"seven bytes (just below png signature)",
			[]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A},
			formatUnknown,
		},
		{
			"random garbage",
			[]byte("not a real image file at all here"),
			formatUnknown,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sniffFormat(bytes.NewReader(c.in))
			if err != nil {
				t.Fatalf("sniffFormat returned err = %v, want nil", err)
			}
			if got != c.want {
				t.Errorf("sniffFormat = %d, want %d", got, c.want)
			}
		})
	}
}

func ftypBytes(brand string) []byte {
	if len(brand) != 4 {
		panic("ftyp brand must be 4 bytes")
	}
	out := make([]byte, 0, 16)
	out = append(out, 0x00, 0x00, 0x00, 0x20) // box size (placeholder)
	out = append(out, []byte("ftyp")...)
	out = append(out, []byte(brand)...)
	out = append(out, 0x00, 0x00, 0x00, 0x00) // minor_version
	return out
}
