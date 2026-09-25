// Package idconv converts between github.com/google/uuid.UUID (used
// everywhere in application code, generated with NewV7 for time-ordered
// primary keys) and pgtype.UUID (the column type sqlc generates).
package idconv

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ToPg converts a uuid.UUID to a valid pgtype.UUID.
func ToPg(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// ToPgPtr converts a *uuid.UUID to pgtype.UUID; nil yields an invalid
// (SQL NULL) value.
func ToPgPtr(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return ToPg(*id)
}

// FromPg converts a pgtype.UUID back to uuid.UUID. An invalid (NULL) value
// converts to the zero UUID.
func FromPg(id pgtype.UUID) uuid.UUID {
	if !id.Valid {
		return uuid.UUID{}
	}
	return id.Bytes
}

// FromPgPtr converts a pgtype.UUID to *uuid.UUID, returning nil for NULL.
func FromPgPtr(id pgtype.UUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	u := uuid.UUID(id.Bytes)
	return &u
}

// NewV7 generates a new UUIDv7 (time-ordered), the id format used for
// every primary key in this codebase. It panics only if the runtime's
// crypto/rand source is broken, which New/V7 treats as unrecoverable.
func NewV7() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic("idconv: crypto/rand unavailable: " + err.Error())
	}
	return id
}
