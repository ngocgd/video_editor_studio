package idconv

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ToPgTimestamptz converts a time.Time to a valid pgtype.Timestamptz.
func ToPgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// FromPgTimestamptz converts a pgtype.Timestamptz back to time.Time; an
// invalid (NULL) value converts to the zero time.
func FromPgTimestamptz(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
