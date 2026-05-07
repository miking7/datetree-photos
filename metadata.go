package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abema/go-mp4"
	bepmeta "github.com/bep/imagemeta"
	evano "github.com/evanoberholster/imagemeta"
)

// extractMetadata returns a non-zero capture date for every file. mtime is the
// guaranteed last-resort fallback, so callers can always render a date.
func extractMetadata(path string, kind FileKind) (time.Time, DateSource, error) {
	mtime, statErr := fileMtime(path)
	if statErr != nil {
		return time.Time{}, DateSourceMtime, statErr
	}
	if kind == KindOther {
		return mtime, DateSourceMtime, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return mtime, DateSourceMtime, err
	}
	defer f.Close()

	// Sniff the actual content rather than trusting the extension: iCloud
	// Optimize Storage, Photos.app exports, and AirDrop "Most Compatible" all
	// re-encode iPhone screenshots without renaming the .PNG suffix.
	format, sniffErr := sniffFormat(f)
	if sniffErr != nil {
		return mtime, DateSourceMtime, sniffErr
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return mtime, DateSourceMtime, err
	}

	t, src, readErr := readByFormat(f, path, kind, format)
	if readErr == nil && !t.IsZero() {
		return t, src, nil
	}
	return mtime, DateSourceMtime, readErr
}

func readByFormat(f io.ReadSeeker, path string, kind FileKind, format sniffedFormat) (time.Time, DateSource, error) {
	switch kind {
	case KindPhoto:
		switch format {
		case formatPNG:
			return readPhotoBep(f, bepmeta.PNG, path)
		case formatWebP:
			return readPhotoBep(f, bepmeta.WebP, "")
		case formatAVIF:
			return readPhotoBep(f, bepmeta.AVIF, "")
		case formatJPEG, formatHEIC, formatCR3:
			return readPhotoEvano(f)
		case formatTIFF:
			// TIFF is the container for both first-class TIFF/CR2/NEF/ARW/DNG
			// (handled by evano) and Pentax PEF (handled by bep). Sniff alone
			// can't distinguish — fall back to extension for the disambiguation.
			if strings.ToLower(filepath.Ext(path)) == ".pef" {
				return readPhotoBep(f, bepmeta.PEF, "")
			}
			return readPhotoEvano(f)
		}
	case KindVideo:
		if format == formatMP4 {
			return readVideo(f)
		}
	}
	return time.Time{}, DateSourceMtime, nil
}

func fileMtime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func readPhotoEvano(r io.ReadSeeker) (time.Time, DateSource, error) {
	ex, err := evano.Decode(r)
	if err != nil && errors.Is(err, evano.ErrMetadataNotSupported) {
		return time.Time{}, DateSourceMtime, err
	}
	// Many cameras leave partial EXIF that returns a non-fatal error alongside
	// usable date fields; check the date fields regardless of err.
	if t := ex.ExifIFD.DateTimeOriginal; !t.IsZero() {
		return t, DateSourceExifOriginal, nil
	}
	if t := ex.IFD0.ModifyDate; !t.IsZero() {
		return t, DateSourceExifDateTime, nil
	}
	if err != nil {
		return time.Time{}, DateSourceMtime, err
	}
	return time.Time{}, DateSourceMtime, nil
}

// readPhotoBep walks tags emitted by bep/imagemeta and returns the highest-
// trust date with provenance. PNG-native chunks (tEXt "Creation Time", tIME)
// are not surfaced by bep so they are read directly here as a final pre-mtime
// step. pngPath is only consulted when format == bepmeta.PNG; pass "" otherwise.
func readPhotoBep(r io.ReadSeeker, format bepmeta.ImageFormat, pngPath string) (time.Time, DateSource, error) {
	var (
		exifOriginal time.Time
		exifModify   time.Time
		xmpCreate    time.Time
	)

	_, decodeErr := bepmeta.Decode(bepmeta.Options{
		R:           r,
		ImageFormat: format,
		Sources:     bepmeta.EXIF | bepmeta.XMP,
		HandleTag: func(tag bepmeta.TagInfo) error {
			s, ok := tag.Value.(string)
			if !ok {
				return nil
			}
			switch tag.Source {
			case bepmeta.EXIF:
				switch tag.Tag {
				case "DateTimeOriginal":
					if t, ok := parseExifDate(s); ok {
						exifOriginal = t
					}
				case "ModifyDate", "DateTime":
					if t, ok := parseExifDate(s); ok {
						exifModify = t
					}
				}
			case bepmeta.XMP:
				if tag.Tag == "CreateDate" {
					if t, ok := parseXMPDate(s); ok {
						xmpCreate = t
					}
				}
			}
			return nil
		},
	})

	if !exifOriginal.IsZero() {
		return exifOriginal, DateSourceExifOriginal, nil
	}
	if !exifModify.IsZero() {
		return exifModify, DateSourceExifDateTime, nil
	}
	if !xmpCreate.IsZero() {
		return xmpCreate, DateSourceXMP, nil
	}

	if format == bepmeta.PNG && pngPath != "" {
		if t, src, ok := readPNGNativeDate(pngPath); ok {
			return t, src, nil
		}
	}

	if decodeErr != nil {
		return time.Time{}, DateSourceMtime, decodeErr
	}
	return time.Time{}, DateSourceMtime, nil
}

