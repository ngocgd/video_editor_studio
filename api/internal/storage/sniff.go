package storage

import (
	"bytes"
	"errors"
)

// sniffBytes is how much of the object finalize reads to identify it: a
// single 512B range GET, never the whole object.
const sniffBytes = 512

// MaxBytesByKind caps upload size per asset kind. A presign request or a
// finalize whose declared/observed size exceeds this is rejected.
var MaxBytesByKind = map[string]int64{
	"image":    25 << 20,  // 25MiB
	"audio":    200 << 20, // 200MiB
	"video":    2 << 30,   // 2GiB
	"document": 5 << 20,   // 5MiB
}

// allowedMimeByKind is the declared-MIME allowlist per kind, used to
// reject a presign request outright. It is not trusted for finalize: only
// the sniffed magic bytes decide the asset's real MIME there.
var allowedMimeByKind = map[string]map[string]bool{
	"image":    {"image/png": true, "image/jpeg": true, "image/webp": true, "image/avif": true},
	"audio":    {"audio/wav": true, "audio/x-wav": true, "audio/flac": true, "audio/mpeg": true},
	"video":    {"video/mp4": true},
	"document": {"text/plain": true, "text/markdown": true},
}

// ErrUnsupportedKindOrMime is returned when kind is unknown or mime is not
// in that kind's allowlist.
var ErrUnsupportedKindOrMime = errors.New("storage: unsupported asset kind/mime combination")

// ValidatePresign checks kind/mime/size against the allowlists before a
// presign is issued.
func ValidatePresign(kind, mime string, byteSize int64) error {
	allowed, ok := allowedMimeByKind[kind]
	if !ok || !allowed[mime] {
		return ErrUnsupportedKindOrMime
	}
	maxBytes, ok := MaxBytesByKind[kind]
	if !ok || byteSize <= 0 || byteSize > maxBytes {
		return ErrUnsupportedKindOrMime
	}
	return nil
}

// magic byte signatures, checked against the first sniffBytes of the
// object regardless of what the uploader declared as Content-Type.
var magicSniffers = []struct {
	mime  string
	match func([]byte) bool
}{
	{"image/png", func(b []byte) bool { return bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) }},
	{"image/jpeg", func(b []byte) bool { return bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}) }},
	{"image/webp", func(b []byte) bool {
		return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
	}},
	{"image/avif", func(b []byte) bool {
		return len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")) && bytes.Contains(b[8:12], []byte("avif"))
	}},
	{"audio/wav", func(b []byte) bool {
		return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE"))
	}},
	{"audio/flac", func(b []byte) bool { return bytes.HasPrefix(b, []byte("fLaC")) }},
	{"audio/mpeg", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte{0x49, 0x44, 0x33}) || (len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0)
	}},
	{"video/mp4", func(b []byte) bool { return len(b) >= 8 && bytes.Equal(b[4:8], []byte("ftyp")) }},
}

// SniffMIME identifies head (the first sniffBytes of an object) by magic
// bytes, ignoring any declared Content-Type. "" means no known signature
// matched.
func SniffMIME(head []byte) string {
	for _, s := range magicSniffers {
		if s.match(head) {
			return s.mime
		}
	}
	return ""
}

// KindAllowsMIME reports whether sniffedMIME belongs to kind's allowlist.
// text/plain and text/markdown have no reliable magic bytes, so "document"
// finalize instead trusts the declared MIME captured at presign time
// (still constrained to the allowlist there) rather than a sniff.
func KindAllowsMIME(kind, sniffedMIME string) bool {
	allowed, ok := allowedMimeByKind[kind]
	if !ok {
		return false
	}
	return allowed[sniffedMIME]
}
