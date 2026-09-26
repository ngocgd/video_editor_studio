// Package importer detects a manuscript upload's text encoding, decodes
// it to UTF-8, and splits it into chapters using regex presets. Nothing
// here talks to the database or storage; callers (api/internal/story) own
// fetching the asset bytes and persisting the result.
package importer

import (
	"errors"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

// Encoding names reported alongside the decoded text, stored verbatim in
// imports.encoding.
const (
	EncodingUTF8    = "utf-8"
	EncodingUTF16LE = "utf-16le"
	EncodingUTF16BE = "utf-16be"
	EncodingGB18030 = "gb18030"
)

// ErrUndetectedEncoding is returned when none of the supported encodings
// decode raw into text that looks like valid, sane content. Per the
// security checklist, a decoding failure is reported to the caller, never
// silently guessed.
var ErrUndetectedEncoding = errors.New("importer: could not detect a supported text encoding")

// utf16BOMLE / utf16BOMBE are the two-byte byte-order marks that
// unambiguously identify UTF-16.
var (
	utf16BOMLE = []byte{0xFF, 0xFE}
	utf16BOMBE = []byte{0xFE, 0xFF}
)

// utf8BOM is the (optional, rare) UTF-8 byte-order mark.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// DecodeText detects raw's encoding and returns its UTF-8 text plus the
// encoding name that was used. Detection order: UTF-16 BOM (unambiguous),
// UTF-8 (valid and not GB18030-only-decodable garbage), then GB18030 (a
// superset of GBK) as the CJK fallback, accepted only when the result is
// mostly Han ideographs. An input that decodes to invalid
// UTF-8 or contains an implausible density of the Unicode replacement
// character under every attempted encoding is reported as
// ErrUndetectedEncoding rather than guessed.
func DecodeText(raw []byte) (text string, encodingName string, err error) {
	switch {
	case hasPrefix(raw, utf16BOMLE):
		decoded, derr := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().Bytes(raw)
		if derr != nil || !utf8.Valid(decoded) {
			return "", "", ErrUndetectedEncoding
		}
		return string(decoded), EncodingUTF16LE, nil
	case hasPrefix(raw, utf16BOMBE):
		decoded, derr := unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder().Bytes(raw)
		if derr != nil || !utf8.Valid(decoded) {
			return "", "", ErrUndetectedEncoding
		}
		return string(decoded), EncodingUTF16BE, nil
	}

	body := trimPrefix(raw, utf8BOM)
	if utf8.Valid(body) && looksPlausible(string(body)) {
		return string(body), EncodingUTF8, nil
	}

	if decoded, derr := simplifiedchinese.GB18030.NewDecoder().Bytes(raw); derr == nil && utf8.Valid(decoded) && looksPlausible(string(decoded)) && looksChinese(string(decoded)) {
		return string(decoded), EncodingGB18030, nil
	}

	return "", "", ErrUndetectedEncoding
}

// gb18030MinCJKRatio is the share of letters that must be Han ideographs
// before a non-UTF-8 upload is accepted as GB18030. GB18030 decodes almost
// any byte stream without error: Windows-1252 or Latin-1 prose turns each
// accented letter plus its neighbour into one ideograph, which leaves most
// letters Latin, while real Chinese prose is overwhelmingly Han.
const gb18030MinCJKRatio = 0.5

// looksChinese reports whether a GB18030 decoding reads as Chinese prose
// rather than mojibake from another single-byte encoding.
func looksChinese(s string) bool {
	return cjkRatio(s) >= gb18030MinCJKRatio
}

// looksPlausible rejects text that is technically valid UTF-8 but is
// mostly replacement characters or control bytes, which happens when a
// non-UTF-8 byte stream is coincidentally valid UTF-8 for a short prefix.
// It is a coarse sanity check, not a language detector.
func looksPlausible(s string) bool {
	if s == "" {
		return true
	}
	replacementCount := 0
	total := 0
	for _, r := range s {
		total++
		if r == utf8.RuneError {
			replacementCount++
		}
	}
	return total == 0 || float64(replacementCount)/float64(total) < 0.01
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if b[i] != p {
			return false
		}
	}
	return true
}

func trimPrefix(b, prefix []byte) []byte {
	if hasPrefix(b, prefix) {
		return b[len(prefix):]
	}
	return b
}