// EXIF dates use ":" separators per the spec.
func parseExifDate(s string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006:01:02 15:04:05", s, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// XMP dates may carry a timezone offset; without one they're floating local.
func parseXMPDate(s string) (time.Time, bool) {
	for _, layout := range []string{
		"2006:01:02 15:04:05-07:00",
		"2006-01-02T15:04:05-07:00",
		"2006:01:02 15:04:05Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006:01:02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

var pngSignature = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// readPNGNativeDate walks PNG chunks for tEXt "Creation Time" (RFC 1123
// timestamp per the PNG spec) and tIME (last modification, big-endian fixed
// 7-byte payload). bep/imagemeta does not emit either.
func readPNGNativeDate(path string) (time.Time, DateSource, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, DateSourceMtime, false
	}
	defer f.Close()

	sig := make([]byte, 8)
	if _, err := io.ReadFull(f, sig); err != nil {
		return time.Time{}, DateSourceMtime, false
	}
	for i := range sig {
		if sig[i] != pngSignature[i] {
			return time.Time{}, DateSourceMtime, false
		}
	}

	var (
		creationText time.Time
		timeChunk    time.Time
	)

	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(f, header); err != nil {
			break
		}
		length := binary.BigEndian.Uint32(header[0:4])
		ctype := string(header[4:8])
		if length > 1<<24 {
			break
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(f, data); err != nil {
			break
		}
		if _, err := f.Seek(4, io.SeekCurrent); err != nil {
			break
		}
		switch ctype {
		case "tEXt":
			if k, v, ok := splitNul(data); ok && string(k) == "Creation Time" {
				if t, ok := parsePNGCreationText(string(v)); ok {
					creationText = t
				}
			}
		case "tIME":
			if t, ok := parsePNGtIME(data); ok {
				timeChunk = t
			}
		case "IEND":
			break
		}
	}

	if !creationText.IsZero() {
		return creationText, DateSourcePNGCreationText, true
	}
	if !timeChunk.IsZero() {
		return timeChunk, DateSourcePNGTime, true
	}
	return time.Time{}, DateSourceMtime, false
}

func splitNul(b []byte) ([]byte, []byte, bool) {
	for i, c := range b {
		if c == 0 {
			return b[:i], b[i+1:], true
		}
	}
	return nil, nil, false
}

// PNG spec recommends RFC 1123 but accepts any human-readable form; try the
// common encodings produced by ImageMagick, libpng, and Photoshop.
func parsePNGCreationText(s string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006:01:02 15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parsePNGtIME(data []byte) (time.Time, bool) {
	if len(data) != 7 {
		return time.Time{}, false
	}
	year := int(binary.BigEndian.Uint16(data[0:2]))
	month := int(data[2])
	day := int(data[3])
	hour := int(data[4])
	minute := int(data[5])
	second := int(data[6])
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), true
}

// MP4 epoch: midnight 1904-01-01 UTC. mvhd CreationTime is seconds from there.
var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

func readVideo(r io.ReadSeeker) (time.Time, DateSource, error) {
	mvhdPath := mp4.BoxPath{mp4.BoxTypeMoov(), mp4.BoxTypeMvhd()}
	boxes, err := mp4.ExtractBoxWithPayload(r, nil, mvhdPath)
	if err != nil {
		return time.Time{}, DateSourceMtime, fmt.Errorf("mp4 extract: %w", err)
	}
	for _, b := range boxes {
		mvhd, ok := b.Payload.(*mp4.Mvhd)
		if !ok {
			continue
		}
		secs := mvhd.GetCreationTime()
		if secs == 0 {
			continue
		}
		t := mp4Epoch.Add(time.Duration(secs) * time.Second)
		return t, DateSourceQuickTime, nil
	}
	return time.Time{}, DateSourceMtime, nil
}
