package idconv

import "github.com/jackc/pgx/v5/pgtype"

// ToPgText converts s to pgtype.Text, mapping "" to SQL NULL.
func ToPgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// ToPgInt8 converts n to a valid pgtype.Int8.
func ToPgInt8(n int64) pgtype.Int8 {
	return pgtype.Int8{Int64: n, Valid: true}
}

// ToPgInt4 converts n to a valid pgtype.Int4.
func ToPgInt4(n int32) pgtype.Int4 {
	return pgtype.Int4{Int32: n, Valid: true}
}
