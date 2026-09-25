package httpx

import (
	"testing"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	encoded := EncodeCursor(id)
	if encoded == "" {
		t.Fatal("expected non-empty cursor for a non-nil id")
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("roundtrip mismatch: got %s want %s", decoded, id)
	}
}

func TestCursorEmptyIsFirstPage(t *testing.T) {
	if got := EncodeCursor(uuid.Nil); got != "" {
		t.Fatalf("expected empty cursor for nil uuid, got %q", got)
	}
	decoded, err := DecodeCursor("")
	if err != nil {
		t.Fatal(err)
	}
	if decoded != uuid.Nil {
		t.Fatalf("expected nil uuid decoding empty cursor, got %s", decoded)
	}
}

func TestCursorRejectsGarbage(t *testing.T) {
	for _, s := range []string{"not-base64!!", "AAAA", "../../etc/passwd"} {
		if _, err := DecodeCursor(s); err != ErrInvalidCursor {
			t.Fatalf("decode(%q): expected ErrInvalidCursor, got %v", s, err)
		}
	}
}

func TestPageLimit(t *testing.T) {
	if got := PageLimit(nil); got != DefaultPageLimit {
		t.Fatalf("nil limit: got %d want %d", got, DefaultPageLimit)
	}
	zero := 0
	if got := PageLimit(&zero); got != DefaultPageLimit {
		t.Fatalf("zero limit: got %d want %d", got, DefaultPageLimit)
	}
	huge := 100000
	if got := PageLimit(&huge); got != MaxPageLimit {
		t.Fatalf("huge limit: got %d want %d", got, MaxPageLimit)
	}
	ten := 10
	if got := PageLimit(&ten); got != 10 {
		t.Fatalf("ten limit: got %d want 10", got)
	}
}
