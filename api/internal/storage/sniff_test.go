package storage

import "testing"

func TestSniffMIMEDetectsKnownSignatures(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0}, "image/png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, "image/jpeg"},
		{"webp", append([]byte("RIFF"), append([]byte{0, 0, 0, 0}, []byte("WEBP")...)...), "image/webp"},
		{"wav", append([]byte("RIFF"), append([]byte{0, 0, 0, 0}, []byte("WAVE")...)...), "audio/wav"},
		{"flac", []byte("fLaCextra"), "audio/flac"},
		{"mp4", append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...), "video/mp4"},
		{"unknown", []byte("not a real file"), ""},
	}

	for _, c := range cases {
		if got := SniffMIME(c.head); got != c.want {
			t.Errorf("%s: SniffMIME() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestKindAllowsMIME(t *testing.T) {
	if !KindAllowsMIME("image", "image/png") {
		t.Error("expected image/png to be allowed for kind=image")
	}
	if KindAllowsMIME("image", "video/mp4") {
		t.Error("expected video/mp4 to be rejected for kind=image")
	}
	if KindAllowsMIME("unknown-kind", "image/png") {
		t.Error("expected an unknown kind to allow nothing")
	}
}

func TestValidatePresign(t *testing.T) {
	if err := ValidatePresign("image", "image/png", 1024); err != nil {
		t.Errorf("expected a valid image presign to pass, got %v", err)
	}
	if err := ValidatePresign("image", "application/x-msdownload", 1024); err != ErrUnsupportedKindOrMime {
		t.Errorf("expected disallowed mime to be rejected, got %v", err)
	}
	if err := ValidatePresign("image", "image/png", MaxBytesByKind["image"]+1); err != ErrUnsupportedKindOrMime {
		t.Errorf("expected over-size request to be rejected, got %v", err)
	}
	if err := ValidatePresign("image", "image/png", 0); err != ErrUnsupportedKindOrMime {
		t.Errorf("expected zero-size request to be rejected, got %v", err)
	}
}
