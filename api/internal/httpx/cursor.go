package httpx

import (
	"encoding/base64"
	"errors"

	"github.com/google/uuid"
)

// Cursor is the shared opaque-cursor pagination helper: every list endpoint
// orders by (tenant_id, id) with UUIDv7 ids, so a cursor is just the last
// seen id, base64url-encoded so it stays an opaque string to API clients.

// DefaultPageLimit and MaxPageLimit bound every cursor-paginated list.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// ErrInvalidCursor is returned by DecodeCursor for a malformed cursor.
var ErrInvalidCursor = errors.New("httpx: invalid cursor")

// EncodeCursor turns the last id of a page into an opaque cursor string.
// The zero UUID encodes to "" (first page).
func EncodeCursor(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// DecodeCursor parses an opaque cursor string back into the id to resume
// after. An empty string decodes to the zero UUID (first page).
func DecodeCursor(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 16 {
		return uuid.Nil, ErrInvalidCursor
	}
	var id uuid.UUID
	copy(id[:], b)
	return id, nil
}

// PageLimit clamps a client-supplied limit into [1, MaxPageLimit], using
// DefaultPageLimit when the client did not specify one.
func PageLimit(requested *int) int32 {
	if requested == nil || *requested <= 0 {
		return DefaultPageLimit
	}
	if *requested > MaxPageLimit {
		return MaxPageLimit
	}
	return int32(*requested)
}
