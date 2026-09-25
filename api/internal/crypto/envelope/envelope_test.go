package envelope

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testSealer(t *testing.T) *Sealer {
	t.Helper()
	var kek [32]byte
	if _, err := rand.Read(kek[:]); err != nil {
		t.Fatal(err)
	}
	s, err := NewSealer("kek-test", kek)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := testSealer(t)
	plaintexts := [][]byte{
		[]byte(""),
		[]byte("a"),
		[]byte("the quick brown fox jumps over the lazy dog"),
		bytes.Repeat([]byte{0xAB}, 4096),
	}
	for _, pt := range plaintexts {
		aad := AAD("tenant-1", "youtube_oauth", "record-1")
		sealed, err := s.Seal(aad, pt)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		got, err := s.Open(sealed, aad)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("roundtrip mismatch: got %q want %q", got, pt)
		}
	}
}

func TestOpenFailsOnTamperedCiphertext(t *testing.T) {
	s := testSealer(t)
	aad := AAD("tenant-1", "kind", "record-1")
	sealed, err := s.Seal(aad, []byte("top secret token"))
	if err != nil {
		t.Fatal(err)
	}
	sealed.Ciphertext[0] ^= 0xFF
	if _, err := s.Open(sealed, aad); err == nil {
		t.Fatal("expected error opening tampered ciphertext, got nil")
	}
}

func TestOpenFailsOnTamperedWrappedDEK(t *testing.T) {
	s := testSealer(t)
	aad := AAD("tenant-1", "kind", "record-1")
	sealed, err := s.Seal(aad, []byte("top secret token"))
	if err != nil {
		t.Fatal(err)
	}
	sealed.WrappedDEK[len(sealed.WrappedDEK)-1] ^= 0xFF
	if _, err := s.Open(sealed, aad); err == nil {
		t.Fatal("expected error opening with a tampered wrapped DEK, got nil")
	}
}

func TestOpenFailsOnMismatchedAAD(t *testing.T) {
	s := testSealer(t)
	sealed, err := s.Seal(AAD("tenant-1", "kind", "record-1"), []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(sealed, AAD("tenant-2", "kind", "record-1")); err == nil {
		t.Fatal("expected error opening with mismatched AAD (different tenant), got nil")
	}
}

func TestOpenFailsOnKeyIDMismatch(t *testing.T) {
	s := testSealer(t)
	aad := AAD("tenant-1", "kind", "record-1")
	sealed, err := s.Seal(aad, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	sealed.KeyID = "kek-other"
	if _, err := s.Open(sealed, aad); err != ErrKeyIDMismatch {
		t.Fatalf("expected ErrKeyIDMismatch, got %v", err)
	}
}

func TestSealIsRandomizedPerCall(t *testing.T) {
	s := testSealer(t)
	aad := AAD("tenant-1", "kind", "record-1")
	pt := []byte("same plaintext")
	a, err := s.Seal(aad, pt)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Seal(aad, pt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Ciphertext, b.Ciphertext) {
		t.Fatal("expected two seals of the same plaintext to produce different ciphertext (fresh DEK/nonce each time)")
	}
}
