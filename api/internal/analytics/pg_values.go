package analytics

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Nullable column readers: nil means NULL, i.e. not available.

func int8Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func int4Ptr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}

func float8Ptr(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func datePtr(v pgtype.Date) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

func tsPtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

// when returns &v when the metric was available on at least one day
// (days > 0), else nil: aggregate queries COALESCE missing sums to 0.
func when[T int64 | float64](days int32, v T) *T {
	if days <= 0 {
		return nil
	}
	return &v
}

// decodeReasons reads an unavailable jsonb map; a malformed value reads
// as no reasons (the column is checked to be an object).
func decodeReasons(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}
